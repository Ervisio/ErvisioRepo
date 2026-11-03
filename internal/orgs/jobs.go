package orgs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/jobs"
	"github.com/Ervisio/ErvisioRepo/internal/mail"
)

// AcceptPath is the web UI path of the invitation link; the page posts the
// token to /api/v1/orgs/invites/accept.
const AcceptPath = "/org-invite"

// OrgInviteArgs mails an organization invitation. Like the identity jobs it
// never holds the token: the worker creates it right before sending.
type OrgInviteArgs struct {
	InviteID uuid.UUID `json:"invite_id"`
}

func (OrgInviteArgs) Kind() string { return "orgs_invite" }

func (OrgInviteArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 5} }

// CleanupArgs deletes invitations expired for more than a week.
type CleanupArgs struct{}

func (CleanupArgs) Kind() string { return "orgs_cleanup" }

// CleanupInterval is how often CleanupArgs runs; CleanupGrace how long
// expired invitations stay listed.
const (
	CleanupInterval = 6 * time.Hour
	CleanupGrace    = 7 * 24 * time.Hour
)

// Mailer holds what the organization workers need.
type Mailer struct {
	Queries   *dbgen.Queries
	Sender    mail.Sender
	PublicURL string
	TTL       time.Duration // invitation lifetime
	Log       *slog.Logger
	Now       func() time.Time
}

// MailerFor builds the Mailer from cfg.
func MailerFor(cfg *config.Config, q *dbgen.Queries, s mail.Sender, log *slog.Logger) *Mailer {
	return &Mailer{Queries: q, Sender: s, PublicURL: cfg.Server.PublicURL, TTL: time.Duration(cfg.Auth.InviteTTL), Log: log}
}

func (m *Mailer) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// JobsOption registers the organization workers and the periodic cleanup.
func JobsOption(m *Mailer) jobs.Option {
	if m.Log == nil {
		m.Log = slog.New(slog.DiscardHandler)
	}
	return func(r *jobs.Registry) {
		river.AddWorker(r.Workers, &inviteWorker{m: m})
		river.AddWorker(r.Workers, &cleanupWorker{m: m})
		r.Periodic = append(r.Periodic, river.NewPeriodicJob(river.PeriodicInterval(CleanupInterval),
			func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{}, nil }, nil))
	}
}

type inviteData struct {
	Instance string
	Org      string
	Inviter  string
	Role     string
	Link     string
	Expires  string
}

func (m *Mailer) instance() string {
	if u, err := url.Parse(m.PublicURL); err == nil && u.Host != "" {
		return u.Host
	}
	return m.PublicURL
}

func (m *Mailer) link(token string) string {
	u, err := url.Parse(m.PublicURL)
	if err != nil {
		return m.PublicURL + AcceptPath + "?token=" + url.QueryEscape(token)
	}
	u = u.JoinPath(AcceptPath)
	u.RawQuery = url.Values{"token": {token}}.Encode()
	return u.String()
}

// days renders a lifetime in whole days, or hours below two days.
func days(d time.Duration, locale string) string {
	unit := func(n int, one, many string) string {
		if n == 1 {
			return "1 " + one
		}
		return fmt.Sprintf("%d %s", n, many)
	}
	if d < 48*time.Hour {
		n := max(int(d/time.Hour), 1)
		if locale == "it" {
			return unit(n, "ora", "ore")
		}
		return unit(n, "hour", "hours")
	}
	n := int(d / (24 * time.Hour))
	if locale == "it" {
		return unit(n, "giorno", "giorni")
	}
	return unit(n, "day", "days")
}

// Deliver sends the invitation now (the worker body, for tests and callers
// without a queue).
func (m *Mailer) Deliver(ctx context.Context, a OrgInviteArgs) error {
	inv, err := m.Queries.GetOrgInvite(ctx, a.InviteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // revoked or the organization was deleted
	}
	if err != nil {
		return fmt.Errorf("get invite: %w", err)
	}
	if inv.AcceptedAt.Valid {
		return nil
	}
	token, hash := auth.NewToken()
	n, err := m.Queries.SetOrgInviteToken(ctx, dbgen.SetOrgInviteTokenParams{ID: inv.ID, TokenHash: hash,
		ExpiresAt: pgtype.Timestamptz{Time: m.now().Add(m.TTL), Valid: true}})
	if err != nil {
		return fmt.Errorf("set invite token: %w", err)
	}
	if n == 0 {
		return nil
	}
	locale := inv.InviterLocale
	inviter := inv.InvitedByName
	if inviter == "" {
		inviter = m.instance()
	}
	subject, body, err := mail.Render(locale, mail.TemplateOrgInvite, inviteData{
		Instance: m.instance(), Org: inv.OrgName, Inviter: inviter, Role: inv.Role,
		Link: m.link(token), Expires: days(m.TTL, locale),
	})
	if err != nil {
		return fmt.Errorf("render invite: %w", err)
	}
	if err := m.Sender.Send(ctx, mail.Message{To: inv.Email, Subject: subject, Body: body}); err != nil {
		return fmt.Errorf("send invite: %w", err)
	}
	return nil
}

type inviteWorker struct {
	river.WorkerDefaults[OrgInviteArgs]
	m *Mailer
}

func (w *inviteWorker) Work(ctx context.Context, j *river.Job[OrgInviteArgs]) error {
	return w.m.Deliver(ctx, j.Args)
}

type cleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	m *Mailer
}

func (w *cleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	n, err := w.m.Queries.DeleteExpiredOrgInvites(ctx, pgtype.Timestamptz{Time: w.m.now().Add(-CleanupGrace), Valid: true})
	if err != nil {
		return fmt.Errorf("delete expired invites: %w", err)
	}
	if n > 0 {
		w.m.Log.InfoContext(ctx, "deleted expired organization invitations", "count", n)
	}
	return nil
}
