package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/auth/authtest"
)

type ctxKey string

// authorizeHandler serves Authorize with the scopes passed in the request
// context under the keys "session" and "token".
func authorizeHandler(m *auth.Middleware) http.Handler {
	get := func(k ctxKey) func(context.Context) ([]string, bool) {
		return func(ctx context.Context) ([]string, bool) {
			s, ok := ctx.Value(k).([]string)
			return s, ok
		}
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return m.LoadSession(m.CSRF(m.Authorize(get("session"), get("token"))(ok)))
}

func TestSecAuthTokenScopes(t *testing.T) {
	env := authtest.New("https://git.example.com")
	m := env.Middleware
	m.Problem = problem
	st := &tokenStore{}
	m.Tokens = &auth.Tokens{Store: st}
	h := authorizeHandler(m)
	do := func(method string, session, token []string, mods ...func(*http.Request)) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/", nil)
		ctx := r.Context()
		if session != nil {
			ctx = context.WithValue(ctx, ctxKey("session"), session)
		}
		if token != nil {
			ctx = context.WithValue(ctx, ctxKey("token"), token)
		}
		r = r.WithContext(ctx)
		for _, f := range mods {
			f(r)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	bearer := func(tok string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
	}
	reader := st.add([]string{auth.ScopeReadUser}, auth.RoleUser, nil)
	writer := st.add([]string{auth.ScopeWriteUser}, auth.RoleUser, nil)
	userAdminScope := st.add([]string{auth.ScopeAdmin}, auth.RoleUser, nil)
	admin := st.add([]string{auth.ScopeAdmin}, auth.RoleAdmin, nil)
	adminNoScope := st.add([]string{auth.ScopeReadUser}, auth.RoleAdmin, nil)
	_, session := env.Login(t, "alice", auth.RoleUser)

	cases := []struct {
		name           string
		method         string
		session, token []string
		mod            func(*http.Request)
		want           int
	}{
		{"public with token", http.MethodGet, nil, nil, bearer(reader), 200},
		{"session-only op refuses token", http.MethodGet, []string{}, nil, bearer(writer), 403},
		{"read scope", http.MethodGet, []string{}, []string{auth.ScopeReadUser}, bearer(reader), 200},
		{"write implies read", http.MethodGet, []string{}, []string{auth.ScopeReadUser}, bearer(writer), 200},
		{"read lacks write", http.MethodPost, []string{}, []string{auth.ScopeWriteUser}, bearer(reader), 403},
		{"write scope, no CSRF needed", http.MethodPost, []string{}, []string{auth.ScopeWriteUser}, bearer(writer), 200},
		{"admin scope needs admin role", http.MethodGet, []string{auth.RoleAdmin}, []string{auth.ScopeAdmin}, bearer(userAdminScope), 403},
		{"admin role needs admin scope", http.MethodGet, []string{auth.RoleAdmin}, []string{auth.ScopeAdmin}, bearer(adminNoScope), 403},
		{"admin", http.MethodGet, []string{auth.RoleAdmin}, []string{auth.ScopeAdmin}, bearer(admin), 200},
		{"invalid token", http.MethodGet, nil, nil, bearer("erp_nope"), 401},
		{"unknown scheme ignored", http.MethodGet, []string{}, []string{auth.ScopeReadUser}, func(r *http.Request) { r.Header.Set("Authorization", "Basic eDp5") }, 401},
		{"session still works", http.MethodPost, []string{}, []string{auth.ScopeWriteUser}, session, 200},
		{"anonymous on token op", http.MethodGet, []string{}, []string{auth.ScopeReadUser}, nil, 401},
		{"token-only op refuses sessions", http.MethodGet, nil, []string{auth.ScopeReadUser}, session, 401},
	}
	for _, c := range cases {
		mods := []func(*http.Request){}
		if c.mod != nil {
			mods = append(mods, c.mod)
		}
		if rec := do(c.method, c.session, c.token, mods...); rec.Code != c.want {
			t.Errorf("%s: %d, want %d (%s)", c.name, rec.Code, c.want, rec.Body)
		}
	}

	// An invalid bearer token is not rescued by a valid session cookie.
	rec := do(http.MethodGet, []string{}, nil, session, bearer("erp_nope"))
	if rec.Code != 401 || !strings.Contains(rec.Header().Get("WWW-Authenticate"), "invalid_token") {
		t.Fatalf("invalid token with session: %d %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	// Tokens are refused when the middleware has none configured.
	m.Tokens = nil
	if rec := do(http.MethodGet, nil, nil, bearer(reader)); rec.Code != 401 {
		t.Fatalf("no token store: %d", rec.Code)
	}
}
