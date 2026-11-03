package mail

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"mime/quotedprintable"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/config"
)

func TestRenderAllTemplates(t *testing.T) {
	data := map[string]string{"Instance": "git.example.com", "Username": "alice", "Link": "https://x/y?token=t", "Expires": "1h"}
	for _, l := range Locales {
		for _, n := range []string{TemplateVerifyEmail, TemplateResetPassword, TemplateRegistrationNotice} {
			subj, body, err := Render(l, n, data)
			if err != nil {
				t.Fatalf("%s/%s: %v", l, n, err)
			}
			if subj == "" || strings.ContainsAny(subj, "\r\n") || !strings.Contains(body, "alice") {
				t.Fatalf("%s/%s: subject %q body %q", l, n, subj, body)
			}
			if n != TemplateRegistrationNotice && !strings.Contains(body, data["Link"]) {
				t.Fatalf("%s/%s: link missing", l, n)
			}
		}
	}
	if _, _, err := Render("it", TemplateVerifyEmail, map[string]string{}); err == nil {
		t.Fatal("missing keys must fail")
	}
	subj, _, err := Render("xx", TemplateVerifyEmail, data)
	if err != nil || !strings.HasPrefix(subj, "Confirm") {
		t.Fatalf("fallback locale: %q %v", subj, err)
	}
}

func TestDisabled(t *testing.T) {
	s, err := New(config.Mail{}, "", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), Message{To: "a@example.com", Subject: "x"}); err != nil {
		t.Fatal(err)
	}
}

// fakeSMTP is a minimal SMTP server that records one transaction.
type fakeSMTP struct {
	ln   net.Listener
	mu   sync.Mutex
	from string
	rcpt []string
	data string
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := func(s string) { _, _ = io.WriteString(c, s+"\r\n") }
	w("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			w("250-fake")
			w("250 8BITMIME")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			f.mu.Lock()
			f.from = line[len("MAIL FROM:"):]
			f.mu.Unlock()
			w("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			f.mu.Lock()
			f.rcpt = append(f.rcpt, line[len("RCPT TO:"):])
			f.mu.Unlock()
			w("250 ok")
		case cmd == "DATA":
			w("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			w("250 queued")
		case cmd == "QUIT":
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}

func TestSMTPSend(t *testing.T) {
	f := startFakeSMTP(t)
	host, port, _ := net.SplitHostPort(f.ln.Addr().String())
	var p int
	for _, ch := range port {
		p = p*10 + int(ch-'0')
	}
	s, err := NewSMTP(config.Mail{Enabled: true, Host: host, Port: p, TLS: "none",
		From: "ErvisioRepo <noreply@git.example.com>", Timeout: config.Duration(5 * time.Second)}, "")
	if err != nil {
		t.Fatal(err)
	}
	body := "Ciao è un test\nhttps://git.example.com/verify-email?token=abc\n"
	if err := s.Send(context.Background(), Message{To: "Bob <bob@example.com>", Subject: "Conferma l'indirizzo è ok", Body: body}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.HasPrefix(f.from, "<noreply@git.example.com>") || len(f.rcpt) != 1 || f.rcpt[0] != "<bob@example.com>" {
		t.Fatalf("envelope from %q rcpt %v", f.from, f.rcpt)
	}
	head, enc, _ := strings.Cut(f.data, "\r\n\r\n")
	for _, want := range []string{"Subject: =?utf-8?q?", "Content-Type: text/plain; charset=utf-8", "To: \"Bob\" <bob@example.com>", "Message-ID: <"} {
		if !strings.Contains(head, want) {
			t.Fatalf("header %q missing in\n%s", want, head)
		}
	}
	dec, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(enc)))
	if err != nil || strings.ReplaceAll(string(dec), "\r\n", "\n") != body {
		t.Fatalf("body %q: %v", dec, err)
	}

	// Header injection through the recipient or the subject is refused.
	if err := s.Send(context.Background(), Message{To: "a@example.com\r\nBcc: x@evil.example", Subject: "x"}); err == nil {
		t.Fatal("injected recipient accepted")
	}
	if err := s.Send(context.Background(), Message{To: "a@example.com", Subject: "x\r\nBcc: x@evil.example"}); err == nil {
		t.Fatal("injected subject accepted")
	}
}

func TestSMTPStartTLSRequired(t *testing.T) {
	f := startFakeSMTP(t)
	host, port, _ := net.SplitHostPort(f.ln.Addr().String())
	var p int
	for _, ch := range port {
		p = p*10 + int(ch-'0')
	}
	s, err := NewSMTP(config.Mail{Enabled: true, Host: host, Port: p, TLS: "starttls", From: "noreply@example.com"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), Message{To: "a@example.com", Subject: "x", Body: "y"}); err == nil ||
		!strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("plaintext fallback: %v", err)
	}
}

func TestRenderOrgInvite(t *testing.T) {
	data := map[string]string{"Instance": "git.example.com", "Org": "acme", "Inviter": "alice", "Role": "member",
		"Link": "https://x/invite?token=t", "Expires": "7 days"}
	for _, l := range Locales {
		subj, body, err := Render(l, TemplateOrgInvite, data)
		if err != nil {
			t.Fatalf("%s: %v", l, err)
		}
		if !strings.Contains(subj, "acme") || strings.ContainsAny(subj, "\r\n") || !strings.Contains(body, data["Link"]) {
			t.Fatalf("%s: subject %q body %q", l, subj, body)
		}
	}
}
