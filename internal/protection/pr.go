package protection

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/git"
)

// Review is a review of a pull request.
type Review struct {
	ReviewerID uuid.UUID
	CommitSHA  string // the head the review was given on
	Approved   bool   // the latest review of the reviewer approves
}

// ReviewQuery selects the reviews of a pull request.
type ReviewQuery struct {
	RepoID  uuid.UUID
	PullID  uuid.UUID
	HeadSHA string
}

// ReviewSource returns the latest review of each reviewer of a pull request
// (implemented by the pull request service).
type ReviewSource interface {
	Reviews(ctx context.Context, q ReviewQuery) ([]Review, error)
}

// Status check states.
const (
	StatePending = "pending"
	StateSuccess = "success"
	StateFailure = "failure"
	StateError   = "error"
)

// CheckStatus is the latest state of one status check context on a commit.
type CheckStatus struct {
	Context string
	State   string
}

// CheckSource returns the latest status of each check context on a commit
// (implemented by the Actions / commit status service).
type CheckSource interface {
	Statuses(ctx context.Context, repoID uuid.UUID, sha string) ([]CheckStatus, error)
}

// NoReviews is a ReviewSource without reviews.
type NoReviews struct{}

// Reviews returns none.
func (NoReviews) Reviews(context.Context, ReviewQuery) ([]Review, error) { return nil, nil }

// NoChecks is a CheckSource without statuses.
type NoChecks struct{}

// Statuses returns none.
func (NoChecks) Statuses(context.Context, uuid.UUID, string) ([]CheckStatus, error) { return nil, nil }

// PRInput describes a pull request about to be merged into Base.
type PRInput struct {
	RepoID   uuid.UUID
	RepoPath string
	Env      []string
	Base     string // target branch name, without refs/heads/
	BaseSHA  string // current tip of the base branch
	HeadSHA  string
	PullID   uuid.UUID
	AuthorID uuid.UUID // the author's own approval does not count
}

// OwnerGroup is the owners of a CODEOWNERS rule matching changed files,
// one of whom must approve.
type OwnerGroup struct {
	Pattern  string
	Owners   []string
	Paths    []string // a sample of the matched paths
	Approved bool
}

// PRStatus is what a pull request still misses before it can be merged.
type PRStatus struct {
	Rule              string // "" when no rule protects the base branch
	RequiredApprovals int
	Approvals         int
	MissingChecks     []string
	Behind            bool // strict checks: the head does not contain the base tip
	CodeOwners        []OwnerGroup
	CodeOwnersFile    string
	CodeOwnersErrors  []string
	// Violations lists every unmet requirement in a readable form.
	Violations []Violation
}

// Mergeable reports whether every requirement is met.
func (s PRStatus) Mergeable() bool { return len(s.Violations) == 0 }

// maxOwnerPaths bounds the changed paths matched against CODEOWNERS.
const maxOwnerPaths = 20000

// PullRequestStatus evaluates the review and check requirements of the
// rule protecting in.Base. The ref update of the merge itself (signatures,
// linear history...) is checked by the hooks when it happens.
func (e *Engine) PullRequestStatus(ctx context.Context, in PRInput) (PRStatus, error) {
	var st PRStatus
	rules, err := e.q.ListBranchProtections(ctx, in.RepoID)
	if err != nil {
		return st, fmt.Errorf("list branch protections: %w", err)
	}
	pats := make([]string, len(rules))
	for i, r := range rules {
		pats[i] = r.Pattern
	}
	i := Select(pats, in.Base)
	if i < 0 {
		return st, nil
	}
	r := rules[i]
	st.Rule, st.RequiredApprovals = r.Pattern, int(r.RequiredApprovals)
	add := func(code, msg string) { st.Violations = append(st.Violations, Violation{Code: code, Message: msg}) }

	var approvers []uuid.UUID
	if r.RequiredApprovals > 0 || r.RequireCodeOwnerReviews {
		reviews, err := e.reviews.Reviews(ctx, ReviewQuery{RepoID: in.RepoID, PullID: in.PullID, HeadSHA: in.HeadSHA})
		if err != nil {
			return st, fmt.Errorf("load reviews: %w", err)
		}
		for _, rv := range reviews {
			if !rv.Approved || rv.ReviewerID == in.AuthorID {
				continue
			}
			if r.DismissStaleReviews && rv.CommitSHA != in.HeadSHA {
				continue
			}
			if !slices.Contains(approvers, rv.ReviewerID) {
				approvers = append(approvers, rv.ReviewerID)
			}
		}
		st.Approvals = len(approvers)
		if st.Approvals < st.RequiredApprovals {
			add("approvals", fmt.Sprintf("%d of %d required approvals", st.Approvals, st.RequiredApprovals))
		}
	}
	if len(r.RequiredStatusChecks) > 0 {
		st.MissingChecks, err = e.missingChecks(ctx, in.RepoID, in.HeadSHA, r.RequiredStatusChecks)
		if err != nil {
			return st, err
		}
		if len(st.MissingChecks) > 0 {
			add(CodeStatusChecks, "required status checks have not passed: "+strings.Join(st.MissingChecks, ", "))
		}
		if r.StrictStatusChecks && in.BaseSHA != "" && !zero(in.BaseSHA) {
			ok, err := git.IsAncestor(ctx, in.RepoPath, in.Env, in.BaseSHA, in.HeadSHA)
			if err != nil {
				return st, fmt.Errorf("check up to date: %w", err)
			}
			if !ok {
				st.Behind = true
				add("behind", "the branch is not up to date with "+in.Base)
			}
		}
	}
	if r.RequireCodeOwnerReviews {
		if err := e.codeOwnerStatus(ctx, in, approvers, &st); err != nil {
			return st, err
		}
		for _, g := range st.CodeOwners {
			if !g.Approved {
				add("code_owners", fmt.Sprintf("review required from the code owners of %s: %s", g.Pattern, strings.Join(g.Owners, " ")))
			}
		}
	}
	return st, nil
}

func (e *Engine) codeOwnerStatus(ctx context.Context, in PRInput, approvers []uuid.UUID, st *PRStatus) error {
	if in.BaseSHA == "" || zero(in.BaseSHA) {
		return nil
	}
	co, err := LoadCodeOwners(ctx, in.RepoPath, in.Env, in.BaseSHA)
	if err != nil || co == nil {
		return err
	}
	st.CodeOwnersFile, st.CodeOwnersErrors = co.Path, co.Errors
	base, err := git.MergeBase(ctx, in.RepoPath, in.Env, in.BaseSHA, in.HeadSHA)
	if err != nil {
		return fmt.Errorf("merge base: %w", err)
	}
	paths, err := git.ChangedPaths(ctx, in.RepoPath, in.Env, base, in.HeadSHA, maxOwnerPaths)
	if err != nil {
		return fmt.Errorf("changed paths: %w", err)
	}
	groups := map[*OwnerRule]*OwnerGroup{}
	var order []*OwnerRule
	for _, p := range paths {
		rule := co.Match(p)
		if rule == nil || len(rule.Owners) == 0 {
			continue
		}
		g, ok := groups[rule]
		if !ok {
			g = &OwnerGroup{Pattern: rule.Pattern, Owners: rule.Owners}
			groups[rule] = g
			order = append(order, rule)
		}
		if len(g.Paths) < 10 {
			g.Paths = append(g.Paths, p)
		}
	}
	res := &ownerResolver{e: e, cache: map[string][]uuid.UUID{}}
	for _, rule := range order {
		g := groups[rule]
		for _, o := range g.Owners {
			ids, err := res.resolve(ctx, o)
			if err != nil {
				return err
			}
			if slices.ContainsFunc(ids, func(id uuid.UUID) bool { return slices.Contains(approvers, id) }) {
				g.Approved = true
				break
			}
		}
		st.CodeOwners = append(st.CodeOwners, *g)
	}
	return nil
}

// ownerResolver maps CODEOWNERS owners to user ids: @user, @org/team (its
// members) and a verified email address. Unknown owners map to nobody.
type ownerResolver struct {
	e     *Engine
	cache map[string][]uuid.UUID
}

func (r *ownerResolver) resolve(ctx context.Context, owner string) ([]uuid.UUID, error) {
	key := strings.ToLower(owner)
	if ids, ok := r.cache[key]; ok {
		return ids, nil
	}
	ids, err := r.lookup(ctx, owner)
	if err != nil {
		return nil, err
	}
	r.cache[key] = ids
	return ids, nil
}

func (r *ownerResolver) lookup(ctx context.Context, owner string) ([]uuid.UUID, error) {
	q := r.e.q
	name, isHandle := strings.CutPrefix(owner, "@")
	if !isHandle {
		id, err := q.GetProtectionUserByVerifiedEmail(ctx, owner)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("lookup owner email: %w", err)
		}
		return []uuid.UUID{id}, nil
	}
	org, team, isTeam := strings.Cut(name, "/")
	if !isTeam {
		u, err := q.GetUserByUsername(ctx, name)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("lookup owner: %w", err)
		}
		return []uuid.UUID{u.ID}, nil
	}
	ns, err := q.GetNamespaceByName(ctx, org)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && ns.Kind != "org") {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lookup owner org: %w", err)
	}
	t, err := q.GetTeamByName(ctx, dbgen.GetTeamByNameParams{OrgID: ns.ID, Name: team})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lookup owner team: %w", err)
	}
	ids, err := q.ListProtectionTeamMembers(ctx, t.ID)
	if err != nil {
		return nil, fmt.Errorf("list team members: %w", err)
	}
	return ids, nil
}

// FileOwners returns the CODEOWNERS owners of each of paths at rev (nil
// when the revision has no CODEOWNERS file).
func FileOwners(ctx context.Context, repoPath string, env []string, rev string, paths []string) (map[string][]string, *CodeOwners, error) {
	co, err := LoadCodeOwners(ctx, repoPath, env, rev)
	if err != nil || co == nil {
		return nil, co, err
	}
	return co.OwnersOf(paths), co, nil
}
