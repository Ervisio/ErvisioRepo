// Package protection implements branch protection rules, tag rules and
// CODEOWNERS (PLAN §5.3). The rules are enforced by the git hooks (Hooks)
// on every ref update: SSH and HTTP pushes in pre-receive, web edits and
// merges (git update-ref) in the "prepared" reference transaction. The
// same Engine answers the dry-run endpoint and, for pull requests, what is
// still missing before a merge (PullRequestStatus).
package protection

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

var (
	// ErrNotFound is returned for an unknown rule.
	ErrNotFound = errors.New("rule not found")
	// ErrInvalid wraps an invalid rule or request.
	ErrInvalid = errors.New("invalid")
	// ErrConflict is returned when a rule with the same pattern exists.
	ErrConflict = errors.New("a rule with this pattern already exists")
	// ErrUnavailable is returned when the repositories are not configured.
	ErrUnavailable = errors.New("repositories are not available")
)

// Tag rule levels: the minimum repository permission for an action.
const (
	LevelWrite    = "write"
	LevelMaintain = "maintain"
	LevelAdmin    = "admin"
	LevelNone     = "none"
)

// maxListed bounds the users and teams of a rule.
const maxListed = 100

// BranchRule is a branch protection rule with its users and teams named.
type BranchRule struct {
	dbgen.BranchProtection
	PushUsers []string
	PushTeams []string
}

// TagRule is a tag rule with its users and teams named.
type TagRule struct {
	dbgen.TagRule
	AllowedUsers []string
	AllowedTeams []string
}

// BranchRuleInput creates or replaces a branch protection rule.
type BranchRuleInput struct {
	Pattern                 string
	RequiredApprovals       int
	RequireCodeOwnerReviews bool
	DismissStaleReviews     bool
	RequiredStatusChecks    []string
	StrictStatusChecks      bool
	RequireLinearHistory    bool
	AllowForcePushes        bool
	AllowDeletions          bool
	RequireSignedCommits    bool
	RestrictPushes          bool
	PushUsers               []string
	PushTeams               []string
	AdminsBypass            bool
}

// TagRuleInput creates or replaces a tag rule. Empty levels take the
// defaults: create "write", update "none", delete "admin".
type TagRuleInput struct {
	Pattern      string
	Create       string
	Update       string
	Delete       string
	AllowedUsers []string
	AllowedTeams []string
	AdminsBypass bool
}

// Options configures an Engine.
type Options struct {
	Pool *pgxpool.Pool
	// Repos authorizes the REST callers; Tier serves the dry run. Both may
	// be nil for an Engine used only by the hooks.
	Repos *repos.Service
	Tier  *gitstore.Tier
	// Reviews and Checks feed the pull request requirements; nil means
	// none (no approvals, no successful checks).
	Reviews ReviewSource
	Checks  CheckSource
	Log     *slog.Logger
}

// Engine evaluates and manages the rules.
type Engine struct {
	pool    *pgxpool.Pool
	q       *dbgen.Queries
	repos   *repos.Service
	tier    *gitstore.Tier
	reviews ReviewSource
	checks  CheckSource
	log     *slog.Logger
}

// New builds an Engine.
func New(o Options) (*Engine, error) {
	if o.Pool == nil {
		return nil, errors.New("protection: no database pool")
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Reviews == nil {
		o.Reviews = NoReviews{}
	}
	if o.Checks == nil {
		o.Checks = NoChecks{}
	}
	return &Engine{pool: o.Pool, q: dbgen.New(o.Pool), repos: o.Repos, tier: o.Tier,
		reviews: o.Reviews, checks: o.Checks, log: o.Log}, nil
}

// SetSources replaces the review and check sources (the pull request and
// Actions services, built after the Engine). Call it before serving.
func (e *Engine) SetSources(r ReviewSource, c CheckSource) {
	if r != nil {
		e.reviews = r
	}
	if c != nil {
		e.checks = c
	}
}

// admin returns the repository if the caller administers it.
func (e *Engine) admin(ctx context.Context, who auth.Principal, owner, name string) (repos.Repo, error) {
	if e.repos == nil {
		return repos.Repo{}, ErrUnavailable
	}
	r, err := e.repos.Get(ctx, who, owner, name)
	if err != nil {
		return repos.Repo{}, err
	}
	if r.Permission < authz.PermAdmin {
		return repos.Repo{}, repos.ErrForbidden
	}
	return r, nil
}

// ListBranchRules returns the branch protection rules of a repository.
func (e *Engine) ListBranchRules(ctx context.Context, who auth.Principal, owner, name string) ([]BranchRule, error) {
	r, err := e.admin(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	rows, err := e.q.ListBranchProtections(ctx, r.ID)
	if err != nil {
		return nil, fmt.Errorf("list branch protections: %w", err)
	}
	out := make([]BranchRule, 0, len(rows))
	for _, row := range rows {
		br, err := e.branchView(ctx, r, row)
		if err != nil {
			return nil, err
		}
		out = append(out, br)
	}
	return out, nil
}

// GetBranchRule returns one branch protection rule.
func (e *Engine) GetBranchRule(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (BranchRule, error) {
	r, err := e.admin(ctx, who, owner, name)
	if err != nil {
		return BranchRule{}, err
	}
	row, err := e.q.GetBranchProtection(ctx, dbgen.GetBranchProtectionParams{RepoID: r.ID, RuleID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return BranchRule{}, ErrNotFound
	}
	if err != nil {
		return BranchRule{}, fmt.Errorf("get branch protection: %w", err)
	}
	return e.branchView(ctx, r, row)
}

// CreateBranchRule adds a branch protection rule.
func (e *Engine) CreateBranchRule(ctx context.Context, who auth.Principal, owner, name string, in BranchRuleInput) (BranchRule, error) {
	r, err := e.admin(ctx, who, owner, name)
	if err != nil {
		return BranchRule{}, err
	}
	users, teams, err := e.validateBranch(ctx, r, &in)
	if err != nil {
		return BranchRule{}, err
	}
	row, err := e.q.InsertBranchProtection(ctx, dbgen.InsertBranchProtectionParams{
		ID: uuid.New(), RepoID: r.ID, Pattern: in.Pattern, RequiredApprovals: int32(in.RequiredApprovals), //nolint:gosec // validated 0..10
		RequireCodeOwnerReviews: in.RequireCodeOwnerReviews, DismissStaleReviews: in.DismissStaleReviews,
		RequiredStatusChecks: in.RequiredStatusChecks, StrictStatusChecks: in.StrictStatusChecks,
		RequireLinearHistory: in.RequireLinearHistory, AllowForcePushes: in.AllowForcePushes,
		AllowDeletions: in.AllowDeletions, RequireSignedCommits: in.RequireSignedCommits,
		RestrictPushes: in.RestrictPushes, PushUserIds: users, PushTeamIds: teams,
		AdminsBypass: in.AdminsBypass, CreatedBy: uuid.NullUUID{UUID: who.UserID, Valid: who.UserID != uuid.Nil},
	})
	if err != nil {
		return BranchRule{}, writeFail("insert branch protection", err)
	}
	e.log.InfoContext(ctx, "branch protection created", "repo", r.ID, "rule", row.ID, "pattern", row.Pattern, "user", who.UserID)
	return e.branchView(ctx, r, row)
}

// UpdateBranchRule replaces a branch protection rule.
func (e *Engine) UpdateBranchRule(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, in BranchRuleInput) (BranchRule, error) {
	r, err := e.admin(ctx, who, owner, name)
	if err != nil {
		return BranchRule{}, err
	}
	users, teams, err := e.validateBranch(ctx, r, &in)
	if err != nil {
		return BranchRule{}, err
	}
	row, err := e.q.UpdateBranchProtection(ctx, dbgen.UpdateBranchProtectionParams{
		RepoID: r.ID, ID: id, Pattern: in.Pattern, RequiredApprovals: int32(in.RequiredApprovals), //nolint:gosec // validated 0..10
		RequireCodeOwnerReviews: in.RequireCodeOwnerReviews, DismissStaleReviews: in.DismissStaleReviews,
		RequiredStatusChecks: in.RequiredStatusChecks, StrictStatusChecks: in.StrictStatusChecks,
		RequireLinearHistory: in.RequireLinearHistory, AllowForcePushes: in.AllowForcePushes,
		AllowDeletions: in.AllowDeletions, RequireSignedCommits: in.RequireSignedCommits,
		RestrictPushes: in.RestrictPushes, PushUserIds: users, PushTeamIds: teams, AdminsBypass: in.AdminsBypass,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return BranchRule{}, ErrNotFound
	}
	if err != nil {
		return BranchRule{}, writeFail("update branch protection", err)
	}
	e.log.InfoContext(ctx, "branch protection updated", "repo", r.ID, "rule", row.ID, "pattern", row.Pattern, "user", who.UserID)
	return e.branchView(ctx, r, row)
}

// DeleteBranchRule removes a branch protection rule.
func (e *Engine) DeleteBranchRule(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) error {
	r, err := e.admin(ctx, who, owner, name)
	if err != nil {
		return err
	}
	n, err := e.q.DeleteBranchProtection(ctx, dbgen.DeleteBranchProtectionParams{RepoID: r.ID, RuleID: id})
	if err != nil {
		return fmt.Errorf("delete branch protection: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	e.log.InfoContext(ctx, "branch protection deleted", "repo", r.ID, "rule", id, "user", who.UserID)
	return nil
}

// ListTagRules returns the tag rules of a repository.
func (e *Engine) ListTagRules(ctx context.Context, who auth.Principal, owner, name string) ([]TagRule, error) {
	r, err := e.admin(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	rows, err := e.q.ListTagRules(ctx, r.ID)
	if err != nil {
		return nil, fmt.Errorf("list tag rules: %w", err)
	}
	out := make([]TagRule, 0, len(rows))
	for _, row := range rows {
		tr, err := e.tagView(ctx, r, row)
		if err != nil {
			return nil, err
		}
		out = append(out, tr)
	}
	return out, nil
}

// GetTagRule returns one tag rule.
func (e *Engine) GetTagRule(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (TagRule, error) {
	r, err := e.admin(ctx, who, owner, name)
	if err != nil {
		return TagRule{}, err
	}
	row, err := e.q.GetTagRule(ctx, dbgen.GetTagRuleParams{RepoID: r.ID, RuleID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return TagRule{}, ErrNotFound
	}
	if err != nil {
		return TagRule{}, fmt.Errorf("get tag rule: %w", err)
	}
	return e.tagView(ctx, r, row)
}

// CreateTagRule adds a tag rule.
func (e *Engine) CreateTagRule(ctx context.Context, who auth.Principal, owner, name string, in TagRuleInput) (TagRule, error) {
	r, err := e.admin(ctx, who, owner, name)
	if err != nil {
		return TagRule{}, err
	}
	users, teams, err := e.validateTag(ctx, r, &in)
	if err != nil {
		return TagRule{}, err
	}
	row, err := e.q.InsertTagRule(ctx, dbgen.InsertTagRuleParams{
		ID: uuid.New(), RepoID: r.ID, Pattern: in.Pattern, CreateMin: in.Create, UpdateMin: in.Update,
		DeleteMin: in.Delete, AllowedUserIds: users, AllowedTeamIds: teams, AdminsBypass: in.AdminsBypass,
		CreatedBy: uuid.NullUUID{UUID: who.UserID, Valid: who.UserID != uuid.Nil},
	})
	if err != nil {
		return TagRule{}, writeFail("insert tag rule", err)
	}
	e.log.InfoContext(ctx, "tag rule created", "repo", r.ID, "rule", row.ID, "pattern", row.Pattern, "user", who.UserID)
	return e.tagView(ctx, r, row)
}

// UpdateTagRule replaces a tag rule.
func (e *Engine) UpdateTagRule(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, in TagRuleInput) (TagRule, error) {
	r, err := e.admin(ctx, who, owner, name)
	if err != nil {
		return TagRule{}, err
	}
	users, teams, err := e.validateTag(ctx, r, &in)
	if err != nil {
		return TagRule{}, err
	}
	row, err := e.q.UpdateTagRule(ctx, dbgen.UpdateTagRuleParams{
		RepoID: r.ID, ID: id, Pattern: in.Pattern, CreateMin: in.Create, UpdateMin: in.Update,
		DeleteMin: in.Delete, AllowedUserIds: users, AllowedTeamIds: teams, AdminsBypass: in.AdminsBypass,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return TagRule{}, ErrNotFound
	}
	if err != nil {
		return TagRule{}, writeFail("update tag rule", err)
	}
	e.log.InfoContext(ctx, "tag rule updated", "repo", r.ID, "rule", row.ID, "pattern", row.Pattern, "user", who.UserID)
	return e.tagView(ctx, r, row)
}

// DeleteTagRule removes a tag rule.
func (e *Engine) DeleteTagRule(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) error {
	r, err := e.admin(ctx, who, owner, name)
	if err != nil {
		return err
	}
	n, err := e.q.DeleteTagRule(ctx, dbgen.DeleteTagRuleParams{RepoID: r.ID, RuleID: id})
	if err != nil {
		return fmt.Errorf("delete tag rule: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	e.log.InfoContext(ctx, "tag rule deleted", "repo", r.ID, "rule", id, "user", who.UserID)
	return nil
}

func writeFail(op string, err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return ErrConflict
	}
	return fmt.Errorf("%s: %w", op, err)
}

func (e *Engine) validateBranch(ctx context.Context, r repos.Repo, in *BranchRuleInput) (users, teams []uuid.UUID, err error) {
	in.Pattern = strings.TrimSpace(in.Pattern)
	if err := ValidatePattern(in.Pattern); err != nil {
		return nil, nil, err
	}
	if in.RequiredApprovals < 0 || in.RequiredApprovals > 10 {
		return nil, nil, fmt.Errorf("%w: requiredApprovals must be between 0 and 10", ErrInvalid)
	}
	if len(in.RequiredStatusChecks) > 50 {
		return nil, nil, fmt.Errorf("%w: at most 50 required status checks", ErrInvalid)
	}
	checks := make([]string, 0, len(in.RequiredStatusChecks))
	for _, c := range in.RequiredStatusChecks {
		c = strings.TrimSpace(c)
		if c == "" || len(c) > 255 {
			return nil, nil, fmt.Errorf("%w: a status check context must be 1 to 255 characters", ErrInvalid)
		}
		if !slices.Contains(checks, c) {
			checks = append(checks, c)
		}
	}
	in.RequiredStatusChecks = checks
	if users, err = e.userIDs(ctx, in.PushUsers); err != nil {
		return nil, nil, err
	}
	if teams, err = e.teamIDs(ctx, r, in.PushTeams); err != nil {
		return nil, nil, err
	}
	return users, teams, nil
}

func (e *Engine) validateTag(ctx context.Context, r repos.Repo, in *TagRuleInput) (users, teams []uuid.UUID, err error) {
	in.Pattern = strings.TrimSpace(in.Pattern)
	if err := ValidatePattern(in.Pattern); err != nil {
		return nil, nil, err
	}
	for _, l := range []struct {
		v   *string
		def string
	}{{&in.Create, LevelWrite}, {&in.Update, LevelNone}, {&in.Delete, LevelAdmin}} {
		if *l.v == "" {
			*l.v = l.def
		}
		if _, ok := levelPermission(*l.v); !ok {
			return nil, nil, fmt.Errorf("%w: level %q (want write, maintain, admin or none)", ErrInvalid, *l.v)
		}
	}
	if users, err = e.userIDs(ctx, in.AllowedUsers); err != nil {
		return nil, nil, err
	}
	if teams, err = e.teamIDs(ctx, r, in.AllowedTeams); err != nil {
		return nil, nil, err
	}
	return users, teams, nil
}

// levelPermission maps a tag rule level to the permission it needs; none
// maps to a permission nobody has.
func levelPermission(l string) (authz.Permission, bool) {
	switch l {
	case LevelWrite:
		return authz.PermWrite, true
	case LevelMaintain:
		return authz.PermMaintain, true
	case LevelAdmin:
		return authz.PermAdmin, true
	case LevelNone:
		return authz.PermAdmin + 1, true
	}
	return 0, false
}

func (e *Engine) userIDs(ctx context.Context, names []string) ([]uuid.UUID, error) {
	if len(names) > maxListed {
		return nil, fmt.Errorf("%w: at most %d users", ErrInvalid, maxListed)
	}
	ids := make([]uuid.UUID, 0, len(names))
	for _, n := range names {
		u, err := e.q.GetUserByUsername(ctx, strings.TrimPrefix(strings.TrimSpace(n), "@"))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: unknown user %q", ErrInvalid, n)
		}
		if err != nil {
			return nil, fmt.Errorf("get user: %w", err)
		}
		if !slices.Contains(ids, u.ID) {
			ids = append(ids, u.ID)
		}
	}
	return ids, nil
}

func (e *Engine) teamIDs(ctx context.Context, r repos.Repo, names []string) ([]uuid.UUID, error) {
	if len(names) == 0 {
		return []uuid.UUID{}, nil
	}
	if r.OwnerKind != "org" {
		return nil, fmt.Errorf("%w: teams can only be listed for repositories of an organization", ErrInvalid)
	}
	if len(names) > maxListed {
		return nil, fmt.Errorf("%w: at most %d teams", ErrInvalid, maxListed)
	}
	ids := make([]uuid.UUID, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		// Accept "team" and "org/team" (the owning organization only).
		if org, t, ok := strings.Cut(strings.TrimPrefix(n, "@"), "/"); ok {
			if !strings.EqualFold(org, r.Owner) {
				return nil, fmt.Errorf("%w: team %q is not a team of %s", ErrInvalid, n, r.Owner)
			}
			n = t
		}
		t, err := e.q.GetTeamByName(ctx, dbgen.GetTeamByNameParams{OrgID: r.OwnerID, Name: n})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: unknown team %q", ErrInvalid, n)
		}
		if err != nil {
			return nil, fmt.Errorf("get team: %w", err)
		}
		if !slices.Contains(ids, t.ID) {
			ids = append(ids, t.ID)
		}
	}
	return ids, nil
}

func (e *Engine) names(ctx context.Context, r repos.Repo, users, teams []uuid.UUID) (un, tn []string, err error) {
	un, tn = []string{}, []string{}
	if len(users) > 0 {
		rows, err := e.q.ResolveProtectionUsers(ctx, users)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve users: %w", err)
		}
		for _, u := range rows {
			un = append(un, u.Name)
		}
	}
	if len(teams) > 0 {
		rows, err := e.q.ResolveProtectionTeams(ctx, dbgen.ResolveProtectionTeamsParams{OrgID: r.OwnerID, Ids: teams})
		if err != nil {
			return nil, nil, fmt.Errorf("resolve teams: %w", err)
		}
		for _, t := range rows {
			tn = append(tn, t.Name)
		}
	}
	return un, tn, nil
}

func (e *Engine) branchView(ctx context.Context, r repos.Repo, row dbgen.BranchProtection) (BranchRule, error) {
	u, t, err := e.names(ctx, r, row.PushUserIds, row.PushTeamIds)
	if err != nil {
		return BranchRule{}, err
	}
	return BranchRule{BranchProtection: row, PushUsers: u, PushTeams: t}, nil
}

func (e *Engine) tagView(ctx context.Context, r repos.Repo, row dbgen.TagRule) (TagRule, error) {
	u, t, err := e.names(ctx, r, row.AllowedUserIds, row.AllowedTeamIds)
	if err != nil {
		return TagRule{}, err
	}
	return TagRule{TagRule: row, AllowedUsers: u, AllowedTeams: t}, nil
}

// SetRepos sets the repository service the REST methods authorize with
// (built after the hook server). Call it before serving.
func (e *Engine) SetRepos(r *repos.Service) { e.repos = r }
