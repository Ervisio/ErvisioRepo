package ghapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/issues"
)

// ---- objects ----

type ghLabel struct {
	ID          int64   `json:"id"`
	NodeID      string  `json:"node_id"`
	URL         string  `json:"url"`
	Name        string  `json:"name"`
	Color       string  `json:"color"`
	Description *string `json:"description"`
	Default     bool    `json:"default"`
}

func (c *call) labelObj(l issues.Label) ghLabel {
	return ghLabel{ID: c.ids.of(l.ID), NodeID: nodeID("Label", l.ID), URL: c.repoAPI() + "/labels/" + esc(l.Name),
		Name: l.Name, Color: l.Color, Description: strp(l.Description)}
}

func (c *call) labelList(ls []issues.Label) []ghLabel {
	out := make([]ghLabel, len(ls))
	for i, l := range ls {
		out[i] = c.labelObj(l)
	}
	return out
}

type ghMilestone struct {
	URL          string  `json:"url"`
	HTMLURL      string  `json:"html_url"`
	LabelsURL    string  `json:"labels_url"`
	ID           int64   `json:"id"`
	NodeID       string  `json:"node_id"`
	Number       int64   `json:"number"`
	Title        string  `json:"title"`
	Description  *string `json:"description"`
	Creator      *ghUser `json:"creator"`
	OpenIssues   int64   `json:"open_issues"`
	ClosedIssues int64   `json:"closed_issues"`
	State        string  `json:"state"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
	ClosedAt     *string `json:"closed_at"`
	DueOn        *string `json:"due_on"`
}

// milestoneObj answers a milestone. Milestones have no number of their
// own: the numeric API id serves as both id and number.
func (c *call) milestoneObj(m *issues.Milestone) *ghMilestone {
	if m == nil {
		return nil
	}
	n := c.ids.of(m.ID)
	self := c.repoAPI() + "/milestones/" + strconv.FormatInt(n, 10)
	return &ghMilestone{URL: self, HTMLURL: c.repoHTML() + "/milestones/" + strconv.FormatInt(n, 10),
		LabelsURL: self + "/labels", ID: n, NodeID: nodeID("Milestone", m.ID), Number: n, Title: m.Title,
		Description: strp(m.Description), OpenIssues: m.OpenIssues, ClosedIssues: m.ClosedIssues, State: m.State,
		CreatedAt: ts(m.CreatedAt), UpdatedAt: ts(m.UpdatedAt), ClosedAt: tsp(m.ClosedAt), DueOn: tsp(m.DueOn)}
}

type ghReactions struct {
	URL        string `json:"url"`
	TotalCount int64  `json:"total_count"`
	PlusOne    int64  `json:"+1"`
	MinusOne   int64  `json:"-1"`
	Laugh      int64  `json:"laugh"`
	Hooray     int64  `json:"hooray"`
	Confused   int64  `json:"confused"`
	Heart      int64  `json:"heart"`
	Rocket     int64  `json:"rocket"`
	Eyes       int64  `json:"eyes"`
}

func reactions(u string, groups []issues.ReactionGroup) ghReactions {
	r := ghReactions{URL: u}
	for _, g := range groups {
		r.TotalCount += g.Count
		switch g.Content {
		case "+1":
			r.PlusOne = g.Count
		case "-1":
			r.MinusOne = g.Count
		case "laugh":
			r.Laugh = g.Count
		case "hooray":
			r.Hooray = g.Count
		case "confused":
			r.Confused = g.Count
		case "heart":
			r.Heart = g.Count
		case "rocket":
			r.Rocket = g.Count
		case "eyes":
			r.Eyes = g.Count
		}
	}
	return r
}

type ghIssuePull struct {
	URL      string  `json:"url"`
	HTMLURL  string  `json:"html_url"`
	DiffURL  string  `json:"diff_url"`
	PatchURL string  `json:"patch_url"`
	MergedAt *string `json:"merged_at"`
}

type ghIssue struct {
	ID                int64        `json:"id"`
	NodeID            string       `json:"node_id"`
	URL               string       `json:"url"`
	RepositoryURL     string       `json:"repository_url"`
	LabelsURL         string       `json:"labels_url"`
	CommentsURL       string       `json:"comments_url"`
	EventsURL         string       `json:"events_url"`
	HTMLURL           string       `json:"html_url"`
	Number            int64        `json:"number"`
	State             string       `json:"state"`
	StateReason       *string      `json:"state_reason"`
	Title             string       `json:"title"`
	Body              *string      `json:"body"`
	User              *ghUser      `json:"user"`
	Labels            []ghLabel    `json:"labels"`
	Assignee          *ghUser      `json:"assignee"`
	Assignees         []*ghUser    `json:"assignees"`
	Milestone         *ghMilestone `json:"milestone"`
	Locked            bool         `json:"locked"`
	ActiveLockReason  *string      `json:"active_lock_reason"`
	Comments          int          `json:"comments"`
	PullRequest       *ghIssuePull `json:"pull_request,omitempty"`
	ClosedAt          *string      `json:"closed_at"`
	CreatedAt         string       `json:"created_at"`
	UpdatedAt         string       `json:"updated_at"`
	ClosedBy          *ghUser      `json:"closed_by"`
	AuthorAssociation string       `json:"author_association"`
	Reactions         ghReactions  `json:"reactions"`
}

// prefetchIssues loads the numeric ids and the users of a list at once.
func (c *call) prefetchIssues(list []issues.Issue) {
	var us []uuid.UUID
	var names []string
	for _, i := range list {
		us = append(us, i.ID, i.AuthorID)
		for _, l := range i.Labels {
			us = append(us, l.ID)
		}
		if i.Milestone != nil {
			us = append(us, i.Milestone.ID)
		}
		names = append(names, i.Assignees...)
		names = append(names, i.ClosedBy)
	}
	c.ids.prefetch(us...)
	c.ids.prefetchUsers(names...)
}

func (c *call) issueObj(i issues.Issue) ghIssue {
	self := c.repoAPI() + "/issues/" + strconv.FormatInt(i.Number, 10)
	html := c.repoHTML() + "/issues/" + strconv.FormatInt(i.Number, 10)
	out := ghIssue{
		ID: c.ids.of(i.ID), NodeID: nodeID("Issue", i.ID), URL: self, RepositoryURL: c.repoAPI(),
		LabelsURL: self + "/labels{/name}", CommentsURL: self + "/comments", EventsURL: self + "/events",
		HTMLURL: html, Number: i.Number, State: i.State, StateReason: strp(i.StateReason), Title: i.Title,
		Body: strp(i.Body), Labels: c.labelList(i.Labels), Assignees: c.usersByName(i.Assignees),
		Milestone: c.milestoneObj(i.Milestone), Locked: i.Locked, ActiveLockReason: strp(i.LockReason),
		Comments: i.CommentCount, ClosedAt: tsp(i.ClosedAt), CreatedAt: ts(i.CreatedAt), UpdatedAt: ts(i.UpdatedAt),
		ClosedBy: c.userByName(i.ClosedBy), AuthorAssociation: "NONE", Reactions: reactions(self+"/reactions", i.Reactions),
	}
	if i.Author != "" {
		out.User = c.userObj(i.AuthorID, "user", i.Author)
	}
	if len(out.Assignees) > 0 {
		out.Assignee = out.Assignees[0]
	}
	if i.Kind == issues.KindPull {
		p := c.repoAPI() + "/pulls/" + strconv.FormatInt(i.Number, 10)
		h := c.repoHTML() + "/pulls/" + strconv.FormatInt(i.Number, 10)
		out.HTMLURL = h
		out.PullRequest = &ghIssuePull{URL: p, HTMLURL: h, DiffURL: h + ".diff", PatchURL: h + ".patch"}
	}
	return out
}

// ---- issues ----

func (c *call) listIssues() error {
	if err := c.read(); err != nil {
		return err
	}
	p := c.pager()
	in := issues.ListInput{State: c.query("state"), Assignee: c.query("assignee"), Author: c.query("creator"),
		Mentioned: c.query("mentioned"), Sort: c.query("sort"), Ascending: c.query("direction") == "asc",
		Offset: p.offset(), Limit: p.limit()}
	if in.State == "" {
		in.State = issues.StateOpen
	}
	if l := c.query("labels"); l != "" {
		for _, n := range strings.Split(l, ",") {
			if n = strings.TrimSpace(n); n != "" {
				in.Labels = append(in.Labels, n)
			}
		}
	}
	switch m := c.query("milestone"); m {
	case "", "none", "*":
		in.Milestone = m
	default:
		id, err := c.ids.parse(m)
		if err != nil {
			return c.ok(http.StatusOK, []ghIssue{})
		}
		in.Milestone = id.String()
	}
	switch in.Sort {
	case "comments", "updated", "created":
	default:
		in.Sort = ""
	}
	list, err := c.s.d.Issues.List(c.ctx, c.who, c.owner, c.repo, in)
	if err != nil {
		return err
	}
	list, more := cut(p, list)
	c.prefetchIssues(list)
	out := make([]ghIssue, len(list))
	for i, is := range list {
		out[i] = c.issueObj(is)
	}
	c.link(p, more)
	return c.ok(http.StatusOK, out)
}

func (c *call) getIssue() error {
	if err := c.read(); err != nil {
		return err
	}
	n, err := c.number()
	if err != nil {
		return err
	}
	i, err := c.s.d.Issues.Get(c.ctx, c.who, c.owner, c.repo, n)
	if err != nil {
		return err
	}
	c.prefetchIssues([]issues.Issue{i})
	return c.ok(http.StatusOK, c.issueObj(i))
}

// issueBody is the body of POST and PATCH issues. Raw fields tell an
// absent field from null.
type issueBody struct {
	Title       *string         `json:"title"`
	Body        json.RawMessage `json:"body"`
	State       *string         `json:"state"`
	StateReason json.RawMessage `json:"state_reason"`
	Assignee    json.RawMessage `json:"assignee"`
	Assignees   *[]string       `json:"assignees"`
	Labels      json.RawMessage `json:"labels"`
	Milestone   json.RawMessage `json:"milestone"`
}

func isNull(m json.RawMessage) bool { return strings.TrimSpace(string(m)) == "null" }

// rawString decodes an optional string; null becomes "".
func rawString(m json.RawMessage) (*string, error) {
	if len(m) == 0 {
		return nil, nil
	}
	s := ""
	if isNull(m) {
		return &s, nil
	}
	if err := json.Unmarshal(m, &s); err != nil {
		return nil, errf(http.StatusUnprocessableEntity, "Invalid request: expected a string")
	}
	return &s, nil
}

// labelNames decodes a list of label names or of {"name": ...} objects.
func labelNames(m json.RawMessage) (*[]string, error) {
	if len(m) == 0 {
		return nil, nil
	}
	out := []string{}
	if isNull(m) {
		return &out, nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(m, &raw); err != nil {
		// PUT and POST labels also accept {"labels": [...]}.
		var obj struct {
			Labels json.RawMessage `json:"labels"`
		}
		if json.Unmarshal(m, &obj) == nil && len(obj.Labels) > 0 {
			return labelNames(obj.Labels)
		}
		return nil, errf(http.StatusUnprocessableEntity, "Invalid request: labels must be an array")
	}
	for _, r := range raw {
		var s string
		if json.Unmarshal(r, &s) == nil {
			out = append(out, s)
			continue
		}
		var o struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(r, &o); err != nil || o.Name == "" {
			return nil, errf(http.StatusUnprocessableEntity, "Invalid request: a label must be a name")
		}
		out = append(out, o.Name)
	}
	return &out, nil
}

// milestoneID decodes the milestone number of a body: nil when absent,
// "" when null.
func (c *call) milestoneID(m json.RawMessage) (*string, error) {
	if len(m) == 0 {
		return nil, nil
	}
	s := ""
	if isNull(m) {
		return &s, nil
	}
	var n json.Number
	if err := json.Unmarshal(m, &n); err != nil {
		var str string
		if json.Unmarshal(m, &str) != nil {
			return nil, errf(http.StatusUnprocessableEntity, "Invalid request: milestone must be a number")
		}
		n = json.Number(str)
	}
	id, err := c.ids.parse(n.String())
	if err != nil {
		return nil, errf(http.StatusUnprocessableEntity, "Validation Failed: milestone %s does not exist", n)
	}
	s = id.String()
	return &s, nil
}

func (b issueBody) assignees() (*[]string, error) {
	if b.Assignees != nil {
		return b.Assignees, nil
	}
	a, err := rawString(b.Assignee)
	if err != nil || a == nil {
		return nil, err
	}
	out := []string{}
	if *a != "" {
		out = append(out, *a)
	}
	return &out, nil
}

func (c *call) createIssue() error {
	if err := c.write(); err != nil {
		return err
	}
	var b issueBody
	if err := c.decode(&b); err != nil {
		return err
	}
	if b.Title == nil {
		return errf(http.StatusUnprocessableEntity, "Invalid request.\n\n\"title\" wasn't supplied.")
	}
	in := issues.CreateInput{Title: *b.Title}
	if s, err := rawString(b.Body); err != nil {
		return err
	} else if s != nil {
		in.Body = *s
	}
	labels, err := labelNames(b.Labels)
	if err != nil {
		return err
	}
	if labels != nil {
		in.Labels = *labels
	}
	as, err := b.assignees()
	if err != nil {
		return err
	}
	if as != nil {
		in.Assignees = *as
	}
	mid, err := c.milestoneID(b.Milestone)
	if err != nil {
		return err
	}
	if mid != nil && *mid != "" {
		u := uuid.MustParse(*mid)
		in.MilestoneID = &u
	}
	i, err := c.s.d.Issues.Create(c.ctx, c.who, c.owner, c.repo, in)
	if err != nil {
		return err
	}
	c.prefetchIssues([]issues.Issue{i})
	out := c.issueObj(i)
	c.w.Header().Set("Location", out.URL)
	return c.ok(http.StatusCreated, out)
}

func (c *call) updateIssue() error {
	if err := c.write(); err != nil {
		return err
	}
	n, err := c.number()
	if err != nil {
		return err
	}
	var b issueBody
	if err := c.decode(&b); err != nil {
		return err
	}
	in := issues.UpdateInput{Title: b.Title, State: b.State}
	if in.Body, err = rawString(b.Body); err != nil {
		return err
	}
	if in.StateReason, err = rawString(b.StateReason); err != nil {
		return err
	}
	if in.StateReason != nil && (*in.StateReason == "" || in.State == nil) {
		in.StateReason = nil
	}
	if in.Labels, err = labelNames(b.Labels); err != nil {
		return err
	}
	if in.Assignees, err = b.assignees(); err != nil {
		return err
	}
	if in.MilestoneID, err = c.milestoneID(b.Milestone); err != nil {
		return err
	}
	return c.applyIssue(n, in)
}

// applyIssue updates issue n and answers it.
func (c *call) applyIssue(n int64, in issues.UpdateInput) error {
	i, err := c.s.d.Issues.Update(c.ctx, c.who, c.owner, c.repo, n, in)
	if err != nil {
		return err
	}
	c.prefetchIssues([]issues.Issue{i})
	return c.ok(http.StatusOK, c.issueObj(i))
}

// ---- issue comments ----

type ghComment struct {
	ID                int64       `json:"id"`
	NodeID            string      `json:"node_id"`
	URL               string      `json:"url"`
	HTMLURL           string      `json:"html_url"`
	IssueURL          string      `json:"issue_url"`
	Body              string      `json:"body"`
	User              *ghUser     `json:"user"`
	CreatedAt         string      `json:"created_at"`
	UpdatedAt         string      `json:"updated_at"`
	AuthorAssociation string      `json:"author_association"`
	Reactions         ghReactions `json:"reactions"`
}

func (c *call) commentObj(cm issues.Comment) ghComment {
	n := c.ids.of(cm.ID)
	self := c.repoAPI() + "/issues/comments/" + strconv.FormatInt(n, 10)
	out := ghComment{ID: n, NodeID: nodeID("IssueComment", cm.ID), URL: self,
		HTMLURL:  c.repoHTML() + "/issues/" + strconv.FormatInt(cm.IssueNumber, 10) + "#issuecomment-" + strconv.FormatInt(n, 10),
		IssueURL: c.repoAPI() + "/issues/" + strconv.FormatInt(cm.IssueNumber, 10), Body: cm.Body,
		CreatedAt: ts(cm.CreatedAt), UpdatedAt: ts(cm.UpdatedAt), AuthorAssociation: "NONE",
		Reactions: reactions(self+"/reactions", cm.Reactions)}
	if cm.Author != "" {
		out.User = c.userObj(cm.AuthorID, "user", cm.Author)
	}
	return out
}

func (c *call) listIssueComments() error {
	if err := c.read(); err != nil {
		return err
	}
	n, err := c.number()
	if err != nil {
		return err
	}
	p := c.pager()
	list, err := c.s.d.Issues.ListComments(c.ctx, c.who, c.owner, c.repo, n, p.offset(), p.limit())
	if err != nil {
		return err
	}
	list, more := cut(p, list)
	var us []uuid.UUID
	for _, cm := range list {
		us = append(us, cm.ID, cm.AuthorID)
	}
	c.ids.prefetch(us...)
	out := make([]ghComment, len(list))
	for i, cm := range list {
		out[i] = c.commentObj(cm)
	}
	c.link(p, more)
	return c.ok(http.StatusOK, out)
}

type bodyOnly struct {
	Body *string `json:"body"`
}

func (c *call) commentBody() (string, error) {
	var b bodyOnly
	if err := c.decode(&b); err != nil {
		return "", err
	}
	if b.Body == nil {
		return "", errf(http.StatusUnprocessableEntity, "Invalid request.\n\n\"body\" wasn't supplied.")
	}
	return *b.Body, nil
}

func (c *call) createIssueComment() error {
	if err := c.write(); err != nil {
		return err
	}
	n, err := c.number()
	if err != nil {
		return err
	}
	body, err := c.commentBody()
	if err != nil {
		return err
	}
	cm, err := c.s.d.Issues.CreateComment(c.ctx, c.who, c.owner, c.repo, n, body)
	if err != nil {
		return err
	}
	out := c.commentObj(cm)
	c.w.Header().Set("Location", out.URL)
	return c.ok(http.StatusCreated, out)
}

func (c *call) getIssueComment() error {
	if err := c.read(); err != nil {
		return err
	}
	id, err := c.idParam()
	if err != nil {
		return err
	}
	cm, err := c.s.d.Issues.GetComment(c.ctx, c.who, c.owner, c.repo, id)
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.commentObj(cm))
}

func (c *call) updateIssueComment() error {
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
	cm, err := c.s.d.Issues.UpdateComment(c.ctx, c.who, c.owner, c.repo, id, body)
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.commentObj(cm))
}

func (c *call) deleteIssueComment() error {
	if err := c.write(); err != nil {
		return err
	}
	id, err := c.idParam()
	if err != nil {
		return err
	}
	if err := c.s.d.Issues.DeleteComment(c.ctx, c.who, c.owner, c.repo, id, c.client()); err != nil {
		return err
	}
	return c.noContent()
}

// ---- issue labels and assignees ----

func (c *call) issueLabels(n int64) (issues.Issue, error) {
	return c.s.d.Issues.Get(c.ctx, c.who, c.owner, c.repo, n)
}

func (c *call) listIssueLabels() error {
	if err := c.read(); err != nil {
		return err
	}
	n, err := c.number()
	if err != nil {
		return err
	}
	i, err := c.issueLabels(n)
	if err != nil {
		return err
	}
	p := c.pager()
	page, more := slice(p, i.Labels)
	c.link(p, more)
	return c.ok(http.StatusOK, c.labelList(page))
}

// changeLabels applies fn to the label names of issue n and answers the
// new labels.
func (c *call) changeLabels(fn func(cur []string) ([]string, error)) error {
	if err := c.write(); err != nil {
		return err
	}
	n, err := c.number()
	if err != nil {
		return err
	}
	i, err := c.issueLabels(n)
	if err != nil {
		return err
	}
	cur := make([]string, len(i.Labels))
	for k, l := range i.Labels {
		cur[k] = l.Name
	}
	next, err := fn(cur)
	if err != nil {
		return err
	}
	i, err = c.s.d.Issues.Update(c.ctx, c.who, c.owner, c.repo, n, issues.UpdateInput{Labels: &next})
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.labelList(i.Labels))
}

func (c *call) bodyLabels() ([]string, error) {
	var raw json.RawMessage
	if err := c.decode(&raw); err != nil {
		return nil, err
	}
	l, err := labelNames(raw)
	if err != nil {
		return nil, err
	}
	if l == nil {
		return []string{}, nil
	}
	return *l, nil
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for _, s := range append(append([]string{}, a...), b...) {
		if k := strings.ToLower(s); !seen[k] {
			seen[k] = true
			out = append(out, s)
		}
	}
	return out
}

func without(a, drop []string) []string {
	rm := map[string]bool{}
	for _, s := range drop {
		rm[strings.ToLower(s)] = true
	}
	out := []string{}
	for _, s := range a {
		if !rm[strings.ToLower(s)] {
			out = append(out, s)
		}
	}
	return out
}

func (c *call) addIssueLabels() error {
	return c.changeLabels(func(cur []string) ([]string, error) {
		add, err := c.bodyLabels()
		return union(cur, add), err
	})
}

func (c *call) setIssueLabels() error {
	return c.changeLabels(func([]string) ([]string, error) { return c.bodyLabels() })
}

func (c *call) clearIssueLabels() error {
	return c.changeLabels(func([]string) ([]string, error) { return []string{}, nil })
}

func (c *call) removeIssueLabel() error {
	name := chi.URLParam(c.r, "name")
	return c.changeLabels(func(cur []string) ([]string, error) {
		next := without(cur, []string{name})
		if len(next) == len(cur) {
			return nil, errf(http.StatusNotFound, "Label does not exist")
		}
		return next, nil
	})
}

type assigneesBody struct {
	Assignees []string `json:"assignees"`
}

func (c *call) changeAssignees(add bool) error {
	if err := c.write(); err != nil {
		return err
	}
	n, err := c.number()
	if err != nil {
		return err
	}
	var b assigneesBody
	if err := c.decode(&b); err != nil {
		return err
	}
	i, err := c.s.d.Issues.Get(c.ctx, c.who, c.owner, c.repo, n)
	if err != nil {
		return err
	}
	next := without(i.Assignees, b.Assignees)
	status := http.StatusOK
	if add {
		next = union(i.Assignees, b.Assignees)
		status = http.StatusCreated
	}
	i, err = c.s.d.Issues.Update(c.ctx, c.who, c.owner, c.repo, n, issues.UpdateInput{Assignees: &next})
	if err != nil {
		return err
	}
	c.prefetchIssues([]issues.Issue{i})
	return c.ok(status, c.issueObj(i))
}

func (c *call) addAssignees() error    { return c.changeAssignees(true) }
func (c *call) removeAssignees() error { return c.changeAssignees(false) }

// listAssignees lists the collaborators, who are the users an issue can
// be assigned to.
func (c *call) listAssignees() error {
	if err := c.read(); err != nil {
		return err
	}
	list, err := c.s.d.Repos.ListCollaborators(c.ctx, c.who, c.owner, c.repo)
	if err != nil {
		return err
	}
	var us []uuid.UUID
	for _, co := range list {
		us = append(us, co.UserID)
	}
	c.ids.prefetch(us...)
	p := c.pager()
	page, more := slice(p, list)
	out := make([]*ghUser, len(page))
	for i, co := range page {
		out[i] = c.userObj(co.UserID, "user", co.Username)
	}
	c.link(p, more)
	return c.ok(http.StatusOK, out)
}

func (c *call) checkAssignee() error {
	if err := c.read(); err != nil {
		return err
	}
	list, err := c.s.d.Repos.ListCollaborators(c.ctx, c.who, c.owner, c.repo)
	if err != nil {
		return err
	}
	name := chi.URLParam(c.r, "username")
	for _, co := range list {
		if strings.EqualFold(co.Username, name) {
			return c.noContent()
		}
	}
	return errNotFound
}

// ---- labels ----

// maxLabelPages bounds the label pages read to find a label by name.
const maxLabelPages = 20

// findLabel returns the label called name.
func (c *call) findLabel(name string) (issues.Label, error) {
	for i := range maxLabelPages {
		list, err := c.s.d.Issues.ListLabels(c.ctx, c.who, c.owner, c.repo, i*maxPerPage, maxPerPage)
		if err != nil {
			return issues.Label{}, err
		}
		for _, l := range list {
			if strings.EqualFold(l.Name, name) {
				return l, nil
			}
		}
		if len(list) < maxPerPage {
			break
		}
	}
	return issues.Label{}, errNotFound
}

func (c *call) listLabels() error {
	if err := c.read(); err != nil {
		return err
	}
	p := c.pager()
	list, err := c.s.d.Issues.ListLabels(c.ctx, c.who, c.owner, c.repo, p.offset(), p.limit())
	if err != nil {
		return err
	}
	list, more := cut(p, list)
	c.link(p, more)
	return c.ok(http.StatusOK, c.labelList(list))
}

func (c *call) getLabel() error {
	if err := c.read(); err != nil {
		return err
	}
	l, err := c.findLabel(chi.URLParam(c.r, "name"))
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.labelObj(l))
}

type labelBody struct {
	Name        *string `json:"name"`
	NewName     *string `json:"new_name"`
	Color       *string `json:"color"`
	Description *string `json:"description"`
}

func (c *call) createLabel() error {
	if err := c.write(); err != nil {
		return err
	}
	var b labelBody
	if err := c.decode(&b); err != nil {
		return err
	}
	if b.Name == nil {
		return errf(http.StatusUnprocessableEntity, "Invalid request.\n\n\"name\" wasn't supplied.")
	}
	if b.Color == nil {
		// GitHub picks a color; use its default label grey.
		grey := "ededed"
		b.Color = &grey
	}
	l, err := c.s.d.Issues.CreateLabel(c.ctx, c.who, c.owner, c.repo,
		issues.LabelInput{Name: b.Name, Color: b.Color, Description: b.Description}, c.client())
	if err != nil {
		return err
	}
	out := c.labelObj(l)
	c.w.Header().Set("Location", out.URL)
	return c.ok(http.StatusCreated, out)
}

func (c *call) updateLabel() error {
	if err := c.write(); err != nil {
		return err
	}
	var b labelBody
	if err := c.decode(&b); err != nil {
		return err
	}
	in := issues.LabelInput{Name: b.NewName, Color: b.Color, Description: b.Description}
	if in.Name == nil {
		in.Name = b.Name
	}
	l, err := c.s.d.Issues.UpdateLabel(c.ctx, c.who, c.owner, c.repo, chi.URLParam(c.r, "name"), in, c.client())
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.labelObj(l))
}

func (c *call) deleteLabel() error {
	if err := c.write(); err != nil {
		return err
	}
	if err := c.s.d.Issues.DeleteLabel(c.ctx, c.who, c.owner, c.repo, chi.URLParam(c.r, "name"), c.client()); err != nil {
		return err
	}
	return c.noContent()
}

// ---- milestones ----

func (c *call) milestoneList(list []issues.Milestone) []*ghMilestone {
	us := make([]uuid.UUID, len(list))
	for i, m := range list {
		us[i] = m.ID
	}
	c.ids.prefetch(us...)
	out := make([]*ghMilestone, len(list))
	for i := range list {
		out[i] = c.milestoneObj(&list[i])
	}
	return out
}

func (c *call) listMilestones() error {
	if err := c.read(); err != nil {
		return err
	}
	p := c.pager()
	list, err := c.s.d.Issues.ListMilestones(c.ctx, c.who, c.owner, c.repo, c.query("state"), p.offset(), p.limit())
	if err != nil {
		return err
	}
	list, more := cut(p, list)
	c.link(p, more)
	return c.ok(http.StatusOK, c.milestoneList(list))
}

func (c *call) milestoneParam() (uuid.UUID, error) { return c.ids.parse(chi.URLParam(c.r, "number")) }

func (c *call) getMilestone() error {
	if err := c.read(); err != nil {
		return err
	}
	id, err := c.milestoneParam()
	if err != nil {
		return err
	}
	m, err := c.s.d.Issues.GetMilestone(c.ctx, c.who, c.owner, c.repo, id)
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.milestoneObj(&m))
}

type milestoneBody struct {
	Title       *string         `json:"title"`
	State       *string         `json:"state"`
	Description *string         `json:"description"`
	DueOn       json.RawMessage `json:"due_on"`
}

func (b milestoneBody) input() (issues.MilestoneInput, error) {
	due, err := rawString(b.DueOn)
	return issues.MilestoneInput{Title: b.Title, Description: b.Description, DueOn: due, State: b.State}, err
}

func (c *call) createMilestone() error {
	if err := c.write(); err != nil {
		return err
	}
	var b milestoneBody
	if err := c.decode(&b); err != nil {
		return err
	}
	if b.Title == nil {
		return errf(http.StatusUnprocessableEntity, "Invalid request.\n\n\"title\" wasn't supplied.")
	}
	in, err := b.input()
	if err != nil {
		return err
	}
	m, err := c.s.d.Issues.CreateMilestone(c.ctx, c.who, c.owner, c.repo, in, c.client())
	if err != nil {
		return err
	}
	out := c.milestoneObj(&m)
	c.w.Header().Set("Location", out.URL)
	return c.ok(http.StatusCreated, out)
}

func (c *call) updateMilestone() error {
	if err := c.write(); err != nil {
		return err
	}
	id, err := c.milestoneParam()
	if err != nil {
		return err
	}
	var b milestoneBody
	if err := c.decode(&b); err != nil {
		return err
	}
	in, err := b.input()
	if err != nil {
		return err
	}
	m, err := c.s.d.Issues.UpdateMilestone(c.ctx, c.who, c.owner, c.repo, id, in, c.client())
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.milestoneObj(&m))
}

func (c *call) deleteMilestone() error {
	if err := c.write(); err != nil {
		return err
	}
	id, err := c.milestoneParam()
	if err != nil {
		return err
	}
	if err := c.s.d.Issues.DeleteMilestone(c.ctx, c.who, c.owner, c.repo, id, c.client()); err != nil {
		return err
	}
	return c.noContent()
}
