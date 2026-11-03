// Package ghsync synchronizes repositories with GitHub (PLAN §5.6, code
// half): GitHub connections (App installations or fine-grained tokens,
// sealed with the master key), one-off imports of the git data, pull
// mirrors, backups to GitHub and bidirectional fast-forward sync.
//
// Every local ref change goes through the normal write path: a Write lease
// of the tier, then git update-ref inside a githooks session, so the
// reference-transaction hook journals the change and branch protection
// sees it like any other write. Objects fetched from GitHub land in the
// leased repository under refs/sync/github/* first.
package ghsync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/githooks"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// Connection kinds and link modes.
const (
	kindApp   = "app"
	kindToken = "token"

	ModeMirror = "mirror"
	ModeBackup = "backup"
	ModeSync   = "sync"
)

// Protocol is the githooks protocol of the writes made by this package:
// the hooks let them through a mirror and do not schedule a backup for
// them (loop prevention).
const Protocol = "ghsync"

// Tuning.
const (
	// MinInterval is the shortest period of the scheduled fetch or check.
	MinInterval = 10 * time.Minute
	// PushDebounce groups the pushes to a repository into one backup.
	PushDebounce = 30 * time.Second
	maxBackoff   = 6 * time.Hour
)

// Errors returned by the service; their messages are safe to show.
var (
	ErrNotFound    = errors.New("not found")
	ErrForbidden   = errors.New("you do not have permission to do this")
	ErrInUse       = errors.New("the connection is used by a repository")
	ErrNoMasterKey = errNoKey
	ErrNotDiverged = errors.New("the branch is not diverged")
	ErrNameTaken   = errors.New("a connection with this name already exists")
	ErrImported    = errors.New("the repository is being imported")
)

// ValidationError is an invalid input; its message is safe to show.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalidf(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

// Repos is the part of the repository service the package needs.
type Repos interface {
	Create(ctx context.Context, who auth.Principal, in repos.CreateInput, c identity.Client) (repos.Repo, error)
	Authorize(ctx context.Context, who auth.Principal, owner, name string, action authz.Action) (repos.Repo, error)
	GetByID(ctx context.Context, who auth.Principal, id uuid.UUID) (repos.Repo, error)
}

// Enqueuer inserts jobs (jobs.Runner).
type Enqueuer interface {
	Enqueue(ctx context.Context, args river.JobArgs) error
}

// Options configure New.
type Options struct {
	Pool *pgxpool.Pool
	Tier *gitstore.Tier
	// MasterKey seals the credentials; without it connections cannot be
	// created (ErrNoMasterKey).
	MasterKey []byte
	// PublicURL is the external URL of the server (webhook URLs).
	PublicURL string
	// Importers import the non-git data of a repository after its git
	// data (see Importer). They run in order; a failure fails the import.
	Importers []Importer
	Log       *slog.Logger
}

// Service is the GitHub sync service.
type Service struct {
	o      Options
	q      *dbgen.Queries
	seal   *sealer
	tokens *tokens
	hooks  atomic.Pointer[githooks.Server]
	repos  atomic.Value // Repos
	jobs   atomic.Value // Enqueuer
}

// New builds the service.
func New(o Options) (*Service, error) {
	if o.Pool == nil || o.Tier == nil {
		return nil, errors.New("ghsync: pool and tier are required")
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	o.PublicURL = strings.TrimRight(o.PublicURL, "/")
	s, err := newSealer(o.MasterKey)
	if err != nil {
		return nil, err
	}
	return &Service{o: o, q: dbgen.New(o.Pool), seal: s, tokens: newTokens()}, nil
}

// SetHooks sets the hook server the local writes open their sessions on.
// Without one (no git hooks) update-ref runs without hooks.
func (s *Service) SetHooks(h *githooks.Server) { s.hooks.Store(h) }

// SetRepos sets the repository service (built after the job runner).
func (s *Service) SetRepos(r Repos) { s.repos.Store(r) }

// SetEnqueuer sets the job runner.
func (s *Service) SetEnqueuer(e Enqueuer) { s.jobs.Store(e) }

func (s *Service) repoSvc() (Repos, error) {
	r, _ := s.repos.Load().(Repos)
	if r == nil {
		return nil, errors.New("ghsync: repository service not set")
	}
	return r, nil
}

func (s *Service) enqueue(ctx context.Context, args river.JobArgs) error {
	e, _ := s.jobs.Load().(Enqueuer)
	if e == nil {
		return errors.New("ghsync: job runner not set")
	}
	return e.Enqueue(ctx, args)
}

func noRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func ts(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func nullUser(id uuid.UUID) uuid.NullUUID { return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil} }

// backoff is the wait after n consecutive failures: 2^n minutes, at most
// maxBackoff.
func backoff(n int32) time.Duration {
	if n < 1 {
		return 0
	}
	if n > 9 {
		return maxBackoff
	}
	return min(time.Duration(1<<n)*time.Minute, maxBackoff)
}

// canManage reports whether who manages the GitHub connections of the
// namespace ownerID: the user itself, the owners and admins of an
// organization, or an instance admin.
func (s *Service) canManage(ctx context.Context, who auth.Principal, ownerID uuid.UUID, ownerKind string) (bool, error) {
	if who.UserID == uuid.Nil {
		return false, nil
	}
	if who.IsAdmin() {
		return true, nil
	}
	if ownerKind == "user" {
		return ownerID == who.UserID, nil
	}
	role, err := s.q.GetOrgMemberRole(ctx, dbgen.GetOrgMemberRoleParams{OrgID: ownerID, UserID: who.UserID})
	if noRows(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("org role: %w", err)
	}
	return authz.OrgRole(role).Manager(), nil
}
