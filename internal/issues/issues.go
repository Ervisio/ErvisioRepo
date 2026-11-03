package issues

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// Lock reasons.
var lockReasons = []string{"", "off-topic", "too heated", "resolved", "spam"}

// CreateInput opens an issue. Labels, Assignees and MilestoneID need the
// triage permission and are ignored without it.
type CreateInput struct {
	Title       string
	Body        string
	Labels      []string
	Assignees   []string
	MilestoneID *uuid.UUID
}

// UpdateInput edits an issue; nil fields are kept. Labels and Assignees
// replace the whole set. MilestoneID "" clears the milestone.
type UpdateInput struct {
	Title       *string
	Body        *string
	State       *string
	StateReason *string
	Labels      *[]string
	Assignees   *[]string
	MilestoneID *string
}

// ListInput filters a search. Milestone is a milestone id, "none" or "*";
// Assignee is a username, "none" or "*".
type ListInput struct {
	State     string // open (default), closed or all
	Labels    []string
	Milestone string
	Assignee  string
	Author    string
	Mentioned string
	Query     string
	Sort      string // created (default), updated, comments or relevance (default with Query)
	Ascending bool
	Offset    int
	Limit     int
}

// get loads an issue of r by number.
func (s *Service) get(ctx context.Context, q *dbgen.Queries, r repos.Repo, number int64) (dbgen.Issue, error) {
	i, err := q.GetIssueByNumber(ctx, dbgen.GetIssueByNumberParams{RepoID: r.ID, Number: number})
	if err != nil {
		return dbgen.Issue{}, notFound(err, ErrNotFound, "get issue")
	}
	return i, nil
}

func (s *Service) lockIssue(ctx context.Context, q *dbgen.Queries, r repos.Repo, number int64) (dbgen.Issue, error) {
	i, err := q.GetIssueForUpdate(ctx, dbgen.GetIssueForUpdateParams{RepoID: r.ID, Number: number})
	if err != nil {
		return dbgen.Issue{}, notFound(err, ErrNotFound, "lock issue row")
	}
	return i, nil
}

// Get returns issue number of owner/name. Pull requests are returned too
// (Kind KindPull): they share the numbering.
func (s *Service) Get(ctx context.Context, who auth.Principal, owner, name string, number int64) (Issue, error) {
	r, err := s.repo(ctx, who, owner, name)
	if err != nil {
		return Issue{}, err
	}
	i, err := s.get(ctx, s.q, r, number)
	if err != nil {
		return Issue{}, err
	}
	return s.hydrateOne(ctx, who.UserID, i)
}

// List searches the issues (not pull requests) of owner/name.
func (s *Service) List(ctx context.Context, who auth.Principal, owner, name string, in ListInput) ([]Issue, error) {
	r, err := s.repo(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	p, ok, err := s.searchParams(ctx, r, in)
	if err != nil || !ok {
		return []Issue{}, err
	}
	p.Kind = KindIssue
	rows, err := s.q.SearchIssues(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("search issues: %w", err)
	}
	return s.hydrate(ctx, who.UserID, rows)
}

// searchParams translates a ListInput; ok is false when a filter names
// something that does not exist (the result is empty).
func (s *Service) searchParams(ctx context.Context, r repos.Repo, in ListInput) (dbgen.SearchIssuesParams, bool, error) {
	p := dbgen.SearchIssuesParams{RepoID: r.ID}
	switch in.State {
	case "", StateOpen:
		p.State = StateOpen
	case StateClosed:
		p.State = StateClosed
	case "all":
	default:
		return p, false, invalid("state must be open, closed or all")
	}
	if len(in.Labels) > 0 {
		names := make([]string, 0, len(in.Labels))
		for _, l := range in.Labels {
			if l = strings.TrimSpace(l); l != "" {
				names = append(names, strings.ToLower(l))
			}
		}
		slices.Sort(names)
		names = slices.Compact(names)
		ls, err := s.q.ListLabelsByNames(ctx, dbgen.ListLabelsByNamesParams{RepoID: r.ID, Names: names})
		if err != nil {
			return p, false, fmt.Errorf("resolve labels: %w", err)
		}
		if len(ls) != len(names) {
			return p, false, nil
		}
		for _, l := range ls {
			p.LabelIds = append(p.LabelIds, l.ID)
		}
	}
	if p.LabelIds == nil {
		p.LabelIds = []uuid.UUID{}
	}
	switch in.Milestone {
	case "":
	case "none":
		p.MilestoneMode = "none"
	case "*":
		p.MilestoneMode = "any"
	default:
		id, err := uuid.Parse(in.Milestone)
		if err != nil {
			return p, false, invalid("milestone must be a milestone id, none or *")
		}
		p.MilestoneMode, p.MilestoneID = "id", id
	}
	switch in.Assignee {
	case "":
	case "none":
		p.AssigneeMode = "none"
	case "*":
		p.AssigneeMode = "any"
	default:
		id, ok, err := s.userID(ctx, in.Assignee)
		if err != nil || !ok {
			return p, false, err
		}
		p.AssigneeMode, p.AssigneeID = "id", id
	}
	if in.Author != "" {
		id, ok, err := s.userID(ctx, in.Author)
		if err != nil || !ok {
			return p, false, err
		}
		p.ByAuthor, p.AuthorID = true, id
	}
	if in.Mentioned != "" {
		id, ok, err := s.userID(ctx, in.Mentioned)
		if err != nil || !ok {
			return p, false, err
		}
		p.ByMention, p.MentionedID = true, id
	}
	p.Query = strings.TrimSpace(in.Query)
	if err := checkText("q", p.Query, 256); err != nil {
		return p, false, err
	}
	switch in.Sort {
	case "":
		p.Sort = "created"
		if p.Query != "" {
			p.Sort = "relevance"
		}
	case "created", "updated", "comments":
		p.Sort = in.Sort
	case "relevance":
		p.Sort = "relevance"
		if p.Query == "" {
			p.Sort = "created"
		}
	default:
		return p, false, invalid("sort must be created, updated, comments or relevance")
	}
	p.Ascending = in.Ascending
	p.Off, p.Lim = pageArgs(in.Offset, in.Limit)
	return p, true, nil
}

// userID resolves an active username.
func (s *Service) userID(ctx context.Context, name string) (uuid.UUID, bool, error) {
	rows, err := s.q.IssueUsersByNames(ctx, []string{strings.ToLower(name)})
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("resolve user: %w", err)
	}
	if len(rows) == 0 {
		return uuid.Nil, false, nil
	}
	return rows[0].ID, true, nil
}

// resolveLabels maps label names to labels of r (case-insensitive).
func (s *Service) resolveLabels(ctx context.Context, r repos.Repo, names []string) ([]dbgen.Label, error) {
	if len(names) > MaxLabelsPerIssue {
		return nil, invalid("an issue has at most %d labels", MaxLabelsPerIssue)
	}
	want := make([]string, 0, len(names))
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			want = append(want, strings.ToLower(n))
		}
	}
	slices.Sort(want)
	want = slices.Compact(want)
	if len(want) == 0 {
		return nil, nil
	}
	ls, err := s.q.ListLabelsByNames(ctx, dbgen.ListLabelsByNamesParams{RepoID: r.ID, Names: want})
	if err != nil {
		return nil, fmt.Errorf("resolve labels: %w", err)
	}
	if len(ls) != len(want) {
		found := map[string]bool{}
		for _, l := range ls {
			found[strings.ToLower(l.Name)] = true
		}
		for _, n := range want {
			if !found[n] {
				return nil, invalid("label %q does not exist", n)
			}
		}
	}
	return ls, nil
}

// resolveAssignees maps usernames to active users who can read r.
func (s *Service) resolveAssignees(ctx context.Context, r repos.Repo, names []string) ([]dbgen.IssueUsersByNamesRow, error) {
	want := make([]string, 0, len(names))
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			want = append(want, strings.ToLower(n))
		}
	}
	slices.Sort(want)
	want = slices.Compact(want)
	if len(want) > MaxAssignees {
		return nil, invalid("an issue has at most %d assignees", MaxAssignees)
	}
	if len(want) == 0 {
		return nil, nil
	}
	us, err := s.q.IssueUsersByNames(ctx, want)
	if err != nil {
		return nil, fmt.Errorf("resolve assignees: %w", err)
	}
	if len(us) != len(want) {
		return nil, invalid("every assignee must be an active user")
	}
	for _, u := range us {
		ok, err := s.userCanRead(ctx, u.ID, r.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, invalid("%s cannot read the repository", u.Name)
		}
	}
	return us, nil
}

// userCanRead reports whether user can read repository repoID.
func (s *Service) userCanRead(ctx context.Context, user, repoID uuid.UUID) (bool, error) {
	u, err := s.q.GetUserByID(ctx, user)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get user: %w", err)
	}
	_, err = s.o.Repos.GetByID(ctx, auth.Principal{UserID: u.ID, Username: u.Username, Role: u.Role}, repoID)
	if errors.Is(err, repos.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// milestoneOf checks that id is a milestone of r.
func (s *Service) milestoneOf(ctx context.Context, q *dbgen.Queries, r repos.Repo, id uuid.UUID) (dbgen.GetMilestonesByIDsRow, error) {
	ms, err := q.GetMilestonesByIDs(ctx, dbgen.GetMilestonesByIDsParams{RepoID: r.ID, Ids: []uuid.UUID{id}})
	if err != nil {
		return dbgen.GetMilestonesByIDsRow{}, fmt.Errorf("get milestone: %w", err)
	}
	if len(ms) == 0 {
		return dbgen.GetMilestonesByIDsRow{}, invalid("milestone %s does not exist", id)
	}
	return ms[0], nil
}

// Create opens an issue.
func (s *Service) Create(ctx context.Context, who auth.Principal, owner, name string, in CreateInput) (Issue, error) {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return Issue{}, err
	}
	title, err := checkTitle("title", in.Title, MaxTitle)
	if err != nil {
		return Issue{}, err
	}
	if err := checkText("body", in.Body, MaxBody); err != nil {
		return Issue{}, err
	}
	var labels []dbgen.Label
	var assignees []dbgen.IssueUsersByNamesRow
	var milestone *dbgen.GetMilestonesByIDsRow
	if canTriage(r) {
		if labels, err = s.resolveLabels(ctx, r, in.Labels); err != nil {
			return Issue{}, err
		}
		if assignees, err = s.resolveAssignees(ctx, r, in.Assignees); err != nil {
			return Issue{}, err
		}
		if in.MilestoneID != nil {
			m, err := s.milestoneOf(ctx, s.q, r, *in.MilestoneID)
			if err != nil {
				return Issue{}, err
			}
			milestone = &m
		}
	}
	var row dbgen.Issue
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		n, err := NextNumber(ctx, q, r.ID)
		if err != nil {
			return err
		}
		id, err := newID()
		if err != nil {
			return err
		}
		var mid uuid.NullUUID
		if milestone != nil {
			mid = nullID(milestone.ID)
		}
		row, err = q.InsertIssue(ctx, dbgen.InsertIssueParams{ID: id, RepoID: r.ID, Number: n, Kind: KindIssue, Title: title,
			Body: in.Body, AuthorID: nullID(who.UserID), MilestoneID: mid})
		if err != nil {
			return fmt.Errorf("insert issue: %w", err)
		}
		for _, l := range labels {
			if err := s.addLabel(ctx, q, row, who.UserID, l); err != nil {
				return err
			}
		}
		for _, u := range assignees {
			if err := s.addAssignee(ctx, q, row, who.UserID, u.ID); err != nil {
				return err
			}
		}
		if milestone != nil {
			if err := s.event(ctx, q, row, who.UserID, "milestoned", uuid.Nil, map[string]any{"title": milestone.Title}); err != nil {
				return err
			}
		}
		if err := s.recordRefs(ctx, q, r, who, refSource{issue: row}, in.Body); err != nil {
			return err
		}
		return s.emit(ctx, q, events.KindIssue, "opened", r.ID, who.UserID, row.ID, issuePayload(row))
	})
	if err != nil {
		return Issue{}, err
	}
	return s.hydrateOne(ctx, who.UserID, row)
}

func (s *Service) addLabel(ctx context.Context, q *dbgen.Queries, i dbgen.Issue, actor uuid.UUID, l dbgen.Label) error {
	n, err := q.AddIssueLabel(ctx, dbgen.AddIssueLabelParams{IssueID: i.ID, LabelID: l.ID})
	if err != nil {
		return fmt.Errorf("add label: %w", err)
	}
	if n == 0 {
		return nil
	}
	if err := s.event(ctx, q, i, actor, "labeled", uuid.Nil, labelData(l)); err != nil {
		return err
	}
	p := issuePayload(i)
	p.Label = l.Name
	return s.emit(ctx, q, events.KindIssue, "labeled", i.RepoID, actor, i.ID, p)
}

func (s *Service) removeLabel(ctx context.Context, q *dbgen.Queries, i dbgen.Issue, actor uuid.UUID, l Label) error {
	n, err := q.RemoveIssueLabel(ctx, dbgen.RemoveIssueLabelParams{IssueID: i.ID, LabelID: l.ID})
	if err != nil {
		return fmt.Errorf("remove label: %w", err)
	}
	if n == 0 {
		return nil
	}
	if err := s.event(ctx, q, i, actor, "unlabeled", uuid.Nil, labelData(dbgen.Label{ID: l.ID, Name: l.Name, Color: l.Color,
		Description: l.Description})); err != nil {
		return err
	}
	p := issuePayload(i)
	p.Label = l.Name
	return s.emit(ctx, q, events.KindIssue, "unlabeled", i.RepoID, actor, i.ID, p)
}

func labelData(l dbgen.Label) map[string]any {
	return map[string]any{"label_id": l.ID, "name": l.Name, "color": l.Color, "description": l.Description}
}

func (s *Service) addAssignee(ctx context.Context, q *dbgen.Queries, i dbgen.Issue, actor, user uuid.UUID) error {
	n, err := q.AddIssueAssignee(ctx, dbgen.AddIssueAssigneeParams{IssueID: i.ID, UserID: user})
	if err != nil {
		return fmt.Errorf("add assignee: %w", err)
	}
	if n == 0 {
		return nil
	}
	if err := s.event(ctx, q, i, actor, "assigned", uuid.Nil, map[string]any{"user_id": user}); err != nil {
		return err
	}
	p := issuePayload(i)
	p.Assignee = &user
	return s.emit(ctx, q, events.KindIssue, "assigned", i.RepoID, actor, i.ID, p)
}

func (s *Service) removeAssignee(ctx context.Context, q *dbgen.Queries, i dbgen.Issue, actor, user uuid.UUID) error {
	n, err := q.RemoveIssueAssignee(ctx, dbgen.RemoveIssueAssigneeParams{IssueID: i.ID, UserID: user})
	if err != nil {
		return fmt.Errorf("remove assignee: %w", err)
	}
	if n == 0 {
		return nil
	}
	if err := s.event(ctx, q, i, actor, "unassigned", uuid.Nil, map[string]any{"user_id": user}); err != nil {
		return err
	}
	p := issuePayload(i)
	p.Assignee = &user
	return s.emit(ctx, q, events.KindIssue, "unassigned", i.RepoID, actor, i.ID, p)
}

// Update edits, closes or reopens an issue. The author may change the
// title, body and state; everything else needs the triage permission.
func (s *Service) Update(ctx context.Context, who auth.Principal, owner, name string, number int64, in UpdateInput) (Issue, error) {
	r, err := s.writer(ctx, who, owner, name)
	if err != nil {
		return Issue{}, err
	}
	triage := canTriage(r)
	if (in.Labels != nil || in.Assignees != nil || in.MilestoneID != nil) && !triage {
		return Issue{}, ErrForbidden
	}
	var title, body string
	if in.Title != nil {
		if title, err = checkTitle("title", *in.Title, MaxTitle); err != nil {
			return Issue{}, err
		}
	}
	if in.Body != nil {
		if err := checkText("body", *in.Body, MaxBody); err != nil {
			return Issue{}, err
		}
		body = *in.Body
	}
	if in.State != nil && *in.State != StateOpen && *in.State != StateClosed {
		return Issue{}, invalid("state must be open or closed")
	}
	if in.StateReason != nil && in.State == nil {
		return Issue{}, invalid("stateReason needs state")
	}
	var labels []dbgen.Label
	if in.Labels != nil {
		if labels, err = s.resolveLabels(ctx, r, *in.Labels); err != nil {
			return Issue{}, err
		}
	}
	var assignees []dbgen.IssueUsersByNamesRow
	if in.Assignees != nil {
		if assignees, err = s.resolveAssignees(ctx, r, *in.Assignees); err != nil {
			return Issue{}, err
		}
	}
	var milestone *dbgen.GetMilestonesByIDsRow
	if in.MilestoneID != nil && *in.MilestoneID != "" {
		id, err := uuid.Parse(*in.MilestoneID)
		if err != nil {
			return Issue{}, invalid("milestoneId is not a valid id")
		}
		m, err := s.milestoneOf(ctx, s.q, r, id)
		if err != nil {
			return Issue{}, err
		}
		milestone = &m
	}
	var row dbgen.Issue
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		cur, err := s.lockIssue(ctx, q, r, number)
		if err != nil {
			return err
		}
		author := cur.AuthorID.Valid && cur.AuthorID.UUID == who.UserID
		if (in.Title != nil || in.Body != nil || in.State != nil) && !author && !triage {
			return ErrForbidden
		}
		row = cur
		if err := s.updateContent(ctx, q, r, who, &row, in.Title != nil, title, in.Body != nil, body); err != nil {
			return err
		}
		if in.State != nil {
			reason := ""
			if in.StateReason != nil {
				reason = *in.StateReason
			}
			if err := s.setState(ctx, q, who.UserID, &row, *in.State, reason, nil); err != nil {
				return err
			}
		}
		if in.Labels != nil {
			if err := s.replaceLabels(ctx, q, row, who.UserID, labels); err != nil {
				return err
			}
		}
		if in.Assignees != nil {
			if err := s.replaceAssignees(ctx, q, row, who.UserID, assignees); err != nil {
				return err
			}
		}
		if in.MilestoneID != nil {
			if err := s.setMilestone(ctx, q, who.UserID, &row, milestone); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Issue{}, err
	}
	return s.hydrateOne(ctx, who.UserID, row)
}

func (s *Service) updateContent(ctx context.Context, q *dbgen.Queries, r repos.Repo, who auth.Principal, row *dbgen.Issue,
	setTitle bool, title string, setBody bool, body string) error {
	cur := *row
	newTitle, newBody := cur.Title, cur.Body
	if setTitle {
		newTitle = title
	}
	if setBody {
		newBody = body
	}
	if newTitle == cur.Title && newBody == cur.Body {
		return nil
	}
	editedAt, editedBy := cur.EditedAt, cur.EditedBy
	if newBody != cur.Body {
		id, err := newID()
		if err != nil {
			return err
		}
		if err := q.InsertIssueEdit(ctx, dbgen.InsertIssueEditParams{ID: id, IssueID: cur.ID, EditorID: nullID(who.UserID),
			Body: cur.Body}); err != nil {
			return fmt.Errorf("record issue edit: %w", err)
		}
		editedAt, editedBy = ts(s.o.Now()), nullID(who.UserID)
	}
	updated, err := q.UpdateIssueContent(ctx, dbgen.UpdateIssueContentParams{ID: cur.ID, Title: newTitle, Body: newBody,
		EditedAt: editedAt, EditedBy: editedBy})
	if err != nil {
		return fmt.Errorf("update issue: %w", err)
	}
	if newTitle != cur.Title {
		if err := s.event(ctx, q, updated, who.UserID, "renamed", uuid.Nil, map[string]any{"from": cur.Title, "to": newTitle}); err != nil {
			return err
		}
	}
	if newBody != cur.Body {
		if err := s.recordRefs(ctx, q, r, who, refSource{issue: updated}, newBody); err != nil {
			return err
		}
	}
	*row = updated
	return s.emit(ctx, q, events.KindIssue, "edited", updated.RepoID, who.UserID, updated.ID, issuePayload(updated))
}

// setState closes or reopens row. reason applies to a close: completed
// (default), not_planned or duplicate. extra is added to the timeline data
// of a close (the commit or pull request that closed it).
func (s *Service) setState(ctx context.Context, q *dbgen.Queries, actor uuid.UUID, row *dbgen.Issue, state, reason string, extra map[string]any) error {
	cur := *row
	var p dbgen.SetIssueStateParams
	switch state {
	case StateClosed:
		switch reason {
		case "":
			reason = ReasonCompleted
		case ReasonCompleted, ReasonNotPlanned, ReasonDuplicate:
		default:
			return invalid("stateReason must be completed, not_planned or duplicate")
		}
		if cur.State == StateClosed && cur.StateReason == reason {
			return nil
		}
		p = dbgen.SetIssueStateParams{ID: cur.ID, State: StateClosed, StateReason: reason, ClosedAt: ts(s.o.Now()), ClosedBy: nullID(actor)}
	case StateOpen:
		if reason != "" && reason != ReasonReopened {
			return invalid("a reopened issue has no stateReason")
		}
		if cur.State == StateOpen {
			return nil
		}
		p = dbgen.SetIssueStateParams{ID: cur.ID, State: StateOpen, StateReason: ReasonReopened}
	}
	updated, err := q.SetIssueState(ctx, p)
	if err != nil {
		return fmt.Errorf("set issue state: %w", err)
	}
	*row = updated
	name, data := "reopened", map[string]any{}
	if state == StateClosed {
		name, data = "closed", map[string]any{"reason": reason}
		for k, v := range extra {
			data[k] = v
		}
	}
	if err := s.event(ctx, q, updated, actor, name, uuid.Nil, data); err != nil {
		return err
	}
	return s.emit(ctx, q, events.KindIssue, name, updated.RepoID, actor, updated.ID, issuePayload(updated))
}

func (s *Service) currentLabels(ctx context.Context, q *dbgen.Queries, i dbgen.Issue) ([]Label, error) {
	rows, err := q.IssueLabelsOf(ctx, []uuid.UUID{i.ID})
	if err != nil {
		return nil, fmt.Errorf("issue labels: %w", err)
	}
	out := make([]Label, 0, len(rows))
	for _, l := range rows {
		out = append(out, Label{ID: l.ID, Name: l.Name, Color: l.Color, Description: l.Description})
	}
	return out, nil
}

func (s *Service) replaceLabels(ctx context.Context, q *dbgen.Queries, i dbgen.Issue, actor uuid.UUID, want []dbgen.Label) error {
	cur, err := s.currentLabels(ctx, q, i)
	if err != nil {
		return err
	}
	keep := map[uuid.UUID]bool{}
	for _, l := range want {
		keep[l.ID] = true
	}
	for _, l := range cur {
		if !keep[l.ID] {
			if err := s.removeLabel(ctx, q, i, actor, l); err != nil {
				return err
			}
		}
	}
	for _, l := range want {
		if err := s.addLabel(ctx, q, i, actor, l); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) replaceAssignees(ctx context.Context, q *dbgen.Queries, i dbgen.Issue, actor uuid.UUID, want []dbgen.IssueUsersByNamesRow) error {
	rows, err := q.IssueAssigneesOf(ctx, []uuid.UUID{i.ID})
	if err != nil {
		return fmt.Errorf("issue assignees: %w", err)
	}
	keep := map[uuid.UUID]bool{}
	for _, u := range want {
		keep[u.ID] = true
	}
	for _, a := range rows {
		if !keep[a.UserID] {
			if err := s.removeAssignee(ctx, q, i, actor, a.UserID); err != nil {
				return err
			}
		}
	}
	for _, u := range want {
		if err := s.addAssignee(ctx, q, i, actor, u.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) setMilestone(ctx context.Context, q *dbgen.Queries, actor uuid.UUID, row *dbgen.Issue, m *dbgen.GetMilestonesByIDsRow) error {
	cur := *row
	var mid uuid.NullUUID
	if m != nil {
		mid = nullID(m.ID)
	}
	if mid == cur.MilestoneID {
		return nil
	}
	if cur.MilestoneID.Valid {
		old, err := q.GetMilestonesByIDs(ctx, dbgen.GetMilestonesByIDsParams{RepoID: cur.RepoID, Ids: []uuid.UUID{cur.MilestoneID.UUID}})
		if err != nil {
			return fmt.Errorf("get milestone: %w", err)
		}
		title := ""
		if len(old) > 0 {
			title = old[0].Title
		}
		if err := s.event(ctx, q, cur, actor, "demilestoned", uuid.Nil, map[string]any{"title": title}); err != nil {
			return err
		}
		p := issuePayload(cur)
		p.Milestone = title
		if err := s.emit(ctx, q, events.KindIssue, "demilestoned", cur.RepoID, actor, cur.ID, p); err != nil {
			return err
		}
	}
	updated, err := q.SetIssueMilestone(ctx, dbgen.SetIssueMilestoneParams{ID: cur.ID, MilestoneID: mid})
	if err != nil {
		return fmt.Errorf("set milestone: %w", err)
	}
	*row = updated
	if m == nil {
		return nil
	}
	if err := s.event(ctx, q, updated, actor, "milestoned", uuid.Nil, map[string]any{"title": m.Title}); err != nil {
		return err
	}
	p := issuePayload(updated)
	p.Milestone = m.Title
	return s.emit(ctx, q, events.KindIssue, "milestoned", updated.RepoID, actor, updated.ID, p)
}

// Lock locks the conversation of an issue (triage).
func (s *Service) Lock(ctx context.Context, who auth.Principal, owner, name string, number int64, reason string, c identity.Client) (Issue, error) {
	if !slices.Contains(lockReasons, reason) {
		return Issue{}, invalid("lockReason must be off-topic, too heated, resolved or spam")
	}
	return s.setLock(ctx, who, owner, name, number, true, reason, c)
}

// Unlock unlocks the conversation of an issue (triage).
func (s *Service) Unlock(ctx context.Context, who auth.Principal, owner, name string, number int64, c identity.Client) (Issue, error) {
	return s.setLock(ctx, who, owner, name, number, false, "", c)
}

func (s *Service) setLock(ctx context.Context, who auth.Principal, owner, name string, number int64, locked bool, reason string, c identity.Client) (Issue, error) {
	r, err := s.triager(ctx, who, owner, name)
	if err != nil {
		return Issue{}, err
	}
	var row dbgen.Issue
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		cur, err := s.lockIssue(ctx, q, r, number)
		if err != nil {
			return err
		}
		row = cur
		if cur.Locked == locked && cur.LockReason == reason {
			return nil
		}
		if row, err = q.SetIssueLock(ctx, dbgen.SetIssueLockParams{ID: cur.ID, Locked: locked, LockReason: reason}); err != nil {
			return fmt.Errorf("set issue lock: %w", err)
		}
		ev, action := "unlocked", ActionIssueUnlocked
		data := map[string]any{}
		if locked {
			ev, action, data = "locked", ActionIssueLocked, map[string]any{"reason": reason}
		}
		if err := s.event(ctx, q, row, who.UserID, ev, uuid.Nil, data); err != nil {
			return err
		}
		if err := s.emit(ctx, q, events.KindIssue, ev, r.ID, who.UserID, row.ID, issuePayload(row)); err != nil {
			return err
		}
		return audit.Record(ctx, q, s.auditEvent(who, action, r, "issue", row.ID, c,
			map[string]any{"number": row.Number, "reason": reason}))
	})
	if err != nil {
		return Issue{}, err
	}
	return s.hydrateOne(ctx, who.UserID, row)
}

// ListEdits returns the previous bodies of an issue, newest first.
func (s *Service) ListEdits(ctx context.Context, who auth.Principal, owner, name string, number int64, offset, limit int) ([]BodyEdit, error) {
	r, err := s.repo(ctx, who, owner, name)
	if err != nil {
		return nil, err
	}
	i, err := s.get(ctx, s.q, r, number)
	if err != nil {
		return nil, err
	}
	off, lim := pageArgs(offset, limit)
	rows, err := s.q.ListIssueEdits(ctx, dbgen.ListIssueEditsParams{IssueID: i.ID, Off: off, Lim: lim})
	if err != nil {
		return nil, fmt.Errorf("list issue edits: %w", err)
	}
	return s.edits(ctx, rows)
}
