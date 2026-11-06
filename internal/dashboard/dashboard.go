// Package dashboard serves the feeds of the web home page: the issues and
// pull requests of the signed-in user, the recent Actions runs, the activity
// feed, and the user's UI preferences. Every feed only covers repositories
// the caller can read.
package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// Page limits of the paged feeds.
const (
	DefaultPerPage = 30
	MaxPerPage     = 100
	maxPage        = 1 << 20
)

// ErrUnauthenticated is returned when a feed needs a signed-in user.
var ErrUnauthenticated = errors.New("sign in to see this")

// ValidationError is an invalid input; its message is safe to show.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

// Service reads the dashboard feeds.
type Service struct {
	q *dbgen.Queries
}

// New builds a Service on pool.
func New(pool *pgxpool.Pool) *Service { return &Service{q: dbgen.New(pool)} }

// scope is the visibility input of the repo_readable SQL function.
type scope struct {
	viewer   uuid.UUID
	allRepos bool
	internal bool
}

func scopeOf(who auth.Principal) scope {
	return scope{viewer: who.UserID, allRepos: who.IsAdmin(),
		internal: who.UserID != uuid.Nil && who.Role != auth.RoleRestricted}
}

func requireUser(who auth.Principal) error {
	if who.UserID == uuid.Nil {
		return ErrUnauthenticated
	}
	return nil
}

// paging turns 1-based page and perPage into a limit and an offset.
func paging(page, perPage int) (lim, off int32) {
	if perPage <= 0 {
		perPage = DefaultPerPage
	}
	perPage = min(perPage, MaxPerPage)
	page = min(max(page, 1), maxPage)
	return int32(perPage), int32((page - 1) * perPage) //nolint:gosec // both bounded above
}

// Label is a label name and color on an issue row.
type Label struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// Issue is an issue or pull request of the issue feed.
type Issue struct {
	Owner, Repo  string
	Number       int64
	Title        string
	Kind         string // issue or pull
	State        string // open, closed or merged
	Author       string
	Labels       []Label
	CommentCount int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// IssueFilter selects the issue feed. Empty fields take the defaults:
// Filter assigned, Type all, State open.
type IssueFilter struct {
	Filter  string // assigned, created, mentioned, review_requested
	Type    string // issue, pr, all
	State   string // open, closed, all
	Page    int
	PerPage int
}

// IssueList is a page of the issue feed with the total match count.
type IssueList struct {
	Total int64
	Items []Issue
}

// Issues lists the issues and pull requests that concern who, newest
// activity first.
func (s *Service) Issues(ctx context.Context, who auth.Principal, f IssueFilter) (IssueList, error) {
	if err := requireUser(who); err != nil {
		return IssueList{}, err
	}
	filter := orDefault(f.Filter, "assigned")
	switch filter {
	case "assigned", "created", "mentioned", "review_requested":
	default:
		return IssueList{}, invalid("filter must be assigned, created, mentioned or review_requested")
	}
	kind := ""
	switch orDefault(f.Type, "all") {
	case "all":
	case "issue":
		kind = "issue"
	case "pr":
		kind = "pull"
	default:
		return IssueList{}, invalid("type must be issue, pr or all")
	}
	state := orDefault(f.State, "open")
	switch state {
	case "open", "closed":
	case "all":
		state = ""
	default:
		return IssueList{}, invalid("state must be open, closed or all")
	}
	sc := scopeOf(who)
	lim, off := paging(f.Page, f.PerPage)
	total, err := s.q.CountUserIssues(ctx, dbgen.CountUserIssuesParams{Viewer: sc.viewer, AllRepos: sc.allRepos,
		Internal: sc.internal, Kind: kind, State: state, Filter: filter})
	if err != nil {
		return IssueList{}, fmt.Errorf("count user issues: %w", err)
	}
	rows, err := s.q.ListUserIssues(ctx, dbgen.ListUserIssuesParams{Viewer: sc.viewer, AllRepos: sc.allRepos,
		Internal: sc.internal, Kind: kind, State: state, Filter: filter, Off: off, Lim: lim})
	if err != nil {
		return IssueList{}, fmt.Errorf("list user issues: %w", err)
	}
	out := IssueList{Total: total, Items: make([]Issue, 0, len(rows))}
	for _, r := range rows {
		labels := []Label{}
		if err := json.Unmarshal(r.Labels, &labels); err != nil {
			return IssueList{}, fmt.Errorf("decode labels of issue %s: %w", r.ID, err)
		}
		st := r.State
		if r.Merged {
			st = "merged"
		}
		out.Items = append(out.Items, Issue{Owner: r.OwnerName, Repo: r.RepoName, Number: r.Number, Title: r.Title,
			Kind: r.Kind, State: st, Author: r.Author, Labels: labels, CommentCount: int(r.CommentCount),
			CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time})
	}
	return out, nil
}

// Run is a workflow run of the runs feed.
type Run struct {
	ID           uuid.UUID
	Owner, Repo  string
	RunNumber    int64
	WorkflowName string
	Title        string
	Branch       string
	Event        string
	Status       string
	Conclusion   string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// RunList is a page of the runs feed with the total match count.
type RunList struct {
	Total int64
	Items []Run
}

// Runs lists the recent workflow runs of the readable repositories. status
// matches a run status or a conclusion (for example failure).
func (s *Service) Runs(ctx context.Context, who auth.Principal, status string, page, perPage int) (RunList, error) {
	if err := requireUser(who); err != nil {
		return RunList{}, err
	}
	status = strings.TrimSpace(status)
	if len(status) > 32 {
		return RunList{}, invalid("status is too long")
	}
	sc := scopeOf(who)
	lim, off := paging(page, perPage)
	total, err := s.q.CountUserActionsRuns(ctx, dbgen.CountUserActionsRunsParams{Viewer: sc.viewer,
		AllRepos: sc.allRepos, Internal: sc.internal, Status: status})
	if err != nil {
		return RunList{}, fmt.Errorf("count user runs: %w", err)
	}
	rows, err := s.q.ListUserActionsRuns(ctx, dbgen.ListUserActionsRunsParams{Viewer: sc.viewer,
		AllRepos: sc.allRepos, Internal: sc.internal, Status: status, Off: off, Lim: lim})
	if err != nil {
		return RunList{}, fmt.Errorf("list user runs: %w", err)
	}
	out := RunList{Total: total, Items: make([]Run, 0, len(rows))}
	for _, r := range rows {
		a := r.ActionsRun
		out.Items = append(out.Items, Run{ID: a.ID, Owner: r.OwnerName, Repo: r.RepoName, RunNumber: a.RunNumber,
			WorkflowName: a.WorkflowName, Title: a.Title, Branch: shortRef(a.Ref), Event: a.Event, Status: a.Status,
			Conclusion: a.Conclusion, CreatedAt: a.CreatedAt.Time, UpdatedAt: a.UpdatedAt.Time})
	}
	return out, nil
}

// shortRef drops the refs/heads/ prefix; tags and pull refs stay whole.
func shortRef(ref string) string { return strings.TrimPrefix(ref, "refs/heads/") }

// Item is one entry of the activity feed.
type Item struct {
	ID          uuid.UUID
	Kind        string
	Action      string
	Actor       string
	Owner, Repo string
	Number      int64
	Title       string
	CreatedAt   time.Time
}

// Activity lists the newest events of the readable repositories.
func (s *Service) Activity(ctx context.Context, who auth.Principal, offset, limit int) ([]Item, error) {
	if err := requireUser(who); err != nil {
		return nil, err
	}
	sc := scopeOf(who)
	if limit <= 0 {
		limit = DefaultPerPage
	}
	limit = min(limit, MaxPerPage)
	off := int32(min(max(offset, 0), math.MaxInt32)) //nolint:gosec // clamped
	rows, err := s.q.ListUserActivity(ctx, dbgen.ListUserActivityParams{Viewer: sc.viewer, AllRepos: sc.allRepos,
		Internal: sc.internal, Off: off, Lim: int32(limit)}) //nolint:gosec // clamped
	if err != nil {
		return nil, fmt.Errorf("list user activity: %w", err)
	}
	out := make([]Item, 0, len(rows))
	for _, r := range rows {
		out = append(out, Item{ID: r.ID, Kind: r.Kind, Action: r.Action, Actor: r.Actor, Owner: r.OwnerName,
			Repo: r.RepoName, Number: r.Number, Title: r.Title, CreatedAt: r.CreatedAt.Time})
	}
	return out, nil
}

// Preferences are the UI choices of a user. Layout "" means the instance
// default.
type Preferences struct {
	Theme  string
	Layout string
}

var (
	themes  = map[string]bool{"system": true, "light": true, "dark": true}
	layouts = map[string]bool{"": true, "console": true, "foglio": true, "corsie": true}
)

// Preferences returns the preferences of who (the defaults when never set).
func (s *Service) Preferences(ctx context.Context, who auth.Principal) (Preferences, error) {
	if err := requireUser(who); err != nil {
		return Preferences{}, err
	}
	p, err := s.q.GetUserPreferences(ctx, who.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Preferences{Theme: "system"}, nil
	}
	if err != nil {
		return Preferences{}, fmt.Errorf("get preferences: %w", err)
	}
	return Preferences{Theme: p.Theme, Layout: p.Layout}, nil
}

// UpdatePreferences changes the given fields (nil keeps the current value).
func (s *Service) UpdatePreferences(ctx context.Context, who auth.Principal, theme, layout *string) (Preferences, error) {
	cur, err := s.Preferences(ctx, who)
	if err != nil {
		return Preferences{}, err
	}
	if theme != nil {
		if !themes[*theme] {
			return Preferences{}, invalid("theme must be system, light or dark")
		}
		cur.Theme = *theme
	}
	if layout != nil {
		if !layouts[*layout] {
			return Preferences{}, invalid("layout must be empty, console, foglio or corsie")
		}
		cur.Layout = *layout
	}
	p, err := s.q.UpsertUserPreferences(ctx, dbgen.UpsertUserPreferencesParams{UserID: who.UserID, Theme: cur.Theme,
		Layout: cur.Layout})
	if err != nil {
		return Preferences{}, fmt.Errorf("save preferences: %w", err)
	}
	return Preferences{Theme: p.Theme, Layout: p.Layout}, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
