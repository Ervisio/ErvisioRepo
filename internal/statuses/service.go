// Package statuses implements commit statuses and check runs/suites
// reported by CI systems (PLAN §5). Both are keyed by commit sha: a ref given
// by a caller is resolved to its commit through the git browsing service.
// The latest status of each context and the latest check run of each name
// feed the required status checks of branch protection (Statuses implements
// protection.CheckSource); every report emits an event so that pull request
// auto-merge and webhooks react to it.
package statuses

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/protection"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// DefaultApp is the app name of check suites reported without a named
// reporter (an anonymous internal caller).
const DefaultApp = "ervisio-actions"

// Limits.
const (
	// MaxStatusesPerContext bounds the statuses of one (commit, context).
	MaxStatusesPerContext = 1000
	// MaxAnnotationsPerRequest bounds the annotations of one create/update.
	MaxAnnotationsPerRequest = 50
	// MaxAnnotations bounds the annotations of one check run.
	MaxAnnotations = 1000
	// MaxPage bounds a page of results.
	MaxPage = 100
)

// Commit status states.
const (
	StateError   = protection.StateError
	StateFailure = protection.StateFailure
	StatePending = protection.StatePending
	StateSuccess = protection.StateSuccess
)

// Check run and suite statuses.
const (
	StatusQueued     = "queued"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
)

// Errors. Repository lookups surface repos.ErrNotFound, repos.ErrForbidden
// and *repos.MovedError unchanged.
var (
	// ErrCommitNotFound: the ref or sha does not name a commit.
	ErrCommitNotFound = errors.New("no commit found for the ref")
	// ErrCheckRunNotFound: unknown check run id in the repository.
	ErrCheckRunNotFound = errors.New("check run not found")
	// ErrCheckSuiteNotFound: unknown check suite id in the repository.
	ErrCheckSuiteNotFound = errors.New("check suite not found")
)

// ValidationError is a rejected input (HTTP 400/422).
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

// RepoAccess resolves a repository for a caller (*repos.Service).
type RepoAccess interface {
	Get(ctx context.Context, who auth.Principal, owner, name string) (repos.Repo, error)
}

// Resolver resolves refs to commits (*gitbrowse.Service).
type Resolver interface {
	Resolve(ctx context.Context, repo gitbrowse.Repo, ref string) (gitbrowse.Resolved, error)
}

// Options configures a Service.
type Options struct {
	Pool   *pgxpool.Pool
	Repos  RepoAccess
	Browse Resolver
	// Bus is kicked after an event is recorded; nil: the periodic drain
	// delivers it.
	Bus *events.Bus
	Log *slog.Logger
	Now func() time.Time
}

// Service implements commit statuses and checks.
type Service struct {
	o   Options
	q   *dbgen.Queries
	log *slog.Logger
}

var _ protection.CheckSource = (*Service)(nil)

// New builds a Service.
func New(o Options) *Service {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{o: o, q: dbgen.New(o.Pool), log: o.Log}
}

// Status is one commit status.
type Status struct {
	ID          uuid.UUID
	RepoID      uuid.UUID
	SHA         string
	State       string
	Context     string
	Description string
	TargetURL   string
	CreatorID   uuid.UUID
	Creator     string // username; empty when unknown
	CreatedAt   time.Time
}

// Combined is the combined status of a commit: the latest status of each
// context and their aggregate state.
type Combined struct {
	SHA      string
	State    string
	Statuses []Status
}

// StatusInput is a new commit status.
type StatusInput struct {
	State       string
	Context     string // "default" when empty
	Description string
	TargetURL   string
}

// Annotation is a line-level message of a check run.
type Annotation struct {
	Path        string `json:"path"`
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line"`
	StartColumn *int   `json:"start_column,omitempty"`
	EndColumn   *int   `json:"end_column,omitempty"`
	Level       string `json:"annotation_level"` // notice, warning or failure
	Message     string `json:"message"`
	Title       string `json:"title,omitempty"`
	RawDetails  string `json:"raw_details,omitempty"`
}

// Output is the report of a check run. Annotations are appended to the
// existing ones on update.
type Output struct {
	Title       string
	Summary     string
	Text        string
	Annotations []Annotation
}

// CheckRun is one check run.
type CheckRun struct {
	ID          uuid.UUID
	SuiteID     uuid.UUID
	RepoID      uuid.UUID
	HeadSHA     string
	HeadBranch  string
	Name        string
	App         string
	Status      string
	Conclusion  string // empty unless completed
	ExternalID  string
	DetailsURL  string
	Title       string
	Summary     string
	Text        string
	Annotations int // number of annotations
	StartedAt   *time.Time
	CompletedAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CheckSuite groups the check runs of one app on one commit.
type CheckSuite struct {
	ID         uuid.UUID
	RepoID     uuid.UUID
	HeadSHA    string
	HeadBranch string
	App        string
	Status     string
	Conclusion string // empty unless completed
	Runs       int    // number of distinct run names
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// CheckRunInput is a new check run.
type CheckRunInput struct {
	Name    string
	HeadSHA string // a commit sha or any ref
	// Status defaults to queued (completed when Conclusion is set).
	Status      string
	Conclusion  string
	ExternalID  string
	DetailsURL  string
	StartedAt   *time.Time
	CompletedAt *time.Time
	Output      *Output
	// App names the reporter; empty: the caller's username, or DefaultApp.
	App string
}

// CheckRunPatch changes a check run; nil fields are kept.
type CheckRunPatch struct {
	Name        *string
	Status      *string
	Conclusion  *string
	ExternalID  *string
	DetailsURL  *string
	StartedAt   *time.Time
	CompletedAt *time.Time
	Output      *Output
}

// CheckRunFilter narrows a list of check runs.
type CheckRunFilter struct {
	Name   string // exact check name
	Status string // queued, in_progress or completed
	App    string
	// All lists every run; otherwise only the latest run of each name.
	All    bool
	Offset int
	Limit  int
}

// ---- access helpers ----

// access resolves the repository; write needs the push permission,
// otherwise reading is enough.
func (s *Service) access(ctx context.Context, who auth.Principal, owner, name string, write bool) (repos.Repo, error) {
	r, err := s.o.Repos.Get(ctx, who, owner, name)
	if err != nil {
		return repos.Repo{}, err
	}
	if write && !authz.Allowed(r.Permission, authz.ActPush) {
		return repos.Repo{}, repos.ErrForbidden
	}
	return r, nil
}

// resolve resolves ref to its commit; branch is the branch name when ref
// names a branch.
func (s *Service) resolve(ctx context.Context, r repos.Repo, ref string) (sha, branch string, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", invalid("a ref or commit sha is required")
	}
	if s.o.Browse == nil {
		return "", "", ErrCommitNotFound
	}
	res, err := s.o.Browse.Resolve(ctx, gitbrowse.Repo{ID: r.ID, OwnerID: r.OwnerID, Name: r.Name, DefaultBranch: r.DefaultBranch}, ref)
	if err != nil {
		if errors.Is(err, gitbrowse.ErrNotFound) || errors.Is(err, gitbrowse.ErrEmpty) || errors.Is(err, gitbrowse.ErrInvalid) {
			return "", "", ErrCommitNotFound
		}
		return "", "", fmt.Errorf("resolve %q: %w", ref, err)
	}
	if res.Kind == gitbrowse.KindBranch {
		branch = strings.TrimPrefix(res.Name, "refs/heads/")
	}
	return res.Commit, branch, nil
}

func (s *Service) tx(ctx context.Context, fn func(*dbgen.Queries) error) error {
	tx, err := s.o.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(s.q.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func (s *Service) kick(ctx context.Context) {
	if s.o.Bus != nil {
		s.o.Bus.Kick(ctx)
	}
}

func page(offset, limit int) (int32, int32) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > MaxPage {
		limit = 30
	}
	return int32(offset), int32(limit) //nolint:gosec // bounded above
}

func nullID(id uuid.UUID) uuid.NullUUID { return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil} }

func tsPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func pgTime(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

// checkURL validates an optional http(s) URL.
func checkURL(field, v string) error {
	if v == "" {
		return nil
	}
	if len(v) > 2048 {
		return invalid("%s is too long (maximum 2048 characters)", field)
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return invalid("%s must be an http or https URL", field)
	}
	return nil
}

func checkLen(field, v string, maxLen int) error {
	if !utf8.ValidString(v) {
		return invalid("%s is not valid UTF-8", field)
	}
	if utf8.RuneCountInString(v) > maxLen {
		return invalid("%s is too long (maximum %d characters)", field, maxLen)
	}
	return nil
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
