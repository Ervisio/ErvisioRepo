package identity

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
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/jobs"
	"github.com/Ervisio/ErvisioRepo/internal/mail"
)

// Mail jobs. Their arguments never hold tokens: the worker creates the
// token right before sending, so a token exists only in the mail.

// VerifyEmailArgs sends a verification link for one address.
type VerifyEmailArgs struct {
	EmailID uuid.UUID `json:"email_id"`
}

func (VerifyEmailArgs) Kind() string { return "identity_verify_email" }

// ResendVerificationArgs sends a verification link if the address belongs
// to an account and is not verified yet.
type ResendVerificationArgs struct {
	Address string `json:"address"`
}

func (ResendVerificationArgs) Kind() string { return "identity_resend_verification" }

// PasswordResetArgs sends a reset link if the address belongs to an account.
type PasswordResetArgs struct {
	Address string `json:"address"`
}

func (PasswordResetArgs) Kind() string { return "identity_password_reset" }

// RegistrationNoticeArgs tells the owner of an address that someone tried
// to register with it.
type RegistrationNoticeArgs struct {
	Address string `json:"address"`
}

func (RegistrationNoticeArgs) Kind() string { return "identity_registration_notice" }

// CleanupArgs deletes expired sessions and one-time tokens.
type CleanupArgs struct{}

func (CleanupArgs) Kind() string { return "identity_cleanup" }

// CleanupInterval is how often CleanupArgs runs.
const CleanupInterval = time.Hour

func mailOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 5} }

func (VerifyEmailArgs) InsertOpts() river.InsertOpts        { return mailOpts() }
func (ResendVerificationArgs) InsertOpts() river.InsertOpts { return mailOpts() }
func (PasswordResetArgs) InsertOpts() river.InsertOpts      { return mailOpts() }
func (RegistrationNoticeArgs) InsertOpts() river.InsertOpts { return mailOpts() }

// Mailer holds what the mail workers need.
type Mailer struct {
	Queries   *dbgen.Queries
	Sender    mail.Sender
	PublicURL string
	VerifyTTL time.Duration
	ResetTTL  time.Duration
	IdleTTL   time.Duration // session idle timeout, for the cleanup job
	Log       *slog.Logger
	Now       func() time.Time
}

func (m *Mailer) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// JobsOption registers the identity workers and the periodic cleanup.
func JobsOption(m *Mailer) jobs.Option {
	if m.Log == nil {
		m.Log = slog.New(slog.DiscardHandler)
	}
	return func(r *jobs.Registry) {
		river.AddWorker(r.Workers, &verifyWorker{m: m})
		river.AddWorker(r.Workers, &resendWorker{m: m})
		river.AddWorker(r.Workers, &resetWorker{m: m})
		river.AddWorker(r.Workers, &noticeWorker{m: m})
		river.AddWorker(r.Workers, &cleanupWorker{m: m})
		r.Periodic = append(r.Periodic, river.NewPeriodicJob(river.PeriodicInterval(CleanupInterval),
			func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{}, nil }, nil))
	}
}

type mailData struct {
	Instance string
	Username string
	Link     string
	Expires  string
}

func (m *Mailer) instance() string {
	if u, err := url.Parse(m.PublicURL); err == nil && u.Host != "" {
		return u.Host
	}
	return m.PublicURL
}

func (m *Mailer) link(path, token string) string {
	u, err := url.Parse(m.PublicURL)
	if err != nil {
		return m.PublicURL + path
	}
	u = u.JoinPath(path)
	if token != "" {
		u.RawQuery = url.Values{"token": {token}}.Encode()
	}
	return u.String()
}

// humanDuration renders a TTL in whole hours (or minutes below one hour).
func humanDuration(d time.Duration, locale string) string {
	if d < time.Hour {
		n := int(d / time.Minute)
		if locale == "it" {
			return plural(n, "minuto", "minuti")
		}
		return plural(n, "minute", "minutes")
	}
	n := int(d / time.Hour)
	if locale == "it" {
		return plural(n, "ora", "ore")
	}
	return plural(n, "hour", "hours")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func (m *Mailer) send(ctx context.Context, to, locale, template string, data mailData) error {
	subject, body, err := mail.Render(locale, template, data)
	if err != nil {
		return err
	}
	if err := m.Sender.Send(ctx, mail.Message{To: to, Subject: subject, Body: body}); err != nil {
		return fmt.Errorf("send %s: %w", template, err)
	}
	return nil
}

// issue replaces the outstanding tokens of purpose for the user with a new one.
func (m *Mailer) issue(ctx context.Context, userID uuid.UUID, emailID uuid.UUID, purpose string, ttl time.Duration) (string, error) {
	if err := m.Queries.DeleteOneTimeTokens(ctx, dbgen.DeleteOneTimeTokensParams{UserID: userID, Purpose: purpose}); err != nil {
		return "", fmt.Errorf("delete old tokens: %w", err)
	}
	token, hash := auth.NewToken()
	if err := m.Queries.InsertOneTimeToken(ctx, dbgen.InsertOneTimeTokenParams{
		TokenHash: hash, Purpose: purpose, UserID: userID, EmailID: uuid.NullUUID{UUID: emailID, Valid: true},
		ExpiresAt: pgtype.Timestamptz{Time: m.now().Add(ttl), Valid: true},
	}); err != nil {
		return "", fmt.Errorf("insert token: %w", err)
	}
	return token, nil
}

type emailRow = dbgen.GetEmailByIDRow

func (m *Mailer) sendVerification(ctx context.Context, e emailRow) error {
	if e.VerifiedAt.Valid || e.SuspendedAt.Valid {
		return nil
	}
	token, err := m.issue(ctx, e.UserID, e.ID, PurposeVerifyEmail, m.VerifyTTL)
	if err != nil {
		return err
	}
	return m.send(ctx, e.Address, e.Locale, mail.TemplateVerifyEmail, mailData{
		Instance: m.instance(), Username: e.Username, Link: m.link("/verify-email", token),
		Expires: humanDuration(m.VerifyTTL, e.Locale),
	})
}

func (m *Mailer) byAddress(ctx context.Context, addr string) (emailRow, bool, error) {
	row, err := m.Queries.GetEmailByAddress(ctx, addr)
	if errors.Is(err, pgx.ErrNoRows) {
		return emailRow{}, false, nil
	}
	if err != nil {
		return emailRow{}, false, fmt.Errorf("get email: %w", err)
	}
	return emailRow(row), true, nil
}

type verifyWorker struct {
	river.WorkerDefaults[VerifyEmailArgs]
	m *Mailer
}

func (w *verifyWorker) Work(ctx context.Context, j *river.Job[VerifyEmailArgs]) error {
	e, err := w.m.Queries.GetEmailByID(ctx, j.Args.EmailID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get email: %w", err)
	}
	return w.m.sendVerification(ctx, e)
}

type resendWorker struct {
	river.WorkerDefaults[ResendVerificationArgs]
	m *Mailer
}

func (w *resendWorker) Work(ctx context.Context, j *river.Job[ResendVerificationArgs]) error {
	e, ok, err := w.m.byAddress(ctx, j.Args.Address)
	if err != nil || !ok {
		return err
	}
	return w.m.sendVerification(ctx, e)
}

type resetWorker struct {
	river.WorkerDefaults[PasswordResetArgs]
	m *Mailer
}

func (w *resetWorker) Work(ctx context.Context, j *river.Job[PasswordResetArgs]) error {
	e, ok, err := w.m.byAddress(ctx, j.Args.Address)
	if err != nil || !ok || e.SuspendedAt.Valid {
		return err
	}
	token, err := w.m.issue(ctx, e.UserID, e.ID, PurposeResetPassword, w.m.ResetTTL)
	if err != nil {
		return err
	}
	return w.m.send(ctx, e.Address, e.Locale, mail.TemplateResetPassword, mailData{
		Instance: w.m.instance(), Username: e.Username, Link: w.m.link("/reset-password", token),
		Expires: humanDuration(w.m.ResetTTL, e.Locale),
	})
}

type noticeWorker struct {
	river.WorkerDefaults[RegistrationNoticeArgs]
	m *Mailer
}

func (w *noticeWorker) Work(ctx context.Context, j *river.Job[RegistrationNoticeArgs]) error {
	e, ok, err := w.m.byAddress(ctx, j.Args.Address)
	if err != nil || !ok {
		return err
	}
	return w.m.send(ctx, e.Address, e.Locale, mail.TemplateRegistrationNotice, mailData{
		Instance: w.m.instance(), Username: e.Username, Link: w.m.link("/login", ""),
	})
}

type cleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	m *Mailer
}

func (w *cleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	sessions, err := w.m.Queries.DeleteExpiredSessions(ctx, pgtype.Timestamptz{Time: w.m.now().Add(-w.m.IdleTTL), Valid: true})
	if err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	tokens, err := w.m.Queries.DeleteExpiredOneTimeTokens(ctx)
	if err != nil {
		return fmt.Errorf("delete expired tokens: %w", err)
	}
	if sessions+tokens > 0 {
		w.m.Log.InfoContext(ctx, "identity cleanup", "sessions", sessions, "tokens", tokens)
	}
	return nil
}

// Deliver runs the worker of a mail or cleanup job synchronously, outside
// the queue (tests and tools).
func (m *Mailer) Deliver(ctx context.Context, args river.JobArgs) error {
	if m.Log == nil {
		m.Log = slog.New(slog.DiscardHandler)
	}
	switch a := args.(type) {
	case VerifyEmailArgs:
		return (&verifyWorker{m: m}).Work(ctx, &river.Job[VerifyEmailArgs]{Args: a})
	case ResendVerificationArgs:
		return (&resendWorker{m: m}).Work(ctx, &river.Job[ResendVerificationArgs]{Args: a})
	case PasswordResetArgs:
		return (&resetWorker{m: m}).Work(ctx, &river.Job[PasswordResetArgs]{Args: a})
	case RegistrationNoticeArgs:
		return (&noticeWorker{m: m}).Work(ctx, &river.Job[RegistrationNoticeArgs]{Args: a})
	case CleanupArgs:
		return (&cleanupWorker{m: m}).Work(ctx, &river.Job[CleanupArgs]{Args: a})
	}
	return fmt.Errorf("identity: unknown job kind %q", args.Kind())
}
