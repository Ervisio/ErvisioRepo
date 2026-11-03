package credentials

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/identity/identitytest"
)

type testEnv struct {
	*identitytest.Env
	svc *Service
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	t.Parallel()
	env := identitytest.New(t, func(c *config.Config) {
		c.Security.Registration = config.RegistrationOpen
		c.Auth.RequireVerifiedEmail = false
	})
	return &testEnv{Env: env, svc: New(Options{Pool: env.Pool, TokenMaxTTL: 30 * 24 * time.Hour})}
}

// user registers an account and returns its principal.
func (e *testEnv) user(t *testing.T, name string) auth.Principal {
	t.Helper()
	ctx := context.Background()
	if _, err := e.Service.Register(ctx, identity.RegisterInput{Username: name, Email: name + "@example.com", Password: "correct horse battery"},
		identity.Client{IP: "192.0.2.1"}); err != nil {
		t.Fatal(err)
	}
	p, err := e.Service.Authenticate(ctx, name, "correct horse battery", identity.Client{IP: "192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTokens(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	admin := e.user(t, "boss")
	alice := e.user(t, "alice")
	exp := time.Now().Add(24 * time.Hour)

	tok, err := e.svc.CreateToken(ctx, alice, NewTokenInput{Name: " ci ", Scopes: []string{auth.ScopeReadRepo, auth.ScopeReadRepo, auth.ScopeReadUser}, ExpiresAt: exp}, "")
	if err != nil {
		t.Fatal(err)
	}
	if tok.Name != "ci" || len(tok.Scopes) != 2 || !auth.ValidAccessTokenFormat(tok.Secret) || tok.Hint != tok.Secret[:8] {
		t.Fatalf("token = %+v", tok)
	}
	// The secret authenticates through the middleware lookup.
	p, err := (&auth.Tokens{Store: e.Queries}).Lookup(ctx, tok.Secret)
	if err != nil || p.UserID != alice.UserID || p.TokenID != tok.ID || !p.HasScope(auth.ScopeReadRepo) || p.HasScope(auth.ScopeWriteRepo) {
		t.Fatalf("lookup = %+v %v", p, err)
	}

	for name, c := range map[string]struct {
		p   auth.Principal
		in  NewTokenInput
		err error
	}{
		"duplicate name": {alice, NewTokenInput{Name: "ci", Scopes: []string{auth.ScopeReadRepo}, ExpiresAt: exp}, ErrNameTaken},
		"empty name":     {alice, NewTokenInput{Name: "  ", Scopes: []string{auth.ScopeReadRepo}, ExpiresAt: exp}, ErrInvalidName},
		"no scope":       {alice, NewTokenInput{Name: "x", ExpiresAt: exp}, ErrInvalidScope},
		"bad scope":      {alice, NewTokenInput{Name: "x", Scopes: []string{"repo:everything"}, ExpiresAt: exp}, ErrInvalidScope},
		"admin by user":  {alice, NewTokenInput{Name: "x", Scopes: []string{auth.ScopeAdmin}, ExpiresAt: exp}, ErrAdminScope},
		"past":           {alice, NewTokenInput{Name: "x", Scopes: []string{auth.ScopeReadRepo}, ExpiresAt: time.Now().Add(-time.Minute)}, ErrExpiry},
		"no expiry":      {alice, NewTokenInput{Name: "x", Scopes: []string{auth.ScopeReadRepo}}, ErrExpiry},
		"too long":       {alice, NewTokenInput{Name: "x", Scopes: []string{auth.ScopeReadRepo}, ExpiresAt: time.Now().Add(31 * 24 * time.Hour)}, ErrExpiry},
	} {
		if _, err := e.svc.CreateToken(ctx, c.p, c.in, ""); !errors.Is(err, c.err) {
			t.Errorf("%s: err = %v, want %v", name, err, c.err)
		}
	}
	if _, err := e.svc.CreateToken(ctx, admin, NewTokenInput{Name: "ops", Scopes: []string{auth.ScopeAdmin}, ExpiresAt: exp}, ""); err != nil {
		t.Fatalf("admin scope by admin: %v", err)
	}

	list, err := e.svc.ListTokens(ctx, alice.UserID)
	if err != nil || len(list) != 1 || list[0].ID != tok.ID {
		t.Fatalf("list = %+v %v", list, err)
	}
	// Another user cannot revoke it.
	if err := e.svc.RevokeToken(ctx, admin.UserID, tok.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign revoke: %v", err)
	}
	if err := e.svc.RevokeToken(ctx, alice.UserID, tok.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := (&auth.Tokens{Store: e.Queries}).Lookup(ctx, tok.Secret); !errors.Is(err, auth.ErrNoToken) {
		t.Fatalf("revoked lookup: %v", err)
	}
	if err := e.svc.RevokeToken(ctx, alice.UserID, uuid.Must(uuid.NewV7()), ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown revoke: %v", err)
	}
}

func TestSSHKeys(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	bob := e.user(t, "bob")
	key := ed25519Key(t)

	k, err := e.svc.AddSSHKey(ctx, alice.UserID, "", key, "")
	if err != nil || k.Type != "ssh-ed25519" || k.Title == "" {
		t.Fatalf("add = %+v %v", k, err)
	}
	// A key belongs to one account only.
	if _, err := e.svc.AddSSHKey(ctx, bob.UserID, "laptop", key, ""); !errors.Is(err, ErrKeyTaken) {
		t.Fatalf("same key for bob: %v", err)
	}
	if _, err := e.svc.AddSSHKey(ctx, alice.UserID, "again", key, ""); !errors.Is(err, ErrKeyTaken) {
		t.Fatalf("same key twice: %v", err)
	}
	if _, err := e.svc.AddSSHKey(ctx, alice.UserID, "junk", "ssh-ed25519 junk", ""); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("junk: %v", err)
	}
	list, err := e.svc.ListSSHKeys(ctx, alice.UserID)
	if err != nil || len(list) != 1 || list[0].Fingerprint != k.Fingerprint {
		t.Fatalf("list = %+v %v", list, err)
	}
	if err := e.svc.RemoveSSHKey(ctx, bob.UserID, k.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign remove: %v", err)
	}
	if err := e.svc.RemoveSSHKey(ctx, alice.UserID, k.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddSSHKey(ctx, bob.UserID, "laptop", key, ""); err != nil {
		t.Fatalf("re-add after removal: %v", err)
	}
}

func TestGPGKeys(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	e.user(t, "boss") // the first account is verified
	alice := e.user(t, "alice")
	bob := e.user(t, "bob")
	armored := armorPublic(t, newEntity(t, "Alice", "alice@example.com", 0))

	k, err := e.svc.AddGPGKey(ctx, alice.UserID, armored, "")
	if err != nil || len(k.KeyID) != 16 || len(k.Emails) != 1 {
		t.Fatalf("add = %+v %v", k, err)
	}
	// The address is not verified yet (mail is enabled in the test config).
	if len(k.Verified) != 0 {
		t.Fatalf("verified = %v", k.Verified)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE emails SET verified_at = now() WHERE user_id = $1`, alice.UserID); err != nil {
		t.Fatal(err)
	}
	list, err := e.svc.ListGPGKeys(ctx, alice.UserID)
	if err != nil || len(list) != 1 || len(list[0].Verified) != 1 {
		t.Fatalf("list = %+v %v", list, err)
	}
	if _, err := e.svc.AddGPGKey(ctx, bob.UserID, armored, ""); !errors.Is(err, ErrKeyTaken) {
		t.Fatalf("same key for bob: %v", err)
	}
	if err := e.svc.RemoveGPGKey(ctx, bob.UserID, k.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign remove: %v", err)
	}
	if err := e.svc.RemoveGPGKey(ctx, alice.UserID, k.ID, ""); err != nil {
		t.Fatal(err)
	}
}
