package orgs_test

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/identity/identitytest"
	"github.com/Ervisio/ErvisioRepo/internal/mail"
	"github.com/Ervisio/ErvisioRepo/internal/orgs"
	"github.com/Ervisio/ErvisioRepo/internal/quota"
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

var client = identity.Client{IP: "198.51.100.7", UserAgent: "test"}

type fixture struct {
	*identitytest.Env
	svc    *orgs.Service
	mailer *orgs.Mailer
	box    *outbox
}

func newFixture(t *testing.T, mod func(*config.Config)) *fixture {
	t.Helper()
	t.Parallel()
	env := identitytest.New(t, mod)
	f := &fixture{Env: env, box: &outbox{}}
	f.svc = orgs.New(orgs.Options{Pool: env.Pool, Jobs: env.Jobs, Config: env.Config})
	f.mailer = orgs.MailerFor(env.Config, env.Queries, f.box, nil)
	return f
}

// user inserts an account with a verified address <name>@example.com.
func (f *fixture) user(t *testing.T, name, role string) auth.Principal {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO namespaces (id, kind, name) VALUES ($1, 'user', $2)`, []any{id, name}},
		{`INSERT INTO users (id, role) VALUES ($1, $2)`, []any{id, role}},
		{`INSERT INTO emails (id, user_id, address, is_primary, verified_at) VALUES ($1, $2, $3, true, now())`,
			[]any{uuid.Must(uuid.NewV7()), id, name + "@example.com"}},
	} {
		if _, err := f.Pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
	}
	return auth.Principal{UserID: id, Username: name, Role: role, Method: auth.MethodSession}
}

func is(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

var linkRE = regexp.MustCompile(`https://git\.example\.com/org-invite\?\S+`)

func (f *fixture) inviteToken(t *testing.T) string {
	t.Helper()
	jobs := f.Jobs.Take()
	if len(jobs) != 1 {
		t.Fatalf("jobs = %v, want one invite", jobs)
	}
	ok(t, f.mailer.Deliver(context.Background(), jobs[0].(orgs.OrgInviteArgs)))
	msgs := f.box.take()
	if len(msgs) != 1 {
		t.Fatalf("mails = %d", len(msgs))
	}
	u, err := url.Parse(linkRE.FindString(msgs[0].Body))
	ok(t, err)
	return u.Query().Get("token")
}

func TestOrgLifecycle(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	s := f.svc
	alice, bob, carol := f.user(t, "alice", "user"), f.user(t, "bob", "user"), f.user(t, "carol", "user")
	mallory := f.user(t, "mallory", "user")
	root := f.user(t, "root", "admin")
	guest := f.user(t, "guest", "restricted")

	_, err := s.CreateOrg(ctx, guest, orgs.CreateOrgInput{Name: "nope"}, client)
	is(t, err, orgs.ErrForbidden)
	o, err := s.CreateOrg(ctx, alice, orgs.CreateOrgInput{Name: "Acme", DisplayName: " Acme Inc "}, client)
	ok(t, err)
	if o.Role != authz.OrgOwner || o.DisplayName != "Acme Inc" || o.BasePermission != "read" {
		t.Fatalf("org = %+v", o)
	}
	_, err = s.CreateOrg(ctx, bob, orgs.CreateOrgInput{Name: "acme"}, client)
	is(t, err, orgs.ErrNameTaken)
	_, err = s.CreateOrg(ctx, bob, orgs.CreateOrgInput{Name: "alice"}, client)
	is(t, err, orgs.ErrNameTaken)
	var ve *orgs.ValidationError
	if _, err = s.CreateOrg(ctx, bob, orgs.CreateOrgInput{Name: "-bad"}, client); !errors.As(err, &ve) {
		t.Fatalf("bad name: %v", err)
	}

	// Outsiders see nothing; instance admins see everything.
	_, err = s.GetOrg(ctx, mallory, "acme")
	is(t, err, orgs.ErrNotFound)
	_, err = s.ListMembers(ctx, mallory, "acme")
	is(t, err, orgs.ErrNotFound)
	if _, err := s.GetOrg(ctx, root, "acme"); err != nil {
		t.Fatal(err)
	}

	// Invite bob as admin (mailed) and carol as member of a team.
	team, err := s.CreateTeam(ctx, alice, "acme", orgs.TeamInput{Name: ptr("core")}, client)
	ok(t, err)
	_, err = s.CreateInvite(ctx, alice, "acme", orgs.InviteInput{Email: "bob@example.com", Role: "admin"}, client)
	ok(t, err)
	_, err = s.CreateInvite(ctx, alice, "acme", orgs.InviteInput{Email: "BOB@example.com", Role: "member"}, client)
	is(t, err, orgs.ErrInvitePending)
	bobToken := f.inviteToken(t)
	_, err = s.AcceptInvite(ctx, carol, bobToken, client)
	is(t, err, orgs.ErrEmailMismatch)
	acc, err := s.AcceptInvite(ctx, bob, bobToken, client)
	ok(t, err)
	if acc.Org != "Acme" || acc.Role != "admin" {
		t.Fatalf("accepted = %+v", acc)
	}
	_, err = s.AcceptInvite(ctx, bob, bobToken, client)
	is(t, err, orgs.ErrInvalidToken)

	// An admin cannot invite owners nor promote to owner.
	_, err = s.CreateInvite(ctx, bob, "acme", orgs.InviteInput{Email: "carol@example.com", Role: "owner"}, client)
	is(t, err, orgs.ErrForbidden)
	_, err = s.CreateInvite(ctx, bob, "acme", orgs.InviteInput{Email: "carol@example.com", Role: "member", Teams: []string{"core"}}, client)
	ok(t, err)
	acc, err = s.AcceptInvite(ctx, carol, f.inviteToken(t), client)
	ok(t, err)
	if acc.Role != "member" {
		t.Fatalf("carol = %+v", acc)
	}
	tm, err := s.ListTeamMembers(ctx, carol, "acme", "core")
	ok(t, err)
	if len(tm) != 1 || tm[0].Username != "carol" {
		t.Fatalf("team members = %+v", tm)
	}
	_, err = s.SetMemberRole(ctx, bob, "acme", "carol", "owner", client)
	is(t, err, orgs.ErrForbidden)
	_, err = s.SetMemberRole(ctx, bob, "acme", "bob", "owner", client)
	is(t, err, orgs.ErrForbidden)
	_, err = s.SetMemberRole(ctx, bob, "acme", "alice", "member", client)
	is(t, err, orgs.ErrForbidden)
	is(t, s.RemoveMember(ctx, bob, "acme", "alice", client), orgs.ErrForbidden)
	_, err = s.SetMemberRole(ctx, carol, "acme", "carol", "admin", client)
	is(t, err, orgs.ErrForbidden)
	_, err = s.SetMemberRole(ctx, bob, "acme", "mallory", "member", client)
	is(t, err, orgs.ErrNotMember)

	// The last owner can neither step down nor leave.
	_, err = s.SetMemberRole(ctx, alice, "acme", "alice", "admin", client)
	is(t, err, orgs.ErrLastOwner)
	is(t, s.RemoveMember(ctx, alice, "acme", "alice", client), orgs.ErrLastOwner)
	m, err := s.SetMemberRole(ctx, alice, "acme", "bob", "owner", client)
	ok(t, err)
	if m.Role != "owner" {
		t.Fatalf("bob = %+v", m)
	}
	ok(t, s.RemoveMember(ctx, alice, "acme", "alice", client))

	// Team maintainers manage their team; members may leave it.
	ok(t, s.SetTeamMember(ctx, bob, "acme", "core", "carol", "maintainer", client))
	is(t, s.SetTeamMember(ctx, carol, "acme", "core", "mallory", "member", client), orgs.ErrNotMember)
	ok(t, s.RemoveTeamMember(ctx, carol, "acme", "core", "carol", client))
	is(t, s.SetTeamMember(ctx, carol, "acme", "core", "carol", "member", client), orgs.ErrForbidden)
	_, err = s.UpdateTeam(ctx, carol, "acme", "core", orgs.TeamInput{Description: ptr("x")}, client)
	is(t, err, orgs.ErrForbidden)
	up, err := s.UpdateTeam(ctx, bob, "acme", "core", orgs.TeamInput{Name: ptr("Platform")}, client)
	ok(t, err)
	if up.ID != team.ID || up.Name != "Platform" {
		t.Fatalf("team = %+v", up)
	}

	// Settings: members cannot change them.
	_, err = s.UpdateOrg(ctx, carol, "acme", orgs.UpdateOrgInput{BasePermission: ptr("write")}, client)
	is(t, err, orgs.ErrForbidden)
	o, err = s.UpdateOrg(ctx, bob, "acme", orgs.UpdateOrgInput{BasePermission: ptr("write"), Description: ptr("d")}, client)
	ok(t, err)
	if o.BasePermission != "write" || o.Description != "d" || o.DisplayName != "Acme Inc" {
		t.Fatalf("org = %+v", o)
	}
	if _, err = s.UpdateOrg(ctx, bob, "acme", orgs.UpdateOrgInput{BasePermission: ptr("root")}, client); !errors.As(err, &ve) {
		t.Fatalf("bad permission: %v", err)
	}
	yes := true
	_, err = s.UpdateOrg(ctx, carol, "acme", orgs.UpdateOrgInput{Require2FA: &yes}, client)
	is(t, err, orgs.ErrForbidden)
	if o, err = s.UpdateOrg(ctx, bob, "acme", orgs.UpdateOrgInput{Require2FA: &yes}, client); err != nil || !o.Require2FA {
		t.Fatalf("require 2fa: %+v, %v", o, err)
	}

	// Audit: members cannot read it; managers see the org entries only.
	_, err = s.AuditLog(ctx, carol, "acme", audit.Filter{})
	is(t, err, orgs.ErrForbidden)
	page, err := s.AuditLog(ctx, bob, "acme", audit.Filter{Action: "org.", Limit: 2})
	ok(t, err)
	if len(page.Entries) != 2 || page.Next == uuid.Nil {
		t.Fatalf("page = %+v", page)
	}
	rest, err := s.AuditLog(ctx, bob, "acme", audit.Filter{Action: "org.", Before: page.Next, Limit: 50})
	ok(t, err)
	if len(rest.Entries) == 0 || rest.Next != uuid.Nil || rest.Entries[0].ID.String() >= page.Next.String() {
		t.Fatalf("rest = %+v", rest)
	}

	q, err := s.Quota(ctx, bob, "acme")
	ok(t, err)
	if q.MaxRepos != quota.Unlimited || q.Repos != 0 {
		t.Fatalf("quota = %+v", q)
	}

	mine, err := s.ListMyOrgs(ctx, carol)
	ok(t, err)
	if len(mine) != 1 || mine[0].Role != authz.OrgMember {
		t.Fatalf("carol orgs = %+v", mine)
	}

	// Deleting: owners only; afterwards the name is free again.
	is(t, s.DeleteOrg(ctx, carol, "acme", client), orgs.ErrForbidden)
	ok(t, s.DeleteOrg(ctx, bob, "acme", client))
	_, err = s.GetOrg(ctx, bob, "acme")
	is(t, err, orgs.ErrNotFound)
	_, err = s.CreateOrg(ctx, carol, orgs.CreateOrgInput{Name: "acme"}, client)
	ok(t, err)
}

func TestInviteWithoutMail(t *testing.T) {
	f := newFixture(t, func(c *config.Config) { c.Mail.Enabled = false })
	ctx := context.Background()
	s := f.svc
	alice, bob := f.user(t, "alice", "user"), f.user(t, "bob", "user")
	_, err := s.CreateOrg(ctx, alice, orgs.CreateOrgInput{Name: "acme"}, client)
	ok(t, err)
	inv, err := s.CreateInvite(ctx, alice, "acme", orgs.InviteInput{Email: "bob@example.com", Role: "member"}, client)
	ok(t, err)
	if inv.Token == "" || len(f.Jobs.Take()) != 0 {
		t.Fatalf("invite = %+v", inv)
	}
	list, err := s.ListInvites(ctx, alice, "acme")
	ok(t, err)
	if len(list) != 1 || list[0].Token != "" || list[0].InvitedBy != "alice" {
		t.Fatalf("list = %+v", list)
	}
	ok(t, s.RevokeInvite(ctx, alice, "acme", inv.ID, client))
	is(t, s.RevokeInvite(ctx, alice, "acme", inv.ID, client), orgs.ErrNotFound)
	_, err = s.AcceptInvite(ctx, bob, inv.Token, client)
	is(t, err, orgs.ErrInvalidToken)
	_, err = s.AcceptInvite(ctx, bob, "garbage", client)
	is(t, err, orgs.ErrInvalidToken)
}

func TestInviteExpiryAndVerification(t *testing.T) {
	f := newFixture(t, func(c *config.Config) { c.Mail.Enabled = false })
	ctx := context.Background()
	now := time.Now()
	s := orgs.New(orgs.Options{Pool: f.Pool, Config: f.Config, Now: func() time.Time { return now }})
	alice, bob := f.user(t, "alice", "user"), f.user(t, "bob", "user")
	_, err := s.CreateOrg(ctx, alice, orgs.CreateOrgInput{Name: "acme"}, client)
	ok(t, err)
	inv, err := s.CreateInvite(ctx, alice, "acme", orgs.InviteInput{Email: "bob@example.com", Role: "member"}, client)
	ok(t, err)
	now = now.Add(time.Duration(f.Config.Auth.InviteTTL) + time.Minute)
	_, err = s.AcceptInvite(ctx, bob, inv.Token, client)
	is(t, err, orgs.ErrInvalidToken)
	// An expired invitation no longer blocks a new one.
	inv, err = s.CreateInvite(ctx, alice, "acme", orgs.InviteInput{Email: "bob@example.com", Role: "member"}, client)
	ok(t, err)
	_, err = f.Pool.Exec(ctx, `UPDATE emails SET verified_at = NULL WHERE user_id = $1`, bob.UserID)
	ok(t, err)
	// Verification is only enforced with mail on; it is off here.
	_, err = s.AcceptInvite(ctx, bob, inv.Token, client)
	ok(t, err)
}

func TestInviteRequiresVerifiedEmail(t *testing.T) {
	f := newFixture(t, func(c *config.Config) { c.Auth.RequireVerifiedEmail = true })
	ctx := context.Background()
	alice, bob := f.user(t, "alice", "user"), f.user(t, "bob", "user")
	_, err := f.svc.CreateOrg(ctx, alice, orgs.CreateOrgInput{Name: "acme"}, client)
	ok(t, err)
	_, err = f.svc.CreateInvite(ctx, alice, "acme", orgs.InviteInput{Email: "bob@example.com", Role: "member"}, client)
	ok(t, err)
	tok := f.inviteToken(t)
	_, err = f.Pool.Exec(ctx, `UPDATE emails SET verified_at = NULL WHERE user_id = $1`, bob.UserID)
	ok(t, err)
	_, err = f.svc.AcceptInvite(ctx, bob, tok, client)
	is(t, err, orgs.ErrEmailNotVerified)
}

func ptr[T any](v T) *T { return &v }
