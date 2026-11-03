package httpapi

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/credentials"
	"github.com/Ervisio/ErvisioRepo/internal/identity/identitytest"
	"github.com/Ervisio/ErvisioRepo/internal/sso"
)

// credentialsRouter is identityRouter plus the credentials service, access
// tokens and an OIDC provider whose issuer is unreachable.
func credentialsRouter(t *testing.T) (http.Handler, *identitytest.Env) {
	t.Helper()
	t.Parallel()
	env := identitytest.New(t, func(c *config.Config) { c.Mail.Enabled = false })
	cookies := auth.CookiesFor(env.Config.Server.PublicURL)
	prov := config.OIDCProvider{Name: "corp", Issuer: "http://127.0.0.1:1/unreachable", ClientID: "erepo",
		Scopes: []string{"openid"}, UsernameClaim: "preferred_username", EmailClaim: "email", NameClaim: "name"}
	m := sso.New(sso.Options{Pool: env.Pool, Identity: env.Service, Providers: []config.OIDCProvider{prov},
		PublicURL: env.Config.Server.PublicURL, Cookies: cookies,
		HTTPClient: &http.Client{Timeout: 2 * time.Second}})
	h := newTestRouter(t, func(d *Deps) {
		d.Identity = env.Service
		d.Credentials = credentials.New(credentials.Options{Pool: env.Pool, TokenMaxTTL: 30 * 24 * time.Hour})
		d.SSO = m
		d.Auth = &auth.Middleware{Sessions: env.Sessions, Tokens: &auth.Tokens{Store: env.Queries}, Cookies: cookies}
		d.Storage = &fakeStorage{}
	})
	return h, env
}

func bearer(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

func newSSHKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}

func createToken(t *testing.T, b *browser, name string, scopes ...string) (secret, id string) {
	t.Helper()
	rec := b.do(http.MethodPost, "/api/v1/user/tokens", map[string]any{"name": name, "scopes": scopes,
		"expiresAt": time.Now().Add(24 * time.Hour).Format(time.RFC3339)})
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("create token: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Secret string
		Token  struct{ ID string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !strings.HasPrefix(out.Secret, auth.TokenPrefix) {
		t.Fatalf("token = %+v %v", out, err)
	}
	return out.Secret, out.Token.ID
}

// TestSecAuthPATHTTP: access tokens reach only the operations that list the
// token scheme, with the scopes listed there, and never manage tokens.
func TestSecAuthPATHTTP(t *testing.T) {
	h, _ := credentialsRouter(t)
	admin := withAdmin(t, h)
	read, readID := createToken(t, admin, "read", auth.ScopeReadUser)
	write, _ := createToken(t, admin, "write", auth.ScopeWriteUser)
	api := newBrowser(t, h, "198.51.100.20") // no cookies, no CSRF header

	cases := []struct {
		name, method, path, token string
		body                      any
		want                      int
	}{
		{"user with user:read", http.MethodGet, "/api/v1/user", read, nil, http.StatusOK},
		{"keys with user:read", http.MethodGet, "/api/v1/user/keys/ssh", read, nil, http.StatusOK},
		{"gpg keys with user:write", http.MethodGet, "/api/v1/user/keys/gpg", write, nil, http.StatusOK},
		{"add key with user:read", http.MethodPost, "/api/v1/user/keys/ssh", read, map[string]string{"title": "x", "key": newSSHKey(t)}, http.StatusForbidden},
		{"add key with user:write", http.MethodPost, "/api/v1/user/keys/ssh", write, map[string]string{"title": "x", "key": newSSHKey(t)}, http.StatusCreated},
		{"list tokens", http.MethodGet, "/api/v1/user/tokens", write, nil, http.StatusForbidden},
		{"create token", http.MethodPost, "/api/v1/user/tokens", write, map[string]any{"name": "y", "scopes": []string{"user:read"}, "expiresAt": time.Now().Add(time.Hour)}, http.StatusForbidden},
		{"oidc links", http.MethodGet, "/api/v1/user/oidc", write, nil, http.StatusForbidden},
		{"admin api", http.MethodGet, "/api/v1/admin/audit", write, nil, http.StatusForbidden},
		{"orgs", http.MethodGet, "/api/v1/orgs", write, nil, http.StatusForbidden},
		{"unknown token", http.MethodGet, "/api/v1/user", auth.TokenPrefix + strings.Repeat("A", 43), nil, http.StatusUnauthorized},
		{"session cookie as bearer", http.MethodGet, "/api/v1/user", admin.session(), nil, http.StatusUnauthorized},
	}
	for _, c := range cases {
		if rec := api.do(c.method, c.path, c.body, bearer(c.token)); rec.Code != c.want {
			t.Errorf("%s: %d %s, want %d", c.name, rec.Code, rec.Body, c.want)
		}
	}

	if rec := admin.do(http.MethodDelete, "/api/v1/user/tokens/"+readID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}
	if rec := api.do(http.MethodGet, "/api/v1/user", nil, bearer(read)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token: %d", rec.Code)
	}

	// The admin scope needs an admin; tokens cannot be created without
	// CSRF protection.
	other := newBrowser(t, h, "198.51.100.30").prime()
	other.noCSRF = true
	if rec := other.do(http.MethodPost, "/api/v1/user/tokens", map[string]any{}); rec.Code != http.StatusForbidden {
		t.Fatalf("no csrf: %d", rec.Code)
	}
}

func TestCredentialsHTTP(t *testing.T) {
	h, _ := credentialsRouter(t)
	admin := withAdmin(t, h)
	key := newSSHKey(t)

	rec := admin.do(http.MethodPost, "/api/v1/user/keys/ssh", map[string]string{"title": "laptop", "key": key + " me@laptop"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add key: %d %s", rec.Code, rec.Body)
	}
	var k struct{ ID, Fingerprint, Type string }
	if err := json.Unmarshal(rec.Body.Bytes(), &k); err != nil || !strings.HasPrefix(k.Fingerprint, "SHA256:") || k.Type != "ssh-ed25519" {
		t.Fatalf("key = %+v %v", k, err)
	}
	for name, c := range map[string]struct {
		body any
		want int
	}{
		"duplicate": {map[string]string{"title": "again", "key": key}, http.StatusConflict},
		"junk":      {map[string]string{"title": "junk", "key": "ssh-ed25519 junk"}, http.StatusBadRequest},
		"no title":  {map[string]string{"title": "", "key": newSSHKey(t)}, http.StatusCreated}, // defaults to the comment or type
	} {
		if rec := admin.do(http.MethodPost, "/api/v1/user/keys/ssh", c.body); rec.Code != c.want {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	rec = admin.do(http.MethodGet, "/api/v1/user/keys/ssh", nil)
	if rec.Code != http.StatusOK || strings.Count(rec.Body.String(), `"fingerprint"`) != 2 {
		t.Fatalf("list keys: %d %s", rec.Code, rec.Body)
	}
	if rec := admin.do(http.MethodDelete, "/api/v1/user/keys/ssh/"+k.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete key: %d", rec.Code)
	}
	if rec := admin.do(http.MethodDelete, "/api/v1/user/keys/ssh/"+k.ID, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("delete again: %d", rec.Code)
	}
	if rec := admin.do(http.MethodPost, "/api/v1/user/keys/gpg", map[string]string{"armoredKey": "junk"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("gpg junk: %d %s", rec.Code, rec.Body)
	}
	for name, body := range map[string]map[string]any{
		"expired":  {"name": "a", "scopes": []string{"user:read"}, "expiresAt": time.Now().Add(-time.Hour)},
		"too long": {"name": "a", "scopes": []string{"user:read"}, "expiresAt": time.Now().Add(365 * 24 * time.Hour)},
		"scope":    {"name": "a", "scopes": []string{"root"}, "expiresAt": time.Now().Add(time.Hour)},
	} {
		if rec := admin.do(http.MethodPost, "/api/v1/user/tokens", body); rec.Code != http.StatusBadRequest {
			t.Errorf("token %s: %d %s", name, rec.Code, rec.Body)
		}
	}
	createToken(t, admin, "dup", auth.ScopeReadRepo)
	if rec := admin.do(http.MethodPost, "/api/v1/user/tokens", map[string]any{"name": "dup", "scopes": []string{"repo:read"},
		"expiresAt": time.Now().Add(time.Hour)}); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate token name: %d", rec.Code)
	}
	rec = admin.do(http.MethodGet, "/api/v1/user/tokens", nil)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "secret") || !strings.Contains(rec.Body.String(), `"hint":"erp_`) {
		t.Fatalf("list tokens: %d %s", rec.Code, rec.Body)
	}
}

func TestOIDCHTTP(t *testing.T) {
	h, _ := credentialsRouter(t)
	anon := newBrowser(t, h, "198.51.100.40").prime()

	rec := anon.do(http.MethodGet, "/api/v1/auth/oidc/providers", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"corp"`) {
		t.Fatalf("providers: %d %s", rec.Code, rec.Body)
	}
	redirect := func(rec *httptest.ResponseRecorder) url.URL {
		t.Helper()
		if rec.Code != http.StatusFound {
			t.Fatalf("status %d %s", rec.Code, rec.Body)
		}
		u, err := url.Parse(rec.Header().Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		return *u
	}
	for path, want := range map[string]string{
		"/auth/oidc/corp/login":                   "provider_error",
		"/auth/oidc/nope/login":                   "unknown_provider",
		"/auth/oidc/corp/callback?state=x&code=y": "invalid_state",
		"/auth/oidc/corp/callback?error=denied":   "invalid_state",
	} {
		u := redirect(anon.do(http.MethodGet, path, nil))
		if u.Path != "/login" || u.Query().Get("oidc_error") != want {
			t.Errorf("%s: redirect %s, want %s", path, u.String(), want)
		}
	}

	admin := withAdmin(t, h)
	rec = admin.do(http.MethodGet, "/api/v1/user/oidc", nil)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("links: %d %s", rec.Code, rec.Body)
	}
	if rec := admin.do(http.MethodPost, "/api/v1/user/oidc/corp/link", map[string]string{}); rec.Code != http.StatusBadGateway {
		t.Fatalf("link unreachable: %d %s", rec.Code, rec.Body)
	}
	if rec := admin.do(http.MethodPost, "/api/v1/user/oidc/nope/link", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("link unknown: %d %s", rec.Code, rec.Body)
	}
	if rec := admin.do(http.MethodDelete, "/api/v1/user/oidc/00000000-0000-0000-0000-000000000001", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unlink unknown: %d %s", rec.Code, rec.Body)
	}
	u := redirect(admin.do(http.MethodGet, "/auth/oidc/corp/callback", nil))
	if u.Path != "/settings" || u.Query().Get("oidc_error") != "invalid_state" {
		t.Fatalf("signed-in callback: %s", u.String())
	}
}
