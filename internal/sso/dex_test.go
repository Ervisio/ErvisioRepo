package sso

import (
	"context"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/bcrypt"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/identity/identitytest"
)

// DexImage is the Dex image of the end-to-end test.
const DexImage = "ghcr.io/dexidp/dex:v2.41.1"

// startDex runs Dex with one static client and one password user. The
// container port is published on a fixed loopback port, so that the issuer
// URL is known before Dex starts.
func startDex(t *testing.T, redirectURI, email, password string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	issuer := fmt.Sprintf("http://127.0.0.1:%d/dex", port)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf(`issuer: %s
storage:
  type: memory
web:
  http: 0.0.0.0:5556
oauth2:
  skipApprovalScreen: true
enablePasswordDB: true
staticClients:
- id: erepo
  secret: dex-client-secret
  name: ErvisioRepo
  redirectURIs: ['%s']
staticPasswords:
- email: %s
  hash: '%s'
  username: alice
  userID: 08a8684b-db88-4b73-90a9-3cd1661f5466
`, issuer, redirectURI, email, hash)
	ctx := context.Background()
	ctr, err := testcontainers.Run(ctx, DexImage,
		testcontainers.WithExposedPorts("5556/tcp"),
		testcontainers.WithFiles(testcontainers.ContainerFile{Reader: strings.NewReader(cfg), ContainerFilePath: "/etc/dex/erepo.yaml", FileMode: 0o644}),
		testcontainers.WithCmd("dex", "serve", "/etc/dex/erepo.yaml"),
		testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			hc.PortBindings = network.PortMap{network.MustParsePort("5556/tcp"): {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: fmt.Sprint(port)}}}
		}),
		testcontainers.WithWaitStrategyAndDeadline(time.Minute,
			wait.ForHTTP("/dex/.well-known/openid-configuration").WithPort("5556/tcp")),
	)
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatalf("start dex: %v", err)
	}
	return issuer
}

var formAction = regexp.MustCompile(`<form[^>]*method="post"[^>]*action="([^"]+)"`)

// TestDexLogin signs in end to end against Dex: discovery, redirect, the
// Dex password form, code exchange with PKCE and ID token verification,
// auto-registration, then a second login on the same link.
func TestDexLogin(t *testing.T) {
	t.Parallel()
	env := identitytest.New(t, nil) // skips without Docker
	const email, password = "alice@dex.example", "dex password 1"
	callback := env.Config.Server.PublicURL + "/auth/oidc/dex/callback"
	issuer := startDex(t, callback, email, password)

	prov := config.OIDCProvider{Name: "dex", Issuer: issuer, ClientID: "erepo", AutoRegister: true}
	cfg := config.Config{Auth: config.Auth{OIDC: []config.OIDCProvider{prov}}}
	applyDefaultsForTest(&cfg)
	m := New(Options{Pool: env.Pool, Identity: env.Service, Providers: cfg.Auth.OIDC,
		Secrets: map[string]string{"dex": "dex-client-secret"}, PublicURL: env.Config.Server.PublicURL})

	login := func() Result {
		t.Helper()
		ctx := context.Background()
		authURL, state, err := m.Begin(ctx, "dex", ModeLogin, nil, "/explore")
		if err != nil {
			t.Fatal(err)
		}
		q := dexAuthorize(t, authURL, callback, email, password)
		res, err := m.Complete(ctx, Callback{Provider: "dex", State: q.Get("state"), Code: q.Get("code"), Cookie: state, Client: cl})
		if err != nil {
			t.Fatalf("complete: %v", err)
		}
		return res
	}
	first := login()
	if first.Login == nil || first.ReturnTo != "/explore" || first.Login.User.Username != "alice" || first.Login.User.Role != "admin" {
		t.Fatalf("first login = %+v", first)
	}
	second := login()
	if second.Login.User.ID != first.Login.User.ID || second.Login.Session.Token == first.Login.Session.Token {
		t.Fatalf("second login = %+v", second)
	}
}

// applyDefaultsForTest fills the provider defaults as config.Parse does.
func applyDefaultsForTest(c *config.Config) {
	for i := range c.Auth.OIDC {
		p := &c.Auth.OIDC[i]
		p.Scopes = []string{"openid", "email", "profile"}
		p.UsernameClaim, p.EmailClaim, p.NameClaim = "preferred_username", "email", "name"
	}
}

// dexAuthorize plays the browser at Dex: it opens authURL, submits the
// password form and returns the query of the redirect to callback.
func dexAuthorize(t *testing.T, authURL, callback, email, password string) url.Values {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	var final *url.URL
	c := &http.Client{Jar: jar, Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		if strings.HasPrefix(req.URL.String(), callback) {
			final = req.URL
			return http.ErrUseLastResponse
		}
		return nil
	}}
	resp, err := c.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	m := formAction.FindSubmatch(body)
	if m == nil {
		t.Fatalf("no login form at %s (%d): %.500s", resp.Request.URL, resp.StatusCode, body)
	}
	action, err := resp.Request.URL.Parse(html.UnescapeString(string(m[1])))
	if err != nil {
		t.Fatal(err)
	}
	resp, err = c.PostForm(action.String(), url.Values{"login": {email}, "password": {password}})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if final == nil {
		t.Fatalf("dex did not redirect to the callback (status %d at %s)", resp.StatusCode, resp.Request.URL)
	}
	return final.Query()
}
