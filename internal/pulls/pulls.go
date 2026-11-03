package pulls

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/issues"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// CreateInput opens a pull request.
type CreateInput struct {
	Title    string
	Body     string
	Head     string // branch of HeadRepo
	HeadRepo string // owner/name; empty = the base repository
	Base     string // branch of the base repository
	Draft    bool
}

// ListOptions filters List.
type ListOptions struct {
	State    string // open (default), closed, merged or all
	Base     string
	Head     string
	HeadRepo string // owner/name
	Author   string
	// ReviewRequested keeps the pull requests with a pending review
	// request for this username.
	ReviewRequested string
	Q               string // full-text search on title and body
	Sort            string // created (default) or updated
	Offset          int
	Limit           int
}

// UpdateInput edits a pull request; nil fields are left alone.
type UpdateInput struct {
	Title *string
	Body  *string
	State *string // open or closed
	Base  *string
	Draft *bool
}

// ---- reading ----

// load returns the issue and pull request rows of number in r.
func (s *Service) load(ctx context.Context, q *dbgen.Queries, repoID uuid.UUID, number int64) (dbgen.Issue, dbgen.PullRequest, error) {
	p, err := q.GetPullRequest(ctx, dbgen.GetPullRequestParams{RepoID: repoID, Number: number})
	if err != nil {
		return dbgen.Issue{}, dbgen.PullRequest{}, notFound(err, ErrNotFound, "get pull request")
	}
	i, err := q.GetIssueByID(ctx, p.IssueID)
	if err != nil {
		return dbgen.Issue{}, dbgen.PullRequest{}, notFound(err, ErrNotFound, "get pull request issue")
	}
	return i, p, nil
}

// repoNames resolves repository ids to owner/name.
type repoNames struct {
	s     *Service
	cache map[uuid.UUID]string
}

func (s *Service) newRepoNames(known ...repos.Repo) *repoNames {
	n := &repoNames{s: s, cache: map[uuid.UUID]string{}}
	for _, r := range known {
		n.cache[r.ID] = r.FullName()
	}
	return n
}

func (n *repoNames) get(ctx context.Context, id uuid.NullUUID) string {
	if !id.Valid {
		return ""
	}
	if v, ok := n.cache[id.UUID]; ok {
		return v
	}
	r, err := n.s.o.Repos.GetByID(ctx, systemPrincipal, id.UUID)
	v := ""
	if err == nil {
		v = r.FullName()
	}
	n.cache[id.UUID] = v
	return v
}

// hydrate builds Pulls from issue rows and their pull request rows.
func (s *Service) hydrate(ctx context.Context, viewer uuid.UUID, base repos.Repo, rows []dbgen.Issue, prs map[uuid.UUID]dbgen.PullRequest) ([]Pull, error) {
	iss, err := s.o.Issues.Hydrate(ctx, viewer, rows)
	if err != nil {
		return nil, fmt.Errorf("hydrate issues: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(rows))
	var users []uuid.UUID
	for _, r := range rows {
		ids = append(ids, r.ID)
		p := prs[r.ID]
		users = append(users, p.MergedBy.UUID, p.AutoMergeBy.UUID)
	}
	reqs, err := s.q.PullReviewRequestsOf(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("review requests: %w", err)
	}
	for _, r := range reqs {
		users = append(users, r.ReviewerID)
	}
	names, err := s.names(ctx, users)
	if err != nil {
		return nil, err
	}
	reqBy := map[uuid.UUID][]string{}
	for _, r := range reqs {
		if n := names[r.ReviewerID]; n != "" {
			reqBy[r.PullID] = append(reqBy[r.PullID], n)
		}
	}
	rn := s.newRepoNames(base)
	out := make([]Pull, 0, len(rows))
	for k, r := range rows {
		p, ok := prs[r.ID]
		if !ok {
			continue
		}
		pl := Pull{
			Issue: iss[k], State: stateOf(r, p), Draft: p.Draft,
			Head:      Ref{RepoID: p.HeadRepoID.UUID, Repo: rn.get(ctx, p.HeadRepoID), Ref: p.HeadRef, SHA: p.HeadSha},
			Base:      Ref{RepoID: p.RepoID, Repo: base.FullName(), Ref: p.BaseRef, SHA: p.BaseSha},
			MergeBase: p.MergeBaseSha, Mergeable: p.MergeableState, ConflictFiles: p.ConflictFiles,
			MergedAt: tsPtr(p.MergedAt), MergedBy: names[p.MergedBy.UUID], MergeCommit: p.MergeCommitSha,
			MergeMethod: p.MergeMethod, RequestedReviewers: reqBy[r.ID],
		}
		if pl.ConflictFiles == nil {
			pl.ConflictFiles = []string{}
		}
		if pl.RequestedReviewers == nil {
			pl.RequestedReviewers = []string{}
		}
		if p.AutoMergeMethod != "" {
			pl.AutoMerge = &AutoMerge{Method: p.AutoMergeMethod, EnabledBy: names[p.AutoMergeBy.UUID],
				Title: p.AutoMergeTitle, Message: p.AutoMergeMessage, DeleteBranch: p.AutoMergeDelete}
		}
		out = append(out, pl)
	}
	return out, nil
}

func (s *Service) one(ctx context.Context, viewer uuid.UUID, base repos.Repo, i dbgen.Issue, p dbgen.PullRequest) (Pull, error) {
	out, err := s.hydrate(ctx, viewer, base, []dbgen.Issue{i}, map[uuid.UUID]dbgen.PullRequest{i.ID: p})
	if err != nil {
		return Pull{}, err
	}
	if len(out) == 0 {
		return Pull{}, ErrNotFound
	}
	return out[0], nil
}

// Get returns pull request number of owner/name.
func (s *Service) Get(ctx context.Context, who auth.Principal, owner, name string, number int64) (Pull, error) {
	r, err := s.o.Repos.Get(ctx, who, owner, name)
	if err != nil {
		return Pull{}, err
	}
	i, p, err := s.load(ctx, s.q, r.ID, number)
	if err != nil {
		return Pull{}, err
	}
	return s.one(ctx, who.UserID, r, i, p)
}

// List returns the pull requests of owner/name.
func (s *Service) List(ctx context.Context, who auth.Principal, owner, name string, o ListOptions) ([]Pull, error) {
	r, err := s.o.Repos.Get(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	arg := dbgen.SearchPullRequestsParams{RepoID: r.ID, BaseRef: o.Base, HeadRef: o.Head}
	switch o.State {
	case "", StateOpen:
		arg.State = StateOpen
	case StateClosed, StateMerged, "all":
		arg.State = o.State
	default:
		return nil, invalid("state must be open, closed, merged or all")
	}
	switch o.Sort {
	case "", "created":
		arg.Sort = "created"
	case "updated":
		arg.Sort = "updated"
	default:
		return nil, invalid("sort must be created or updated")
	}
	if o.HeadRepo != "" {
		ho, hn, ok := strings.Cut(o.HeadRepo, "/")
		if !ok {
			return nil, invalid("headRepo must be owner/name")
		}
		hr, err := s.o.Repos.Get(ctx, who, ho, hn)
		if errors.Is(err, repos.ErrNotFound) {
			return []Pull{}, nil
		}
		if err != nil {
			return nil, err
		}
		arg.ByHeadRepo, arg.HeadRepoID = true, hr.ID
	}
	if o.Author != "" {
		us, err := s.q.IssueUsersByNames(ctx, []string{strings.ToLower(o.Author)})
		if err != nil {
			return nil, fmt.Errorf("resolve author: %w", err)
		}
		if len(us) == 0 {
			return []Pull{}, nil
		}
		arg.ByAuthor, arg.AuthorID = true, us[0].ID
	}
	if o.ReviewRequested != "" {
		us, err := s.q.IssueUsersByNames(ctx, []string{strings.ToLower(o.ReviewRequested)})
		if err != nil {
			return nil, fmt.Errorf("resolve reviewer: %w", err)
		}
		if len(us) == 0 {
			return []Pull{}, nil
		}
		arg.ByReviewer, arg.ReviewerID = true, us[0].ID
	}
	arg.Query = strings.TrimSpace(o.Q)
	arg.Off, arg.Lim = pageArgs(o.Offset, o.Limit)
	rows, err := s.q.SearchPullRequests(ctx, arg)
	if err != nil {
		return nil, fmt.Errorf("search pull requests: %w", err)
	}
	ids := make([]uuid.UUID, len(rows))
	for k, i := range rows {
		ids[k] = i.ID
	}
	prs := map[uuid.UUID]dbgen.PullRequest{}
	if len(ids) > 0 {
		ps, err := s.q.GetPullRequestsByIDs(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("get pull requests: %w", err)
		}
		for _, p := range ps {
			prs[p.IssueID] = p
		}
	}
	return s.hydrate(ctx, who.UserID, r, rows, prs)
}

// ---- create ----

// forkRoots returns repo and its fork ancestors, nearest first.
func (s *Service) forkChain(ctx context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	chain := []uuid.UUID{id}
	cur := id
	for range maxForkDepth {
		f, err := s.q.GetRepoFork(ctx, cur)
		if errors.Is(err, pgx.ErrNoRows) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("get fork: %w", err)
		}
		if !f.ParentID.Valid {
			break
		}
		cur = f.ParentID.UUID
		chain = append(chain, cur)
	}
	return chain, nil
}

// related reports whether two repositories belong to the same fork network
// (one is an ancestor of the other, or they share one).
func (s *Service) related(ctx context.Context, a, b uuid.UUID) (bool, error) {
	ca, err := s.forkChain(ctx, a)
	if err != nil {
		return false, err
	}
	cb, err := s.forkChain(ctx, b)
	if err != nil {
		return false, err
	}
	in := map[uuid.UUID]bool{}
	for _, id := range ca {
		in[id] = true
	}
	for _, id := range cb {
		if in[id] {
			return true, nil
		}
	}
	return false, nil
}

// Create opens a pull request on owner/name.
func (s *Service) Create(ctx context.Context, who auth.Principal, owner, name string, in CreateInput) (Pull, error) {
	base, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return Pull{}, err
	}
	title, err := checkTitle("title", in.Title, MaxTitle)
	if err != nil {
		return Pull{}, err
	}
	if err := checkText("body", in.Body, MaxBody); err != nil {
		return Pull{}, err
	}
	in.Head, in.Base = strings.TrimSpace(in.Head), strings.TrimSpace(in.Base)
	if in.Base == "" {
		in.Base = base.DefaultBranch
	}
	for _, b := range []string{in.Head, in.Base} {
		if err := git.ValidateBranchName(b); err != nil {
			return Pull{}, invalid("invalid branch name %q", b)
		}
	}
	head := base
	if in.HeadRepo != "" && !strings.EqualFold(in.HeadRepo, base.FullName()) {
		ho, hn, ok := strings.Cut(in.HeadRepo, "/")
		if !ok {
			return Pull{}, invalid("headRepo must be owner/name")
		}
		head, err = s.o.Repos.Get(ctx, who, ho, hn)
		if err != nil {
			return Pull{}, err
		}
		ok, err = s.related(ctx, base.ID, head.ID)
		if err != nil {
			return Pull{}, err
		}
		if !ok {
			return Pull{}, invalid("%s is not a fork of %s", head.FullName(), base.FullName())
		}
	}
	if head.ID == base.ID && in.Head == in.Base {
		return Pull{}, invalid("head and base are the same branch")
	}
	exists, err := s.q.OpenPullExists(ctx, dbgen.OpenPullExistsParams{RepoID: base.ID, HeadRepoID: nullID(head.ID), HeadRef: in.Head, BaseRef: in.Base})
	if err != nil {
		return Pull{}, fmt.Errorf("check open pull request: %w", err)
	}
	if exists {
		return Pull{}, fmt.Errorf("%w: a pull request for %s:%s into %s is already open", ErrConflict, head.FullName(), in.Head, in.Base)
	}

	bl, err := s.acquire(ctx, base.ID, gitstore.Write)
	if err != nil {
		return Pull{}, err
	}
	defer bl.Release()
	headSHA, err := s.headTip(ctx, bl, base.ID, head.ID, in.Head)
	if err != nil {
		return Pull{}, err
	}
	if headSHA == "" {
		return Pull{}, invalid("branch %q does not exist in %s", in.Head, head.FullName())
	}
	baseSHA, err := git.ResolveRef(ctx, bl.Path, nil, "refs/heads/"+in.Base)
	if err != nil {
		return Pull{}, fmt.Errorf("resolve base: %w", err)
	}
	if baseSHA == "" {
		return Pull{}, invalid("branch %q does not exist in %s", in.Base, base.FullName())
	}
	mb, err := git.MergeBase(ctx, bl.Path, nil, baseSHA, headSHA)
	if err != nil {
		return Pull{}, fmt.Errorf("merge base: %w", err)
	}
	if mb == "" {
		return Pull{}, invalid("%s and %s have no common history", in.Base, in.Head)
	}
	if contained, err := git.IsAncestor(ctx, bl.Path, nil, headSHA, baseSHA); err != nil {
		return Pull{}, fmt.Errorf("compare branches: %w", err)
	} else if contained {
		return Pull{}, invalid("%s is already contained in %s: nothing to merge", in.Head, in.Base)
	}

	var row dbgen.Issue
	var pr dbgen.PullRequest
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		n, err := issues.NextNumber(ctx, q, base.ID)
		if err != nil {
			return err
		}
		id, err := newID()
		if err != nil {
			return err
		}
		row, err = q.InsertIssue(ctx, dbgen.InsertIssueParams{ID: id, RepoID: base.ID, Number: n, Kind: issues.KindPull,
			Title: title, Body: in.Body, AuthorID: nullID(who.UserID)})
		if err != nil {
			return fmt.Errorf("insert issue: %w", err)
		}
		pr, err = q.InsertPullRequest(ctx, dbgen.InsertPullRequestParams{IssueID: id, RepoID: base.ID, Number: n,
			HeadRepoID: nullID(head.ID), HeadRef: in.Head, HeadSha: headSHA, BaseRef: in.Base, BaseSha: baseSHA,
			MergeBaseSha: mb, Draft: in.Draft})
		if err != nil {
			if isUnique(err) {
				return fmt.Errorf("%w: a pull request for these branches is already open", ErrConflict)
			}
			return fmt.Errorf("insert pull request: %w", err)
		}
		if err := s.recordRefs(ctx, q, base, who, row, uuid.Nil, in.Body); err != nil {
			return err
		}
		return s.emitPull(ctx, q, "opened", who.UserID, row, pr, nil)
	})
	if err != nil {
		return Pull{}, err
	}
	s.log.InfoContext(ctx, "pull request opened", "repo", base.ID, "number", pr.Number, "user", who.UserID)
	if np, err := s.refresh(ctx, bl, who.UserID, pr); err != nil {
		s.log.WarnContext(ctx, "pull request test merge failed", "repo", base.ID, "number", pr.Number, "err", err)
	} else {
		pr = np
	}
	bl.Release()
	return s.one(ctx, who.UserID, base, row, pr)
}

// headTip resolves branch of the head repository into the base repository
// held by bl, fetching its objects from a fork. "" means the branch does
// not exist.
func (s *Service) headTip(ctx context.Context, bl *gitstore.Lease, baseID, headID uuid.UUID, branch string) (string, error) {
	ref := "refs/heads/" + branch
	if headID == baseID {
		sha, err := git.ResolveRef(ctx, bl.Path, nil, ref)
		if err != nil {
			return "", fmt.Errorf("resolve head: %w", err)
		}
		return sha, nil
	}
	hl, err := s.acquire(ctx, headID, gitstore.Read)
	if err != nil {
		return "", err
	}
	defer hl.Release()
	sha, err := git.ResolveRef(ctx, hl.Path, nil, ref)
	if err != nil {
		return "", fmt.Errorf("resolve head: %w", err)
	}
	if sha == "" {
		return "", nil
	}
	// Objects only: the refs/pull/<n>/head update records them in the
	// journal under a hook session.
	if err := git.FetchBranches(ctx, bl.Path, nil, hl.Path, ref); err != nil {
		return "", fmt.Errorf("fetch head from fork: %w", err)
	}
	return sha, nil
}

// ---- update ----

// Update edits the title, body, state, base or draft flag of a pull request.
func (s *Service) Update(ctx context.Context, who auth.Principal, owner, name string, number int64, in UpdateInput) (Pull, error) {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return Pull{}, err
	}
	i, p, err := s.load(ctx, s.q, r.ID, number)
	if err != nil {
		return Pull{}, err
	}
	author := i.AuthorID.Valid && i.AuthorID.UUID == who.UserID
	if !author && !canTriage(r) {
		return Pull{}, ErrForbidden
	}
	if in.Base != nil && *in.Base != p.BaseRef {
		if p.MergedAt.Valid || i.State != issues.StateOpen {
			return Pull{}, fmt.Errorf("%w: only an open pull request can change its base", ErrConflict)
		}
		if !author && !canWrite(r) {
			return Pull{}, ErrForbidden
		}
		if err := s.retarget(ctx, who, r, i, p, strings.TrimSpace(*in.Base)); err != nil {
			return Pull{}, err
		}
	}
	if in.Title != nil || in.Body != nil || in.State != nil || in.Draft != nil {
		err = s.tx(ctx, func(q *dbgen.Queries) error {
			row, err := q.GetIssueForUpdate(ctx, dbgen.GetIssueForUpdateParams{RepoID: r.ID, Number: number})
			if err != nil {
				return notFound(err, ErrNotFound, "lock pull request")
			}
			pr, err := q.GetPullRequestForUpdate(ctx, dbgen.GetPullRequestForUpdateParams{RepoID: r.ID, Number: number})
			if err != nil {
				return notFound(err, ErrNotFound, "lock pull request")
			}
			before := row.Body
			if err := s.updateContent(ctx, q, who.UserID, &row, pr, in.Title, in.Body); err != nil {
				return err
			}
			if row.Body != before {
				if err := s.recordRefs(ctx, q, r, who, row, uuid.Nil, row.Body); err != nil {
					return err
				}
			}
			if in.Draft != nil && *in.Draft != pr.Draft {
				if pr.MergedAt.Valid {
					return fmt.Errorf("%w: the pull request is merged", ErrConflict)
				}
				if pr, err = q.SetPullDraft(ctx, dbgen.SetPullDraftParams{Draft: *in.Draft, IssueID: pr.IssueID}); err != nil {
					return fmt.Errorf("set draft: %w", err)
				}
				action := "ready_for_review"
				if pr.Draft {
					action = "converted_to_draft"
				}
				if err := s.timeline(ctx, q, row, who.UserID, action, nil); err != nil {
					return err
				}
				if err := s.emitPull(ctx, q, action, who.UserID, row, pr, nil); err != nil {
					return err
				}
			}
			if in.State != nil {
				return s.setState(ctx, q, who.UserID, &row, pr, *in.State)
			}
			return nil
		})
		if err != nil {
			return Pull{}, err
		}
	}
	i, p, err = s.load(ctx, s.q, r.ID, number)
	if err != nil {
		return Pull{}, err
	}
	return s.one(ctx, who.UserID, r, i, p)
}

func (s *Service) updateContent(ctx context.Context, q *dbgen.Queries, actor uuid.UUID, row *dbgen.Issue, pr dbgen.PullRequest, title, body *string) error {
	cur := *row
	nt, nb := cur.Title, cur.Body
	if title != nil {
		v, err := checkTitle("title", *title, MaxTitle)
		if err != nil {
			return err
		}
		nt = v
	}
	if body != nil {
		if err := checkText("body", *body, MaxBody); err != nil {
			return err
		}
		nb = *body
	}
	if nt == cur.Title && nb == cur.Body {
		return nil
	}
	if nb != cur.Body {
		id, err := newID()
		if err != nil {
			return err
		}
		if err := q.InsertIssueEdit(ctx, dbgen.InsertIssueEditParams{ID: id, IssueID: cur.ID, EditorID: nullID(actor), Body: cur.Body}); err != nil {
			return fmt.Errorf("insert edit: %w", err)
		}
	}
	updated, err := q.UpdateIssueContent(ctx, dbgen.UpdateIssueContentParams{ID: cur.ID, Title: nt, Body: nb,
		EditedAt: ts(s.o.Now()), EditedBy: nullID(actor)})
	if err != nil {
		return fmt.Errorf("update pull request: %w", err)
	}
	*row = updated
	if nt != cur.Title {
		if err := s.timeline(ctx, q, updated, actor, "renamed", map[string]any{"from": cur.Title, "to": nt}); err != nil {
			return err
		}
	}
	return s.emitPull(ctx, q, "edited", actor, updated, pr, nil)
}

// setState closes or reopens a pull request (not merged).
func (s *Service) setState(ctx context.Context, q *dbgen.Queries, actor uuid.UUID, row *dbgen.Issue, pr dbgen.PullRequest, state string) error {
	cur := *row
	if pr.MergedAt.Valid {
		if state == StateOpen {
			return fmt.Errorf("%w: a merged pull request cannot be reopened", ErrConflict)
		}
		return nil
	}
	var arg dbgen.SetIssueStateParams
	switch state {
	case StateClosed:
		if cur.State == issues.StateClosed {
			return nil
		}
		arg = dbgen.SetIssueStateParams{ID: cur.ID, State: issues.StateClosed, StateReason: issues.ReasonCompleted,
			ClosedAt: ts(s.o.Now()), ClosedBy: nullID(actor)}
	case StateOpen:
		if cur.State == issues.StateOpen {
			return nil
		}
		ok, err := q.OpenPullExists(ctx, dbgen.OpenPullExistsParams{RepoID: pr.RepoID, HeadRepoID: pr.HeadRepoID, HeadRef: pr.HeadRef, BaseRef: pr.BaseRef})
		if err != nil {
			return fmt.Errorf("check open pull request: %w", err)
		}
		if ok {
			return fmt.Errorf("%w: another pull request for these branches is open", ErrConflict)
		}
		if !pr.HeadRepoID.Valid {
			return fmt.Errorf("%w: the head repository was deleted", ErrConflict)
		}
		arg = dbgen.SetIssueStateParams{ID: cur.ID, State: issues.StateOpen, StateReason: issues.ReasonReopened}
	default:
		return invalid("state must be open or closed")
	}
	updated, err := q.SetIssueState(ctx, arg)
	if err != nil {
		return fmt.Errorf("set pull request state: %w", err)
	}
	*row = updated
	name := "reopened"
	if state == StateClosed {
		name = "closed"
		if _, err := q.SetPullAutoMerge(ctx, dbgen.SetPullAutoMergeParams{IssueID: pr.IssueID}); err != nil {
			return fmt.Errorf("clear auto-merge: %w", err)
		}
	}
	if err := s.timeline(ctx, q, updated, actor, name, nil); err != nil {
		return err
	}
	return s.emitPull(ctx, q, name, actor, updated, pr, nil)
}

// retarget changes the base branch of an open pull request.
func (s *Service) retarget(ctx context.Context, who auth.Principal, r repos.Repo, i dbgen.Issue, p dbgen.PullRequest, branch string) error {
	if err := git.ValidateBranchName(branch); err != nil {
		return invalid("invalid branch name %q", branch)
	}
	if p.HeadRepoID.UUID == r.ID && branch == p.HeadRef {
		return invalid("head and base are the same branch")
	}
	ok, err := s.q.OpenPullExists(ctx, dbgen.OpenPullExistsParams{RepoID: r.ID, HeadRepoID: p.HeadRepoID, HeadRef: p.HeadRef, BaseRef: branch})
	if err != nil {
		return fmt.Errorf("check open pull request: %w", err)
	}
	if ok {
		return fmt.Errorf("%w: a pull request for these branches is already open", ErrConflict)
	}
	l, err := s.acquire(ctx, r.ID, gitstore.Write)
	if err != nil {
		return err
	}
	defer l.Release()
	sha, err := git.ResolveRef(ctx, l.Path, nil, "refs/heads/"+branch)
	if err != nil {
		return fmt.Errorf("resolve base: %w", err)
	}
	if sha == "" {
		return invalid("branch %q does not exist", branch)
	}
	before := p.BaseRef
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		np, err := q.SetPullBase(ctx, dbgen.SetPullBaseParams{BaseRef: branch, BaseSha: sha, IssueID: p.IssueID})
		if err != nil {
			return fmt.Errorf("set base: %w", err)
		}
		p = np
		if err := s.timeline(ctx, q, i, who.UserID, "base_ref_changed", map[string]any{"from": before, "to": branch}); err != nil {
			return err
		}
		return s.emitPull(ctx, q, "edited", who.UserID, i, np, func(pl *events.PullPayload) { pl.Before = before })
	})
	if err != nil {
		return err
	}
	if _, err := s.refresh(ctx, l, who.UserID, p); err != nil {
		s.log.WarnContext(ctx, "pull request test merge failed", "repo", r.ID, "number", p.Number, "err", err)
	}
	return nil
}

// ---- commits, files, diff ----

// pullRange returns the browse repository and the merge base and head of a
// pull request, read from the base repository (which holds the head
// objects under refs/pull/<n>/head).
func (s *Service) pullRange(ctx context.Context, who auth.Principal, owner, name string, number int64) (gitbrowse.Repo, dbgen.PullRequest, error) {
	r, err := s.o.Repos.Get(ctx, who, owner, name)
	if err != nil {
		return gitbrowse.Repo{}, dbgen.PullRequest{}, err
	}
	_, p, err := s.load(ctx, s.q, r.ID, number)
	if err != nil {
		return gitbrowse.Repo{}, dbgen.PullRequest{}, err
	}
	if p.MergeBaseSha == "" || p.HeadSha == "" {
		return gitbrowse.Repo{}, dbgen.PullRequest{}, fmt.Errorf("%w: the pull request has no common history with its base", ErrConflict)
	}
	return gitRepo(r), p, nil
}

// ListCommits returns the commits of a pull request, oldest first.
func (s *Service) ListCommits(ctx context.Context, who auth.Principal, owner, name string, number int64, offset, limit int) ([]gitbrowse.Commit, bool, error) {
	gr, p, err := s.pullRange(ctx, who, owner, name, number)
	if err != nil {
		return nil, false, err
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > MaxCommitsPage {
		limit = 100
	}
	return s.o.Browse.CommitRange(ctx, gr, p.MergeBaseSha, p.HeadSha, offset, limit)
}

// ListFiles returns the files a pull request changes.
func (s *Service) ListFiles(ctx context.Context, who auth.Principal, owner, name string, number int64) ([]gitbrowse.FileChange, error) {
	gr, p, err := s.pullRange(ctx, who, owner, name, number)
	if err != nil {
		return nil, err
	}
	return s.o.Browse.ChangedFiles(ctx, gr, p.MergeBaseSha, p.HeadSha)
}

// Diff returns the diff of a pull request (merge base to head), limited to
// paths when given.
func (s *Service) Diff(ctx context.Context, who auth.Principal, owner, name string, number int64, paths []string) (gitbrowse.Diff, error) {
	gr, p, err := s.pullRange(ctx, who, owner, name, number)
	if err != nil {
		return gitbrowse.Diff{}, err
	}
	return s.o.Browse.DiffRange(ctx, gr, p.MergeBaseSha, p.HeadSha, paths)
}

// ---- mergeability ----

// Mergeability returns the merge status of a pull request.
func (s *Service) Mergeability(ctx context.Context, who auth.Principal, owner, name string, number int64) (Mergeability, error) {
	r, err := s.o.Repos.Get(ctx, who, owner, name)
	if err != nil {
		return Mergeability{}, err
	}
	i, p, err := s.load(ctx, s.q, r.ID, number)
	if err != nil {
		return Mergeability{}, err
	}
	m := Mergeability{State: stateOf(i, p), Mergeable: p.MergeableState, ConflictFiles: p.ConflictFiles,
		HeadSHA: p.HeadSha, BaseSHA: p.BaseSha, MergeBase: p.MergeBaseSha}
	if m.ConflictFiles == nil {
		m.ConflictFiles = []string{}
	}
	if m.State != StateOpen {
		return m, nil
	}
	if s.o.Protection != nil {
		l, err := s.acquire(ctx, r.ID, gitstore.Read)
		if err != nil {
			return Mergeability{}, err
		}
		st, err := s.o.Protection.PullRequestStatus(ctx, s.prInput(l, i, p))
		l.Release()
		if err != nil {
			return Mergeability{}, fmt.Errorf("protection status: %w", err)
		}
		m.Rule, m.RequiredApprove, m.Approvals = st.Rule, st.RequiredApprovals, st.Approvals
		m.MissingChecks, m.Behind, m.CodeOwners = st.MissingChecks, st.Behind, st.CodeOwners
		m.CodeOwnersFile, m.CodeOwnersErrors, m.Violations = st.CodeOwnersFile, st.CodeOwnersErrors, st.Violations
	}
	m.CanMerge = m.Mergeable == MergeClean && !p.Draft && len(m.Violations) == 0
	return m, nil
}
