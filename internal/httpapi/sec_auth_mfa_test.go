package httpapi

import (
	"encoding/base32"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/auth/mfa"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/identity/identitytest"
)

// mfaClock is the TOTP clock of a test: real time plus whole steps.
type mfaClock struct {
	base time.Time
	off  atomic.Int64
}

func (c *mfaClock) now() time.Time { return c.base.Add(time.Duration(c.off.Load()) * mfa.TOTPPeriod) }
func (c *mfaClock) next()          { c.off.Add(1) }

type mfaEnv struct {
	*identitytest.Env
	h     http.Handler
	clock *mfaClock
}

func mfaRouter(t *testing.T, mod func(*config.Config)) *mfaEnv {
	t.Helper()
	t.Parallel()
	env := identitytest.New(t, func(c *config.Config) {
		c.Mail.Enabled = false
		if mod != nil {
			mod(c)
		}
	})
	clock := &mfaClock{base: time.Now()}
	svc, err := mfa.New(mfa.Options{Pool: env.Pool, Identity: env.Service, Limiter: env.Limiter,
		Config: env.Config, Now: clock.now})
	if err != nil {
		t.Fatal(err)
	}
	h := newTestRouter(t, func(d *Deps) {
		d.Identity = env.Service
		d.MFA = svc
		d.Auth = &auth.Middleware{Sessions: env.Sessions, Cookies: auth.CookiesFor(env.Config.Server.PublicURL)}
		d.Storage = &fakeStorage{}
		d.Meta.Registration = env.Config.Security.Registration
	})
	return &mfaEnv{Env: env, h: h, clock: clock}
}

func (e *mfaEnv) code(secret []byte) string { return mfa.HOTP(secret, mfa.TOTPStep(e.clock.now())) }

func decodeJSON[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return v
}

// enableTOTP turns on the authenticator app of the signed-in browser b and
// returns its secret and recovery codes.
func (e *mfaEnv) enableTOTP(t *testing.T, b *browser) ([]byte, []string) {
	t.Helper()
	rec := b.do(http.MethodPost, "/api/v1/user/security/totp", map[string]string{"password": testPassword})
	if rec.Code != http.StatusOK {
		t.Fatalf("setup totp: %d %s", rec.Code, rec.Body)
	}
	setup := decodeJSON[struct{ Secret, URI string }](t, rec.Body.Bytes())
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(setup.Secret)
	if err != nil {
		t.Fatal(err)
	}
	rec = b.do(http.MethodPost, "/api/v1/user/security/totp/confirm", map[string]string{"code": e.code(secret)})
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm totp: %d %s", rec.Code, rec.Body)
	}
	codes := decodeJSON[struct{ Codes []string }](t, rec.Body.Bytes()).Codes
	if len(codes) != mfa.RecoveryCodeCount {
		t.Fatalf("%d recovery codes", len(codes))
	}
	return secret, codes
}

// pendingLogin signs in with the password and expects the 202 of a login
// waiting for its second factor, with no session.
func pendingLogin(t *testing.T, h http.Handler, ip string) *browser {
	t.Helper()
	b := newBrowser(t, h, ip).prime()
	rec := b.login("boss", testPassword)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	if b.session() != "" {
		t.Fatal("session cookie set before the second factor")
	}
	if b.cookies["__Host-"+mfa.CookieBase] == "" {
		t.Fatal("no pending login cookie")
	}
	return b
}

func TestSecAuthMFATOTPReplay(t *testing.T) {
	e := mfaRouter(t, nil)
	admin := withAdmin(t, e.h)
	secret, _ := e.enableTOTP(t, admin)

	// The code used to confirm the setup cannot sign in within its window.
	b := pendingLogin(t, e.h, "198.51.100.20")
	used := e.code(secret)
	assertProblem(t, b.do(http.MethodPost, "/api/v1/auth/mfa/totp", map[string]string{"code": used}), http.StatusUnauthorized)

	e.clock.next()
	code := e.code(secret)
	if rec := b.do(http.MethodPost, "/api/v1/auth/mfa/totp", map[string]string{"code": code}); rec.Code != http.StatusOK || b.session() == "" {
		t.Fatalf("verify totp: %d %s", rec.Code, rec.Body)
	}
	if b.cookies["__Host-"+mfa.CookieBase] != "" {
		t.Fatal("pending login cookie kept after the login")
	}
	if rec := b.do(http.MethodGet, "/api/v1/user", nil); rec.Code != http.StatusOK {
		t.Fatalf("user after mfa: %d", rec.Code)
	}

	// Same code, same window, from another login: rejected.
	c := pendingLogin(t, e.h, "198.51.100.21")
	assertProblem(t, c.do(http.MethodPost, "/api/v1/auth/mfa/totp", map[string]string{"code": code}), http.StatusUnauthorized)
	// Nor as the reauthentication of a sensitive change.
	assertProblem(t, admin.do(http.MethodPost, "/api/v1/user/security/recovery-codes", map[string]string{"code": code}), http.StatusForbidden)
	// An older step after a newer one: rejected too.
	assertProblem(t, c.do(http.MethodPost, "/api/v1/auth/mfa/totp", map[string]string{"code": mfa.HOTP(secret, mfa.TOTPStep(e.clock.now())-1)}), http.StatusUnauthorized)
	e.clock.next()
	if rec := c.do(http.MethodPost, "/api/v1/auth/mfa/totp", map[string]string{"code": e.code(secret)}); rec.Code != http.StatusOK {
		t.Fatalf("next window: %d %s", rec.Code, rec.Body)
	}
}

func TestSecAuthMFARecoveryReuse(t *testing.T) {
	e := mfaRouter(t, nil)
	admin := withAdmin(t, e.h)
	_, codes := e.enableTOTP(t, admin)

	b := pendingLogin(t, e.h, "198.51.100.30")
	if rec := b.do(http.MethodPost, "/api/v1/auth/mfa/recovery", map[string]string{"code": codes[0]}); rec.Code != http.StatusOK || b.session() == "" {
		t.Fatalf("recovery: %d %s", rec.Code, rec.Body)
	}
	c := pendingLogin(t, e.h, "198.51.100.31")
	assertProblem(t, c.do(http.MethodPost, "/api/v1/auth/mfa/recovery", map[string]string{"code": codes[0]}), http.StatusUnauthorized)
	// A used code does not reauthenticate either.
	assertProblem(t, admin.do(http.MethodPost, "/api/v1/user/security/recovery-codes", map[string]string{"code": codes[0]}), http.StatusForbidden)

	st := decodeJSON[struct{ RecoveryCodesLeft, RecoveryCodesTotal int }](t, admin.do(http.MethodGet, "/api/v1/user/security", nil).Body.Bytes())
	if st.RecoveryCodesLeft != mfa.RecoveryCodeCount-1 || st.RecoveryCodesTotal != mfa.RecoveryCodeCount {
		t.Fatalf("status %+v", st)
	}

	// Regenerating revokes the old set.
	rec := admin.do(http.MethodPost, "/api/v1/user/security/recovery-codes", map[string]string{"password": testPassword})
	if rec.Code != http.StatusOK {
		t.Fatalf("regenerate: %d %s", rec.Code, rec.Body)
	}
	assertProblem(t, c.do(http.MethodPost, "/api/v1/auth/mfa/recovery", map[string]string{"code": codes[1]}), http.StatusUnauthorized)
	fresh := decodeJSON[struct{ Codes []string }](t, rec.Body.Bytes()).Codes
	if rec := c.do(http.MethodPost, "/api/v1/auth/mfa/recovery", map[string]string{"code": fresh[0]}); rec.Code != http.StatusOK {
		t.Fatalf("fresh code: %d %s", rec.Code, rec.Body)
	}
}

func TestSecAuthMFABypass(t *testing.T) {
	e := mfaRouter(t, nil)
	admin := withAdmin(t, e.h)
	secret, codes := e.enableTOTP(t, admin)
	e.clock.next()

	b := pendingLogin(t, e.h, "198.51.100.40")
	pending := b.cookies["__Host-"+mfa.CookieBase]
	// The pending login is not a session.
	assertProblem(t, b.do(http.MethodGet, "/api/v1/user", nil), http.StatusUnauthorized)
	assertProblem(t, b.do(http.MethodGet, "/api/v1/user/security", nil), http.StatusUnauthorized)
	forged := b.clone()
	forged.cookies["__Host-"+auth.SessionCookie] = pending
	assertProblem(t, forged.do(http.MethodGet, "/api/v1/user", nil), http.StatusUnauthorized)
	// Its token is no passkey challenge and no enrollment.
	assertProblem(t, b.do(http.MethodPost, "/api/v1/auth/mfa/passkey", map[string]any{"challenge": pending, "credential": map[string]any{"id": "x"}}), http.StatusUnauthorized)
	assertProblem(t, b.do(http.MethodPost, "/api/v1/auth/mfa/enroll/totp", nil), http.StatusUnauthorized)
	// No cookie, no second step.
	anon := newBrowser(t, e.h, "198.51.100.41").prime()
	assertProblem(t, anon.do(http.MethodPost, "/api/v1/auth/mfa/totp", map[string]string{"code": e.code(secret)}), http.StatusUnauthorized)
	assertProblem(t, anon.do(http.MethodGet, "/api/v1/auth/mfa", nil), http.StatusUnauthorized)
	// The CSRF check applies to the second step.
	noCSRF := b.clone()
	noCSRF.noCSRF = true
	if rec := noCSRF.do(http.MethodPost, "/api/v1/auth/mfa/totp", map[string]string{"code": e.code(secret)}); rec.Code != http.StatusForbidden {
		t.Fatalf("no csrf: %d", rec.Code)
	}

	if rec := b.do(http.MethodGet, "/api/v1/auth/mfa", nil); rec.Code != http.StatusOK {
		t.Fatalf("challenge: %d %s", rec.Code, rec.Body)
	}
	if rec := b.do(http.MethodPost, "/api/v1/auth/mfa/totp", map[string]string{"code": e.code(secret)}); rec.Code != http.StatusOK {
		t.Fatalf("verify: %d %s", rec.Code, rec.Body)
	}
	// The pending login is used up.
	replay := newBrowser(t, e.h, "198.51.100.42").prime()
	replay.cookies["__Host-"+mfa.CookieBase] = pending
	assertProblem(t, replay.do(http.MethodPost, "/api/v1/auth/mfa/recovery", map[string]string{"code": codes[0]}), http.StatusUnauthorized)

	// Wrong codes burn the pending login after MaxAttempts.
	c := pendingLogin(t, e.h, "198.51.100.43")
	for range mfa.MaxAttempts {
		assertProblem(t, c.do(http.MethodPost, "/api/v1/auth/mfa/recovery", map[string]string{"code": "aaaa-aaaa-aaaa-aaaa"}), http.StatusUnauthorized)
	}
	rec := c.do(http.MethodPost, "/api/v1/auth/mfa/recovery", map[string]string{"code": codes[1]})
	if rec.Code == http.StatusOK || c.session() != "" {
		t.Fatalf("login after %d wrong codes: %d", mfa.MaxAttempts, rec.Code)
	}

	// Removing the factor needs a reauthentication.
	assertProblem(t, admin.do(http.MethodDelete, "/api/v1/user/security/totp", map[string]string{"password": "wrong password!"}), http.StatusForbidden)
	assertProblem(t, admin.do(http.MethodDelete, "/api/v1/user/security/totp", map[string]string{}), http.StatusForbidden)
	if rec := admin.do(http.MethodDelete, "/api/v1/user/security/totp", map[string]string{"password": testPassword}); rec.Code != http.StatusNoContent {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
	if rec := newBrowser(t, e.h, "198.51.100.44").prime().login("boss", testPassword); rec.Code != http.StatusOK {
		t.Fatalf("login without factor: %d", rec.Code)
	}
}

func TestSecAuthMFAPolicyEnroll(t *testing.T) {
	e := mfaRouter(t, func(c *config.Config) { c.Auth.Require2FA = config.Require2FAAdmins })
	b := newBrowser(t, e.h, "198.51.100.50").prime()
	if rec := b.register("boss", "boss@example.com", testPassword); rec.Code != http.StatusAccepted {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	rec := b.login("boss", testPassword)
	if rec.Code != http.StatusAccepted || b.session() != "" {
		t.Fatalf("admin without factor: %d %s", rec.Code, rec.Body)
	}
	ch := decodeJSON[struct {
		EnrollmentRequired bool `json:"enrollmentRequired"`
		Methods            []string
	}](t, rec.Body.Bytes())
	if !ch.EnrollmentRequired || len(ch.Methods) != 1 || ch.Methods[0] != mfa.MethodTOTP {
		t.Fatalf("challenge %+v", ch)
	}
	// An enrollment is not a second factor.
	assertProblem(t, b.do(http.MethodPost, "/api/v1/auth/mfa/totp", map[string]string{"code": "123456"}), http.StatusUnauthorized)
	rec = b.do(http.MethodPost, "/api/v1/auth/mfa/enroll/totp", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("enroll: %d %s", rec.Code, rec.Body)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(decodeJSON[struct{ Secret string }](t, rec.Body.Bytes()).Secret)
	if err != nil {
		t.Fatal(err)
	}
	assertProblem(t, b.do(http.MethodPost, "/api/v1/auth/mfa/enroll/totp/confirm", map[string]string{"code": "000000"}), http.StatusUnauthorized)
	rec = b.do(http.MethodPost, "/api/v1/auth/mfa/enroll/totp/confirm", map[string]string{"code": e.code(secret)})
	if rec.Code != http.StatusOK || b.session() == "" {
		t.Fatalf("confirm enroll: %d %s", rec.Code, rec.Body)
	}
	if n := len(decodeJSON[struct{ RecoveryCodes []string }](t, rec.Body.Bytes()).RecoveryCodes); n != mfa.RecoveryCodeCount {
		t.Fatalf("%d recovery codes", n)
	}
	// The last factor of an account that must have one stays.
	assertProblem(t, b.do(http.MethodDelete, "/api/v1/user/security/totp", map[string]string{"password": testPassword}), http.StatusConflict)
	st := decodeJSON[struct{ Required, Totp bool }](t, b.do(http.MethodGet, "/api/v1/user/security", nil).Body.Bytes())
	if !st.Required || !st.Totp {
		t.Fatalf("status %+v", st)
	}
}
