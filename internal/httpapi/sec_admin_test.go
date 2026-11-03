package httpapi

import (
	"net/http"
	"testing"

	"github.com/Ervisio/ErvisioRepo/internal/config"
)

var configLocalOnly = config.Storage{
	Targets: []config.Target{{Name: "disk", Kind: "local"}},
	Policy:  map[string]config.Policy{"lfs": {Primary: "disk"}},
}

// TestSecAdminLoopbackOnly: until admin auth exists (phase 2), the admin API
// must refuse remote and proxied clients before touching the storage.
func TestSecAdminLoopbackOnly(t *testing.T) {
	fs := &fakeStorage{}
	h := newTestRouter(t, func(d *Deps) { d.Storage = fs })
	cases := map[string]func(*http.Request){
		"remote":          func(*http.Request) {},
		"x-forwarded-for": func(r *http.Request) { fromLoopback(r); r.Header.Set("X-Forwarded-For", "203.0.113.7") },
		"forwarded":       func(r *http.Request) { fromLoopback(r); r.Header.Set("Forwarded", "for=203.0.113.7") },
		"x-real-ip":       func(r *http.Request) { fromLoopback(r); r.Header.Set("X-Real-Ip", "203.0.113.7") },
		"bad remote addr": func(r *http.Request) { r.RemoteAddr = "localhost" },
		// DNS rebinding: loopback connection, foreign Host.
		"rebound host": func(r *http.Request) { fromLoopback(r); r.Host = "evil.example:3400" },
		"no host":      func(r *http.Request) { fromLoopback(r); r.Host = "" },
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			assertProblem(t, do(h, http.MethodGet, "/api/v1/admin/storage", mod), http.StatusForbidden)
			assertProblem(t, do(h, http.MethodPost, "/api/v1/admin/storage/disk/test", mod), http.StatusForbidden)
		})
	}
	if len(fs.checked) != 0 {
		t.Fatalf("storage reached from a remote client: %v", fs.checked)
	}
	if rec := do(h, http.MethodGet, "/api/v1/admin/storage", func(r *http.Request) { r.RemoteAddr = "[::1]:4000"; r.Host = "[::1]:3400" }); rec.Code != http.StatusOK {
		t.Fatalf("IPv6 loopback refused: %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/v1/admin/storage", func(r *http.Request) { fromLoopback(r); r.Host = "localhost:5173" }); rec.Code != http.StatusOK {
		t.Fatalf("localhost host refused: %d", rec.Code)
	}
	// CSRF: a cross-site page in the admin's browser cannot trigger a test.
	for _, site := range []string{"cross-site", "same-site"} {
		assertProblem(t, do(h, http.MethodPost, "/api/v1/admin/storage/disk/test", func(r *http.Request) {
			fromLoopback(r)
			r.Header.Set("Sec-Fetch-Site", site)
		}), http.StatusForbidden)
	}
	if len(fs.checked) != 0 {
		t.Fatalf("storage reached from a cross-site request: %v", fs.checked)
	}
	if rec := do(h, http.MethodPost, "/api/v1/admin/storage/disk/test", func(r *http.Request) {
		fromLoopback(r)
		r.Header.Set("Sec-Fetch-Site", "same-origin")
	}); rec.Code != http.StatusOK {
		t.Fatalf("same-origin test refused: %d %s", rec.Code, rec.Body)
	}
	// Public endpoints are unaffected.
	if rec := do(h, http.MethodGet, "/api/v1/meta"); rec.Code != http.StatusOK {
		t.Fatalf("meta: %d", rec.Code)
	}
}
