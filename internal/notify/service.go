// Package notify keeps the notifications of the users (PLAN §5.7): one
// thread per user and subject (issue, pull request or release), refreshed
// by the events of the bus with the reason the user receives it, plus the
// watch settings per repository and per thread. Threads are also mailed
// (internal/mail) to users who did not turn email off.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/mail"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// Reasons a user receives a thread, from the strongest.
const (
	ReasonReviewRequested = "review_requested"
	ReasonAssigned        = "assigned"
	ReasonMentioned       = "mentioned"
	ReasonAuthor          = "author"
	ReasonComment         = "comment"
	ReasonStateChange     = "state_change"
	ReasonSubscribed      = "subscribed"
)

// Watch levels of a repository.
const (
	LevelWatching      = "watching"
	LevelParticipating = "participating"
	LevelIgnoring      = "ignoring"
)

// Subject types.
const (
	SubjectIssue   = "issue"
	SubjectPull    = "pull_request"
	SubjectRelease = "release"
)

const (
	defaultPageSize = 30
	maxPageSize     = 100
)

var (
	// ErrNotFound means the thread (or the repository) does not exist for
	// the caller.
	ErrNotFound = errors.New("notification thread not found")
	// ErrUnauthenticated means the caller is anonymous.
	ErrUnauthenticated = errors.New("notifications need a signed-in user")
)

// ValidationError is an invalid request.
type ValidationError struct{ msg string }

func (e *ValidationError) Error() string { return e.msg }

// Repos is the part of the repository service notify uses.
type Repos interface {
	Get(ctx context.Context, who auth.Principal, owner, name string) (repos.Repo, error)
	GetByID(ctx context.Context, who auth.Principal, id uuid.UUID) (repos.Repo, error)
}

// Enqueuer inserts jobs (jobs.Runner).
type Enqueuer interface {
	Enqueue(ctx context.Context, args river.JobArgs) error
}

// Options configure the service.
type Options struct {
	Pool *pgxpool.Pool
	// PublicURL is the base of the links in the mails.
	PublicURL string
	// Sender mails the notifications; nil disables mail.
	Sender mail.Sender
	Log    *slog.Logger
}

// Service keeps the notifications.
type Service struct {
	o     Options
	q     *dbgen.Queries
	repos atomic.Value // Repos
	jobs  atomic.Value // Enqueuer
}

// New builds the service.
func New(o Options) *Service {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return &Service{o: o, q: dbgen.New(o.Pool)}
}

// SetRepos sets the repository service (built after the job runner).
func (s *Service) SetRepos(r Repos) { s.repos.Store(r) }

// SetEnqueuer sets the job runner.
func (s *Service) SetEnqueuer(e Enqueuer) { s.jobs.Store(e) }

func (s *Service) reposSvc() (Repos, error) {
	r, _ := s.repos.Load().(Repos)
	if r == nil {
		return nil, errors.New("notify: repository service not set")
	}
	return r, nil
}

// Thread is a notification thread of the caller.
type Thread struct {
	ID          uuid.UUID
	RepoID      uuid.UUID
	Owner       string
	Repo        string
	SubjectType string
	Number      int64
	Title       string
	Reason      string
	Summary     string
	Unread      bool
	LastReadAt  *time.Time
	UpdatedAt   time.Time
}

func threadOut(t dbgen.NotificationThread, owner, repo string) Thread {
	out := Thread{ID: t.ID, RepoID: t.RepoID, Owner: owner, Repo: repo, SubjectType: t.SubjectType, Number: t.Number,
		Title: t.Title, Reason: t.Reason, Summary: t.Summary, Unread: t.Unread, UpdatedAt: t.UpdatedAt.Time}
	if t.LastReadAt.Valid {
		v := t.LastReadAt.Time
		out.LastReadAt = &v
	}
	return out
}

func signedIn(who auth.Principal) error {
	if who.UserID == uuid.Nil {
		return ErrUnauthenticated
	}
	return nil
}

// ListOptions filter the threads.
type ListOptions struct {
	// All includes the read threads.
	All bool
	// Participating leaves out the threads received as a watcher.
	Participating bool
	Since         *time.Time
	// Owner and Name, when set, keep the threads of one repository the
	// caller can read.
	Owner, Name   string
	Offset, Limit int
}

// List returns the threads of the caller that are not done, the most
// recently updated first, and whether more threads follow the page.
// Repository errors (an unreadable Owner/Name) are returned as they are.
func (s *Service) List(ctx context.Context, who auth.Principal, o ListOptions) ([]Thread, bool, error) {
	if err := signedIn(who); err != nil {
		return nil, false, err
	}
	if o.Limit <= 0 {
		o.Limit = defaultPageSize
	}
	o.Limit = min(o.Limit, maxPageSize)
	o.Offset = max(o.Offset, 0)
	p := dbgen.ListNotificationThreadsParams{UserID: who.UserID, IncludeRead: o.All, Participating: o.Participating,
		Off: int32(min(o.Offset, 1<<31-1)), Lim: int32(o.Limit + 1)} //nolint:gosec // bounded
	if o.Since != nil {
		p.Since = pgtype.Timestamptz{Time: *o.Since, Valid: true}
	}
	if o.Owner != "" || o.Name != "" {
		rs, err := s.reposSvc()
		if err != nil {
			return nil, false, err
		}
		repo, err := rs.Get(ctx, who, o.Owner, o.Name)
		if err != nil {
			return nil, false, err
		}
		p.RepoID = uuid.NullUUID{UUID: repo.ID, Valid: true}
	}
	rows, err := s.q.ListNotificationThreads(ctx, p)
	if err != nil {
		return nil, false, fmt.Errorf("list notifications: %w", err)
	}
	more := len(rows) > o.Limit
	if more {
		rows = rows[:o.Limit]
	}
	out := make([]Thread, 0, len(rows))
	for _, r := range rows {
		out = append(out, threadOut(r.NotificationThread, r.OwnerName, r.RepoName))
	}
	return out, more, nil
}

// RepoCount is the number of threads of the caller in one repository.
type RepoCount struct {
	RepoID        uuid.UUID
	Owner, Repo   string
	Count, Unread int64
}

// RepoCounts returns, per repository, the threads of the caller that match
// all and participating (as in List) and the unread ones among them, the
// most recently updated repository first.
func (s *Service) RepoCounts(ctx context.Context, who auth.Principal, all, participating bool) ([]RepoCount, error) {
	if err := signedIn(who); err != nil {
		return nil, err
	}
	rows, err := s.q.ListNotificationRepoCounts(ctx, dbgen.ListNotificationRepoCountsParams{UserID: who.UserID,
		IncludeRead: all, Participating: participating})
	if err != nil {
		return nil, fmt.Errorf("count notifications per repository: %w", err)
	}
	out := make([]RepoCount, 0, len(rows))
	for _, r := range rows {
		out = append(out, RepoCount{RepoID: r.RepoID, Owner: r.OwnerName, Repo: r.RepoName, Count: r.Total, Unread: r.Unread})
	}
	return out, nil
}

// Count returns the number of unread threads of the caller.
func (s *Service) Count(ctx context.Context, who auth.Principal) (int64, error) {
	if err := signedIn(who); err != nil {
		return 0, err
	}
	n, err := s.q.CountUnreadNotifications(ctx, who.UserID)
	if err != nil {
		return 0, fmt.Errorf("count notifications: %w", err)
	}
	return n, nil
}

func (s *Service) thread(ctx context.Context, who auth.Principal, id uuid.UUID) (dbgen.GetNotificationThreadRow, error) {
	if err := signedIn(who); err != nil {
		return dbgen.GetNotificationThreadRow{}, err
	}
	row, err := s.q.GetNotificationThread(ctx, dbgen.GetNotificationThreadParams{ID: id, UserID: who.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, ErrNotFound
	}
	if err != nil {
		return row, fmt.Errorf("get notification: %w", err)
	}
	return row, nil
}

// Get returns one thread of the caller.
func (s *Service) Get(ctx context.Context, who auth.Principal, id uuid.UUID) (Thread, error) {
	row, err := s.thread(ctx, who, id)
	if err != nil {
		return Thread{}, err
	}
	return threadOut(row.NotificationThread, row.OwnerName, row.RepoName), nil
}

// MarkRead marks a thread read.
func (s *Service) MarkRead(ctx context.Context, who auth.Principal, id uuid.UUID) error {
	if err := signedIn(who); err != nil {
		return err
	}
	n, err := s.q.MarkNotificationThreadRead(ctx, dbgen.MarkNotificationThreadReadParams{ID: id, UserID: who.UserID})
	if err != nil {
		return fmt.Errorf("mark notification read: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkDone marks a thread done: it leaves the list until a new event.
func (s *Service) MarkDone(ctx context.Context, who auth.Principal, id uuid.UUID) error {
	if err := signedIn(who); err != nil {
		return err
	}
	n, err := s.q.MarkNotificationThreadDone(ctx, dbgen.MarkNotificationThreadDoneParams{ID: id, UserID: who.UserID})
	if err != nil {
		return fmt.Errorf("mark notification done: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkAllRead marks read the threads updated up to before (now when nil).
func (s *Service) MarkAllRead(ctx context.Context, who auth.Principal, before *time.Time) error {
	if err := signedIn(who); err != nil {
		return err
	}
	t := time.Now()
	if before != nil {
		t = *before
	}
	_, err := s.q.MarkNotificationsRead(ctx, dbgen.MarkNotificationsReadParams{UserID: who.UserID,
		Before: pgtype.Timestamptz{Time: t, Valid: true}})
	if err != nil {
		return fmt.Errorf("mark notifications read: %w", err)
	}
	return nil
}

// EmailEnabled reports whether the caller receives notifications by mail.
func (s *Service) EmailEnabled(ctx context.Context, who auth.Principal) (bool, error) {
	if err := signedIn(who); err != nil {
		return false, err
	}
	on, err := s.q.GetEmailNotifications(ctx, who.UserID)
	if err != nil {
		return false, fmt.Errorf("get notification settings: %w", err)
	}
	return on, nil
}

// SetEmailEnabled turns the mails of the caller on or off.
func (s *Service) SetEmailEnabled(ctx context.Context, who auth.Principal, on bool) error {
	if err := signedIn(who); err != nil {
		return err
	}
	if err := s.q.SetEmailNotifications(ctx, dbgen.SetEmailNotificationsParams{ID: who.UserID, EmailNotifications: on}); err != nil {
		return fmt.Errorf("set notification settings: %w", err)
	}
	return nil
}

// ThreadSubscription is the subscription of the caller to the subject of a
// thread.
type ThreadSubscription struct {
	Subscribed bool
	Ignored    bool
	// Explicit is false when the default rules apply.
	Explicit bool
}

// ThreadSubscription returns the subscription of a thread: without an
// explicit choice the caller is subscribed (it receives the thread).
func (s *Service) ThreadSubscription(ctx context.Context, who auth.Principal, threadID uuid.UUID) (ThreadSubscription, error) {
	row, err := s.thread(ctx, who, threadID)
	if err != nil {
		return ThreadSubscription{}, err
	}
	sub, err := s.q.GetThreadSubscription(ctx, dbgen.GetThreadSubscriptionParams{UserID: who.UserID,
		SubjectKey: row.NotificationThread.SubjectKey})
	if errors.Is(err, pgx.ErrNoRows) {
		return ThreadSubscription{Subscribed: true}, nil
	}
	if err != nil {
		return ThreadSubscription{}, fmt.Errorf("get thread subscription: %w", err)
	}
	return ThreadSubscription{Subscribed: sub.Subscribed, Ignored: !sub.Subscribed, Explicit: true}, nil
}

// SetThreadSubscription subscribes the caller to the subject of a thread,
// or unsubscribes it when ignored.
func (s *Service) SetThreadSubscription(ctx context.Context, who auth.Principal, threadID uuid.UUID, ignored bool) (ThreadSubscription, error) {
	row, err := s.thread(ctx, who, threadID)
	if err != nil {
		return ThreadSubscription{}, err
	}
	t := row.NotificationThread
	sub, err := s.q.UpsertThreadSubscription(ctx, dbgen.UpsertThreadSubscriptionParams{UserID: who.UserID,
		RepoID: t.RepoID, SubjectKey: t.SubjectKey, Subscribed: !ignored})
	if err != nil {
		return ThreadSubscription{}, fmt.Errorf("set thread subscription: %w", err)
	}
	return ThreadSubscription{Subscribed: sub.Subscribed, Ignored: !sub.Subscribed, Explicit: true}, nil
}

// DeleteThreadSubscription returns a thread to the default rules.
func (s *Service) DeleteThreadSubscription(ctx context.Context, who auth.Principal, threadID uuid.UUID) error {
	row, err := s.thread(ctx, who, threadID)
	if err != nil {
		return err
	}
	if err := s.q.DeleteThreadSubscription(ctx, dbgen.DeleteThreadSubscriptionParams{UserID: who.UserID,
		SubjectKey: row.NotificationThread.SubjectKey}); err != nil {
		return fmt.Errorf("delete thread subscription: %w", err)
	}
	return nil
}

// RepoSubscription is the watch level of the caller on a repository.
type RepoSubscription struct {
	Level    string
	Explicit bool
}

// RepoSubscription returns the watch level of the caller on a repository
// it can read (participating by default). Repository errors are returned
// as they are.
func (s *Service) RepoSubscription(ctx context.Context, who auth.Principal, owner, name string) (RepoSubscription, error) {
	if err := signedIn(who); err != nil {
		return RepoSubscription{}, err
	}
	rs, err := s.reposSvc()
	if err != nil {
		return RepoSubscription{}, err
	}
	repo, err := rs.Get(ctx, who, owner, name)
	if err != nil {
		return RepoSubscription{}, err
	}
	sub, err := s.q.GetRepoSubscription(ctx, dbgen.GetRepoSubscriptionParams{UserID: who.UserID, RepoID: repo.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return RepoSubscription{Level: LevelParticipating}, nil
	}
	if err != nil {
		return RepoSubscription{}, fmt.Errorf("get repository subscription: %w", err)
	}
	return RepoSubscription{Level: sub.Level, Explicit: true}, nil
}

// SetRepoSubscription sets the watch level of the caller on a repository
// it can read.
func (s *Service) SetRepoSubscription(ctx context.Context, who auth.Principal, owner, name, level string) (RepoSubscription, error) {
	if err := signedIn(who); err != nil {
		return RepoSubscription{}, err
	}
	switch level {
	case LevelWatching, LevelParticipating, LevelIgnoring:
	default:
		return RepoSubscription{}, &ValidationError{msg: "level must be watching, participating or ignoring"}
	}
	rs, err := s.reposSvc()
	if err != nil {
		return RepoSubscription{}, err
	}
	repo, err := rs.Get(ctx, who, owner, name)
	if err != nil {
		return RepoSubscription{}, err
	}
	sub, err := s.q.UpsertRepoSubscription(ctx, dbgen.UpsertRepoSubscriptionParams{UserID: who.UserID,
		RepoID: repo.ID, Level: level})
	if err != nil {
		return RepoSubscription{}, fmt.Errorf("set repository subscription: %w", err)
	}
	return RepoSubscription{Level: sub.Level, Explicit: true}, nil
}
