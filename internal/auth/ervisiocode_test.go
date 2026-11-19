package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/auth/authtest"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

func TestErvisioCodeDeviceAuthorization(t *testing.T) {
	m := authtest.New("https://git.example.com").Middleware
	m.Problem = problem
	store := &tokenStore{}
	m.Tokens = &auth.Tokens{Store: store}
	deviceScopes := []string{auth.ScopeErvisioCodeDevice}
	device := func(row *dbgen.GetTokenByHashRow) { row.IsErvisiocodeDevice = true }
	valid := store.add(deviceScopes, auth.RoleAdmin, device)
	expired := store.add(deviceScopes, auth.RoleUser, func(row *dbgen.GetTokenByHashRow) {
		device(row)
		row.ExpiresAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}
	})
	ordinary := store.add(deviceScopes, auth.RoleUser, nil)
	broad := store.add([]string{auth.ScopeErvisioCodeDevice, auth.ScopeReadRepo}, auth.RoleUser, device)
	for _, c := range []struct {
		name, secret string
		v1           bool
		scopes       []string
		want         int
	}{
		{"device operation", valid, true, deviceScopes, http.StatusNoContent},
		{"legacy token lookup", valid, false, deviceScopes, http.StatusUnauthorized},
		{"public operation", valid, true, nil, http.StatusForbidden},
		{"unrelated token operation", valid, true, []string{auth.ScopeReadRepo}, http.StatusForbidden},
		{"unregistered bearer", ordinary, true, deviceScopes, http.StatusForbidden},
		{"mis-scoped registration", broad, true, deviceScopes, http.StatusUnauthorized},
		{"expired registration", expired, true, deviceScopes, http.StatusUnauthorized},
	} {
		t.Run(c.name, func(t *testing.T) {
			authz := m.Authorize(func(context.Context) ([]string, bool) { return nil, false },
				func(context.Context) ([]string, bool) { return c.scopes, c.scopes != nil })
			handler := authz(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			if c.v1 {
				handler = m.LoadV1Session(handler)
			} else {
				handler = m.LoadSession(handler)
			}
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", "Bearer "+c.secret)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Errorf("status %d, want %d", rec.Code, c.want)
			}
		})
	}
}
