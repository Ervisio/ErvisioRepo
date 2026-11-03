package identity_test

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/identity/identitytest"
	"github.com/Ervisio/ErvisioRepo/internal/mail"
)

type outbox struct {
	mu   sync.Mutex
	msgs []mail.Message
}

func (o *outbox) Send(_ context.Context, m mail.Message) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.msgs = append(o.msgs, m)
	return nil
}

func (o *outbox) take() []mail.Message {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := o.msgs
	o.msgs = nil
	return out
}

var linkRE = regexp.MustCompile(`https://git\.example\.com/\S+`)

// tokenOf extracts the token of the only link in m.
func tokenOf(t *testing.T, m mail.Message) string {
	t.Helper()
	u, err := url.Parse(linkRE.FindString(m.Body))
	if err != nil {
		t.Fatal(err)
	}
	tok := u.Query().Get("token")
	if !auth.ValidTokenFormat(tok) {
		t.Fatalf("no token in mail %q", m.Body)
	}
	return tok
}

type fixture struct {
	*identitytest.Env
	box    *outbox
	mailer *identity.Mailer
	now    time.Time
}

func newFixture(t *testing.T, mod func(*config.Config)) *fixture {
	t.Helper()
	t.Parallel()
	f := &fixture{Env: identitytest.New(t, mod), box: &outbox{}, now: time.Now()}
	f.mailer = &identity.Mailer{Queries: f.Queries, Sender: f.box, PublicURL: f.Config.Server.PublicURL,
		VerifyTTL: time.Hour, ResetTTL: time.Hour, IdleTTL: time.Hour, Now: func() time.Time { return f.now }}
	return f
}

// deliver runs every queued job and returns the mails sent.
func (f *fixture) deliver(t *testing.T) []mail.Message {
	t.Helper()
	for _, j := range f.Jobs.Take() {
		if err := f.mailer.Deliver(context.Background(), j); err != nil {
			t.Fatalf("job %s: %v", j.Kind(), err)
		}
	}
	return f.box.take()
}

var client = identity.Client{IP: "198.51.100.1", UserAgent: "test"}

const pw = "correct horse battery"

func (f *fixture) register(t *testing.T, name, email, invite string) identity.RegisterResult {
	t.Helper()
	res, err := f.Service.Register(context.Background(), identity.RegisterInput{Username: name, Email: email, Password: pw, InviteToken: invite}, client)
	if err != nil {
		t.Fatalf("register %s: %v", name, err)
	}
	return res
}

func actions(t *testing.T, q *dbgen.Queries) map[string]int {
	t.Helper()
	rows, err := q.ListAuditEvents(context.Background(), dbgen.ListAuditEventsParams{MaxRows: 1000})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, r := range rows {
		out[r.Action]++
	}
	return out
}

func TestRegisterInviteVerifyLogin(t *testing.T) {
	f := newFixture(t, nil) // registration = invite
	ctx := context.Background()
	s := f.Service

	// The first account is the admin whatever the policy, already verified.
	if res := f.register(t, "Boss", "root@example.com", ""); res.VerificationRequired {
		t.Fatal("first admin must not need verification")
	}
	if len(f.Jobs.Take()) != 0 {
		t.Fatal("no mail for the first admin")
	}
	admin, err := s.Login(ctx, "boss", pw, "", client)
	if err != nil {
		t.Fatal(err)
	}
	if admin.User.Role != auth.RoleAdmin || !admin.User.EmailVerified || admin.User.Username != "Boss" {
		t.Fatalf("admin = %+v", admin.User)
	}

	in := identity.RegisterInput{Username: "alice", Email: "alice@example.com", Password: pw}
	if _, err := s.Register(ctx, in, client); !errors.Is(err, identity.ErrInviteRequired) {
		t.Fatalf("no invite: %v", err)
	}
	in.InviteToken = "not-a-token"
	if _, err := s.Register(ctx, in, client); !errors.Is(err, identity.ErrInviteRequired) {
		t.Fatalf("bad invite: %v", err)
	}
	bound, err := s.CreateInvite(ctx, admin.Session.Principal(), "someone@example.com", client)
	if err != nil {
		t.Fatal(err)
	}
	in.InviteToken = bound.Token
	if _, err := s.Register(ctx, in, client); !errors.Is(err, identity.ErrInviteRequired) {
		t.Fatalf("invite bound to another address: %v", err)
	}
	inv, err := s.CreateInvite(ctx, admin.Session.Principal(), "", client)
	if err != nil {
		t.Fatal(err)
	}
	if res := f.register(t, "alice", "Alice@Example.com", inv.Token); !res.VerificationRequired {
		t.Fatal("verification must be required")
	}
	if _, err := s.Register(ctx, identity.RegisterInput{Username: "carol", Email: "carol@example.com", Password: pw, InviteToken: inv.Token}, client); !errors.Is(err, identity.ErrInviteRequired) {
		t.Fatalf("invite reused: %v", err)
	}

	// Unverified: login refused, a new link is sent.
	msgs := f.deliver(t)
	if len(msgs) != 1 || msgs[0].To != "Alice@Example.com" {
		t.Fatalf("verification mails = %+v", msgs)
	}
	if _, err := s.Login(ctx, "alice", pw, "", client); !errors.Is(err, identity.ErrEmailNotVerified) {
		t.Fatalf("unverified login: %v", err)
	}
	msgs = f.deliver(t)
	if len(msgs) != 1 {
		t.Fatalf("resend at login: %d mails", len(msgs))
	}
	tok := tokenOf(t, msgs[0])
	if err := s.VerifyEmail(ctx, tok, client); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyEmail(ctx, tok, client); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("token reused: %v", err)
	}
	u, err := s.Login(ctx, "alice@example.com", pw, "", client)
	if err != nil {
		t.Fatal(err)
	}
	if u.User.Role != auth.RoleUser || !u.User.EmailVerified {
		t.Fatalf("alice = %+v", u.User)
	}

	// Names are one namespace, case-insensitive, with reserved words.
	inv2, _ := s.CreateInvite(ctx, admin.Session.Principal(), "", client)
	for name, want := range map[string]error{"ALICE": identity.ErrUsernameTaken, "admin": identity.ErrReservedName, "-x": identity.ErrInvalidName} {
		_, err := s.Register(ctx, identity.RegisterInput{Username: name, Email: "new@example.com", Password: pw, InviteToken: inv2.Token}, client)
		if !errors.Is(err, want) {
			t.Fatalf("register %q: %v, want %v", name, err, want)
		}
	}
	if _, err := s.Register(ctx, identity.RegisterInput{Username: "dave", Email: "dave@example.com", Password: "short", InviteToken: inv2.Token}, client); !errors.Is(err, identity.ErrWeakPassword) {
		t.Fatalf("weak password: %v", err)
	}

	// A taken address answers like a success and warns its owner.
	res := f.register(t, "mallory", "ALICE@example.com", inv2.Token)
	if !res.VerificationRequired {
		t.Fatal("duplicate must look like a success")
	}
	msgs = f.deliver(t)
	if len(msgs) != 1 || !strings.Contains(msgs[0].Body, "alice") {
		t.Fatalf("notice = %+v", msgs)
	}
	if _, err := s.Login(ctx, "mallory", pw, "", client); !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Fatalf("duplicate created an account: %v", err)
	}
	// The invite was not consumed by the duplicate.
	f.register(t, "erin", "erin@example.com", inv2.Token)

	got := actions(t, f.Queries)
	for _, a := range []string{audit.ActionRegister, audit.ActionFirstAdmin, audit.ActionInviteCreated, audit.ActionEmailVerified,
		audit.ActionRegisterDuplicate, audit.ActionLogin, audit.ActionLoginFailed} {
		if got[a] == 0 {
			t.Errorf("audit action %s missing: %v", a, got)
		}
	}
	if n, _ := s.CountUsers(ctx); n != 3 {
		t.Fatalf("users = %d", n)
	}
}

func TestRegistrationPolicies(t *testing.T) {
	t.Run("closed", func(t *testing.T) {
		f := newFixture(t, func(c *config.Config) { c.Security.Registration = config.RegistrationClosed })
		f.register(t, "boss", "root@example.com", "")
		_, err := f.Service.Register(context.Background(), identity.RegisterInput{Username: "bob", Email: "bob@example.com", Password: pw}, client)
		if !errors.Is(err, identity.ErrRegistrationClosed) {
			t.Fatalf("closed: %v", err)
		}
	})
	t.Run("open without mail", func(t *testing.T) {
		f := newFixture(t, func(c *config.Config) {
			c.Security.Registration = config.RegistrationOpen
			c.Mail.Enabled = false
		})
		f.register(t, "boss", "root@example.com", "")
		if res := f.register(t, "bob", "bob@example.com", ""); res.VerificationRequired {
			t.Fatal("no verification without mail")
		}
		if len(f.Jobs.Take()) != 0 {
			t.Fatal("jobs enqueued with mail disabled")
		}
		if _, err := f.Service.Login(context.Background(), "bob", pw, "", client); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("concurrent first users", func(t *testing.T) {
		f := newFixture(t, func(c *config.Config) { c.Security.Registration = config.RegistrationOpen })
		var wg sync.WaitGroup
		for i := range 4 {
			wg.Go(func() {
				name := "user" + string(rune('a'+i))
				_, _ = f.Service.Register(context.Background(), identity.RegisterInput{Username: name, Email: name + "@example.com", Password: pw}, identity.Client{IP: name})
			})
		}
		wg.Wait()
		if got := actions(t, f.Queries)[audit.ActionFirstAdmin]; got != 1 {
			t.Fatalf("first admins = %d", got)
		}
	})
}

func TestPasswordReset(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	s := f.Service
	f.register(t, "boss", "root@example.com", "")
	l1, err := s.Login(ctx, "boss", pw, "", client)
	if err != nil {
		t.Fatal(err)
	}

	// Unknown address: same answer, no mail.
	if err := s.RequestPasswordReset(ctx, "nobody@example.com", client); err != nil {
		t.Fatal(err)
	}
	if msgs := f.deliver(t); len(msgs) != 0 {
		t.Fatalf("mail to unknown address: %+v", msgs)
	}
	if err := s.RequestPasswordReset(ctx, "bad", client); !errors.Is(err, identity.ErrInvalidEmail) {
		t.Fatalf("bad address: %v", err)
	}

	// Two requests: only the last link works.
	for range 2 {
		if err := s.RequestPasswordReset(ctx, "ROOT@example.com", client); err != nil {
			t.Fatal(err)
		}
	}
	msgs := f.deliver(t)
	if len(msgs) != 2 {
		t.Fatalf("reset mails = %d", len(msgs))
	}
	oldTok, tok := tokenOf(t, msgs[0]), tokenOf(t, msgs[1])
	if err := s.ResetPassword(ctx, oldTok, "new password 123", client); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("superseded token: %v", err)
	}
	if err := s.ResetPassword(ctx, tok, "short", client); !errors.Is(err, identity.ErrWeakPassword) {
		t.Fatalf("weak: %v", err)
	}
	if err := s.ResetPassword(ctx, tok, "new password 123", client); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetPassword(ctx, tok, "other password 123", client); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("token reused: %v", err)
	}
	// Every session is revoked.
	if _, err := f.Sessions.Lookup(ctx, l1.Session.Token); !errors.Is(err, auth.ErrNoSession) {
		t.Fatalf("session survived reset: %v", err)
	}
	if _, err := s.Login(ctx, "boss", pw, "", client); !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Fatalf("old password: %v", err)
	}
	if _, err := s.Login(ctx, "boss", "new password 123", "", client); err != nil {
		t.Fatal(err)
	}

	// Expired link.
	if err := s.RequestPasswordReset(ctx, "root@example.com", client); err != nil {
		t.Fatal(err)
	}
	f.now = time.Now().Add(-2 * time.Hour) // the token is issued already expired
	tok = tokenOf(t, f.deliver(t)[0])
	if err := s.ResetPassword(ctx, tok, "third password 123", client); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("expired token: %v", err)
	}
	if got := actions(t, f.Queries); got[audit.ActionPasswordReset] != 1 || got[audit.ActionPasswordResetRequest] != 4 {
		t.Fatalf("audit = %v", got)
	}
}

func TestLoginRotationLogoutCleanup(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	s := f.Service
	f.register(t, "boss", "root@example.com", "")
	a, err := s.Login(ctx, "boss", pw, "", client)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Login(ctx, "boss", pw, a.Session.Token, client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Sessions.Lookup(ctx, a.Session.Token); !errors.Is(err, auth.ErrNoSession) {
		t.Fatalf("old session not rotated: %v", err)
	}
	sb, err := f.Sessions.Lookup(ctx, b.Session.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Logout(ctx, sb.Principal(), client); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Sessions.Lookup(ctx, b.Session.Token); !errors.Is(err, auth.ErrNoSession) {
		t.Fatalf("session survived logout: %v", err)
	}
	if _, err := s.User(ctx, uuid.Must(uuid.NewV7())); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}

	// Suspended users cannot log in.
	if err := f.Queries.SetUserSuspended(ctx, dbgen.SetUserSuspendedParams{ID: a.User.ID, Suspended: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login(ctx, "boss", pw, "", client); !errors.Is(err, identity.ErrSuspended) {
		t.Fatalf("suspended: %v", err)
	}

	// Cleanup removes idle sessions.
	if _, err := f.Sessions.Create(ctx, a.Session.Principal(), "", ""); err != nil {
		t.Fatal(err)
	}
	f.now = time.Now().Add(2 * time.Hour)
	if err := f.mailer.Deliver(ctx, identity.CleanupArgs{}); err != nil {
		t.Fatal(err)
	}
	if n, err := f.Queries.DeleteUserSessions(ctx, a.User.ID); err != nil || n != 0 {
		t.Fatalf("sessions left after cleanup: %d %v", n, err)
	}
}
