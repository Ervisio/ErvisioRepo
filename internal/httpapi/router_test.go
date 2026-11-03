package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/auth/authtest"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

type fakeJobs bool

func (f fakeJobs) Running() bool { return bool(f) }

var testID = uuid.MustParse("019a4a4e-0000-7000-8000-000000000001")

func newTestRouter(t *testing.T, mod func(*Deps)) http.Handler {
	t.Helper()
	d := Deps{
		DB:   fakeDB{},
		Jobs: fakeJobs(true),
		Meta: Meta{Version: "0.1.0", Commit: "abc", InstanceID: testID,
			PublicURL: "https://git.example.com", Registration: "invite"},
		Web: fstest.MapFS{
			"index.html":        {Data: []byte("<!doctype html><title>spa</title>")},
			"assets/app-123.js": {Data: []byte("console.log(1)")},
			"favicon.svg":       {Data: []byte("<svg/>")},
		},
		HasIndex: true,
	}
	if mod != nil {
		mod(&d)
	}
	return NewRouter(d)
}

func do(h http.Handler, method, target string, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = "203.0.113.7:5555"
	for _, m := range mod {
		m(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func assertProblem(t *testing.T, rec *httptest.ResponseRecorder, status int) v1gen.Problem {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type = %q", ct)
	}
	var p v1gen.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Status != status || p.Title == "" {
		t.Fatalf("bad problem %s: %v", rec.Body, err)
	}
	return p
}

func TestHealthz(t *testing.T) {
	h := newTestRouter(t, func(d *Deps) { d.DB = fakeDB{errors.New("down")} })
	if rec := do(h, "GET", "/healthz"); rec.Code != 200 {
		t.Fatalf("healthz = %d", rec.Code)
	}
}

func TestReadyz(t *testing.T) {
	tests := []struct {
		name   string
		db     Pinger
		jobs   JobsStatus
		status int
		detail string
	}{
		{"ready", fakeDB{}, fakeJobs(true), 200, ""},
		{"db down", fakeDB{errors.New("down")}, fakeJobs(true), 503, "database unreachable"},
		{"jobs stopped", fakeDB{}, fakeJobs(false), 503, "job queue not running"},
		{"not wired", nil, nil, 503, "server is starting"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestRouter(t, func(d *Deps) { d.DB, d.Jobs = tt.db, tt.jobs })
			rec := do(h, "GET", "/readyz")
			if tt.status == 200 {
				if rec.Code != 200 {
					t.Fatalf("status = %d", rec.Code)
				}
				return
			}
			if p := assertProblem(t, rec, tt.status); p.Detail == nil || *p.Detail != tt.detail {
				t.Fatalf("detail = %v", p.Detail)
			}
		})
	}
}

func TestMeta(t *testing.T) {
	rec := do(newTestRouter(t, nil), "GET", "/api/v1/meta")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var m v1gen.Meta
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Name != "ErvisioRepo" || m.Version != "0.1.0" || m.Commit == nil || *m.Commit != "abc" ||
		m.InstanceId != testID || m.PublicUrl != "https://git.example.com" || m.Registration != v1gen.MetaRegistrationInvite {
		t.Fatalf("meta = %+v", m)
	}

	rec = do(newTestRouter(t, func(d *Deps) { d.Meta.Commit = "" }), "GET", "/api/v1/meta")
	if strings.Contains(rec.Body.String(), "commit") {
		t.Fatalf("empty commit must be omitted: %s", rec.Body)
	}
}

func TestAPIErrorsAreProblems(t *testing.T) {
	h := newTestRouter(t, nil)
	assertProblem(t, do(h, "GET", "/api/v1/nope"), 404)
	assertProblem(t, do(h, "GET", "/api/v3/repos"), 404)
	assertProblem(t, do(h, "GET", "/api"), 404)
	assertProblem(t, do(h, "POST", "/api/v1/meta", authtest.AnonCSRF(auth.CookiesFor("https://git.example.com"))), 405)
	// Without a CSRF token an unsafe request is refused before routing.
	assertProblem(t, do(h, "POST", "/api/v1/meta"), 403)
}

func TestPanicIsProblem(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	h := recoverer(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	assertProblem(t, do(h, "GET", "/x"), 500)
	if !strings.Contains(logs.String(), "panic in handler") {
		t.Fatalf("panic not logged: %s", logs.String())
	}
}

func TestAccessLogOmitsQuery(t *testing.T) {
	var logs bytes.Buffer
	h := newTestRouter(t, func(d *Deps) { d.Log = slog.New(slog.NewJSONHandler(&logs, nil)) })
	do(h, "GET", "/healthz?token=supersecret", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer supersecret")
	})
	out := logs.String()
	if !strings.Contains(out, `"path":"/healthz"`) || !strings.Contains(out, `"request_id"`) {
		t.Fatalf("missing fields: %s", out)
	}
	if strings.Contains(out, "supersecret") {
		t.Fatalf("secret leaked into log: %s", out)
	}
}

func TestMetricsLoopbackOnly(t *testing.T) {
	h := newTestRouter(t, nil)
	local := func(r *http.Request) { r.RemoteAddr = "127.0.0.1:4000" }
	do(h, "GET", "/healthz", local) // generate one sample

	rec := do(h, "GET", "/metrics", local)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `erepo_http_requests_total{code="200",method="GET",route="/healthz"}`) {
		t.Fatalf("metrics %d: %s", rec.Code, rec.Body)
	}
	assertProblem(t, do(h, "GET", "/metrics"), 403)
	assertProblem(t, do(h, "GET", "/metrics", local, func(r *http.Request) {
		r.Header.Set("X-Forwarded-For", "198.51.100.1")
	}), 403)
	if rec := do(h, "GET", "/metrics", func(r *http.Request) { r.RemoteAddr = "[::1]:4000" }); rec.Code != 200 {
		t.Fatalf("ipv6 loopback = %d", rec.Code)
	}
}

func TestSPA(t *testing.T) {
	h := newTestRouter(t, nil)
	tests := []struct {
		path, wantBody, wantCache string
		status                    int
	}{
		{"/", "spa", "no-cache", 200},
		{"/acme/repo/pulls/3", "spa", "no-cache", 200},
		{"/assets/app-123.js", "console.log", "immutable", 200},
		{"/favicon.svg", "<svg/>", "", 200},
		{"/missing.js", "", "", 404},
		{"/../../etc/passwd", "invalid URL path", "", 400}, // rejected by net/http
	}
	for _, tt := range tests {
		rec := do(h, "GET", tt.path)
		if rec.Code != tt.status || !strings.Contains(rec.Body.String(), tt.wantBody) ||
			!strings.Contains(rec.Header().Get("Cache-Control"), tt.wantCache) {
			t.Errorf("%s: %d %q cache=%q", tt.path, rec.Code, rec.Body, rec.Header().Get("Cache-Control"))
		}
	}
	if rec := do(h, "POST", "/"); rec.Code != 405 {
		t.Errorf("POST / = %d", rec.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := do(newTestRouter(t, nil), "GET", "/")
	h := rec.Header()
	if !strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'none'") ||
		!strings.Contains(h.Get("Content-Security-Policy"), "default-src 'self'") ||
		h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") == "" {
		t.Fatalf("headers = %v", h)
	}
}

func TestSPAMissing(t *testing.T) {
	h := newTestRouter(t, func(d *Deps) { d.Web, d.HasIndex = fstest.MapFS{}, false })
	rec := do(h, "GET", "/anything")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "make web") {
		t.Fatalf("got %d %q", rec.Code, rec.Body)
	}
}
