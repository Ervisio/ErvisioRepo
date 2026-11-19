// Package presence tells the members of a repository who else works on it:
// branch, head, uncommitted tracked files and unpushed commits of each
// client (heartbeats), plus file locks per branch that the pre-receive
// hook enforces. The feature is off by default and turned on per
// repository by its admins.
//
// Heartbeats are upserted in repo_presence; a row is live for TTL after
// its last heartbeat and a sweeper deletes expired rows. Changes travel to
// the event streams of every server instance through Postgres
// LISTEN/NOTIFY on one channel, whose payload only names the change: each
// instance reloads the row it needs. Locks are the Git LFS locks
// (lfs_locks), one per (ref, path).
package presence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// Timing of the feature.
const (
	// Interval is the heartbeat period suggested to the clients.
	Interval = 30 * time.Second
	// TTL is how long a presence stays live after its last heartbeat.
	TTL = 90 * time.Second
	// PingInterval is the period of the keep-alive comments of a stream.
	PingInterval = 25 * time.Second
	// sweepInterval is the period of the expiry sweeper.
	sweepInterval = 15 * time.Second
)

// Input limits.
const (
	MaxDirty  = 500
	MaxPath   = 4096
	MaxBranch = 255
	maxHead   = 64
	maxAhead  = 1 << 30
	// maxLocks bounds the locks listed in a state.
	maxLocks = 5000
)

// File states of a dirty file.
var fileStates = []string{"modified", "added", "deleted", "renamed", "conflicted"}

const headsPrefix = "refs/heads/"

// Errors; their messages are safe to show. Repository lookups also return
// repos.ErrNotFound and *repos.MovedError.
var (
	ErrForbidden    = errors.New("you cannot use presence and file locks on this repository")
	ErrDisabled     = errors.New("presence and file locks are turned off for this repository")
	ErrLockNotFound = errors.New("lock not found")
	ErrLockExists   = errors.New("the path is already locked on this branch")
	ErrUnavailable  = errors.New("presence is not available")
)

// ValidationError is an invalid input; its message is safe to show.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

// User is the public identity shown next to a presence or a lock.
type User struct {
	Login     string
	Name      string
	AvatarURL string
}

// DirtyFile is a tracked file with uncommitted changes.
type DirtyFile struct {
	Path  string `json:"path"`
	State string `json:"state"`
}

// Presence is the reported state of one client of a user.
type Presence struct {
	UserID      uuid.UUID
	User        User
	ClientID    uuid.UUID
	Branch      string
	Head        string
	Dirty       []DirtyFile
	Truncated   bool
	Ahead       int
	Unpublished bool
	Active      bool
	UpdatedAt   time.Time
}

// Lock is a file lock on a branch.
type Lock struct {
	ID       uuid.UUID
	Path     string
	Branch   string // short name for refs/heads/*, the full ref otherwise
	OwnerID  uuid.UUID
	Owner    User
	LockedAt time.Time
}

// State is what a caller sees of the feature on a repository.
type State struct {
	Enabled        bool
	CanUse         bool
	CanManage      bool
	CanForceUnlock bool
	Interval       time.Duration
	TTL            time.Duration
	// Others are the live presences of the other users (empty unless
	// CanUse); Locks every lock of the repository.
	Others []Presence
	Locks  []Lock
}

// Heartbeat is the state a client reports.
type Heartbeat struct {
	ClientID    uuid.UUID
	Branch      string
	Head        string
	Dirty       []DirtyFile
	Truncated   bool
	Ahead       int
	Unpublished bool
	Active      bool
}

// Repos looks repositories up as a caller sees them (*repos.Service).
type Repos interface {
	Get(ctx context.Context, who auth.Principal, owner, name string) (repos.Repo, error)
	GetByID(ctx context.Context, who auth.Principal, id uuid.UUID) (repos.Repo, error)
}

// Options configure the service.
type Options struct {
	Pool *pgxpool.Pool
	Log  *slog.Logger
}

// Service implements the feature.
type Service struct {
	pool  *pgxpool.Pool
	q     *dbgen.Queries
	log   *slog.Logger
	repos Repos
	hub   *hub
	now   func() time.Time
}

// New returns the service. Call SetRepos before serving requests and Run
// to deliver events.
func New(o Options) *Service {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return &Service{pool: o.Pool, q: dbgen.New(o.Pool), log: o.Log, hub: newHub(), now: time.Now}
}

// SetRepos sets the repository lookups.
func (s *Service) SetRepos(r Repos) { s.repos = r }

// access is what a caller may do on a repository.
type access struct {
	repo    repos.Repo
	enabled bool
	use     bool // canUse, without the token scope
	manage  bool
	force   bool
	write   bool // a session, or a token with the repository write scope
}

func (s *Service) lookup(ctx context.Context, who auth.Principal, owner, name string) (access, error) {
	if s.repos == nil {
		return access{}, ErrUnavailable
	}
	r, err := s.repos.Get(ctx, who, owner, name)
	if err != nil {
		return access{}, err
	}
	return s.accessFor(ctx, who, r)
}

// accessFor applies the rules: write permission or more, or membership of
// the owning organization of a public repository, may use the feature;
// admins manage it; maintainers, admins and organization owners break
// other users' locks.
func (s *Service) accessFor(ctx context.Context, who auth.Principal, r repos.Repo) (access, error) {
	a := access{repo: r}
	en, err := s.q.GetRepoPresenceEnabled(ctx, r.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, repos.ErrNotFound
	}
	if err != nil {
		return a, fmt.Errorf("get presence setting: %w", err)
	}
	a.enabled = en
	if who.UserID == uuid.Nil {
		return a, nil
	}
	var role authz.OrgRole
	if r.OwnerKind == "org" {
		v, err := s.q.GetOrgMemberRole(ctx, dbgen.GetOrgMemberRoleParams{OrgID: r.OwnerID, UserID: who.UserID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return a, fmt.Errorf("get member role: %w", err)
		}
		role = authz.OrgRole(v)
	}
	a.use = authz.Allowed(r.Permission, authz.ActPush) || (r.Visibility == authz.Public && role.Member())
	a.manage = r.Permission >= authz.PermAdmin
	a.force = r.Permission >= authz.PermMaintain || role == authz.OrgOwner || who.IsAdmin()
	a.write = who.Method != auth.MethodToken || who.HasScope(auth.ScopeWriteRepo)
	return a, nil
}

// writer checks a change: the caller may use the feature with this
// credential, and the feature is on.
func (a access) writer() error {
	if !a.use || !a.write {
		return ErrForbidden
	}
	if !a.enabled {
		return ErrDisabled
	}
	return nil
}

// State returns the state of owner/name for the caller.
func (s *Service) State(ctx context.Context, who auth.Principal, owner, name string) (State, error) {
	a, err := s.lookup(ctx, who, owner, name)
	if err != nil {
		return State{}, err
	}
	return s.state(ctx, who, a)
}

func (s *Service) state(ctx context.Context, who auth.Principal, a access) (State, error) {
	st := State{
		Enabled: a.enabled, CanUse: a.use && a.write, CanManage: a.manage && a.write, CanForceUnlock: a.force && a.write,
		Interval: Interval, TTL: TTL, Others: []Presence{}, Locks: []Lock{},
	}
	if !a.enabled {
		return st, nil
	}
	locks, err := s.q.ListPresenceLocks(ctx, dbgen.ListPresenceLocksParams{RepoID: a.repo.ID, MaxRows: maxLocks})
	if err != nil {
		return st, fmt.Errorf("list locks: %w", err)
	}
	for _, l := range locks {
		st.Locks = append(st.Locks, lockFrom(l.ID, l.Path, l.Refname, l.OwnerID, l.CreatedAt, l.Login, l.DisplayName, l.AvatarUrl))
	}
	if !a.use {
		return st, nil
	}
	rows, err := s.q.ListLivePresence(ctx, dbgen.ListLivePresenceParams{RepoID: a.repo.ID,
		Since: pgtype.Timestamptz{Time: s.now().Add(-TTL), Valid: true}})
	if err != nil {
		return st, fmt.Errorf("list presence: %w", err)
	}
	for _, r := range rows {
		if r.UserID == who.UserID {
			continue
		}
		st.Others = append(st.Others, presenceFrom(dbgen.GetLivePresenceRow(r)))
	}
	return st, nil
}

// SetEnabled turns the feature on or off (repository admins). Turning it
// off forgets the presences and ends the streams; locks are kept.
func (s *Service) SetEnabled(ctx context.Context, who auth.Principal, owner, name string, enabled bool, c identity.Client) (State, error) {
	a, err := s.lookup(ctx, who, owner, name)
	if err != nil {
		return State{}, err
	}
	if !a.manage || !a.write {
		return State{}, repos.ErrForbidden
	}
	err = s.inTx(ctx, func(q *dbgen.Queries) error {
		n, err := q.SetRepoPresenceEnabled(ctx, dbgen.SetRepoPresenceEnabledParams{ID: a.repo.ID, Enabled: enabled})
		if err != nil {
			return fmt.Errorf("set presence setting: %w", err)
		}
		if n == 0 {
			return nil
		}
		org := uuid.Nil
		if a.repo.OwnerKind == "org" {
			org = a.repo.OwnerID
		}
		if err := audit.Record(ctx, q, audit.Event{Actor: who.UserID, Action: audit.ActionRepoPresence, TargetType: "repo",
			TargetID: a.repo.ID.String(), IP: c.IP, Data: map[string]any{"enabled": enabled}, OrgID: org}); err != nil {
			return err
		}
		if enabled {
			return nil
		}
		if err := q.DeleteRepoPresence(ctx, a.repo.ID); err != nil {
			return fmt.Errorf("delete presence: %w", err)
		}
		return notify(ctx, q, note{Repo: a.repo.ID, Kind: kindDisabled})
	})
	if err != nil {
		return State{}, err
	}
	a.enabled = enabled
	return s.state(ctx, who, a)
}

func checkHeartbeat(in *Heartbeat) error {
	if in.ClientID == uuid.Nil {
		return invalid("clientId is required")
	}
	if len(in.Branch) > MaxBranch || !utf8.ValidString(in.Branch) || strings.ContainsRune(in.Branch, 0) {
		return invalid("branch is invalid or longer than %d bytes", MaxBranch)
	}
	if len(in.Head) > maxHead || strings.Trim(strings.ToLower(in.Head), "0123456789abcdef") != "" {
		return invalid("head must be an object ID")
	}
	in.Head = strings.ToLower(in.Head)
	if len(in.Dirty) > MaxDirty {
		return invalid("dirty lists more than %d files; send the first %d with truncated", MaxDirty, MaxDirty)
	}
	for _, f := range in.Dirty {
		if err := checkPath(f.Path); err != nil {
			return err
		}
		if !slices.Contains(fileStates, f.State) {
			return invalid("unknown file state %q", f.State)
		}
	}
	if in.Ahead < 0 || in.Ahead > maxAhead {
		return invalid("ahead must be a positive count")
	}
	if in.Dirty == nil {
		in.Dirty = []DirtyFile{}
	}
	return nil
}

func checkPath(p string) error {
	if p == "" || len(p) > MaxPath || strings.ContainsRune(p, 0) || !utf8.ValidString(p) {
		return invalid("path must be 1-%d bytes of UTF-8 without NUL", MaxPath)
	}
	return nil
}

// Heartbeat records the state of a client and returns the state of the
// repository. Other instances and streams hear of it only when something
// other than the time changed (or the presence was not live).
func (s *Service) Heartbeat(ctx context.Context, who auth.Principal, owner, name string, in Heartbeat) (State, error) {
	a, err := s.lookup(ctx, who, owner, name)
	if err != nil {
		return State{}, err
	}
	if err := a.writer(); err != nil {
		return State{}, err
	}
	if err := checkHeartbeat(&in); err != nil {
		return State{}, err
	}
	dirty, err := json.Marshal(in.Dirty)
	if err != nil {
		return State{}, fmt.Errorf("encode dirty files: %w", err)
	}
	key := dbgen.GetPresenceForUpdateParams{RepoID: a.repo.ID, UserID: who.UserID, ClientID: in.ClientID}
	err = s.inTx(ctx, func(q *dbgen.Queries) error {
		prev, err := q.GetPresenceForUpdate(ctx, key)
		found := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("get presence: %w", err)
		}
		if err := q.UpsertPresence(ctx, dbgen.UpsertPresenceParams{
			RepoID: a.repo.ID, UserID: who.UserID, ClientID: in.ClientID, Branch: in.Branch, Head: in.Head, Dirty: dirty,
			Truncated: in.Truncated, Ahead: int32(in.Ahead), Unpublished: in.Unpublished, Active: in.Active, //nolint:gosec // bounded by maxAhead
		}); err != nil {
			return fmt.Errorf("upsert presence: %w", err)
		}
		if found && prev.UpdatedAt.Time.After(s.now().Add(-TTL)) && same(prev, in) {
			return nil
		}
		return notify(ctx, q, note{Repo: a.repo.ID, Kind: kindPresence, User: who.UserID, Client: in.ClientID})
	})
	if err != nil {
		return State{}, err
	}
	return s.state(ctx, who, a)
}

// same reports whether a stored presence already holds the reported state.
func same(prev dbgen.GetPresenceForUpdateRow, in Heartbeat) bool {
	if prev.Branch != in.Branch || prev.Head != in.Head || prev.Truncated != in.Truncated || int(prev.Ahead) != in.Ahead ||
		prev.Unpublished != in.Unpublished || prev.Active != in.Active {
		return false
	}
	var dirty []DirtyFile
	if err := json.Unmarshal(prev.Dirty, &dirty); err != nil {
		return false
	}
	return slices.Equal(dirty, in.Dirty)
}

// Leave removes the presence of a client.
func (s *Service) Leave(ctx context.Context, who auth.Principal, owner, name string, client uuid.UUID) error {
	a, err := s.lookup(ctx, who, owner, name)
	if err != nil {
		return err
	}
	if err := a.writer(); err != nil {
		return err
	}
	return s.inTx(ctx, func(q *dbgen.Queries) error {
		n, err := q.DeletePresence(ctx, dbgen.DeletePresenceParams{RepoID: a.repo.ID, UserID: who.UserID, ClientID: client})
		if err != nil {
			return fmt.Errorf("delete presence: %w", err)
		}
		if n == 0 {
			return nil
		}
		return notify(ctx, q, note{Repo: a.repo.ID, Kind: kindGone, User: who.UserID, Client: client, Login: who.Username})
	})
}

func (s *Service) inTx(ctx context.Context, fn func(q *dbgen.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(s.q.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func presenceFrom(r dbgen.GetLivePresenceRow) Presence {
	p := Presence{
		UserID: r.UserID, User: User{Login: r.Login, Name: r.DisplayName, AvatarURL: r.AvatarUrl}, ClientID: r.ClientID,
		Branch: r.Branch, Head: r.Head, Truncated: r.Truncated, Ahead: int(r.Ahead), Unpublished: r.Unpublished,
		Active: r.Active, UpdatedAt: r.UpdatedAt.Time.UTC(), Dirty: []DirtyFile{},
	}
	_ = json.Unmarshal(r.Dirty, &p.Dirty)
	return p
}

func lockFrom(id uuid.UUID, path, ref string, owner uuid.UUID, at pgtype.Timestamptz, login, name, avatar string) Lock {
	return Lock{ID: id, Path: path, Branch: strings.TrimPrefix(ref, headsPrefix), OwnerID: owner,
		Owner: User{Login: login, Name: name, AvatarURL: avatar}, LockedAt: at.Time.UTC()}
}
