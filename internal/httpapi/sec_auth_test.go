package httpapi

import (
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/config"
)

// TestSecAuthBruteForce: repeated failures are slowed down per account
// (whatever the client IP) and per IP (whatever the account), and the
// backoff also blocks the right password.
func TestSecAuthBruteForce(t *testing.T) {
	h, _ := identityRouter(t, func(c *config.Config) {
		c.Mail.Enabled = false
		c.Auth.LoginFreeFailuresAccount = 3
		c.Auth.LoginFreeFailuresIP = 6
		c.Auth.LoginBackoffBase = config.Duration(time.Minute)
		c.Auth.LoginBackoffMax = config.Duration(time.Hour)
	})
	withAdmin(t, h)

	// Per account, from rotating IPs: three free failures, the fourth
	// starts the backoff.
	for i := range 4 {
		b := newBrowser(t, h, "203.0.113."+strconv.Itoa(i+1)).prime()
		assertProblem(t, b.login("boss", "wrong password!"), http.StatusUnauthorized)
	}
	b := newBrowser(t, h, "203.0.113.50").prime()
	rec := b.login("boss", testPassword)
	assertProblem(t, rec, http.StatusTooManyRequests)
	if ra, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || ra < 1 || ra > 60 {
		t.Fatalf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
	// Through the email address too: the account key is the user id.
	assertProblem(t, b.login("boss@example.com", testPassword), http.StatusTooManyRequests)
	if b.session() != "" {
		t.Fatal("session issued during backoff")
	}

	// Per IP, across many (unknown) accounts.
	ip := newBrowser(t, h, "192.0.2.77").prime()
	for i := range 7 {
		assertProblem(t, ip.login("user"+strconv.Itoa(i), "wrong password!"), http.StatusUnauthorized)
	}
	assertProblem(t, ip.login("someone-else", "wrong password!"), http.StatusTooManyRequests)
	// Behind an untrusted proxy, X-Forwarded-For does not escape the limit.
	rec = ip.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"login": "x", "password": "y"},
		func(r *http.Request) { r.Header.Set("X-Forwarded-For", "10.9.9.9") })
	assertProblem(t, rec, http.StatusTooManyRequests)
}

// TestSecAuthSessionReuse: a session cookie is useless after logout, after
// a new login rotated it and after a password reset; cookies carry the
// hardened attributes.
func TestSecAuthSessionReuse(t *testing.T) {
	h, env := identityRouter(t, func(c *config.Config) { c.Mail.Enabled = false })
	admin := withAdmin(t, h)

	var sess *http.Cookie
	rec := newBrowser(t, h, "198.51.100.10").prime().login("boss", testPassword)
	for _, c := range rec.Result().Cookies() {
		switch c.Name {
		case "__Host-" + auth.SessionCookie:
			sess = c
		case "__Host-" + auth.CSRFCookie:
			if !c.Secure || c.Path != "/" || c.Domain != "" || c.SameSite != http.SameSiteLaxMode {
				t.Fatalf("csrf cookie = %+v", c)
			}
		}
	}
	if sess == nil || !sess.HttpOnly || !sess.Secure || sess.Path != "/" || sess.Domain != "" || sess.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %+v", sess)
	}
	if !auth.ValidTokenFormat(sess.Value) {
		t.Fatalf("session token format %q", sess.Value)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("login response cacheable")
	}

	// Rotation: logging in again from the same browser kills the old session.
	stale := admin.clone()
	if rec := admin.login("boss", testPassword); rec.Code != http.StatusOK {
		t.Fatalf("relogin: %d", rec.Code)
	}
	if admin.session() == stale.session() {
		t.Fatal("session not rotated at login")
	}
	assertProblem(t, stale.do(http.MethodGet, "/api/v1/user", nil), http.StatusUnauthorized)
	if _, ok := stale.cookies["__Host-"+auth.SessionCookie]; ok {
		t.Fatal("stale session cookie not cleared")
	}

	// Logout.
	before := admin.clone()
	if rec := admin.do(http.MethodPost, "/api/v1/auth/logout", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("logout: %d %s", rec.Code, rec.Body)
	}
	if admin.session() != "" {
		t.Fatal("logout left the session cookie")
	}
	assertProblem(t, before.do(http.MethodGet, "/api/v1/user", nil), http.StatusUnauthorized)
	// Replaying the old cookie with its CSRF token does not reach admin ops:
	// the dead session is treated as anonymous.
	assertProblem(t, before.do(http.MethodPost, "/api/v1/admin/invites", map[string]string{}), http.StatusUnauthorized)

	// A raw database token hash is not a valid cookie.
	forged := newBrowser(t, h, "198.51.100.10").prime()
	forged.cookies["__Host-"+auth.SessionCookie] = string(auth.HashToken(sess.Value))
	assertProblem(t, forged.do(http.MethodGet, "/api/v1/user", nil), http.StatusUnauthorized)

	// Expiry: the idle timeout is enforced on lookup.
	b := newBrowser(t, h, "198.51.100.10").prime()
	b.login("boss", testPassword)
	env.Sessions.Now = func() time.Time {
		return time.Now().Add(time.Duration(env.Config.Auth.SessionIdleTimeout) + time.Minute)
	}
	assertProblem(t, b.do(http.MethodGet, "/api/v1/user", nil), http.StatusUnauthorized)
	env.Sessions.Now = nil
}

// TestSecAuthCSRF: unsafe requests need the CSRF header matching the cookie
// and, with a session, the session's own token; cross-site requests are
// refused outright.
func TestSecAuthCSRF(t *testing.T) {
	h, _ := identityRouter(t, func(c *config.Config) { c.Mail.Enabled = false })
	admin := withAdmin(t, h)

	anon := newBrowser(t, h, "198.51.100.20")
	anon.noCSRF = true
	assertProblem(t, anon.login("boss", testPassword), http.StatusForbidden) // no cookie, no header
	anon.prime()
	assertProblem(t, anon.login("boss", testPassword), http.StatusForbidden) // cookie, no header
	anon.noCSRF = false
	assertProblem(t, anon.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"login": "boss", "password": testPassword},
		func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }), http.StatusForbidden)
	assertProblem(t, anon.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"login": "boss", "password": testPassword},
		func(r *http.Request) { r.Header.Set(auth.CSRFHeader, "AAAA") }), http.StatusForbidden)

	// Double submit with an attacker-chosen pair is refused on a session.
	planted, _ := auth.NewToken()
	for _, path := range []string{"/api/v1/auth/logout", "/api/v1/admin/invites", "/api/v1/admin/storage/disk/test"} {
		assertProblem(t, admin.do(http.MethodPost, path, map[string]string{}, func(r *http.Request) {
			r.Header.Del("Cookie")
			r.AddCookie(&http.Cookie{Name: "__Host-" + auth.SessionCookie, Value: admin.session()})
			r.AddCookie(&http.Cookie{Name: "__Host-" + auth.CSRFCookie, Value: planted})
			r.Header.Set(auth.CSRFHeader, planted)
		}), http.StatusForbidden)
	}
	// The session still works with its real token.
	if rec := admin.do(http.MethodPost, "/api/v1/admin/invites", map[string]string{}); rec.Code != http.StatusCreated {
		t.Fatalf("invite with valid token: %d %s", rec.Code, rec.Body)
	}
	// Safe methods never need the token.
	admin.noCSRF = true
	if rec := admin.do(http.MethodGet, "/api/v1/user", nil); rec.Code != http.StatusOK {
		t.Fatalf("GET without token: %d", rec.Code)
	}
}

// TestSecAuthEnumeration: the answers of login, password reset, resend and
// register do not reveal whether an account or address exists, by status,
// body or (roughly) timing.
func TestSecAuthEnumeration(t *testing.T) {
	h, _ := identityRouter(t, func(c *config.Config) {
		c.Security.Registration = config.RegistrationOpen
		c.Auth.LoginFreeFailuresAccount = 1000
		c.Auth.LoginFreeFailuresIP = 1000
		c.Auth.ActionsPerIPPerHour = 1000
		// Realistic hash cost so that it dominates the timing.
		c.Auth.Argon2Memory = 8 << 20
		c.Auth.Argon2Iterations = 2
	})
	withAdmin(t, h)
	b := newBrowser(t, h, "198.51.100.30").prime()

	same := func(what string, a, c func() (int, string)) {
		t.Helper()
		sa, ba := a()
		sc, bc := c()
		if sa != sc || ba != bc {
			t.Fatalf("%s differs: %d %s vs %d %s", what, sa, ba, sc, bc)
		}
	}
	call := func(path string, body map[string]string) func() (int, string) {
		return func() (int, string) {
			rec := b.do(http.MethodPost, path, body)
			return rec.Code, rec.Body.String()
		}
	}
	same("login", call("/api/v1/auth/login", map[string]string{"login": "boss", "password": "wrong password!"}),
		call("/api/v1/auth/login", map[string]string{"login": "nobody", "password": "wrong password!"}))
	same("login by email", call("/api/v1/auth/login", map[string]string{"login": "boss@example.com", "password": "wrong password!"}),
		call("/api/v1/auth/login", map[string]string{"login": "nobody@example.com", "password": "wrong password!"}))
	same("reset", call("/api/v1/auth/password/reset", map[string]string{"email": "boss@example.com"}),
		call("/api/v1/auth/password/reset", map[string]string{"email": "nobody@example.com"}))
	same("resend", call("/api/v1/auth/email/resend", map[string]string{"email": "boss@example.com"}),
		call("/api/v1/auth/email/resend", map[string]string{"email": "nobody@example.com"}))
	same("register", call("/api/v1/auth/register", map[string]string{"username": "dup", "email": "BOSS@example.com", "password": testPassword}),
		call("/api/v1/auth/register", map[string]string{"username": "fresh", "email": "fresh@example.com", "password": testPassword}))
	if rec := b.login("dup", testPassword); rec.Code != http.StatusUnauthorized {
		t.Fatalf("duplicate email created an account: %d", rec.Code)
	}

	// Timing: unknown accounts pay the same password hash as known ones.
	median := func(login string) time.Duration {
		var d []time.Duration
		for range 9 {
			start := time.Now()
			b.login(login, "wrong password!")
			d = append(d, time.Since(start))
		}
		slices.Sort(d)
		return d[len(d)/2]
	}
	median("boss") // warm up
	known, unknown := median("boss"), median("nobody")
	if ratio := float64(max(known, unknown)) / float64(min(known, unknown)); ratio > 3 {
		t.Fatalf("login timing reveals accounts: known %v, unknown %v", known, unknown)
	}
	regTime := func(email string, i int) time.Duration {
		start := time.Now()
		b.register("timing"+strconv.Itoa(i), email, testPassword)
		return time.Since(start)
	}
	var dup, fresh []time.Duration
	for i := range 7 {
		dup = append(dup, regTime("boss@example.com", 2*i))
		fresh = append(fresh, regTime("t"+strconv.Itoa(i)+"@example.com", 2*i+1))
	}
	slices.Sort(dup)
	slices.Sort(fresh)
	if ratio := float64(max(dup[3], fresh[3])) / float64(min(dup[3], fresh[3])); ratio > 3 {
		t.Fatalf("register timing reveals addresses: dup %v, fresh %v", dup[3], fresh[3])
	}
}
