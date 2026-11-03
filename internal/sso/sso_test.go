package sso

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/identity/identitytest"
)

const testPassword = "correct horse battery"

type testEnv struct {
	*identitytest.Env
	idp *fakeIdP
	m   *Manager
}

func newTestEnv(t *testing.T, autoRegister bool) *testEnv {
	t.Helper()
	t.Parallel()
	idp := newFakeIdP(t)
	env := identitytest.New(t, func(c *config.Config) { c.Security.Registration = config.RegistrationOpen })
	prov := config.OIDCProvider{Name: "corp", DisplayName: "Corp SSO", Issuer: idp.Issuer(), ClientID: "erepo",
		Scopes: []string{"openid", "email", "profile"}, UsernameClaim: "preferred_username", EmailClaim: "email",
		NameClaim: "name", AutoRegister: autoRegister}
	m := New(Options{Pool: env.Pool, Identity: env.Service, Providers: []config.OIDCProvider{prov},
		Secrets: map[string]string{"corp": "s3cret"}, PublicURL: env.Config.Server.PublicURL})
	return &testEnv{Env: env, idp: idp, m: m}
}

var cl = identity.Client{IP: "192.0.2.7", UserAgent: "test"}

// password registers a password account (the first one is the admin) and
// returns a session principal for it.
func (e *testEnv) password(t *testing.T, name string) auth.Principal {
	t.Helper()
	ctx := context.Background()
	if _, err := e.Service.Register(ctx, identity.RegisterInput{Username: name, Email: name + "@example.com", Password: testPassword}, cl); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE emails SET verified_at = now()`); err != nil {
		t.Fatal(err)
	}
	lr, err := e.Service.Login(ctx, name, testPassword, "", cl)
	if err != nil {
		t.Fatal(err)
	}
	s, err := e.Sessions.Lookup(ctx, lr.Session.Token)
	if err != nil {
		t.Fatal(err)
	}
	return s.Principal()
}

// flow runs a full flow with the given claims; cur is the principal at
// the callback.
func (e *testEnv) flow(t *testing.T, mode string, begin, cur *auth.Principal, claims map[string]any) (Result, error) {
	t.Helper()
	ctx := context.Background()
	authURL, state, err := e.m.Begin(ctx, "corp", mode, begin, "/settings")
	if err != nil {
		return Result{}, err
	}
	e.idp.Claims = claims
	q := e.idp.authorize(authURL)
	return e.m.Complete(ctx, Callback{Provider: "corp", State: q.Get("state"), Code: q.Get("code"), Cookie: state, Current: cur, Client: cl})
}

func subject(sub, user, email string, verified bool) map[string]any {
	return map[string]any{"sub": sub, "preferred_username": user, "email": email, "email_verified": verified, "name": "Some One"}
}

func TestAutoRegisterAndLogin(t *testing.T) {
	e := newTestEnv(t, true)
	ctx := context.Background()
	e.password(t, "boss")

	res, err := e.flow(t, ModeLogin, nil, nil, subject("u-1", "Alice.S", "alice@corp.example", true))
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeLogin || res.ReturnTo != "/settings" || res.Login == nil || res.Login.User.Username != "Alice-S" {
		t.Fatalf("result = %+v", res)
	}
	uid := res.Login.User.ID
	// The second login finds the link by issuer#subject, even if the
	// claims changed.
	res, err = e.flow(t, ModeLogin, nil, nil, subject("u-1", "renamed", "other@corp.example", true))
	if err != nil || res.Login.User.ID != uid {
		t.Fatalf("second login = %+v %v", res, err)
	}
	links, err := e.m.Links(ctx, uid)
	if err != nil || len(links) != 1 || links[0].Label != "Corp SSO" {
		t.Fatalf("links = %+v %v", links, err)
	}
	// The only way to sign in cannot be unlinked.
	if err := e.m.Unlink(ctx, uid, links[0].ID, ""); !errors.Is(err, ErrLastMethod) {
		t.Fatalf("unlink last: %v", err)
	}
	// A name collision gets a suffix.
	res, err = e.flow(t, ModeLogin, nil, nil, subject("u-2", "boss", "boss2@corp.example", true))
	if err != nil || res.Login.User.Username != "boss-2" {
		t.Fatalf("collision = %+v %v", res, err)
	}
	// Verification is required by default: an unverified email cannot
	// create an account.
	if _, err := e.flow(t, ModeLogin, nil, nil, subject("u-3", "carol", "carol@corp.example", false)); !errors.Is(err, ErrEmailUnverified) {
		t.Fatalf("unverified: %v", err)
	}
}

func TestFlowChecks(t *testing.T) {
	e := newTestEnv(t, true)
	ctx := context.Background()
	claims := subject("u-1", "alice", "alice@corp.example", true)

	if _, _, err := e.m.Begin(ctx, "nope", ModeLogin, nil, "/"); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("unknown provider: %v", err)
	}
	authURL, state, err := e.m.Begin(ctx, "corp", ModeLogin, nil, "//evil.example/x")
	if err != nil {
		t.Fatal(err)
	}
	e.idp.Claims = claims
	q := e.idp.authorize(authURL)
	cb := Callback{Provider: "corp", State: q.Get("state"), Code: q.Get("code"), Cookie: state, Client: cl}

	// The state must match the cookie of the browser that started the flow.
	bad := cb
	bad.Cookie = "other"
	if _, err := e.m.Complete(ctx, bad); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("cookie mismatch: %v", err)
	}
	bad = cb
	bad.Cookie, bad.State = "", ""
	if _, err := e.m.Complete(ctx, bad); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("no state: %v", err)
	}
	res, err := e.m.Complete(ctx, cb)
	if err != nil || res.ReturnTo != "/" {
		t.Fatalf("complete = %+v %v", res, err)
	}
	// A state is used once.
	if _, err := e.m.Complete(ctx, cb); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("replay: %v", err)
	}

	// The PKCE verifier of another flow does not redeem the code.
	authURL, state, _ = e.m.Begin(ctx, "corp", ModeLogin, nil, "/")
	_, state2, _ := e.m.Begin(ctx, "corp", ModeLogin, nil, "/")
	q = e.idp.authorize(authURL)
	if _, err := e.m.Complete(ctx, Callback{Provider: "corp", State: state2, Cookie: state2, Code: q.Get("code"), Client: cl}); !errors.Is(err, ErrProvider) {
		t.Fatalf("code of another flow: %v", err)
	}
	_ = state

	// A wrong nonce in the ID token is refused.
	e.idp.BadNonce = true
	if _, err := e.flow(t, ModeLogin, nil, nil, claims); !errors.Is(err, ErrProvider) {
		t.Fatalf("bad nonce: %v", err)
	}
	e.idp.BadNonce = false

	// A provider error is reported as such.
	_, state, _ = e.m.Begin(ctx, "corp", ModeLogin, nil, "/")
	if _, err := e.m.Complete(ctx, Callback{Provider: "corp", State: state, Cookie: state, ProviderError: "access_denied", Client: cl}); !errors.Is(err, ErrProvider) {
		t.Fatalf("provider error: %v", err)
	}
}

func TestLinkAndUnlink(t *testing.T) {
	e := newTestEnv(t, false)
	ctx := context.Background()
	alice := e.password(t, "alice")

	// Without auto-registration an unknown identity cannot sign in.
	claims := subject("u-a", "whatever", "alice@example.com", true)
	if _, err := e.flow(t, ModeLogin, nil, nil, claims); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("unlinked login: %v", err)
	}
	res, err := e.flow(t, ModeLink, &alice, &alice, claims)
	if err != nil || res.Mode != ModeLink || res.Login != nil {
		t.Fatalf("link = %+v %v", res, err)
	}
	res, err = e.flow(t, ModeLogin, nil, nil, claims)
	if err != nil || res.Login.User.ID != alice.UserID {
		t.Fatalf("linked login = %+v %v", res, err)
	}
	// A second identity of the same provider is refused.
	if _, err := e.flow(t, ModeLink, &alice, &alice, subject("u-b", "x", "", false)); !errors.Is(err, ErrAlreadyLinked) {
		t.Fatalf("second link: %v", err)
	}
	links, _ := e.m.Links(ctx, alice.UserID)
	if len(links) != 1 {
		t.Fatalf("links = %+v", links)
	}
	if err := e.m.Unlink(ctx, uuid.Must(uuid.NewV7()), links[0].ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign unlink: %v", err)
	}
	// Alice has a password, so the link can go.
	if err := e.m.Unlink(ctx, alice.UserID, links[0].ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.flow(t, ModeLogin, nil, nil, claims); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("login after unlink: %v", err)
	}
}

func TestSafeReturnTo(t *testing.T) {
	for in, want := range map[string]string{
		"/x/y?z=1": "/x/y?z=1", "": "/", "x": "/", "//evil": "/", "/\\evil": "/", "https://evil": "/", "/a\nb": "/", "/a\\b": "/",
	} {
		if got := SafeReturnTo(in); got != want {
			t.Errorf("SafeReturnTo(%q) = %q, want %q", in, got, want)
		}
	}
}

func FuzzSafeReturnTo(f *testing.F) {
	for _, s := range []string{"/", "//x", "/\\x", "/a?b#c"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := SafeReturnTo(s)
		u, err := url.Parse(out)
		if err != nil || u.Host != "" || u.Scheme != "" || out[0] != '/' {
			t.Fatalf("SafeReturnTo(%q) = %q is not a local path", s, out)
		}
	})
}
