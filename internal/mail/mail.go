// Package mail sends plain-text notification mail over SMTP, rendered from
// embedded templates in every supported locale.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
	"net/smtp"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/config"
)

// Templates.
const (
	TemplateVerifyEmail        = "verify_email"
	TemplateResetPassword      = "reset_password"
	TemplateRegistrationNotice = "registration_notice"
	TemplateOrgInvite          = "org_invite"
	TemplateNotification       = "notification"
)

// Locales with templates; anything else falls back to DefaultLocale.
var Locales = []string{"en", "it"}

// DefaultLocale is used for unknown locales.
const DefaultLocale = "en"

//go:embed templates
var templateFS embed.FS

// templates holds one set per locale, keyed by file name.
var templates = func() map[string]*template.Template {
	out := map[string]*template.Template{}
	for _, l := range Locales {
		out[l] = template.Must(template.New("").Option("missingkey=error").ParseFS(templateFS, "templates/"+l+"/*.txt"))
	}
	return out
}()

// Message is one mail to send.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Sender delivers messages.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Render renders a template. The first line of each template is
// "Subject: ..." and the rest is the body.
func Render(locale, name string, data any) (subject, body string, err error) {
	if !validLocale(locale) {
		locale = DefaultLocale
	}
	var buf bytes.Buffer
	if err := templates[locale].ExecuteTemplate(&buf, name+".txt", data); err != nil {
		return "", "", fmt.Errorf("render mail %s/%s: %w", locale, name, err)
	}
	first, rest, _ := strings.Cut(buf.String(), "\n")
	subject, ok := strings.CutPrefix(first, "Subject: ")
	if !ok {
		return "", "", fmt.Errorf("mail template %s/%s has no subject line", locale, name)
	}
	return strings.TrimSpace(subject), strings.TrimLeft(rest, "\n"), nil
}

func validLocale(l string) bool { return slices.Contains(Locales, l) }

// Disabled is the Sender used when [mail] is not enabled: it drops messages
// and logs that it did, without the content.
type Disabled struct{ Log *slog.Logger }

// Send drops m.
func (d Disabled) Send(ctx context.Context, m Message) error {
	if d.Log != nil {
		d.Log.WarnContext(ctx, "mail disabled, message dropped", "subject", m.Subject)
	}
	return nil
}

// SMTP sends mail through an SMTP relay.
type SMTP struct {
	cfg      config.Mail
	password string
	from     *netmail.Address
	// TLSConfig overrides the TLS settings (tests).
	TLSConfig *tls.Config
}

// NewSMTP returns a sender for cfg. password is the content of
// cfg.PasswordFile, empty without authentication.
func NewSMTP(cfg config.Mail, password string) (*SMTP, error) {
	from, err := netmail.ParseAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("mail from: %w", err)
	}
	return &SMTP{cfg: cfg, password: password, from: from}, nil
}

// New returns the Sender configured by cfg: SMTP when enabled, Disabled
// otherwise.
func New(cfg config.Mail, password string, log *slog.Logger) (Sender, error) {
	if !cfg.Enabled {
		return Disabled{Log: log}, nil
	}
	return NewSMTP(cfg, password)
}

// Send delivers m, honouring ctx and the configured timeout.
func (s *SMTP) Send(ctx context.Context, m Message) error {
	to, err := netmail.ParseAddress(m.To)
	if err != nil {
		return fmt.Errorf("mail recipient: %w", err)
	}
	msg, err := s.format(to, m)
	if err != nil {
		return err
	}
	timeout := time.Duration(s.cfg.Timeout)
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	tlsCfg := s.TLSConfig
	if tlsCfg == nil {
		tlsCfg = &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}
	}

	var conn net.Conn
	dialer := &net.Dialer{}
	if s.cfg.TLS == "tls" {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsCfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer func() { _ = c.Close() }()
	if s.cfg.HeloName != "" {
		if err := c.Hello(s.cfg.HeloName); err != nil {
			return fmt.Errorf("smtp hello: %w", err)
		}
	}
	if s.cfg.TLS == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("smtp server does not offer STARTTLS")
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if s.cfg.Username != "" {
		// PlainAuth refuses to send credentials over an unencrypted
		// connection to a remote host.
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.password, s.cfg.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(s.from.Address); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := c.Rcpt(to.Address); err != nil {
		return fmt.Errorf("smtp rcpt: %w", err)
	}
	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := wc.Write(msg); err != nil {
		_ = wc.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("smtp end data: %w", err)
	}
	if err := c.Quit(); err != nil {
		return fmt.Errorf("smtp quit: %w", err)
	}
	return nil
}

// format builds the RFC 5322 message. Header values come from parsed
// addresses and a Q-encoded subject, so they cannot inject headers.
func (s *SMTP) format(to *netmail.Address, m Message) ([]byte, error) {
	if strings.ContainsAny(m.Subject, "\r\n") {
		return nil, errors.New("mail subject contains a line break")
	}
	idb := make([]byte, 16)
	_, _ = rand.Read(idb)
	domain := "localhost"
	if _, d, ok := strings.Cut(s.from.Address, "@"); ok {
		domain = d
	}
	var b bytes.Buffer
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", s.from.String())
	h("To", to.String())
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	h("Date", time.Now().UTC().Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(idb)+"@"+domain+">")
	h("MIME-Version", "1.0")
	h("Content-Type", "text/plain; charset=utf-8")
	h("Content-Transfer-Encoding", "quoted-printable")
	h("Auto-Submitted", "auto-generated")
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	if _, err := qp.Write([]byte(strings.ReplaceAll(m.Body, "\n", "\r\n"))); err != nil {
		return nil, fmt.Errorf("encode mail body: %w", err)
	}
	if err := qp.Close(); err != nil {
		return nil, fmt.Errorf("encode mail body: %w", err)
	}
	return b.Bytes(), nil
}
