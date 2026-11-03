package httpapi

import (
	"net/http"
	"testing"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/auth/authtest"
	"github.com/Ervisio/ErvisioRepo/internal/config"
)

var configLocalOnly = config.Storage{
	Targets: []config.Target{{Name: "disk", Kind: "local"}},
	Policy:  map[string]config.Policy{"lfs": {Primary: "disk"}},
}

// TestSecAdminRequiresAdmin: the admin API refuses anonymous callers,
// ordinary users, forged sessions and requests without a valid CSRF token,
// before touching the storage. Loopback or proxy headers grant nothing.
func TestSecAdminRequiresAdmin(t *testing.T) {
	fs := &fakeStorage{}
	h, env, asAdmin := adminRouter(t, func(d *Deps) { d.Storage = fs })
	_, asUser := env.Login(t, "alice", auth.RoleUser)
	_, asRestricted := env.Login(t, "bob", auth.RoleRestricted)
	cookies := env.Middleware.Cookies
	forged, _ := auth.NewToken()

	cases := map[string]struct {
		mod    func(*http.Request)
		status int
	}{
		"anonymous":  {func(*http.Request) {}, http.StatusUnauthorized},
		"anon csrf":  {authtest.AnonCSRF(cookies), http.StatusUnauthorized},
		"loopback":   {func(r *http.Request) { r.RemoteAddr = "127.0.0.1:4000"; r.Host = "127.0.0.1:3400" }, http.StatusUnauthorized},
		"user":       {asUser, http.StatusForbidden},
		"restricted": {asRestricted, http.StatusForbidden},
		"forged":     {env.WithSession(forged, forged), http.StatusUnauthorized},
		"x-forwarded-for": {func(r *http.Request) {
			r.RemoteAddr = "127.0.0.1:4000"
			r.Header.Set("X-Forwarded-For", "127.0.0.1")
		}, http.StatusUnauthorized},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assertProblem(t, do(h, http.MethodGet, "/api/v1/admin/storage", tc.mod), tc.status)
			// Unsafe requests of anonymous callers fail CSRF first (403) or
			// authentication (401); either way they are refused.
			rec := do(h, http.MethodPost, "/api/v1/admin/storage/disk/test", tc.mod)
			if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
				t.Fatalf("POST status = %d", rec.Code)
			}
		})
	}
	if len(fs.checked) != 0 {
		t.Fatalf("storage reached by a non-admin: %v", fs.checked)
	}

	// CSRF on the admin's own session.
	csrfCases := map[string]func(*http.Request){
		"cross-site": func(r *http.Request) { asAdmin(r); r.Header.Set("Sec-Fetch-Site", "cross-site") },
		"same-site":  func(r *http.Request) { asAdmin(r); r.Header.Set("Sec-Fetch-Site", "same-site") },
		"no header":  func(r *http.Request) { asAdmin(r); r.Header.Del(auth.CSRFHeader) },
		"wrong header": func(r *http.Request) {
			asAdmin(r)
			r.Header.Set(auth.CSRFHeader, forged)
		},
		// Double submit alone is not enough: the token is bound to the session.
		"matching foreign token": func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: cookies.SessionName(), Value: sessionCookie(t, asAdmin, cookies)})
			authtest.WithCSRF(cookies, forged)(r)
		},
	}
	for name, mod := range csrfCases {
		t.Run(name, func(t *testing.T) {
			assertProblem(t, do(h, http.MethodPost, "/api/v1/admin/storage/disk/test", mod), http.StatusForbidden)
		})
	}
	if len(fs.checked) != 0 {
		t.Fatalf("storage reached without a valid CSRF token: %v", fs.checked)
	}

	if rec := do(h, http.MethodPost, "/api/v1/admin/storage/disk/test", func(r *http.Request) {
		asAdmin(r)
		r.Header.Set("Sec-Fetch-Site", "same-origin")
	}); rec.Code != http.StatusOK {
		t.Fatalf("admin test refused: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodGet, "/api/v1/admin/storage", asAdmin); rec.Code != http.StatusOK {
		t.Fatalf("admin status refused: %d %s", rec.Code, rec.Body)
	}
	// Public endpoints are unaffected.
	if rec := do(h, http.MethodGet, "/api/v1/meta"); rec.Code != http.StatusOK {
		t.Fatalf("meta: %d", rec.Code)
	}
}

// TestSecAdminDemotedOrSuspended: role and suspension are read from the
// database on every request, so they take effect on live sessions.
func TestSecAdminDemotedOrSuspended(t *testing.T) {
	fs := &fakeStorage{}
	h, env, _ := adminRouter(t, func(d *Deps) { d.Storage = fs })
	p, asAdmin := env.Login(t, "ops", auth.RoleAdmin)
	if rec := do(h, http.MethodGet, "/api/v1/admin/storage", asAdmin); rec.Code != http.StatusOK {
		t.Fatalf("admin refused: %d", rec.Code)
	}
	env.Store.AddUser(p.UserID, "ops", auth.RoleUser)
	assertProblem(t, do(h, http.MethodGet, "/api/v1/admin/storage", asAdmin), http.StatusForbidden)
	env.Store.Suspend(p.UserID)
	rec := do(h, http.MethodGet, "/api/v1/admin/storage", asAdmin)
	assertProblem(t, rec, http.StatusUnauthorized)
	if c := rec.Result().Cookies(); len(c) == 0 {
		t.Fatal("stale session cookie not cleared")
	}
}

// sessionCookie extracts the session cookie value set by a request modifier.
func sessionCookie(t *testing.T, mod func(*http.Request), c auth.Cookies) string {
	t.Helper()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	mod(r)
	ck, err := r.Cookie(c.SessionName())
	if err != nil {
		t.Fatal(err)
	}
	return ck.Value
}
