package ghapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/issues"
	"github.com/Ervisio/ErvisioRepo/internal/pulls"
)

// ---- objects ----

type ghPullRef struct {
	Label string  `json:"label"`
	Ref   string  `json:"ref"`
	SHA   string  `json:"sha"`
	User  *ghUser `json:"user"`
	Repo  *ghRepo `json:"repo"`
}

type ghHref struct {
	Href string `json:"href"`
}

type ghPull struct {
	URL                string            `json:"url"`
	ID                 int64             `json:"id"`
	NodeID             string            `json:"node_id"`
	HTMLURL            string            `json:"html_url"`
	DiffURL            string            `json:"diff_url"`
	PatchURL           string            `json:"patch_url"`
	IssueURL           string            `json:"issue_url"`
	CommitsURL         string            `json:"commits_url"`
	ReviewCommentsURL  string            `json:"review_comments_url"`
	ReviewCommentURL   string            `json:"review_comment_url"`
	CommentsURL        string            `json:"comments_url"`
	StatusesURL        string            `json:"statuses_url"`
	Number             int64             `json:"number"`
	State              string            `json:"state"`
	Locked             bool              `json:"locked"`
	Title              string            `json:"title"`
	User               *ghUser           `json:"user"`
	Body               *string           `json:"body"`
	Labels             []ghLabel         `json:"labels"`
	Milestone          *ghMilestone      `json:"milestone"`
	ActiveLockReason   *string           `json:"active_lock_reason"`
	CreatedAt          string            `json:"created_at"`
	UpdatedAt          string            `json:"updated_at"`
	ClosedAt           *string           `json:"closed_at"`
	MergedAt           *string           `json:"merged_at"`
	MergeCommitSHA     *string           `json:"merge_commit_sha"`
	Assignee           *ghUser           `json:"assignee"`
	Assignees          []*ghUser         `json:"assignees"`
	RequestedReviewers []*ghUser         `json:"requested_reviewers"`
	RequestedTeams     []any             `json:"requested_teams"`
	Head               ghPullRef         `json:"head"`
	Base               ghPullRef         `json:"base"`
	Links              map[string]ghHref `json:"_links"`
	AuthorAssociation  string            `json:"author_association"`
	AutoMerge          map[string]any    `json:"auto_merge"`
	Draft              bool              `json:"draft"`
	Merged             bool              `json:"merged"`
	Mergeable          *bool             `json:"mergeable"`
	Rebaseable         *bool             `json:"rebaseable"`
	MergeableState     string            `json:"mergeable_state"`
	MergedBy           *ghUser           `json:"merged_by"`
	Comments           int               `json:"comments"`
	// The counters below are computed only for a single pull request.
	ReviewComments      int  `json:"review_comments"`
	MaintainerCanModify bool `json:"maintainer_can_modify"`
	Commits             int  `json:"commits"`
	Additions           int  `json:"additions"`
	Deletions           int  `json:"deletions"`
	ChangedFiles        int  `json:"changed_files"`
}

// repoByName answers the repository owner/name, nil when it is gone or
// not visible to the caller.
func (c *call) repoByName(full string) *ghRepo {
	o, n, ok := strings.Cut(full, "/")
	if !ok {
		return nil
	}
	for _, r := range c.repos {
		if strings.EqualFold(r.FullName, full) {
			return r
		}
	}
	r, err := c.s.d.Repos.Get(c.ctx, c.who, o, n)
	if err != nil {
		return nil
	}
	return c.repoObj(r)
}

func (c *call) pullRef(r pulls.Ref) ghPullRef {
	out := ghPullRef{Ref: r.Ref, SHA: r.SHA, Label: r.Ref}
	if r.Repo == "" {
		return out
	}
	owner, _, _ := strings.Cut(r.Repo, "/")
	out.Label = owner + ":" + r.Ref
	out.User = c.userByName(owner)
	out.Repo = c.repoByName(r.Repo)
	return out
}

func (c *call) prefetchPulls(list []pulls.Pull) {
	is := make([]issues.Issue, len(list))
	var names []string
	for i, p := range list {
		is[i] = p.Issue
		names = append(names, p.RequestedReviewers...)
		names = append(names, p.MergedBy)
		if o, _, ok := strings.Cut(p.Head.Repo, "/"); ok {
			names = append(names, o)
		}
		if o, _, ok := strings.Cut(p.Base.Repo, "/"); ok {
			names = append(names, o)
		}
	}
	c.prefetchIssues(is)
	c.ids.prefetchUsers(names...)
}

func boolp(b bool) *bool { return &b }

func (c *call) pullObj(p pulls.Pull) ghPull {
	i := p.Issue
	num := strconv.FormatInt(i.Number, 10)
	self := c.repoAPI() + "/pulls/" + num
	html := c.repoHTML() + "/pulls/" + num
	issue := c.repoAPI() + "/issues/" + num
	out := ghPull{
		URL: self, ID: c.ids.of(i.ID), NodeID: nodeID("PullRequest", i.ID), HTMLURL: html,
		DiffURL: html + ".diff", PatchURL: html + ".patch", IssueURL: issue, CommitsURL: self + "/commits",
		ReviewCommentsURL: self + "/comments", ReviewCommentURL: c.repoAPI() + "/pulls/comments{/number}",
		CommentsURL: issue + "/comments", StatusesURL: c.repoAPI() + "/statuses/" + p.Head.SHA,
		Number: i.Number, State: i.State, Locked: i.Locked, Title: i.Title, Body: strp(i.Body),
		Labels: c.labelList(i.Labels), Milestone: c.milestoneObj(i.Milestone), ActiveLockReason: strp(i.LockReason),
		CreatedAt: ts(i.CreatedAt), UpdatedAt: ts(i.UpdatedAt), ClosedAt: tsp(i.ClosedAt), MergedAt: tsp(p.MergedAt),
		MergeCommitSHA: strp(p.MergeCommit), Assignees: c.usersByName(i.Assignees),
		RequestedReviewers: c.usersByName(p.RequestedReviewers), RequestedTeams: []any{},
		Head: c.pullRef(p.Head), Base: c.pullRef(p.Base), AuthorAssociation: "NONE", Draft: p.Draft,
		Merged: p.State == pulls.StateMerged, MergedBy: c.userByName(p.MergedBy), Comments: i.CommentCount,
		Links: map[string]ghHref{
			"self": {self}, "html": {html}, "issue": {issue}, "comments": {issue + "/comments"},
			"review_comments": {self + "/comments"}, "review_comment": {c.repoAPI() + "/pulls/comments{/number}"},
			"commits": {self + "/commits"}, "statuses": {c.repoAPI() + "/statuses/" + p.Head.SHA},
		},
	}
	if out.State == pulls.StateMerged {
		out.State = pulls.StateClosed
	}
	if i.Author != "" {
		out.User = c.userObj(i.AuthorID, "user", i.Author)
	}
	if len(out.Assignees) > 0 {
		out.Assignee = out.Assignees[0]
	}
	switch p.Mergeable {
	case pulls.MergeClean:
		out.Mergeable, out.Rebaseable, out.MergeableState = boolp(true), boolp(true), "clean"
	case pulls.MergeConflict:
		out.Mergeable, out.Rebaseable, out.MergeableState = boolp(false), boolp(false), "dirty"
	default:
		out.MergeableState = "unknown"
	}
	if p.Draft && out.MergeableState != "dirty" {
		out.MergeableState = "draft"
	}
	if a := p.AutoMerge; a != nil {
		out.AutoMerge = map[string]any{"enabled_by": c.userByName(a.EnabledBy), "merge_method": a.Method,
			"commit_title": a.Title, "commit_message": a.Message}
	}
	return out
}

func (c *call) pullList(list []pulls.Pull) []ghPull {
	c.prefetchPulls(list)
	out := make([]ghPull, len(list))
	for i, p := range list {
		out[i] = c.pullObj(p)
	}
	return out
}

// onePull answers a single pull request with its counters.
func (c *call) onePull(status int, p pulls.Pull) error {
	c.prefetchPulls([]pulls.Pull{p})
	out := c.pullObj(p)
	n := p.Issue.Number
	if files, err := c.s.d.Pulls.ListFiles(c.ctx, c.who, c.owner, c.repo, n); err == nil {
		out.ChangedFiles = len(files)
		for _, f := range files {
			out.Additions += f.Additions
			out.Deletions += f.Deletions
		}
	}
	if cs, _, err := c.s.d.Pulls.ListCommits(c.ctx, c.who, c.owner, c.repo, n, 0, maxPerPage); err == nil {
		out.Commits = len(cs)
	}
	if cm, err := c.s.d.Pulls.ListComments(c.ctx, c.who, c.owner, c.repo, n, uuid.Nil, 0, maxPerPage); err == nil {
		out.ReviewComments = len(cm)
	}
	if status == http.StatusCreated {
		c.w.Header().Set("Location", out.URL)
	}
	return c.ok(status, out)
}

// ---- pull requests ----

func (c *call) listPulls() error {
	if err := c.read(); err != nil {
		return err
	}
	p := c.pager()
	o := pulls.ListOptions{State: c.query("state"), Base: c.query("base"), Offset: p.offset(), Limit: p.limit()}
	if o.State == "" {
		o.State = pulls.StateOpen
	}
	if o.State != pulls.StateOpen && o.State != pulls.StateClosed && o.State != "all" {
		return errf(http.StatusUnprocessableEntity, "Validation Failed: state must be open, closed or all")
	}
	if h := c.query("head"); h != "" {
		owner, ref, ok := strings.Cut(h, ":")
		if !ok {
			ref, owner = owner, c.owner
		}
		o.Head = ref
		if !strings.EqualFold(owner, c.owner) {
			o.HeadRepo = owner + "/" + c.repo
		}
	}
	if c.query("sort") == "updated" {
		o.Sort = "updated"
	}
	list, err := c.s.d.Pulls.List(c.ctx, c.who, c.owner, c.repo, o)
	if err != nil {
		return err
	}
	list, more := cut(p, list)
	c.link(p, more)
	return c.ok(http.StatusOK, c.pullList(list))
}

func (c *call) getPull() error {
	if err := c.read(); err != nil {
		return err
	}
	n, err := c.number()
	if err != nil {
		return err
	}
	p, err := c.s.d.Pulls.Get(c.ctx, c.who, c.owner, c.repo, n)
	if err != nil {
		return err
	}
	return c.onePull(http.StatusOK, p)
}

type pullBody struct {
	Title               *string `json:"title"`
	Body                *string `json:"body"`
	Head                string  `json:"head"`
	HeadRepo            string  `json:"head_repo"`
	Base                *string `json:"base"`
	Draft               *bool   `json:"draft"`
	State               *string `json:"state"`
	Issue               *int64  `json:"issue"`
	MaintainerCanModify *bool   `json:"maintainer_can_modify"`
}

func (c *call) createPull() error {
	if err := c.write(); err != nil {
		return err
	}
	var b pullBody
	if err := c.decode(&b); err != nil {
		return err
	}
	if b.Issue != nil {
		return errf(http.StatusUnprocessableEntity, "Converting an issue into a pull request is not supported")
	}
	if b.Head == "" || b.Base == nil {
		return errf(http.StatusUnprocessableEntity, "Validation Failed: head and base are required")
	}
	in := pulls.CreateInput{Head: b.Head, HeadRepo: b.HeadRepo, Base: *b.Base, Draft: b.Draft != nil && *b.Draft}
	if b.Title != nil {
		in.Title = *b.Title
	}
	if b.Body != nil {
		in.Body = *b.Body
	}
	// head is "owner:branch" for a fork; the fork keeps the base name
	// unless head_repo says otherwise.
	if owner, ref, ok := strings.Cut(b.Head, ":"); ok {
		in.Head = ref
		if in.HeadRepo == "" && !strings.EqualFold(owner, c.owner) {
			in.HeadRepo = owner + "/" + c.repo
		}
	}
	p, err := c.s.d.Pulls.Create(c.ctx, c.who, c.owner, c.repo, in)
	if err != nil {
		return err
	}
	return c.onePull(http.StatusCreated, p)
}

func (c *call) updatePull() error {
	if err := c.write(); err != nil {
		return err
	}
	n, err := c.number()
	if err != nil {
		return err
	}
	var b pullBody
	if err := c.decode(&b); err != nil {
		return err
	}
	p, err := c.s.d.Pulls.Update(c.ctx, c.who, c.owner, c.repo, n,
		pulls.UpdateInput{Title: b.Title, Body: b.Body, State: b.State, Base: b.Base, Draft: b.Draft})
	if err != nil {
		return err
	}
	return c.onePull(http.StatusOK, p)
}

func (c *call) listPullCommits() error {
	if err := c.read(); err != nil {
		return err
	}
	n, err := c.number()
	if err != nil {
		return err
	}
	p := c.pager()
	list, more, err := c.s.d.Pulls.ListCommits(c.ctx, c.who, c.owner, c.repo, n, p.offset(), p.per)
	if err != nil {
		return err
	}
	out := make([]ghCommit, len(list))
	for i, cm := range list {
		out[i] = c.commitObj(cm)
	}
	c.link(p, more)
	return c.ok(http.StatusOK, out)
}

func (c *call) listPullFiles() error {
	if err := c.read(); err != nil {
		return err
	}
	n, err := c.number()
	if err != nil {
		return err
	}
	pr, err := c.s.d.Pulls.Get(c.ctx, c.who, c.owner, c.repo, n)
	if err != nil {
		return err
	}
	files, err := c.s.d.Pulls.ListFiles(c.ctx, c.who, c.owner, c.repo, n)
	if err != nil {
		return err
	}
	p := c.pager()
	page, more := slice(p, files)
	out := make([]ghFile, len(page))
	for i, f := range page {
		// The blob ids are not part of the change list.
		out[i] = c.fileObj(pr.Head.SHA, "", f)
	}
	c.link(p, more)
	return c.ok(http.StatusOK, out)
}

func (c *call) checkMerged() error {
	if err := c.read(); err != nil {
		return err
	}
	n, err := c.number()
	if err != nil {
		return err
	}
	p, err := c.s.d.Pulls.Get(c.ctx, c.who, c.owner, c.repo, n)
	if err != nil {
		return err
	}
	if p.State != pulls.StateMerged {
		return errNotFound
	}
	return c.noContent()
}

type mergeBody struct {
	CommitTitle   string `json:"commit_title"`
	CommitMessage string `json:"commit_message"`
	SHA           string `json:"sha"`
	MergeMethod   string `json:"merge_method"`
}

func (c *call) mergePull() error {
	if err := c.write(); err != nil {
		return err
	}
	n, err := c.number()
	if err != nil {
		return err
	}
	var b mergeBody
	if err := c.decode(&b); err != nil {
		return err
	}
	sha, err := c.s.d.Pulls.Merge(c.ctx, c.who, c.owner, c.repo, n,
		pulls.MergeInput{Method: b.MergeMethod, Title: b.CommitTitle, Message: b.CommitMessage, SHA: b.SHA})
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, map[string]any{"sha": sha, "merged": true, "message": "Pull Request successfully merged"})
}

// ---- reviews ----

type ghReview struct {
	ID                int64             `json:"id"`
	NodeID            string            `json:"node_id"`
	User              *ghUser           `json:"user"`
	Body              string            `json:"body"`
	State             string            `json:"state"`
	HTMLURL           string            `json:"html_url"`
	PullRequestURL    string            `json:"pull_request_url"`
	AuthorAssociation string            `json:"author_association"`
	Links             map[string]ghHref `json:"_links"`
	SubmittedAt       *string           `json:"submitted_at,omitempty"`
	CommitID          string            `json:"commit_id"`
}

func (c *call) reviewObj(n int64, r pulls.Review) ghReview {
	id := c.ids.of(r.ID)
	pr := c.repoAPI() + "/pulls/" + strconv.FormatInt(n, 10)
	html := c.repoHTML() + "/pulls/" + strconv.FormatInt(n, 10) + "#pullrequestreview-" + strconv.FormatInt(id, 10)
	out := ghReview{ID: id, NodeID: nodeID("PullRequestReview", r.ID), Body: r.Body, State: strings.ToUpper(r.State),
		HTMLURL: html, PullRequestURL: pr, AuthorAssociation: "NONE",
		Links: map[string]ghHref{"html": {html}, "pull_request": {pr}}, SubmittedAt: tsp(r.SubmittedAt), CommitID: r.CommitSHA}
	if r.Reviewer != "" {
		out.User = c.userObj(r.ReviewerID, "user", r.Reviewer)
	}
	return out
}

func (c *call) listReviews() error {
	if err := c.read(); err != nil {
		return err
	}
	n, err := c.number()
	if err != nil {
		return err
	}
	p := c.pager()
	list, err := c.s.d.Pulls.ListReviews(c.ctx, c.who, c.owner, c.repo, n, p.offset(), p.limit())
	if err != nil {
		return err
	}
	list, more := cut(p, list)
	var us []uuid.UUID
	for _, r := range list {
		us = append(us, r.ID, r.ReviewerID)
	}
	c.ids.prefetch(us...)
	out := make([]ghReview, len(list))
	for i, r := range list {
		out[i] = c.reviewObj(n, r)
	}
	c.link(p, more)
	return c.ok(http.StatusOK, out)
}

// reviewParams parses {number} and {id}.
func (c *call) reviewParams() (int64, uuid.UUID, error) {
	n, err := c.number()
	if err != nil {
		return 0, uuid.Nil, err
	}
	id, err := c.idParam()
	return n, id, err
}

func (c *call) getReview() error {
	if err := c.read(); err != nil {
		return err
	}
	n, id, err := c.reviewParams()
	if err != nil {
		return err
	}
	r, err := c.s.d.Pulls.GetReview(c.ctx, c.who, c.owner, c.repo, n, id)
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.reviewObj(n, r))
}

// reviewEvent maps a GitHub review event to the service one.
func reviewEvent(e string) (string, error) {
	switch strings.ToUpper(e) {
	case "":
		return "", nil
	case "APPROVE":
		return pulls.EventApprove, nil
	case "REQUEST_CHANGES":
		return pulls.EventRequestChanges, nil
	case "COMMENT":
		return pulls.EventComment, nil
	}
	return "", errf(http.StatusUnprocessableEntity, "Validation Failed: event must be APPROVE, REQUEST_CHANGES or COMMENT")
}

type draftBody struct {
	Path      string `json:"path"`
	Position  *int   `json:"position"`
	Body      string `json:"body"`
	Line      int    `json:"line"`
	Side      string `json:"side"`
	StartLine int    `json:"start_line"`
}

type reviewBody struct {
	CommitID string      `json:"commit_id"`
	Body     string      `json:"body"`
	Event    string      `json:"event"`
	Comments []draftBody `json:"comments"`
	Message  string      `json:"message"`
}

func (c *call) createReview() error {
	if err := c.write(); err != nil {
		return err
	}
	n, err := c.number()
	if err != nil {
		return err
	}
	var b reviewBody
	if err := c.decode(&b); err != nil {
		return err
	}
	ev, err := reviewEvent(b.Event)
	if err != nil {
		return err
	}
	in := pulls.ReviewInput{Event: ev, Body: b.Body, CommitSHA: b.CommitID}
	for _, d := range b.Comments {
		if d.Line == 0 {
			return errf(http.StatusUnprocessableEntity, "Validation Failed: review comments need line (position is not supported)")
		}
		in.Comments = append(in.Comments, pulls.DraftComment{Path: d.Path, Side: d.Side, Line: d.Line,
			StartLine: d.StartLine, Body: d.Body})
	}
	r, err := c.s.d.Pulls.CreateReview(c.ctx, c.who, c.owner, c.repo, n, in)
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.reviewObj(n, r))
}

func (c *call) submitReview() error {
	if err := c.write(); err != nil {
		return err
	}
	n, id, err := c.reviewParams()
	if err != nil {
		return err
	}
	var b reviewBody
	if err := c.decode(&b); err != nil {
		return err
	}
	ev, err := reviewEvent(b.Event)
	if err != nil {
		return err
	}
	if ev == "" {
		return errf(http.StatusUnprocessableEntity, "Invalid request.\n\n\"event\" wasn't supplied.")
	}
	r, err := c.s.d.Pulls.SubmitReview(c.ctx, c.who, c.owner, c.repo, n, id, ev, b.Body)
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.reviewObj(n, r))
}

func (c *call) deletePendingReview() error {
	if err := c.write(); err != nil {
		return err
	}
	n, id, err := c.reviewParams()
	if err != nil {
		return err
	}
	r, err := c.s.d.Pulls.GetReview(c.ctx, c.who, c.owner, c.repo, n, id)
	if err != nil {
		return err
	}
	if err := c.s.d.Pulls.DeletePendingReview(c.ctx, c.who, c.owner, c.repo, n, id); err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.reviewObj(n, r))
}

func (c *call) dismissReview() error {
	if err := c.write(); err != nil {
		return err
	}
	n, id, err := c.reviewParams()
	if err != nil {
		return err
	}
	var b reviewBody
	if err := c.decode(&b); err != nil {
		return err
	}
	r, err := c.s.d.Pulls.DismissReview(c.ctx, c.who, c.owner, c.repo, n, id, b.Message)
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.reviewObj(n, r))
}

// ---- review comments ----

type ghReviewComment struct {
	URL                 string            `json:"url"`
	PullRequestReviewID *int64            `json:"pull_request_review_id"`
	ID                  int64             `json:"id"`
	NodeID              string            `json:"node_id"`
	DiffHunk            string            `json:"diff_hunk"`
	Path                string            `json:"path"`
	CommitID            string            `json:"commit_id"`
	OriginalCommitID    string            `json:"original_commit_id"`
	InReplyToID         *int64            `json:"in_reply_to_id,omitempty"`
	User                *ghUser           `json:"user"`
	Body                string            `json:"body"`
	CreatedAt           string            `json:"created_at"`
	UpdatedAt           string            `json:"updated_at"`
	HTMLURL             string            `json:"html_url"`
	PullRequestURL      string            `json:"pull_request_url"`
	AuthorAssociation   string            `json:"author_association"`
	Links               map[string]ghHref `json:"_links"`
	StartLine           *int              `json:"start_line"`
	OriginalStartLine   *int              `json:"original_start_line"`
	StartSide           *string           `json:"start_side"`
	Line                *int              `json:"line"`
	OriginalLine        int               `json:"original_line"`
	Side                string            `json:"side"`
	SubjectType         string            `json:"subject_type"`
}

// reviewCommentObj answers a review comment of pull request n (0 when
// unknown: the comment endpoints by id do not carry the number).
func (c *call) reviewCommentObj(n int64, cm pulls.Comment) ghReviewComment {
	id := c.ids.of(cm.ID)
	self := c.repoAPI() + "/pulls/comments/" + strconv.FormatInt(id, 10)
	pr, html := c.repoAPI()+"/pulls", c.repoHTML()+"/pulls"
	if n > 0 {
		pr += "/" + strconv.FormatInt(n, 10)
		html += "/" + strconv.FormatInt(n, 10) + "#discussion_r" + strconv.FormatInt(id, 10)
	}
	side := cm.Side
	if side == "" {
		side = "RIGHT"
	}
	out := ghReviewComment{URL: self, ID: id, NodeID: nodeID("PullRequestReviewComment", cm.ID), DiffHunk: cm.DiffHunk,
		Path: cm.Path, CommitID: cm.CommitSHA, OriginalCommitID: cm.OriginalCommitSHA, Body: cm.Body,
		CreatedAt: ts(cm.CreatedAt), UpdatedAt: ts(cm.UpdatedAt), HTMLURL: html, PullRequestURL: pr,
		AuthorAssociation: "NONE", Links: map[string]ghHref{"self": {self}, "html": {html}, "pull_request": {pr}},
		StartLine: cm.StartLine, OriginalStartLine: cm.OriginalStartLine, Line: cm.Line, OriginalLine: cm.OriginalLine,
		Side: side, SubjectType: "line"}
	if cm.StartLine != nil {
		out.StartSide = &side
	}
	if cm.ReviewID != uuid.Nil {
		r := c.ids.of(cm.ReviewID)
		out.PullRequestReviewID = &r
	}
	if cm.InReplyTo != uuid.Nil {
		r := c.ids.of(cm.InReplyTo)
		out.InReplyToID = &r
	}
	if cm.Author != "" {
		out.User = c.userObj(cm.AuthorID, "user", cm.Author)
	}
	return out
}

func (c *call) reviewComments(n int64, review uuid.UUID) error {
	p := c.pager()
	list, err := c.s.d.Pulls.ListComments(c.ctx, c.who, c.owner, c.repo, n, review, p.offset(), p.limit())
	if err != nil {
		return err
	}
	list, more := cut(p, list)
	var us []uuid.UUID
	for _, cm := range list {
		us = append(us, cm.ID, cm.ReviewID, cm.InReplyTo, cm.AuthorID)
	}
	c.ids.prefetch(us...)
	out := make([]ghReviewComment, len(list))
	for i, cm := range list {
		out[i] = c.reviewCommentObj(n, cm)
	}
	c.link(p, more)
	return c.ok(http.StatusOK, out)
}

func (c *call) listPullComments() error {
	if err := c.read(); err != nil {
		return err
	}
	n, err := c.number()
	if err != nil {
		return err
	}
	return c.reviewComments(n, uuid.Nil)
}

func (c *call) listReviewComments() error {
	if err := c.read(); err != nil {
		return err
	}
	n, id, err := c.reviewParams()
	if err != nil {
		return err
	}
	return c.reviewComments(n, id)
}

type reviewCommentBody struct {
	Body      string `json:"body"`
	CommitID  string `json:"commit_id"`
	Path      string `json:"path"`
	Position  *int   `json:"position"`
	Line      int    `json:"line"`
	Side      string `json:"side"`
	StartLine int    `json:"start_line"`
	InReplyTo *int64 `json:"in_reply_to"`
}

func (c *call) createPullComment() error {
	if err := c.write(); err != nil {
		return err
	}
	n, err := c.number()
	if err != nil {
		return err
	}
	var b reviewCommentBody
	if err := c.decode(&b); err != nil {
		return err
	}
	in := pulls.CommentInput{Body: b.Body, Path: b.Path, Side: b.Side, Line: b.Line, StartLine: b.StartLine,
		CommitSHA: b.CommitID}
	if b.InReplyTo != nil {
		id, err := c.ids.parse(strconv.FormatInt(*b.InReplyTo, 10))
		if err != nil {
			return errf(http.StatusUnprocessableEntity, "Validation Failed: in_reply_to does not exist")
		}
		in.InReplyTo = id
	} else if b.Line == 0 {
		return errf(http.StatusUnprocessableEntity, "Validation Failed: line is required (position is not supported)")
	}
	cm, err := c.s.d.Pulls.CreateComment(c.ctx, c.who, c.owner, c.repo, n, in)
	if err != nil {
		return err
	}
	out := c.reviewCommentObj(n, cm)
	c.w.Header().Set("Location", out.URL)
	return c.ok(http.StatusCreated, out)
}

func (c *call) updatePullComment() error {
	if err := c.write(); err != nil {
		return err
	}
	id, err := c.idParam()
	if err != nil {
		return err
	}
	body, err := c.commentBody()
	if err != nil {
		return err
	}
	cm, err := c.s.d.Pulls.UpdateComment(c.ctx, c.who, c.owner, c.repo, id, body)
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.reviewCommentObj(0, cm))
}

func (c *call) deletePullComment() error {
	if err := c.write(); err != nil {
		return err
	}
	id, err := c.idParam()
	if err != nil {
		return err
	}
	if err := c.s.d.Pulls.DeleteComment(c.ctx, c.who, c.owner, c.repo, id); err != nil {
		return err
	}
	return c.noContent()
}

// ---- requested reviewers ----

func (c *call) listRequestedReviewers() error {
	if err := c.read(); err != nil {
		return err
	}
	n, err := c.number()
	if err != nil {
		return err
	}
	p, err := c.s.d.Pulls.Get(c.ctx, c.who, c.owner, c.repo, n)
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, map[string]any{"users": c.usersByName(p.RequestedReviewers), "teams": []any{}})
}

type reviewersBody struct {
	Reviewers     []string `json:"reviewers"`
	TeamReviewers []string `json:"team_reviewers"`
}

func (c *call) changeReviewers(add bool) error {
	if err := c.write(); err != nil {
		return err
	}
	n, err := c.number()
	if err != nil {
		return err
	}
	var b reviewersBody
	if err := c.decode(&b); err != nil {
		return err
	}
	if len(b.TeamReviewers) > 0 {
		return errf(http.StatusUnprocessableEntity, "Team reviewers are not supported")
	}
	var p pulls.Pull
	status := http.StatusOK
	if add {
		p, err = c.s.d.Pulls.RequestReviewers(c.ctx, c.who, c.owner, c.repo, n, b.Reviewers)
		status = http.StatusCreated
	} else {
		p, err = c.s.d.Pulls.RemoveReviewers(c.ctx, c.who, c.owner, c.repo, n, b.Reviewers)
	}
	if err != nil {
		return err
	}
	c.prefetchPulls([]pulls.Pull{p})
	return c.ok(status, c.pullObj(p))
}

func (c *call) requestReviewers() error { return c.changeReviewers(true) }
func (c *call) removeReviewers() error  { return c.changeReviewers(false) }
