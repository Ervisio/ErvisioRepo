package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbtest"
)

func runCLI(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = run(context.Background(), args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestCLI(t *testing.T) {
	tests := []struct {
		args     []string
		code     int
		inStdout string
		inStderr string
	}{
		{[]string{"version"}, 0, "ervisio-repo ", ""},
		{[]string{"help"}, 0, "Usage:", ""},
		{nil, 2, "", "missing command"},
		{[]string{"bogus"}, 2, "", `unknown command "bogus"`},
		{[]string{"migrate"}, 2, "", `migrate needs "up" or "status"`},
		{[]string{"migrate", "down"}, 2, "", `migrate needs "up" or "status"`},
		{[]string{"serve", "--nope"}, 2, "", "flag provided but not defined"},
		{[]string{"serve", "--config", "/nonexistent/config.toml"}, 1, "", "read config"},
		{[]string{"serve", "extra"}, 2, "", "unexpected arguments"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			code, out, errOut := runCLI(tt.args...)
			if code != tt.code || !strings.Contains(out, tt.inStdout) || !strings.Contains(errOut, tt.inStderr) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
			}
		})
	}
}

func TestNewLogger(t *testing.T) {
	var buf bytes.Buffer
	newLogger(config.Log{Level: "warn", Format: "json"}, &buf).Info("hidden")
	newLogger(config.Log{Level: "warn", Format: "json"}, &buf).Warn("shown")
	if strings.Contains(buf.String(), "hidden") || !json.Valid(bytes.TrimSpace(buf.Bytes())) {
		t.Fatalf("json logger: %q", buf.String())
	}
	buf.Reset()
	newLogger(config.Log{Level: "debug", Format: "text"}, &buf).Debug("dbg")
	if !strings.HasPrefix(buf.String(), "time=") {
		t.Fatalf("text logger: %q", buf.String())
	}
}

func TestServeAndMigrate(t *testing.T) {
	url := dbtest.URL(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	toml := "[server]\npublic_url = \"http://localhost:3400\"\n[database]\nurl = \"" + url + "\"\n"
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}

	if code, _, errOut := runCLI("migrate", "up", "--config", path); code != 0 {
		t.Fatalf("migrate up: %s", errOut)
	}
	code, out, errOut := runCLI("migrate", "status", "--config", path)
	if code != 0 || !strings.Contains(out, "00001_settings.sql") {
		t.Fatalf("migrate status: %d %s %s", code, out, errOut)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n")[1:] {
		if !strings.Contains(line, " applied ") && !strings.HasSuffix(line, " applied") {
			t.Fatalf("migration not applied: %q", line)
		}
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, cfg, slog.New(slog.DiscardHandler), ln) }()

	base := "http://" + ln.Addr().String()
	waitFor(t, base+"/readyz")
	resp, err := http.Get(base + "/api/v1/meta")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"instanceId"`) {
		t.Fatalf("meta: %d %s", resp.StatusCode, body)
	}
	// Anonymous callers do not reach admin endpoints.
	resp, err = http.Get(base + "/api/v1/admin/storage")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous admin storage: %d", resp.StatusCode)
	}

	// The first account becomes admin. Cookies are set by hand: the
	// loopback public URL makes them __Host- and Secure, which a jar would
	// not send over plain http.
	csrf := cookieFrom(t, resp, "__Host-erepo_csrf")
	post := func(path, sessionTok, csrfTok string, body any) *http.Response {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, base+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrfTok)
		req.AddCookie(&http.Cookie{Name: "__Host-erepo_csrf", Value: csrfTok})
		if sessionTok != "" {
			req.AddCookie(&http.Cookie{Name: "__Host-erepo_session", Value: sessionTok})
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp = post("/api/v1/auth/register", "", csrf, map[string]string{
		"username": "boss", "email": "boss@example.com", "password": "correct horse battery staple"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("register: %d", resp.StatusCode)
	}
	resp = post("/api/v1/auth/login", "", csrf, map[string]string{"login": "boss", "password": "correct horse battery staple"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	session := cookieFrom(t, resp, "__Host-erepo_session")

	req, _ := http.NewRequest(http.MethodGet, base+"/api/v1/admin/storage", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-erepo_session", Value: session})
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"storage_not_configured"`) {
		t.Fatalf("admin storage: %d %s", resp.StatusCode, body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(40 * time.Second):
		t.Fatal("serve did not shut down")
	}
}

func cookieFrom(t *testing.T, resp *http.Response, name string) string {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == name && c.Value != "" {
			return c.Value
		}
	}
	t.Fatalf("no %s cookie in %v", name, resp.Header.Values("Set-Cookie"))
	return ""
}

func waitFor(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(url); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s never became ready", url)
}
