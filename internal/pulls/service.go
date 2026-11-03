// Package pulls implements pull requests (PLAN §4.4): a pull request is an
// issue of kind "pull" plus a pull_requests row. The package keeps the
// refs/pull/<n>/head and refs/pull/<n>/merge refs of the base repository
// up to date, computes mergeability with git merge-tree, merges (merge
// commit, squash or rebase) through the hooks so that branch protection and
// the pack journal see every ref update, and manages reviews, review
// requests, inline comments with re-anchoring, suggestions, auto-merge and
// forks. Comments, labels, assignees and the milestone of a pull request
// are those of its issue (internal/issues).
package pulls

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/githooks"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/issues"
	"github.com/Ervisio/ErvisioRepo/internal/protection"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// Pull request states as exposed by the API.
const (
	StateOpen   = "open"
	StateClosed = "closed"
	StateMerged = "merged"
)

// Mergeability states (pull_requests.mergeable_state).
const (
	MergeUnknown  = "unknown"
	MergeClean    = "clean"
	MergeConflict = "conflict"
	MergeError    = "error"
)

// Merge methods.
const (
	MethodMerge  = "merge"
	MethodSquash = "squash"
	MethodRebase = "rebase"
)

// Review states.
const (
	ReviewPending          = "pending"
	ReviewCommented        = "commented"
	ReviewApproved         = "approved"
	ReviewChangesRequested = "changes_requested"
	ReviewDismissed        = "dismissed"
)

// Comment sides.
const (
	SideLeft  = "LEFT"
	SideRight = "RIGHT"
)

// Limits.
const (
	MaxTitle          = issues.MaxTitle
	MaxBody           = issues.MaxBody
	MaxReviewers      = 50
	MaxReviewComments = 100
	MaxDismissMessage = 1000
	// MaxRebaseCommits bounds the commits a rebase merge replays.
	MaxRebaseCommits = 250
	// MaxCommitsPage bounds one page of ListCommits.
	MaxCommitsPage = 250
	// maxForkDepth bounds the fork chain walked to relate two repositories.
	maxForkDepth = 10
)

// Errors. Repository lookups surface repos.ErrNotFound, repos.ErrForbidden
// and *repos.MovedError unchanged.
var (
	ErrNotFound        = errors.New("pull request not found")
	ErrReviewNotFound  = errors.New("review not found")
	ErrCommentNotFound = errors.New("review comment not found")
	ErrForbidden       = repos.ErrForbidden
	// ErrConflict: the request conflicts with the current state (an open
	// pull request for the branches exists, the head moved, the branches
	// conflict, the review is not pending...).
	ErrConflict = errors.New("conflict")
	// ErrNotMergeable: a merge requirement is not met (HTTP 405).
	ErrNotMergeable = errors.New("the pull request cannot be merged")
	// ErrUnavailable: the repository storage is not available right now.
	ErrUnavailable = errors.New("repository unavailable")
	// ErrLocked: the conversation is locked and the caller cannot triage.
	ErrLocked = issues.ErrLocked
)

// ValidationError is a rejected input (HTTP 400).
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

// NotMergeableError lists the unmet requirements of a merge.
type NotMergeableError struct {
	Reasons []string
}

func (e *NotMergeableError) Error() string {
	if len(e.Reasons) == 0 {
		return ErrNotMergeable.Error()
	}
	return ErrNotMergeable.Error() + ": " + strings.Join(e.Reasons, "; ")
}

// Unwrap makes errors.Is(err, ErrNotMergeable) true.
func (e *NotMergeableError) Unwrap() error { return ErrNotMergeable }

// RepoAccess resolves repositories for a caller (*repos.Service).
type RepoAccess interface {
	Get(ctx context.Context, who auth.Principal, owner, name string) (repos.Repo, error)
	GetByID(ctx context.Context, who auth.Principal, id uuid.UUID) (repos.Repo, error)
	Create(ctx context.Context, who auth.Principal, in repos.CreateInput, c identity.Client) (repos.Repo, error)
	// Abandon removes a repository created by the service that could not
	// be finished (a half-created fork).
	Abandon(ctx context.Context, id uuid.UUID) error
}

// Browser reads and edits repository content (*gitbrowse.Service).
type Browser interface {
	DiffRange(ctx context.Context, repo gitbrowse.Repo, from, to string, paths []string) (gitbrowse.Diff, error)
	ChangedFiles(ctx context.Context, repo gitbrowse.Repo, from, to string) ([]gitbrowse.FileChange, error)
	CommitRange(ctx context.Context, repo gitbrowse.Repo, from, to string, offset, limit int) ([]gitbrowse.Commit, bool, error)
	Identity(ctx context.Context, userID uuid.UUID) ([]string, error)
	Blob(ctx context.Context, repo gitbrowse.Repo, ref, path string) (gitbrowse.Blob, error)
	Edit(ctx context.Context, repo gitbrowse.Repo, e gitbrowse.Edit) (gitbrowse.EditResult, error)
}

// Issues is the issue service the pull requests share their issue part
// with (*issues.Service).
type Issues interface {
	Hydrate(ctx context.Context, viewer uuid.UUID, rows []dbgen.Issue) ([]issues.Issue, error)
	CloseFromText(ctx context.Context, repoID, actorID uuid.UUID, text string, src issues.CloseSource) ([]issues.Ref, error)
	RecordRefs(ctx context.Context, q *dbgen.Queries, r repos.Repo, who auth.Principal, issue dbgen.Issue, reviewComment uuid.UUID, text string) error
}

// Protection evaluates the merge requirements (*protection.Engine).
type Protection interface {
	PullRequestStatus(ctx context.Context, in protection.PRInput) (protection.PRStatus, error)
}

// Options configures a Service.
type Options struct {
	Pool   *pgxpool.Pool
	Repos  RepoAccess
	Browse Browser
	Tier   *gitstore.Tier
	// Hooks runs the managed hooks on the ref updates of the service
	// (protection, pack journal, push events); nil disables them.
	Hooks *githooks.Server
	// Protection evaluates the merge requirements; nil means none.
	Protection Protection
	Issues     Issues
	Bus        *events.Bus
	Log        *slog.Logger
	Now        func() time.Time
}

// Service implements the pull request flows.
type Service struct {
	o   Options
	q   *dbgen.Queries
	log *slog.Logger
}

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

// ---- domain types ----

// Ref is one side of a pull request.
type Ref struct {
	RepoID uuid.UUID // uuid.Nil when the head repository was deleted
	Repo   string    // owner/name; empty when the head repository is gone
	Ref    string    // branch name
	SHA    string
}

// AutoMerge is a pending auto-merge.
type AutoMerge struct {
	Method       string
	EnabledBy    string
	Title        string
	Message      string
	DeleteBranch bool
}

// Pull is a pull request.
type Pull struct {
	Issue              issues.Issue
	State              string // open, closed or merged
	Draft              bool
	Head               Ref
	Base               Ref
	MergeBase          string
	Mergeable          string
	ConflictFiles      []string
	MergedAt           *time.Time
	MergedBy           string
	MergeCommit        string
	MergeMethod        string
	AutoMerge          *AutoMerge
	RequestedReviewers []string
}

// Review is a review of a pull request.
type Review struct {
	ID             uuid.UUID
	Reviewer       string
	ReviewerID     uuid.UUID
	State          string
	Body           string
	CommitSHA      string
	SubmittedAt    *time.Time
	DismissedAt    *time.Time
	DismissedBy    string
	DismissMessage string
	CreatedAt      time.Time
}

// Comment is an inline review comment.
type Comment struct {
	ID                uuid.UUID
	ReviewID          uuid.UUID // uuid.Nil outside a review
	Pending           bool
	InReplyTo         uuid.UUID // uuid.Nil for the first comment of a thread
	Author            string
	AuthorID          uuid.UUID
	Path              string
	Side              string
	Line              *int
	StartLine         *int
	OriginalLine      int
	OriginalStartLine *int
	OriginalCommitSHA string
	CommitSHA         string
	DiffHunk          string
	Body              string
	Outdated          bool
	Resolved          bool
	ResolvedBy        string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Mergeability is the merge status of a pull request: the conflict check
// and the branch protection requirements.
type Mergeability struct {
	State            string
	Mergeable        string
	CanMerge         bool
	ConflictFiles    []string
	HeadSHA          string
	BaseSHA          string
	MergeBase        string
	Rule             string
	RequiredApprove  int
	Approvals        int
	MissingChecks    []string
	Behind           bool
	CodeOwners       []protection.OwnerGroup
	CodeOwnersFile   string
	CodeOwnersErrors []string
	Violations       []protection.Violation
}

// ---- access ----

// systemPrincipal reads any repository (background handlers).
var systemPrincipal = auth.Principal{Role: auth.RoleAdmin}

func canTriage(r repos.Repo) bool { return authz.Allowed(r.Permission, authz.ActTriage) }
func canWrite(r repos.Repo) bool  { return authz.Allowed(r.Permission, authz.ActPush) }
func canMerge(r repos.Repo) bool  { return authz.Allowed(r.Permission, authz.ActMerge) }

// writer resolves owner/name for a signed-in user.
func (s *Service) writer(ctx context.Context, who auth.Principal, owner, name string) (repos.Repo, error) {
	r, err := s.o.Repos.Get(ctx, who, owner, name)
	if err != nil {
		return repos.Repo{}, err
	}
	if who.UserID == uuid.Nil {
		return repos.Repo{}, ErrForbidden
	}
	return r, nil
}

// principalOf builds the principal of a user; ok is false for a missing or
// suspended user.
func (s *Service) principalOf(ctx context.Context, user uuid.UUID) (auth.Principal, bool, error) {
	if user == uuid.Nil {
		return auth.Principal{}, false, nil
	}
	u, err := s.q.GetUserByID(ctx, user)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Principal{}, false, nil
	}
	if err != nil {
		return auth.Principal{}, false, fmt.Errorf("get user: %w", err)
	}
	if u.SuspendedAt.Valid {
		return auth.Principal{}, false, nil
	}
	return auth.Principal{UserID: u.ID, Username: u.Username, Role: u.Role}, true, nil
}

// ---- helpers ----

func gitRepo(r repos.Repo) gitbrowse.Repo {
	return gitbrowse.Repo{ID: r.ID, OwnerID: r.OwnerID, Name: r.Name, DefaultBranch: r.DefaultBranch}
}

func nullID(id uuid.UUID) uuid.NullUUID { return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil} }

func tsPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func int4(v int) pgtype.Int4 {
	if v <= 0 {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(v), Valid: true} //nolint:gosec // line numbers are validated
}

// isUser reports whether id is set and names user.
func isUser(id uuid.NullUUID, user uuid.UUID) bool {
	return id.Valid && id.UUID == user
}

func intPtr(v pgtype.Int4) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int32)
	return &n
}

func pageArgs(offset, limit int) (int32, int32) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	return int32(offset), int32(limit) //nolint:gosec // bounded above
}

func checkText(name, v string, maxLen int) error {
	if utf8.RuneCountInString(v) > maxLen {
		return invalid("%s is longer than %d characters", name, maxLen)
	}
	if !utf8.ValidString(v) || strings.ContainsRune(v, 0) {
		return invalid("%s is not valid text", name)
	}
	return nil
}

func checkTitle(name, v string, maxLen int) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", invalid("%s is required", name)
	}
	return v, checkText(name, v, maxLen)
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
	if s.o.Bus != nil {
		s.o.Bus.Kick(ctx)
	}
	return nil
}

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func notFound(err error, sentinel error, what string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return sentinel
	}
	return fmt.Errorf("%s: %w", what, err)
}

func newID() (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("new id: %w", err)
	}
	return id, nil
}

// names resolves user ids to usernames (missing ids map to "").
func (s *Service) names(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := map[uuid.UUID]string{}
	seen := map[uuid.UUID]bool{}
	var want []uuid.UUID
	for _, id := range ids {
		if id != uuid.Nil && !seen[id] {
			seen[id] = true
			want = append(want, id)
		}
	}
	if len(want) == 0 {
		return out, nil
	}
	rows, err := s.q.IssueUserNames(ctx, want)
	if err != nil {
		return nil, fmt.Errorf("user names: %w", err)
	}
	for _, r := range rows {
		out[r.ID] = r.Name
	}
	return out, nil
}

// acquire takes a lease, mapping storage failures to ErrUnavailable.
func (s *Service) acquire(ctx context.Context, repoID uuid.UUID, mode gitstore.Mode) (*gitstore.Lease, error) {
	if s.o.Tier == nil {
		return nil, fmt.Errorf("%w: no git storage", ErrUnavailable)
	}
	l, err := s.o.Tier.Acquire(ctx, repoID, mode)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return l, nil
}

// session opens a hook session on a Write lease; the returned env is nil
// and close a no-op without hooks.
func (s *Service) session(ctx context.Context, l *gitstore.Lease, user uuid.UUID, protocol string) (*githooks.Session, []string, func(), error) {
	if s.o.Hooks == nil {
		return nil, nil, func() {}, nil
	}
	sess, err := s.o.Hooks.Begin(ctx, l, user, protocol)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("begin hook session: %w", err)
	}
	return sess, sess.Env(), sess.Close, nil
}

func (s *Service) emitPull(ctx context.Context, q *dbgen.Queries, action string, actor uuid.UUID, i dbgen.Issue, p dbgen.PullRequest, extra func(*events.PullPayload)) error {
	pl := pullPayload(i, p)
	if extra != nil {
		extra(&pl)
	}
	return events.Emit(ctx, q, events.Event{Kind: events.KindPull, Action: action, RepoID: p.RepoID, ActorID: actor, SubjectID: p.IssueID}, pl)
}

func pullPayload(i dbgen.Issue, p dbgen.PullRequest) events.PullPayload {
	pl := events.PullPayload{Number: p.Number, Title: i.Title, State: stateOf(i, p), Draft: p.Draft, HeadRef: p.HeadRef,
		HeadSHA: p.HeadSha, BaseRef: p.BaseRef, BaseSHA: p.BaseSha}
	if p.HeadRepoID.Valid {
		pl.HeadRepo = p.HeadRepoID.UUID.String()
	}
	return pl
}

// timeline appends an entry to the timeline of the issue of a pull request.
func (s *Service) timeline(ctx context.Context, q *dbgen.Queries, i dbgen.Issue, actor uuid.UUID, name string, data map[string]any) error {
	id, err := newID()
	if err != nil {
		return err
	}
	if data == nil {
		data = map[string]any{}
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if err := q.InsertIssueEvent(ctx, dbgen.InsertIssueEventParams{ID: id, RepoID: i.RepoID, IssueID: i.ID,
		ActorID: nullID(actor), Event: name, Data: b}); err != nil {
		return fmt.Errorf("insert %s event: %w", name, err)
	}
	return nil
}

func stateOf(i dbgen.Issue, p dbgen.PullRequest) string {
	switch {
	case p.MergedAt.Valid:
		return StateMerged
	case i.State == issues.StateOpen:
		return StateOpen
	}
	return StateClosed
}
