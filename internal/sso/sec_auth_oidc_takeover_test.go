package sso

import (
	"context"
	"errors"
	"testing"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
)

// TestSecAuthOIDCTakeover: an external identity that claims the (verified)
// email address of an existing account never signs in to that account,
// never gets linked to it and never creates a second account with it.
func TestSecAuthOIDCTakeover(t *testing.T) {
	t.Parallel()
	for _, auto := range []bool{false, true} {
		t.Run(map[bool]string{false: "linking only", true: "auto-register"}[auto], func(t *testing.T) { takeover(t, auto) })
	}
}

func takeover(t *testing.T, auto bool) {
	{
		e := newTestEnv(t, auto)
		ctx := context.Background()
		victim := e.password(t, "victim")

		for _, email := range []string{"victim@example.com", "VICTIM@Example.COM"} {
			res, err := e.flow(t, ModeLogin, nil, nil, subject("attacker", "victim", email, true))
			want := ErrNotLinked
			if auto {
				want = ErrEmailTaken
			}
			if !errors.Is(err, want) || res.Login != nil {
				t.Fatalf("auto=%v %s: result %+v, err %v, want %v", auto, email, res, err, want)
			}
		}
		links, err := e.m.Links(ctx, victim.UserID)
		if err != nil || len(links) != 0 {
			t.Fatalf("victim links = %+v %v", links, err)
		}
		var n int
		if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("users = %d %v", n, err)
		}
	}
}

// TestSecAuthOIDCLinkRequiresSession: linking needs a signed-in session at
// the start, and the callback must come back to that same session.
func TestSecAuthOIDCLinkRequiresSession(t *testing.T) {
	e := newTestEnv(t, false)
	ctx := context.Background()
	alice := e.password(t, "alice")
	bob := e.password(t, "bob")
	claims := subject("idp-alice", "alice", "alice@example.com", true)

	if _, _, err := e.m.Begin(ctx, "corp", ModeLink, nil, "/"); !errors.Is(err, ErrLinkSession) {
		t.Fatalf("anonymous link: %v", err)
	}
	tok := alice
	tok.Method, tok.SessionID, tok.TokenID = auth.MethodToken, [16]byte{}, [16]byte{1}
	if _, _, err := e.m.Begin(ctx, "corp", ModeLink, &tok, "/"); !errors.Is(err, ErrLinkSession) {
		t.Fatalf("token link: %v", err)
	}
	// Callback without session, with another user, with another session
	// of the same user.
	other := alice
	other.SessionID = bob.SessionID
	for name, cur := range map[string]*auth.Principal{"anonymous": nil, "other user": &bob, "other session": &other} {
		if _, err := e.flow(t, ModeLink, &alice, cur, claims); !errors.Is(err, ErrLinkSession) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, u := range []auth.Principal{alice, bob} {
		if links, _ := e.m.Links(ctx, u.UserID); len(links) != 0 {
			t.Fatalf("links of %s = %+v", u.Username, links)
		}
	}
}
