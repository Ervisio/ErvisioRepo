// Package actions is the server side of Ervisio Actions (PLAN §6):
// workflow discovery and triggers, the job planner, the runner service,
// secrets and variables, logs and the reporting to checks and statuses.
package actions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
	"github.com/Ervisio/ErvisioRepo/internal/statuses"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
	"github.com/Ervisio/ErvisioRepo/internal/webhooks"
)

// AppName is the app of the check runs reported by Ervisio Actions.
const AppName = "Ervisio Actions"

// KindWorkflowRun is the bus event of a completed workflow run (action
// "completed"); notifications use it to report failures.
const KindWorkflowRun = "workflow_run"

// Errors.
var (
	ErrNotFound   = errors.New("actions: not found")
	ErrForbidden  = errors.New("actions: forbidden")
	ErrDisabled   = errors.New("actions: not available")
	ErrNotWired   = errors.New("actions: service is not wired yet")
	ErrConflict   = errors.New("actions: conflict")
	errNoWorkflow = errors.New("actions: workflow not found")
)

// ValidationError is a rejected input (HTTP 400/422).
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

// Repos resolves repositories with the caller's permission.
type Repos interface {
	Get(ctx context.Context, who auth.Principal, owner, name string) (repos.Repo, error)
	GetByID(ctx context.Context, who auth.Principal, id uuid.UUID) (repos.Repo, error)
}

// Browser reads repository content.
type Browser interface {
	Tree(ctx context.Context, repo gitbrowse.Repo, ref, path string, lastCommits bool) (gitbrowse.Tree, error)
	Blob(ctx context.Context, repo gitbrowse.Repo, ref, path string) (gitbrowse.Blob, error)
	Resolve(ctx context.Context, repo gitbrowse.Repo, ref string) (gitbrowse.Resolved, error)
	ChangedFiles(ctx context.Context, repo gitbrowse.Repo, from, to string) ([]gitbrowse.FileChange, error)
}

// Statuses reports check runs and commit statuses.
type Statuses interface {
	CreateCheckRun(ctx context.Context, who auth.Principal, owner, name string, in statuses.CheckRunInput) (statuses.CheckRun, error)
	UpdateCheckRun(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, p statuses.CheckRunPatch) (statuses.CheckRun, error)
	CreateStatus(ctx context.Context, who auth.Principal, owner, name, ref string, in statuses.StatusInput) (statuses.Status, error)
}

// Payloads builds the GitHub-shaped payload of bus events.
type Payloads interface {
	GitHubEvents(ctx context.Context, ev events.Event) ([]webhooks.GitHubEvent, error)
}

// Enqueuer inserts River jobs.
type Enqueuer interface {
	Enqueue(ctx context.Context, args river.JobArgs) error
}

// Kicker wakes the event bus after events were emitted.
type Kicker interface {
	Kick(ctx context.Context)
}

// IDTokens is the Phase 9 hook for OIDC id-tokens of jobs: when set, its
// claims are minted for jobs with the id-token: write permission. Nothing
// calls it yet: the runner contract has no field to carry the request URL.
type IDTokens interface {
	RequestURL(ctx context.Context, repoID, runID, jobID uuid.UUID) (url, token string, err error)
}

// Deps are the services Actions uses once the rest of the server is up.
type Deps struct {
	Repos    Repos
	Browse   Browser
	Statuses Statuses
	Payloads Payloads
	Jobs     Enqueuer
	Bus      Kicker
	IDTokens IDTokens
	// Secrets is the Phase 9 hook for external secret stores.
	Secrets SecretHook
}

// Options configure New.
type Options struct {
	Pool   *pgxpool.Pool
	Log    *slog.Logger
	Config config.Actions
	// PublicURL is the external base URL, without a trailing slash.
	PublicURL string
	// MasterKey seals secrets and signs runtime tokens; empty disables
	// secrets.
	MasterKey []byte
	// Storage holds archived logs (policy "actions"); nil keeps logs in
	// Postgres.
	Storage *tier.Tier
	Now     func() time.Time
}

// Service is Ervisio Actions.
type Service struct {
	pool   *pgxpool.Pool
	q      *dbgen.Queries
	log    *slog.Logger
	cfg    config.Actions
	public string
	seal   *sealer
	jwtKey []byte
	logs   *tier.Class
	now    func() time.Time

	deps atomic.Pointer[Deps]
	// masks holds the secret values of running jobs (job id → []string)
	// so that the server masks them in logs too.
	masks sync.Map

	hub       *hub
	listening atomic.Bool
}

// system acts for Actions inside the server (check runs, statuses).
var system = auth.Principal{Username: "ervisio-actions", Role: auth.RoleAdmin}

// New creates the service. Call Wire before serving requests.
func New(o Options) (*Service, error) {
	if o.Pool == nil {
		return nil, errors.New("actions: a database pool is required")
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	sl, err := newSealer(o.MasterKey)
	if err != nil {
		return nil, err
	}
	key, err := runtimeKey(o.MasterKey)
	if err != nil {
		return nil, err
	}
	s := &Service{pool: o.Pool, q: dbgen.New(o.Pool), log: o.Log.With("component", "actions"), cfg: o.Config,
		public: strings.TrimRight(o.PublicURL, "/"), seal: sl, jwtKey: key, now: o.Now, hub: newHub()}
	if o.Storage != nil {
		c, err := o.Storage.Class("actions")
		if err != nil {
			s.log.Warn("no actions storage policy, logs stay in the database", "err", err)
		} else {
			s.logs = c
		}
	}
	if len(o.MasterKey) == 0 {
		s.log.Warn("security.master_key_file is not set: actions secrets are disabled and runtime tokens do not survive restarts")
	}
	return s, nil
}

// Wire sets the services Actions depends on.
func (s *Service) Wire(d Deps) {
	cp := d
	s.deps.Store(&cp)
}

func (s *Service) d() (*Deps, error) {
	d := s.deps.Load()
	if d == nil || d.Repos == nil || d.Browse == nil {
		return nil, ErrNotWired
	}
	return d, nil
}

// VerifyRuntimeToken checks an ACTIONS_RUNTIME_TOKEN (artifacts, cache).
func (s *Service) VerifyRuntimeToken(token string) (RuntimeClaims, error) {
	return VerifyRuntimeToken(s.jwtKey, token, s.now())
}

// PublicURL is the external base URL the service was configured with.
func (s *Service) PublicURL() string { return s.public }

func browseRepo(r repos.Repo) gitbrowse.Repo {
	return gitbrowse.Repo{ID: r.ID, OwnerID: r.OwnerID, Name: r.Name, DefaultBranch: r.DefaultBranch}
}

// tx runs fn in a transaction.
func (s *Service) tx(ctx context.Context, fn func(q *dbgen.Queries) error) error {
	return pgx.BeginFunc(ctx, s.pool, func(t pgx.Tx) error { return fn(s.q.WithTx(t)) })
}

func nullID(id uuid.UUID) uuid.NullUUID { return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil} }

func (s *Service) maxTimeoutMinutes() int32 {
	m := min(time.Duration(s.cfg.MaxJobTimeout)/time.Minute, 1<<20)
	if m < 1 {
		m = 360
	}
	return int32(m) // #nosec G115 -- bounded above
}

func (s *Service) maxJobs() int {
	if s.cfg.MaxJobsPerRun <= 0 {
		return 512
	}
	return s.cfg.MaxJobsPerRun
}

func (s *Service) offlineAfter() time.Duration {
	d := time.Duration(s.cfg.RunnerOfflineAfter)
	if d <= 0 {
		d = 2 * time.Minute
	}
	return d
}

func (s *Service) enqueue(ctx context.Context, args river.JobArgs) {
	d := s.deps.Load()
	if d == nil || d.Jobs == nil {
		return
	}
	if err := d.Jobs.Enqueue(ctx, args); err != nil {
		s.log.Warn("enqueue actions job", "kind", args.Kind(), "err", err)
	}
}

func (s *Service) kick(ctx context.Context) {
	if d := s.deps.Load(); d != nil && d.Bus != nil {
		d.Bus.Kick(ctx)
	}
}
