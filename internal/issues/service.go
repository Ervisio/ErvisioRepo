// Package issues implements issues (PLAN §4.3): numbering shared with pull
// requests, state with reason, locking, labels, milestones, assignees,
// comments with edit history, reactions, the timeline, Markdown
// cross-references, auto-close from commits, issue templates, attachments
// and full-text search. Every change emits an event on the internal bus
// (internal/events) in the same transaction.
package issues

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

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// Kinds of numbered items. Issues and pull requests share the numbering of
// a repository (NextNumber).
const (
	KindIssue = "issue"
	KindPull  = "pull"
)

// States and state reasons.
const (
	StateOpen   = "open"
	StateClosed = "closed"

	ReasonCompleted  = "completed"
	ReasonNotPlanned = "not_planned"
	ReasonDuplicate  = "duplicate"
	ReasonReopened   = "reopened"
)

// Limits.
const (
	MaxTitle            = 256
	MaxBody             = 65536
	MaxLabelName        = 50
	MaxLabelDescription = 100
	MaxMilestoneTitle   = 255
	MaxMilestoneDesc    = 10000
	MaxAssignees        = 10
	MaxLabelsPerIssue   = 100
)

// Audit actions.
const (
	ActionLabelCreated      = "issue_label.created"
	ActionLabelUpdated      = "issue_label.updated"
	ActionLabelDeleted      = "issue_label.deleted"
	ActionMilestoneCreated  = "milestone.created"
	ActionMilestoneUpdated  = "milestone.updated"
	ActionMilestoneDeleted  = "milestone.deleted"
	ActionIssueLocked       = "issue.locked"
	ActionIssueUnlocked     = "issue.unlocked"
	ActionCommentDeleted    = "issue_comment.deleted"
	ActionAttachmentDeleted = "issue_attachment.deleted"
)

// Errors. Repository lookups surface repos.ErrNotFound, repos.ErrForbidden
// and *repos.MovedError unchanged; ErrForbidden is repos.ErrForbidden.
var (
	ErrNotFound          = errors.New("issue not found")
	ErrCommentNotFound   = errors.New("comment not found")
	ErrLabelNotFound     = errors.New("label not found")
	ErrMilestoneNotFound = errors.New("milestone not found")
	ErrAttachmentGone    = errors.New("attachment not found")
	ErrLabelTaken        = errors.New("a label with this name already exists")
	ErrMilestoneTaken    = errors.New("a milestone with this title already exists")
	ErrForbidden         = repos.ErrForbidden
	// ErrLocked: the conversation is locked and the caller cannot triage.
	ErrLocked = errors.New("the conversation is locked")
	// ErrNoStorage means no storage policy is configured for attachments.
	ErrNoStorage = errors.New("attachments are not available: no storage policy")
)

// ValidationError is a rejected input (HTTP 400).
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

// RepoAccess resolves repositories for a caller (*repos.Service).
type RepoAccess interface {
	Get(ctx context.Context, who auth.Principal, owner, name string) (repos.Repo, error)
	GetByID(ctx context.Context, who auth.Principal, id uuid.UUID) (repos.Repo, error)
}

// Browser reads repository content (*gitbrowse.Service).
type Browser interface {
	Tree(ctx context.Context, repo gitbrowse.Repo, ref, path string, lastCommits bool) (gitbrowse.Tree, error)
	Blob(ctx context.Context, repo gitbrowse.Repo, ref, path string) (gitbrowse.Blob, error)
	CommitsBetween(ctx context.Context, repo gitbrowse.Repo, oldOID, newOID string, limit int) ([]gitbrowse.Commit, error)
}

// Storage opens the storage class of a data type (*tier.Tier).
type Storage interface {
	Class(policy string) (*tier.Class, error)
}

// Options configures a Service.
type Options struct {
	Pool    *pgxpool.Pool
	Repos   RepoAccess
	Browse  Browser // nil: no templates, pushes do not close issues
	Storage Storage // nil: no attachments
	Bus     *events.Bus
	// MaxAttachmentSize bounds one attachment; 0 uses DefaultMaxAttachmentSize.
	MaxAttachmentSize int64
	Log               *slog.Logger
	Now               func() time.Time
}

// Service implements the issue flows.
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
	if o.MaxAttachmentSize <= 0 {
		o.MaxAttachmentSize = DefaultMaxAttachmentSize
	}
	return &Service{o: o, q: dbgen.New(o.Pool), log: o.Log}
}

// ---- domain types ----

// Label is a repository label.
type Label struct {
	ID          uuid.UUID
	Name        string
	Color       string // six lowercase hex digits
	Description string
}

// Milestone groups issues.
type Milestone struct {
	ID           uuid.UUID
	Title        string
	Description  string
	State        string
	DueOn        *time.Time
	OpenIssues   int64
	ClosedIssues int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ClosedAt     *time.Time
}

// ReactionGroup counts one reaction content on an issue or comment.
type ReactionGroup struct {
	Content       string
	Count         int64
	ViewerReacted bool
}

// Issue is an issue (or the issue part of a pull request).
type Issue struct {
	ID           uuid.UUID
	RepoID       uuid.UUID
	Number       int64
	Kind         string
	Title        string
	Body         string
	State        string
	StateReason  string
	Locked       bool
	LockReason   string
	AuthorID     uuid.UUID
	Author       string
	Labels       []Label
	Assignees    []string
	Milestone    *Milestone
	CommentCount int
	Reactions    []ReactionGroup
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ClosedAt     *time.Time
	ClosedBy     string
	EditedAt     *time.Time
}

// Comment is a comment on an issue.
type Comment struct {
	ID          uuid.UUID
	IssueID     uuid.UUID
	IssueNumber int64
	AuthorID    uuid.UUID
	Author      string
	Body        string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	EditedAt    *time.Time
	EditedBy    string
	Reactions   []ReactionGroup
}

// BodyEdit is a previous body of an issue or comment.
type BodyEdit struct {
	ID        uuid.UUID
	Editor    string
	Body      string
	CreatedAt time.Time
}

// Reaction is one user's reaction.
type Reaction struct {
	ID        uuid.UUID
	Content   string
	User      string
	CreatedAt time.Time
}

// ---- event payload ----

// EventPayload is the payload of the issue, issue_comment, label and
// milestone events on the bus. Only the fields relevant to the action are
// set.
type EventPayload struct {
	Number      int64      `json:"number,omitempty"`
	IssueKind   string     `json:"issue_kind,omitempty"`
	Title       string     `json:"title,omitempty"`
	State       string     `json:"state,omitempty"`
	StateReason string     `json:"state_reason,omitempty"`
	Label       string     `json:"label,omitempty"`
	Assignee    *uuid.UUID `json:"assignee,omitempty"`
	Milestone   string     `json:"milestone,omitempty"`
	CommentID   *uuid.UUID `json:"comment_id,omitempty"`
	Commit      string     `json:"commit,omitempty"`
}

func issuePayload(i dbgen.Issue) EventPayload {
	return EventPayload{Number: i.Number, IssueKind: i.Kind, Title: i.Title, State: i.State, StateReason: i.StateReason}
}

func (s *Service) emit(ctx context.Context, q *dbgen.Queries, kind, action string, repo, actor, subject uuid.UUID, p EventPayload) error {
	return events.Emit(ctx, q, events.Event{Kind: kind, Action: action, RepoID: repo, ActorID: actor, SubjectID: subject}, p)
}

// event appends an entry to the timeline of an issue.
func (s *Service) event(ctx context.Context, q *dbgen.Queries, i dbgen.Issue, actor uuid.UUID, name string, comment uuid.UUID, data map[string]any) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("event id: %w", err)
	}
	if data == nil {
		data = map[string]any{}
	}
	b, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("event data: %w", err)
	}
	if err := q.InsertIssueEvent(ctx, dbgen.InsertIssueEventParams{ID: id, RepoID: i.RepoID, IssueID: i.ID,
		ActorID: nullID(actor), Event: name, CommentID: nullID(comment), Data: b}); err != nil {
		return fmt.Errorf("insert %s event: %w", name, err)
	}
	return nil
}

// ---- access ----

func canTriage(r repos.Repo) bool { return authz.Allowed(r.Permission, authz.ActTriage) }

// repo resolves owner/name for reading.
func (s *Service) repo(ctx context.Context, who auth.Principal, owner, name string) (repos.Repo, error) {
	return s.o.Repos.Get(ctx, who, owner, name)
}

// writer resolves owner/name for a signed-in user (any write on issues).
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

// triager resolves owner/name for a user with the triage permission.
func (s *Service) triager(ctx context.Context, who auth.Principal, owner, name string) (repos.Repo, error) {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return repos.Repo{}, err
	}
	if !canTriage(r) {
		return repos.Repo{}, ErrForbidden
	}
	return r, nil
}

// systemPrincipal reads any repository (background handlers).
var systemPrincipal = auth.Principal{Role: auth.RoleAdmin}

func (s *Service) auditEvent(who auth.Principal, action string, r repos.Repo, targetType string, target uuid.UUID, c identity.Client, data map[string]any) audit.Event {
	org := uuid.Nil
	if r.OwnerKind == "org" {
		org = r.OwnerID
	}
	if data == nil {
		data = map[string]any{}
	}
	data["repo"] = r.FullName()
	return audit.Event{Actor: who.UserID, Action: action, TargetType: targetType, TargetID: target.String(), IP: c.IP, Data: data, OrgID: org}
}

// ---- helpers ----

func nullID(id uuid.UUID) uuid.NullUUID { return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil} }

func tsPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func pageArgs(offset, limit int) (int32, int32) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	return int32(offset), int32(limit) //nolint:gosec // bounded above
}

func checkText(name, v string, max int) error {
	if utf8.RuneCountInString(v) > max {
		return invalid("%s is longer than %d characters", name, max)
	}
	if !utf8.ValidString(v) || strings.ContainsRune(v, 0) {
		return invalid("%s is not valid text", name)
	}
	return nil
}

func checkTitle(name, v string, max int) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", invalid("%s is required", name)
	}
	return v, checkText(name, v, max)
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
	s.o.Bus.Kick(ctx)
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
