package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/identity/identitytest"
)

// browser drives the router like a browser: it keeps the cookies it is
// given and echoes the CSRF cookie in the X-CSRF-Token header.
type browser struct {
	t       *testing.T
	h       http.Handler
	ip      string
	cookies map[string]string
	noCSRF  bool
}

func newBrowser(t *testing.T, h http.Handler, ip string) *browser {
	return &browser{t: t, h: h, ip: ip, cookies: map[string]string{}}
}

func (b *browser) clone() *browser {
	c := *b
	c.cookies = map[string]string{}
	for k, v := range b.cookies {
		c.cookies[k] = v
	}
	return &c
}

func (b *browser) do(method, target string, body any, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	b.t.Helper()
	var req *http.Request
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			b.t.Fatal(err)
		}
		req = httptest.NewRequest(method, target, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	req.RemoteAddr = b.ip + ":5555"
	for k, v := range b.cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	if v, ok := b.cookies["__Host-"+auth.CSRFCookie]; ok && !b.noCSRF {
		req.Header.Set(auth.CSRFHeader, v)
	}
	for _, m := range mods {
		m(req)
	}
	rec := httptest.NewRecorder()
	b.h.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 || c.Value == "" {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c.Value
		}
	}
	return rec
}

// prime fetches a page so the browser gets its CSRF cookie.
func (b *browser) prime() *browser {
	b.t.Helper()
	if rec := b.do(http.MethodGet, "/api/v1/meta", nil); rec.Code != http.StatusOK {
		b.t.Fatalf("meta: %d", rec.Code)
	}
	return b
}

func (b *browser) login(login, password string) *httptest.ResponseRecorder {
	b.t.Helper()
	return b.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"login": login, "password": password})
}

func (b *browser) register(name, email, password string) *httptest.ResponseRecorder {
	b.t.Helper()
	return b.do(http.MethodPost, "/api/v1/auth/register", map[string]string{"username": name, "email": email, "password": password})
}

func (b *browser) session() string { return b.cookies["__Host-"+auth.SessionCookie] }

const testPassword = "correct horse battery"

// identityRouter is a router on a real identity service and database.
func identityRouter(t *testing.T, mod func(*config.Config)) (http.Handler, *identitytest.Env) {
	t.Helper()
	t.Parallel()
	env := identitytest.New(t, mod)
	h := newTestRouter(t, func(d *Deps) {
		d.Identity = env.Service
		d.Auth = &auth.Middleware{Sessions: env.Sessions, Cookies: auth.CookiesFor(env.Config.Server.PublicURL)}
		d.Storage = &fakeStorage{}
		d.Meta.Registration = env.Config.Security.Registration
	})
	return h, env
}

// withAdmin registers the first account (the admin) and returns a browser
// signed in as it.
func withAdmin(t *testing.T, h http.Handler) *browser {
	t.Helper()
	b := newBrowser(t, h, "198.51.100.10").prime()
	if rec := b.register("boss", "boss@example.com", testPassword); rec.Code != http.StatusAccepted {
		t.Fatalf("register admin: %d %s", rec.Code, rec.Body)
	}
	if rec := b.login("boss", testPassword); rec.Code != http.StatusOK {
		t.Fatalf("login admin: %d %s", rec.Code, rec.Body)
	}
	return b
}

func TestAuthFlowHTTP(t *testing.T) {
	h, _ := identityRouter(t, func(c *config.Config) { c.Mail.Enabled = false })
	admin := withAdmin(t, h)

	rec := admin.do(http.MethodGet, "/api/v1/user", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("user: %d %s", rec.Code, rec.Body)
	}
	var u struct {
		Username, Role string
		EmailVerified  bool `json:"emailVerified"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil || u.Username != "boss" || u.Role != "admin" || !u.EmailVerified {
		t.Fatalf("user = %+v %v", u, err)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "" && !strings.Contains(cc, "no-store") {
		t.Logf("cache-control %q", cc)
	}

	// Admin creates an invite; a new user registers with it.
	rec = admin.do(http.MethodPost, "/api/v1/admin/invites", map[string]string{})
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite: %d %s", rec.Code, rec.Body)
	}
	var inv struct{ Token string }
	_ = json.Unmarshal(rec.Body.Bytes(), &inv)
	b := newBrowser(t, h, "198.51.100.11").prime()
	if rec := b.register("alice", "alice@example.com", testPassword); rec.Code != http.StatusForbidden {
		t.Fatalf("register without invite: %d", rec.Code)
	}
	rec = b.do(http.MethodPost, "/api/v1/auth/register", map[string]string{"username": "alice", "email": "alice@example.com",
		"password": testPassword, "inviteToken": inv.Token})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	if rec := b.login("alice", testPassword); rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	// A plain user cannot use the admin API.
	assertProblem(t, b.do(http.MethodGet, "/api/v1/admin/storage", nil), http.StatusForbidden)
	assertProblem(t, b.do(http.MethodPost, "/api/v1/admin/invites", map[string]string{}), http.StatusForbidden)
	if rec := admin.do(http.MethodGet, "/api/v1/admin/storage", nil); rec.Code != http.StatusOK {
		t.Fatalf("admin storage: %d %s", rec.Code, rec.Body)
	}

	// Errors are problems.
	assertProblem(t, b.register("x", "not-an-email", testPassword), http.StatusBadRequest)
	assertProblem(t, newBrowser(t, h, "198.51.100.12").prime().do(http.MethodGet, "/api/v1/user", nil), http.StatusUnauthorized)
}
