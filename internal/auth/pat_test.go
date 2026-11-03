package auth_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// tokenStore is an in-memory auth.TokenStore.
type tokenStore struct {
	rows    map[string]dbgen.GetTokenByHashRow
	touched int
}

func (s *tokenStore) add(scopes []string, role string, mod func(*dbgen.GetTokenByHashRow)) string {
	tok, hash := auth.NewAccessToken()
	row := dbgen.GetTokenByHashRow{ID: uuid.New(), UserID: uuid.New(), Scopes: scopes, Username: "tok", Role: role}
	if mod != nil {
		mod(&row)
	}
	if s.rows == nil {
		s.rows = map[string]dbgen.GetTokenByHashRow{}
	}
	s.rows[string(hash)] = row
	return tok
}

func (s *tokenStore) GetTokenByHash(_ context.Context, h []byte) (dbgen.GetTokenByHashRow, error) {
	r, ok := s.rows[string(h)]
	if !ok {
		return r, pgx.ErrNoRows
	}
	return r, nil
}

func (s *tokenStore) TouchToken(context.Context, uuid.UUID) error { s.touched++; return nil }

func TestAccessTokenFormat(t *testing.T) {
	tok, hash := auth.NewAccessToken()
	if !strings.HasPrefix(tok, auth.TokenPrefix) || len(tok) != len(auth.TokenPrefix)+43 || len(hash) != 32 {
		t.Fatalf("token %q", tok)
	}
	if !auth.ValidAccessTokenFormat(tok) {
		t.Fatal("own token refused")
	}
	session, _ := auth.NewToken()
	for _, bad := range []string{"", session, "erp_", "erp_" + session + "x", "ERP_" + session, "erp_" + strings.Repeat("!", 43)} {
		if auth.ValidAccessTokenFormat(bad) {
			t.Errorf("accepted %q", bad)
		}
	}
	if h := auth.TokenHint(tok); h != tok[:8] {
		t.Fatalf("hint %q", h)
	}
	for _, s := range auth.AllScopes {
		if !auth.ValidScope(s) {
			t.Fatal(s)
		}
	}
	if auth.ValidScope("everything:write") {
		t.Fatal("unknown scope accepted")
	}
}

func TestTokensLookup(t *testing.T) {
	st := &tokenStore{}
	now := time.Now()
	ts := &auth.Tokens{Store: st, Now: func() time.Time { return now }}
	good := st.add([]string{auth.ScopeReadUser}, auth.RoleUser, nil)
	expired := st.add(nil, auth.RoleUser, func(r *dbgen.GetTokenByHashRow) {
		r.ExpiresAt = pgtype.Timestamptz{Time: now.Add(-time.Second), Valid: true}
	})
	suspended := st.add(nil, auth.RoleUser, func(r *dbgen.GetTokenByHashRow) {
		r.SuspendedAt = pgtype.Timestamptz{Time: now, Valid: true}
	})
	p, err := ts.Lookup(context.Background(), good)
	if err != nil || p.Method != auth.MethodToken || !p.HasScope(auth.ScopeReadUser) || p.HasScope(auth.ScopeWriteUser) || st.touched != 1 {
		t.Fatalf("lookup = %+v %v", p, err)
	}
	unknown, _ := auth.NewAccessToken()
	for _, tok := range []string{expired, suspended, unknown, "garbage"} {
		if _, err := ts.Lookup(context.Background(), tok); err != auth.ErrNoToken {
			t.Errorf("lookup(%q) = %v", tok, err)
		}
	}
}

func TestPrincipalHasScope(t *testing.T) {
	p := auth.Principal{Method: auth.MethodToken, Scopes: []string{auth.ScopeWriteRepo}}
	if !p.HasScope(auth.ScopeReadRepo) || !p.HasScope(auth.ScopeWriteRepo) || p.HasScope(auth.ScopeReadUser) || p.HasScope(auth.ScopeAdmin) {
		t.Fatal("write implies read only")
	}
	p.Method = auth.MethodSession
	if p.HasScope(auth.ScopeWriteRepo) {
		t.Fatal("session callers hold no token scopes")
	}
}
