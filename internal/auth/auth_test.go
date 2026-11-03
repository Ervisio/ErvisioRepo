package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/auth/authtest"
)

func testHasher(t *testing.T) *auth.Hasher {
	t.Helper()
	h, err := auth.NewHasher(auth.Argon2Params{MemoryKiB: 64, Iterations: 1, Parallelism: 1}, 2)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHasher(t *testing.T) {
	ctx := context.Background()
	if _, err := auth.NewHasher(auth.Argon2Params{MemoryKiB: 4, Iterations: 1, Parallelism: 1}, 1); err == nil {
		t.Fatal("weak parameters accepted")
	}
	h := testHasher(t)
	enc, err := h.Hash(ctx, "secret password")
	if err != nil || !strings.HasPrefix(enc, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatalf("hash %q %v", enc, err)
	}
	if again, _ := h.Hash(ctx, "secret password"); again == enc {
		t.Fatal("salt not random")
	}
	if ok, rehash, err := h.Verify(ctx, enc, "secret password"); !ok || rehash || err != nil {
		t.Fatalf("verify = %v %v %v", ok, rehash, err)
	}
	if ok, _, _ := h.Verify(ctx, enc, "Secret password"); ok {
		t.Fatal("wrong password accepted")
	}
	if _, _, err := h.Verify(ctx, "plain", "x"); err != auth.ErrMalformedHash {
		t.Fatalf("malformed: %v", err)
	}
	if _, err := h.Hash(ctx, strings.Repeat("a", auth.MaxPasswordBytes+1)); err == nil {
		t.Fatal("overlong password hashed")
	}
	long := strings.Repeat("a", auth.MaxPasswordBytes)
	encLong, _ := h.Hash(ctx, long)
	if ok, _, _ := h.Verify(ctx, encLong, long+"b"); ok {
		t.Fatal("overlong password matched its truncation")
	}

	// Parameters changed: the old hash still verifies and asks for a rehash.
	h2, _ := auth.NewHasher(auth.Argon2Params{MemoryKiB: 128, Iterations: 1, Parallelism: 1}, 1)
	if ok, rehash, _ := h2.Verify(ctx, enc, "secret password"); !ok || !rehash {
		t.Fatalf("rehash not requested: %v %v", ok, rehash)
	}
	h.VerifyDummy(ctx, "anything")

}

func TestTokens(t *testing.T) {
	a, ha := auth.NewToken()
	b, _ := auth.NewToken()
	if a == b || len(a) != 43 || len(ha) != 32 || !auth.ValidTokenFormat(a) {
		t.Fatalf("token %q", a)
	}
	if string(auth.HashToken(a)) != string(ha) {
		t.Fatal("hash mismatch")
	}
	for _, bad := range []string{"", "short", a + "A", strings.Repeat("!", 43)} {
		if auth.ValidTokenFormat(bad) {
			t.Errorf("ValidTokenFormat(%q)", bad)
		}
	}
	if !auth.EqualTokens(a, a) || auth.EqualTokens(a, b) || auth.EqualTokens(a, a[:42]) {
		t.Fatal("EqualTokens")
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := auth.NewLimiter(auth.LimiterConfig{
		FreeFailuresAccount: 2, FreeFailuresIP: 3, BackoffBase: time.Second, BackoffMax: 4 * time.Second, ActionsPerHour: 2,
	}, func() time.Time { return now })
	acct, ip := auth.AccountKey("bob"), auth.IPKey("192.0.2.1")
	l.Fail(acct, ip)
	l.Fail(acct, ip)
	if w := l.LoginWait(acct, ip); w != 0 {
		t.Fatalf("free failures blocked: %v", w)
	}
	l.Fail(acct, ip)
	if w := l.LoginWait(acct); w != time.Second {
		t.Fatalf("first backoff %v", w)
	}
	if w := l.LoginWait(ip); w != 0 {
		t.Fatalf("ip blocked early: %v", w)
	}
	for range 5 {
		l.Fail(acct)
	}
	if w := l.LoginWait(acct); w != 4*time.Second {
		t.Fatalf("backoff not capped: %v", w)
	}
	now = now.Add(5 * time.Second)
	if w := l.LoginWait(acct); w != 0 {
		t.Fatalf("backoff did not expire: %v", w)
	}
	l.Reset(acct)
	l.Fail(acct)
	if w := l.LoginWait(acct); w != 0 {
		t.Fatal("reset ignored")
	}
	// Old failures are forgiven.
	l.Fail(acct)
	now = now.Add(time.Minute)
	l.Fail(acct)
	if w := l.LoginWait(acct); w != 0 {
		t.Fatal("stale failures counted")
	}

	if ok, _ := l.AllowAction("ip:x"); !ok {
		t.Fatal("action 1")
	}
	l.AllowAction("ip:x")
	ok, wait := l.AllowAction("ip:x")
	if ok || wait != time.Hour {
		t.Fatalf("action budget: %v %v", ok, wait)
	}
	now = now.Add(time.Hour)
	if ok, _ := l.AllowAction("ip:x"); !ok {
		t.Fatal("budget not refilled")
	}
}

func TestClientIP(t *testing.T) {
	trusted, err := auth.ParseTrustedProxies([]string{"10.0.0.0/8", "::1/128"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.ParseTrustedProxies([]string{"nope"}); err == nil {
		t.Fatal("bad CIDR accepted")
	}
	cases := []struct {
		remote, xff, want string
	}{
		{"203.0.113.5:1234", "1.2.3.4", "203.0.113.5"},                     // untrusted peer: header ignored
		{"10.1.1.1:80", "198.51.100.7", "198.51.100.7"},                    // trusted proxy
		{"10.1.1.1:80", "6.6.6.6, 198.51.100.7, 10.2.2.2", "198.51.100.7"}, // spoofed left part ignored
		{"10.1.1.1:80", "garbage", "10.1.1.1"},
		{"[::1]:80", "198.51.100.8", "198.51.100.8"},
		{"[::ffff:192.0.2.9]:80", "", "192.0.2.9"},
		{"pipe", "", "pipe"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := auth.ClientIP(r, trusted); got != c.want {
			t.Errorf("ClientIP(%s, %q) = %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
}

func TestCookies(t *testing.T) {
	for url, secure := range map[string]bool{
		"https://git.example.com": true, "http://localhost:3400": true, "http://127.0.0.1": true,
		"http://git.example.com": false, "::bad": false,
	} {
		if c := auth.CookiesFor(url); c.Secure != secure {
			t.Errorf("CookiesFor(%s) = %v", url, c.Secure)
		}
	}
	c := auth.Cookies{Secure: true}
	w := httptest.NewRecorder()
	c.SetSession(w, "tok", time.Now().Add(time.Hour))
	c.SetCSRF(w, "csrf")
	ck := w.Result().Cookies()
	if ck[0].Name != "__Host-erepo_session" || !ck[0].HttpOnly || !ck[0].Secure || ck[0].SameSite != http.SameSiteLaxMode || ck[0].Path != "/" {
		t.Fatalf("session cookie %+v", ck[0])
	}
	if ck[1].Name != "__Host-erepo_csrf" || ck[1].HttpOnly {
		t.Fatalf("csrf cookie %+v", ck[1])
	}
	if (auth.Cookies{}).SessionName() != "erepo_session" {
		t.Fatal("insecure name")
	}
}

func TestSessions(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	st := authtest.NewStore()
	st.Now = clock
	ss := &auth.Sessions{Store: st, Idle: time.Hour, MaxAge: 3 * time.Hour, Now: clock}
	id := uuid.New()
	st.AddUser(id, "alice", auth.RoleUser)

	s, err := ss.Create(ctx, auth.Principal{UserID: id, Username: "alice", Role: auth.RoleUser}, "192.0.2.1", strings.Repeat("u", 600))
	if err != nil {
		t.Fatal(err)
	}
	if !auth.ValidTokenFormat(s.Token) || !auth.ValidTokenFormat(s.CSRFToken) || s.Token == s.CSRFToken {
		t.Fatal("bad secrets")
	}
	got, err := ss.Lookup(ctx, s.Token)
	if err != nil || got.ID != s.ID || got.Principal().Method != auth.MethodSession || got.Principal().SessionID != s.ID {
		t.Fatalf("lookup %+v %v", got, err)
	}
	if _, err := ss.Lookup(ctx, "garbage"); err != auth.ErrNoSession {
		t.Fatal("garbage token")
	}
	if _, err := ss.Lookup(ctx, string(auth.HashToken(s.Token))); err != auth.ErrNoSession {
		t.Fatal("hash as token")
	}
	// Activity keeps the session alive up to the absolute limit.
	for range 2 {
		now = now.Add(50 * time.Minute)
		if _, err := ss.Lookup(ctx, s.Token); err != nil {
			t.Fatalf("active session expired: %v", err)
		}
	}
	now = now.Add(81 * time.Minute) // past MaxAge
	if _, err := ss.Lookup(ctx, s.Token); err != auth.ErrNoSession || st.Len() != 0 {
		t.Fatalf("absolute expiry: %v len %d", err, st.Len())
	}
	// Idle expiry.
	s, _ = ss.Create(ctx, auth.Principal{UserID: id}, "", "")
	now = now.Add(61 * time.Minute)
	if _, err := ss.Lookup(ctx, s.Token); err != auth.ErrNoSession {
		t.Fatal("idle expiry")
	}
	// Suspension and revocation.
	s, _ = ss.Create(ctx, auth.Principal{UserID: id}, "", "")
	s2, _ := ss.Create(ctx, auth.Principal{UserID: id}, "", "")
	if err := ss.Revoke(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.Lookup(ctx, s.Token); err != auth.ErrNoSession {
		t.Fatal("revoked session alive")
	}
	st.Suspend(id)
	if _, err := ss.Lookup(ctx, s2.Token); err != auth.ErrNoSession {
		t.Fatal("suspended user session alive")
	}
	if err := ss.RevokeUser(ctx, id); err != nil {
		t.Fatal(err)
	}
}

func problem(w http.ResponseWriter, status int, detail string) {
	w.WriteHeader(status)
	_, _ = w.Write([]byte(detail))
}

func TestMiddleware(t *testing.T) {
	env := authtest.New("https://git.example.com")
	m := env.Middleware
	m.Problem = problem
	m.Log = nil
	var seen auth.Principal
	var seenOK bool
	var info auth.RequestInfo
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, seenOK = auth.PrincipalFrom(r.Context())
		info = auth.RequestInfoFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	scopes := func(ctx context.Context) ([]string, bool) {
		v, ok := ctx.Value(scopeKey{}).([]string)
		return v, ok
	}
	h := m.LoadSession(m.CSRF(m.Scopes(scopes)(final)))
	do := func(method string, sc []string, mods ...func(*http.Request)) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/x", nil)
		r.RemoteAddr = "192.0.2.4:5555"
		if sc != nil {
			r = r.WithContext(context.WithValue(r.Context(), scopeKey{}, sc))
		}
		for _, f := range mods {
			f(r)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// Anonymous GET: passes, gets a CSRF cookie.
	rec := do(http.MethodGet, nil)
	if rec.Code != 200 || seenOK || info.IP != "192.0.2.4" || !strings.Contains(rec.Header().Get("Set-Cookie"), "__Host-erepo_csrf=") {
		t.Fatalf("anon get: %d %v %+v %q", rec.Code, seenOK, info, rec.Header().Get("Set-Cookie"))
	}
	if rec := do(http.MethodGet, []string{}); rec.Code != 401 {
		t.Fatalf("anon on user op: %d", rec.Code)
	}
	if rec := do(http.MethodPost, nil); rec.Code != 403 {
		t.Fatalf("anon post without token: %d", rec.Code)
	}
	if rec := do(http.MethodPost, nil, authtest.AnonCSRF(m.Cookies)); rec.Code != 200 {
		t.Fatalf("anon post with token: %d", rec.Code)
	}
	if rec := do(http.MethodPost, nil, authtest.AnonCSRF(m.Cookies), func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }); rec.Code != 403 {
		t.Fatalf("cross-site: %d", rec.Code)
	}

	p, asUser := env.Login(t, "alice", auth.RoleUser)
	if rec := do(http.MethodGet, []string{}, asUser); rec.Code != 200 || !seenOK || seen.UserID != p.UserID || info.CSRFToken == "" {
		t.Fatalf("user op: %d %+v", rec.Code, seen)
	}
	if rec := do(http.MethodGet, []string{auth.RoleAdmin}, asUser); rec.Code != 403 {
		t.Fatalf("user on admin op: %d", rec.Code)
	}
	if rec := do(http.MethodPost, []string{}, asUser); rec.Code != 200 {
		t.Fatalf("user post: %d", rec.Code)
	}
	// The double-submit pair alone is not enough with a session.
	if rec := do(http.MethodPost, []string{}, asUser, authtest.AnonCSRF(m.Cookies)); rec.Code != 403 {
		t.Fatalf("foreign token on session: %d", rec.Code)
	}
	_, asAdmin := env.Login(t, "root", auth.RoleAdmin)
	if rec := do(http.MethodGet, []string{auth.RoleAdmin}, asAdmin); rec.Code != 200 || !seen.IsAdmin() {
		t.Fatalf("admin op: %d", rec.Code)
	}
	// A dead session is cleared and treated as anonymous.
	tok, _ := auth.NewToken()
	rec = do(http.MethodGet, []string{}, env.WithSession(tok, tok))
	if rec.Code != 401 || !strings.Contains(strings.Join(rec.Header().Values("Set-Cookie"), ";"), "__Host-erepo_session=; Path=/; Max-Age=0") {
		t.Fatalf("dead session: %d %v", rec.Code, rec.Header().Values("Set-Cookie"))
	}
}

type scopeKey struct{}
