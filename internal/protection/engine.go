package protection

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// ProtocolMerge is the hook session protocol of a pull request merge: the
// pull request requirements (reviews, checks) are then checked by the
// merge itself (PullRequestStatus), not refused as a direct push.
const ProtocolMerge = "merge"

// Violation codes.
const (
	CodePushRestricted      = "push_restricted"
	CodeForcePush           = "force_push"
	CodeDeletion            = "deletion"
	CodeLinearHistory       = "linear_history"
	CodeUnsignedCommit      = "unsigned_commit"
	CodeTooManyCommits      = "too_many_commits"
	CodePullRequestRequired = "pull_request_required"
	CodeStatusChecks        = "status_checks"
	CodeTagCreate           = "tag_create"
	CodeTagUpdate           = "tag_update"
	CodeTagDelete           = "tag_delete"
)

// Ref kinds.
const (
	KindBranch = "branch"
	KindTag    = "tag"
	KindOther  = "other"
)

// maxSignedCommits bounds the commits whose signature one update checks.
const maxSignedCommits = 10000

// Update is one ref update to check.
type Update struct {
	Ref string // full name: refs/heads/main
	Old string // zero or "" when the ref is created
	New string // zero or "" when the ref is deleted
}

// Input is a set of ref updates of one repository by one user.
type Input struct {
	RepoID   uuid.UUID
	RepoPath string   // a leased path of the repository
	Env      []string // extra git variables (push quarantine)
	PusherID uuid.UUID
	Protocol string // "ssh", "http", "web", ProtocolMerge...
	Updates  []Update
}

// Violation is one requirement an update does not meet.
type Violation struct {
	Code    string
	Message string
	Commit  string // the offending commit, when there is one
}

// Result is the outcome for one update. Rule is the pattern of the rule
// that applies ("" for none).
type Result struct {
	Ref        string
	Kind       string
	Rule       string
	Violations []Violation
}

// Allowed reports whether the update meets every rule.
func (r Result) Allowed() bool { return len(r.Violations) == 0 }

func zero(oid string) bool { return strings.Trim(oid, "0") == "" }

// pusher is the permission context of the user updating the refs, loaded
// lazily.
type pusher struct {
	id        uuid.UUID
	perm      authz.Permission
	teams     []uuid.UUID
	teamsDone bool
}

// CheckUpdates evaluates updates against the rules of the repository. It
// fails closed: an error means the updates must be refused.
func (e *Engine) CheckUpdates(ctx context.Context, in Input) ([]Result, error) {
	branches, err := e.q.ListBranchProtections(ctx, in.RepoID)
	if err != nil {
		return nil, fmt.Errorf("list branch protections: %w", err)
	}
	tags, err := e.q.ListTagRules(ctx, in.RepoID)
	if err != nil {
		return nil, fmt.Errorf("list tag rules: %w", err)
	}
	out := make([]Result, 0, len(in.Updates))
	if len(branches) == 0 && len(tags) == 0 {
		for _, u := range in.Updates {
			out = append(out, Result{Ref: u.Ref, Kind: refKind(u.Ref)})
		}
		return out, nil
	}
	p, err := e.loadPusher(ctx, in.RepoID, in.PusherID)
	if err != nil {
		return nil, err
	}
	bp := make([]string, len(branches))
	for i, b := range branches {
		bp[i] = b.Pattern
	}
	tp := make([]string, len(tags))
	for i, t := range tags {
		tp[i] = t.Pattern
	}
	for _, u := range in.Updates {
		res := Result{Ref: u.Ref, Kind: refKind(u.Ref)}
		switch res.Kind {
		case KindBranch:
			name := strings.TrimPrefix(u.Ref, "refs/heads/")
			if i := Select(bp, name); i >= 0 {
				res.Rule = branches[i].Pattern
				res.Violations, err = e.checkBranch(ctx, in, p, branches[i], u)
			}
		case KindTag:
			name := strings.TrimPrefix(u.Ref, "refs/tags/")
			if i := Select(tp, name); i >= 0 {
				res.Rule = tags[i].Pattern
				res.Violations, err = e.checkTag(ctx, p, tags[i], u)
			}
		}
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

func refKind(ref string) string {
	switch {
	case strings.HasPrefix(ref, "refs/heads/"):
		return KindBranch
	case strings.HasPrefix(ref, "refs/tags/"):
		return KindTag
	}
	return KindOther
}

// loadPusher computes the permission of the user on the repository. An
// unknown or anonymous user has no permission.
func (e *Engine) loadPusher(ctx context.Context, repoID, userID uuid.UUID) (*pusher, error) {
	p := &pusher{id: userID}
	if userID == uuid.Nil {
		return p, nil
	}
	u, err := e.q.GetUserByID(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get pusher: %w", err)
	}
	if u.SuspendedAt.Valid {
		return p, nil
	}
	row, err := e.q.GetRepo(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("get repo: %w", err)
	}
	f, err := repos.Facts(ctx, e.q, auth.Principal{UserID: u.ID, Username: u.Username, Role: u.Role}, row.Repo, row.OwnerKind)
	if err != nil {
		return nil, err
	}
	p.perm = authz.RepoPermission(f)
	return p, nil
}

// listed reports whether the pusher is one of users or a member of one of
// teams.
func (e *Engine) listed(ctx context.Context, p *pusher, users, teams []uuid.UUID) (bool, error) {
	if p.id == uuid.Nil {
		return false, nil
	}
	if slices.Contains(users, p.id) {
		return true, nil
	}
	if len(teams) == 0 {
		return false, nil
	}
	if !p.teamsDone {
		t, err := e.q.ListProtectionUserTeams(ctx, p.id)
		if err != nil {
			return false, fmt.Errorf("list user teams: %w", err)
		}
		p.teams, p.teamsDone = t, true
	}
	for _, t := range teams {
		if slices.Contains(p.teams, t) {
			return true, nil
		}
	}
	return false, nil
}

func (e *Engine) checkBranch(ctx context.Context, in Input, p *pusher, r dbgen.BranchProtection, u Update) ([]Violation, error) {
	if r.AdminsBypass && p.perm >= authz.PermAdmin {
		return nil, nil
	}
	var v []Violation
	add := func(code, msg, commit string) { v = append(v, Violation{Code: code, Message: msg, Commit: commit}) }

	if r.RestrictPushes {
		ok, err := e.listed(ctx, p, r.PushUserIds, r.PushTeamIds)
		if err != nil {
			return nil, err
		}
		if !ok {
			add(CodePushRestricted, "you are not allowed to push to this branch", "")
		}
	}
	if zero(u.New) {
		if !r.AllowDeletions {
			add(CodeDeletion, "the branch cannot be deleted", "")
		}
		return v, nil
	}
	if !zero(u.Old) && !r.AllowForcePushes {
		ok, err := git.IsAncestor(ctx, in.RepoPath, in.Env, u.Old, u.New)
		if err != nil {
			return nil, fmt.Errorf("check fast-forward: %w", err)
		}
		if !ok {
			add(CodeForcePush, "force pushes are not allowed (the update is not a fast-forward)", "")
		}
	}
	if r.RequireLinearHistory {
		merges, _, err := git.NewCommits(ctx, in.RepoPath, in.Env, u.New, true, 1)
		if err != nil {
			return nil, fmt.Errorf("list merge commits: %w", err)
		}
		if len(merges) > 0 {
			add(CodeLinearHistory, "merge commits are not allowed (linear history is required)", merges[0])
		}
	}
	if r.RequireSignedCommits {
		vs, err := e.checkSignatures(ctx, in, u.New)
		if err != nil {
			return nil, err
		}
		v = append(v, vs...)
	}
	if in.Protocol != ProtocolMerge {
		if r.RequiredApprovals > 0 || r.RequireCodeOwnerReviews {
			add(CodePullRequestRequired, "changes must be made through a pull request with the required reviews", "")
		}
		if len(r.RequiredStatusChecks) > 0 {
			missing, err := e.missingChecks(ctx, in.RepoID, u.New, r.RequiredStatusChecks)
			if err != nil {
				return nil, err
			}
			if len(missing) > 0 {
				add(CodeStatusChecks, "required status checks have not passed: "+strings.Join(missing, ", "), u.New)
			}
		}
	}
	return v, nil
}

func (e *Engine) checkSignatures(ctx context.Context, in Input, tip string) ([]Violation, error) {
	ids, more, err := git.NewCommits(ctx, in.RepoPath, in.Env, tip, false, maxSignedCommits)
	if err != nil {
		return nil, fmt.Errorf("list new commits: %w", err)
	}
	if more {
		return []Violation{{Code: CodeTooManyCommits,
			Message: fmt.Sprintf("the update brings more than %d new commits; push them in smaller parts so that their signatures can be checked", maxSignedCommits)}}, nil
	}
	var v []Violation
	for chunk := range slices.Chunk(ids, 500) {
		raws, err := git.RawCommits(ctx, in.RepoPath, in.Env, chunk)
		if err != nil {
			return nil, fmt.Errorf("read commits: %w", err)
		}
		for i, raw := range raws {
			reason, err := e.verifyCommit(ctx, raw)
			if err != nil {
				return nil, err
			}
			if reason != "" {
				v = append(v, Violation{Code: CodeUnsignedCommit, Message: "commit signature required: " + reason, Commit: chunk[i]})
				if len(v) >= 10 {
					return v, nil
				}
			}
		}
	}
	return v, nil
}

// missingChecks returns the required contexts without a successful status
// on sha.
func (e *Engine) missingChecks(ctx context.Context, repoID uuid.UUID, sha string, required []string) ([]string, error) {
	st, err := e.checks.Statuses(ctx, repoID, sha)
	if err != nil {
		return nil, fmt.Errorf("load statuses: %w", err)
	}
	var missing []string
	for _, c := range required {
		ok := false
		for _, s := range st {
			if s.Context == c && s.State == StateSuccess {
				ok = true
				break
			}
		}
		if !ok {
			missing = append(missing, c)
		}
	}
	return missing, nil
}

func (e *Engine) checkTag(ctx context.Context, p *pusher, r dbgen.TagRule, u Update) ([]Violation, error) {
	if r.AdminsBypass && p.perm >= authz.PermAdmin {
		return nil, nil
	}
	level, code, verb := r.UpdateMin, CodeTagUpdate, "moved"
	switch {
	case zero(u.Old):
		level, code, verb = r.CreateMin, CodeTagCreate, "created"
	case zero(u.New):
		level, code, verb = r.DeleteMin, CodeTagDelete, "deleted"
	}
	ok, err := e.listed(ctx, p, r.AllowedUserIds, r.AllowedTeamIds)
	if err != nil {
		return nil, err
	}
	if ok {
		return nil, nil
	}
	need, known := levelPermission(level)
	if known && p.perm >= need {
		return nil, nil
	}
	msg := fmt.Sprintf("tags matching %q cannot be %s", r.Pattern, verb)
	if level != LevelNone {
		msg += fmt.Sprintf(" without the %s permission", level)
	}
	return []Violation{{Code: code, Message: msg}}, nil
}

// Explain formats the violations of results as the lines git shows the
// pusher ("remote: ..."); "" when every update is allowed.
func Explain(results []Result) string {
	var b strings.Builder
	for _, r := range results {
		if r.Allowed() {
			continue
		}
		fmt.Fprintf(&b, "error: %s is protected by the %s rule %q:\n", r.Ref, r.Kind, r.Rule)
		for _, v := range r.Violations {
			if v.Commit != "" {
				fmt.Fprintf(&b, "  - %s (commit %s)\n", v.Message, v.Commit)
			} else {
				fmt.Fprintf(&b, "  - %s\n", v.Message)
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// Evaluate is the dry run of a ref update for the REST API: ref may be a
// full name or a branch name, empty new means a deletion, empty old a
// creation (or the current value when the ref exists). user names the
// pusher ("" for the caller).
func (e *Engine) Evaluate(ctx context.Context, who auth.Principal, owner, name, ref, oldID, newID, user string) (Result, error) {
	r, err := e.admin(ctx, who, owner, name)
	if err != nil {
		return Result{}, err
	}
	if e.tier == nil {
		return Result{}, ErrUnavailable
	}
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "refs/") {
		ref = "refs/heads/" + ref
	}
	if err := git.ValidateRefName(ref); err != nil {
		return Result{}, fmt.Errorf("%w: ref: %w", ErrInvalid, err)
	}
	for _, id := range []string{oldID, newID} {
		if id != "" {
			if err := git.ValidateObjectID(id); err != nil {
				return Result{}, fmt.Errorf("%w: object id: %w", ErrInvalid, err)
			}
		}
	}
	pusherID := who.UserID
	if user = strings.TrimPrefix(strings.TrimSpace(user), "@"); user != "" {
		u, err := e.q.GetUserByUsername(ctx, user)
		if errors.Is(err, pgx.ErrNoRows) {
			return Result{}, fmt.Errorf("%w: unknown user %q", ErrInvalid, user)
		}
		if err != nil {
			return Result{}, fmt.Errorf("get user: %w", err)
		}
		pusherID = u.ID
	}
	lease, err := e.tier.Acquire(ctx, r.ID, gitstore.Read)
	if err != nil {
		return Result{}, fmt.Errorf("acquire repository: %w", err)
	}
	defer lease.Release()
	if oldID == "" {
		if oldID, err = git.ResolveRef(ctx, lease.Path, nil, ref); err != nil {
			return Result{}, fmt.Errorf("resolve ref: %w", err)
		}
	}
	res, err := e.CheckUpdates(ctx, Input{RepoID: r.ID, RepoPath: lease.Path, PusherID: pusherID, Protocol: "dry-run",
		Updates: []Update{{Ref: ref, Old: oldID, New: newID}}})
	if err != nil {
		return Result{}, err
	}
	return res[0], nil
}
