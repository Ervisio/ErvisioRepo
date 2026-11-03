package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/issues"
	"github.com/Ervisio/ErvisioRepo/internal/quota"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

// Issues is the issue service used by the issue endpoints; it is
// implemented by *issues.Service.
type Issues interface {
	List(ctx context.Context, who auth.Principal, owner, name string, in issues.ListInput) ([]issues.Issue, error)
	Get(ctx context.Context, who auth.Principal, owner, name string, number int64) (issues.Issue, error)
	Create(ctx context.Context, who auth.Principal, owner, name string, in issues.CreateInput) (issues.Issue, error)
	Update(ctx context.Context, who auth.Principal, owner, name string, number int64, in issues.UpdateInput) (issues.Issue, error)
	Lock(ctx context.Context, who auth.Principal, owner, name string, number int64, reason string, c identity.Client) (issues.Issue, error)
	Unlock(ctx context.Context, who auth.Principal, owner, name string, number int64, c identity.Client) (issues.Issue, error)
	ListEdits(ctx context.Context, who auth.Principal, owner, name string, number int64, offset, limit int) ([]issues.BodyEdit, error)
	ListTimeline(ctx context.Context, who auth.Principal, owner, name string, number int64, offset, limit int) ([]issues.TimelineEvent, error)
	ListTemplates(ctx context.Context, who auth.Principal, owner, name string) (issues.Templates, error)

	ListComments(ctx context.Context, who auth.Principal, owner, name string, number int64, offset, limit int) ([]issues.Comment, error)
	GetComment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (issues.Comment, error)
	CreateComment(ctx context.Context, who auth.Principal, owner, name string, number int64, body string) (issues.Comment, error)
	UpdateComment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, body string) (issues.Comment, error)
	DeleteComment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, c identity.Client) error
	ListCommentEdits(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, offset, limit int) ([]issues.BodyEdit, error)

	ListIssueReactions(ctx context.Context, who auth.Principal, owner, name string, number int64, content string, offset, limit int) ([]issues.Reaction, error)
	AddIssueReaction(ctx context.Context, who auth.Principal, owner, name string, number int64, content string) ([]issues.ReactionGroup, error)
	RemoveIssueReaction(ctx context.Context, who auth.Principal, owner, name string, number int64, content string) ([]issues.ReactionGroup, error)
	ListCommentReactions(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, content string, offset, limit int) ([]issues.Reaction, error)
	AddCommentReaction(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, content string) ([]issues.ReactionGroup, error)
	RemoveCommentReaction(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, content string) ([]issues.ReactionGroup, error)

	ListLabels(ctx context.Context, who auth.Principal, owner, name string, offset, limit int) ([]issues.Label, error)
	CreateLabel(ctx context.Context, who auth.Principal, owner, name string, in issues.LabelInput, c identity.Client) (issues.Label, error)
	UpdateLabel(ctx context.Context, who auth.Principal, owner, name, label string, in issues.LabelInput, c identity.Client) (issues.Label, error)
	DeleteLabel(ctx context.Context, who auth.Principal, owner, name, label string, c identity.Client) error

	ListMilestones(ctx context.Context, who auth.Principal, owner, name, state string, offset, limit int) ([]issues.Milestone, error)
	GetMilestone(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (issues.Milestone, error)
	CreateMilestone(ctx context.Context, who auth.Principal, owner, name string, in issues.MilestoneInput, c identity.Client) (issues.Milestone, error)
	UpdateMilestone(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, in issues.MilestoneInput, c identity.Client) (issues.Milestone, error)
	DeleteMilestone(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, c identity.Client) error

	UploadAttachment(ctx context.Context, who auth.Principal, owner, name string, in issues.AttachmentInput) (issues.Attachment, error)
	GetAttachment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (issues.Attachment, error)
	OpenAttachment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, rng *blob.Range) (io.ReadCloser, issues.Attachment, error)
	DeleteAttachment(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, c identity.Client) error
}

var _ Issues = (*issues.Service)(nil)

var errIssuesUnavailable = problemResponse{status: http.StatusServiceUnavailable, detail: "issues are not available"}

// issuesFail maps an issue service error to a problem response.
func issuesFail(err error, method, suffix string) (issueResponse, error) {
	var ve *issues.ValidationError
	switch {
	case errors.As(err, &ve):
		return badRequest(err.Error()), nil
	case errors.Is(err, issues.ErrNotFound), errors.Is(err, issues.ErrCommentNotFound), errors.Is(err, issues.ErrLabelNotFound),
		errors.Is(err, issues.ErrMilestoneNotFound), errors.Is(err, issues.ErrAttachmentGone):
		return problemResponse{status: http.StatusNotFound, detail: err.Error()}, nil
	case errors.Is(err, issues.ErrLabelTaken), errors.Is(err, issues.ErrMilestoneTaken), quota.IsExceeded(err):
		return problemResponse{status: http.StatusConflict, detail: err.Error()}, nil
	case errors.Is(err, issues.ErrLocked):
		return problemResponse{status: http.StatusForbidden, detail: err.Error()}, nil
	case errors.Is(err, issues.ErrNoStorage):
		return problemResponse{status: http.StatusServiceUnavailable, detail: err.Error()}, nil
	}
	r, err2 := reposFail(err, method, suffix)
	if err2 != nil {
		return nil, err2
	}
	return r.(issueResponse), nil
}

// issueResponse is answered by problemResponse and movedResponse.
type issueResponse interface {
	v1gen.ListIssueTemplatesResponseObject
	v1gen.ListIssuesResponseObject
	v1gen.CreateIssueResponseObject
	v1gen.DeleteIssueAttachmentResponseObject
	v1gen.DeleteIssueCommentResponseObject
	v1gen.GetIssueCommentResponseObject
	v1gen.UpdateIssueCommentResponseObject
	v1gen.ListIssueCommentEditsResponseObject
	v1gen.ListIssueCommentReactionsResponseObject
	v1gen.AddIssueCommentReactionResponseObject
	v1gen.RemoveIssueCommentReactionResponseObject
	v1gen.GetIssueResponseObject
	v1gen.UpdateIssueResponseObject
	v1gen.ListIssueCommentsResponseObject
	v1gen.CreateIssueCommentResponseObject
	v1gen.ListIssueEditsResponseObject
	v1gen.UnlockIssueResponseObject
	v1gen.LockIssueResponseObject
	v1gen.ListIssueReactionsResponseObject
	v1gen.AddIssueReactionResponseObject
	v1gen.RemoveIssueReactionResponseObject
	v1gen.ListIssueTimelineResponseObject
	v1gen.ListLabelsResponseObject
	v1gen.CreateLabelResponseObject
	v1gen.DeleteLabelResponseObject
	v1gen.UpdateLabelResponseObject
	v1gen.ListMilestonesResponseObject
	v1gen.CreateMilestoneResponseObject
	v1gen.DeleteMilestoneResponseObject
	v1gen.GetMilestoneResponseObject
	v1gen.UpdateMilestoneResponseObject
}

var (
	_ issueResponse = problemResponse{}
	_ issueResponse = movedResponse{}
)

func issuesSuffix(tail string) string { return "/issues" + tail }

func numSuffix(n int64, tail string) string {
	return issuesSuffix("/" + strconv.FormatInt(n, 10) + tail)
}

func commentSuffix(id uuid.UUID, tail string) string {
	return issuesSuffix("/comments/" + id.String() + tail)
}

// ---- DTOs ----

func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nonNilStrings(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

func labelDTO(l issues.Label) v1gen.Label {
	return v1gen.Label{Id: l.ID, Name: l.Name, Color: l.Color, Description: l.Description}
}

func milestoneDTO(m issues.Milestone) v1gen.Milestone {
	return v1gen.Milestone{Id: m.ID, Title: m.Title, Description: m.Description, State: v1gen.MilestoneState(m.State),
		DueOn: m.DueOn, OpenIssues: m.OpenIssues, ClosedIssues: m.ClosedIssues, CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt, ClosedAt: m.ClosedAt}
}

func groupsDTO(gs []issues.ReactionGroup) []v1gen.ReactionGroup {
	out := make([]v1gen.ReactionGroup, 0, len(gs))
	for _, g := range gs {
		out = append(out, v1gen.ReactionGroup{Content: g.Content, Count: g.Count, ViewerReacted: g.ViewerReacted})
	}
	return out
}

func issueDTO(i issues.Issue) v1gen.Issue {
	labels := make([]v1gen.Label, 0, len(i.Labels))
	for _, l := range i.Labels {
		labels = append(labels, labelDTO(l))
	}
	out := v1gen.Issue{Id: i.ID, Number: i.Number, Title: i.Title, Body: i.Body, State: v1gen.IssueState(i.State),
		StateReason: i.StateReason, Locked: i.Locked, LockReason: i.LockReason, Author: i.Author, Labels: labels,
		Assignees: nonNilStrings(i.Assignees), CommentCount: i.CommentCount, Reactions: groupsDTO(i.Reactions),
		CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt, ClosedAt: i.ClosedAt, ClosedBy: optStr(i.ClosedBy), EditedAt: i.EditedAt}
	if i.Milestone != nil {
		m := milestoneDTO(*i.Milestone)
		out.Milestone = &m
	}
	return out
}

func commentDTO(c issues.Comment) v1gen.IssueComment {
	return v1gen.IssueComment{Id: c.ID, IssueNumber: c.IssueNumber, Author: c.Author, Body: c.Body, CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt, EditedAt: c.EditedAt, EditedBy: optStr(c.EditedBy), Reactions: groupsDTO(c.Reactions)}
}

func editsDTO(es []issues.BodyEdit) []v1gen.BodyEdit {
	out := make([]v1gen.BodyEdit, 0, len(es))
	for _, e := range es {
		out = append(out, v1gen.BodyEdit{Id: e.ID, Editor: e.Editor, Body: e.Body, CreatedAt: e.CreatedAt})
	}
	return out
}

func reactionsDTO(rs []issues.Reaction) []v1gen.Reaction {
	out := make([]v1gen.Reaction, 0, len(rs))
	for _, r := range rs {
		out = append(out, v1gen.Reaction{Id: r.ID, Content: r.Content, User: r.User, CreatedAt: r.CreatedAt})
	}
	return out
}

func timelineDTO(t issues.TimelineEvent) v1gen.TimelineEvent {
	out := v1gen.TimelineEvent{Id: t.ID, Event: t.Event, Actor: t.Actor, CreatedAt: t.CreatedAt,
		Assignee: optStr(t.Assignee), Milestone: optStr(t.Milestone), RenameFrom: optStr(t.RenameFrom),
		RenameTo: optStr(t.RenameTo), StateReason: optStr(t.StateReason), LockReason: optStr(t.LockReason)}
	if t.Event == "renamed" {
		out.RenameFrom, out.RenameTo = &t.RenameFrom, &t.RenameTo
	}
	if t.Label != nil {
		l := labelDTO(*t.Label)
		out.Label = &l
	}
	if t.Comment != nil {
		c := commentDTO(*t.Comment)
		out.Comment = &c
	}
	if src := t.Source; src != nil {
		ts := v1gen.TimelineSource{Kind: v1gen.TimelineSourceKind(src.Kind), Redacted: src.Redacted}
		if !src.Redacted {
			ts.Repo, ts.Title, ts.Commit = optStr(src.Repo), optStr(src.Title), optStr(src.Commit)
			if src.Number > 0 {
				n := src.Number
				ts.Number = &n
			}
		}
		out.Source = &ts
	}
	return out
}

func templatesDTO(t issues.Templates) v1gen.IssueTemplates {
	links := make([]v1gen.IssueContactLink, 0, len(t.Config.ContactLinks))
	for _, l := range t.Config.ContactLinks {
		links = append(links, v1gen.IssueContactLink{Name: l.Name, Url: l.URL, About: l.About})
	}
	out := v1gen.IssueTemplates{Config: v1gen.IssueTemplateConfig{BlankIssuesEnabled: t.Config.BlankIssuesEnabled, ContactLinks: links},
		Templates: make([]v1gen.IssueTemplate, 0, len(t.Templates))}
	for _, tp := range t.Templates {
		it := v1gen.IssueTemplate{FileName: tp.FileName, Kind: v1gen.IssueTemplateKind(tp.Kind), Name: tp.Name, About: tp.About,
			Title: tp.Title, Labels: nonNilStrings(tp.Labels), Assignees: nonNilStrings(tp.Assignees), Body: tp.Body}
		if tp.Form != nil {
			f := tp.Form
			it.Form = &f
		}
		out.Templates = append(out.Templates, it)
	}
	return out
}

// issueAttachmentDTO is the IssueAttachment schema, returned by the upload
// route outside the generated server.
type issueAttachmentDTO struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	ContentType string    `json:"contentType"`
	URL         string    `json:"url"`
	CreatedAt   time.Time `json:"createdAt"`
}

func attachmentURL(owner, name string, id uuid.UUID) string {
	return "/api/v1/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/issues/attachments/" + id.String() + "/download"
}

// ---- issues ----

func (s *v1Server) ListIssues(ctx context.Context, req v1gen.ListIssuesRequestObject) (v1gen.ListIssuesResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	p := req.Params
	offset, limit := page(p.Offset, p.Limit)
	in := issues.ListInput{Milestone: deref(p.Milestone), Assignee: deref(p.Assignee), Author: deref(p.Author),
		Mentioned: deref(p.Mentioned), Query: deref(p.Q), Offset: offset, Limit: limit}
	if p.State != nil {
		in.State = string(*p.State)
	}
	if p.Sort != nil {
		in.Sort = string(*p.Sort)
	}
	if p.Direction != nil {
		in.Ascending = *p.Direction == v1gen.Asc
	}
	if p.Labels != nil {
		for _, l := range strings.Split(*p.Labels, ",") {
			if l = strings.TrimSpace(l); l != "" {
				in.Labels = append(in.Labels, l)
			}
		}
	}
	list, err := s.issues.List(ctx, principal(ctx), req.Owner, req.Name, in)
	if err != nil {
		return issuesFail(err, http.MethodGet, issuesSuffix(""))
	}
	out := make(v1gen.ListIssues200JSONResponse, 0, len(list))
	for _, i := range list {
		out = append(out, issueDTO(i))
	}
	return out, nil
}

func (s *v1Server) CreateIssue(ctx context.Context, req v1gen.CreateIssueRequestObject) (v1gen.CreateIssueResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	b := req.Body
	in := issues.CreateInput{Title: b.Title, Body: deref(b.Body), Labels: deref(b.Labels), Assignees: deref(b.Assignees),
		MilestoneID: b.MilestoneId}
	i, err := s.issues.Create(ctx, principal(ctx), req.Owner, req.Name, in)
	if err != nil {
		return issuesFail(err, http.MethodPost, issuesSuffix(""))
	}
	return v1gen.CreateIssue201JSONResponse(issueDTO(i)), nil
}

func (s *v1Server) GetIssue(ctx context.Context, req v1gen.GetIssueRequestObject) (v1gen.GetIssueResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	i, err := s.issues.Get(ctx, principal(ctx), req.Owner, req.Name, req.Number)
	if err != nil {
		return issuesFail(err, http.MethodGet, numSuffix(req.Number, ""))
	}
	return v1gen.GetIssue200JSONResponse(issueDTO(i)), nil
}

func (s *v1Server) UpdateIssue(ctx context.Context, req v1gen.UpdateIssueRequestObject) (v1gen.UpdateIssueResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	b := req.Body
	in := issues.UpdateInput{Title: b.Title, Body: b.Body, StateReason: b.StateReason, Labels: b.Labels,
		Assignees: b.Assignees, MilestoneID: b.MilestoneId}
	if b.State != nil {
		st := string(*b.State)
		in.State = &st
	}
	i, err := s.issues.Update(ctx, principal(ctx), req.Owner, req.Name, req.Number, in)
	if err != nil {
		return issuesFail(err, http.MethodPatch, numSuffix(req.Number, ""))
	}
	return v1gen.UpdateIssue200JSONResponse(issueDTO(i)), nil
}

func (s *v1Server) LockIssue(ctx context.Context, req v1gen.LockIssueRequestObject) (v1gen.LockIssueResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	reason := ""
	if req.Body != nil {
		reason = deref(req.Body.LockReason)
	}
	i, err := s.issues.Lock(ctx, principal(ctx), req.Owner, req.Name, req.Number, reason, clientOf(ctx))
	if err != nil {
		return issuesFail(err, http.MethodPut, numSuffix(req.Number, "/lock"))
	}
	return v1gen.LockIssue200JSONResponse(issueDTO(i)), nil
}

func (s *v1Server) UnlockIssue(ctx context.Context, req v1gen.UnlockIssueRequestObject) (v1gen.UnlockIssueResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	i, err := s.issues.Unlock(ctx, principal(ctx), req.Owner, req.Name, req.Number, clientOf(ctx))
	if err != nil {
		return issuesFail(err, http.MethodDelete, numSuffix(req.Number, "/lock"))
	}
	return v1gen.UnlockIssue200JSONResponse(issueDTO(i)), nil
}

func (s *v1Server) ListIssueEdits(ctx context.Context, req v1gen.ListIssueEditsRequestObject) (v1gen.ListIssueEditsResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit)
	list, err := s.issues.ListEdits(ctx, principal(ctx), req.Owner, req.Name, req.Number, offset, limit)
	if err != nil {
		return issuesFail(err, http.MethodGet, numSuffix(req.Number, "/edits"))
	}
	return v1gen.ListIssueEdits200JSONResponse(editsDTO(list)), nil
}

func (s *v1Server) ListIssueTimeline(ctx context.Context, req v1gen.ListIssueTimelineRequestObject) (v1gen.ListIssueTimelineResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit)
	list, err := s.issues.ListTimeline(ctx, principal(ctx), req.Owner, req.Name, req.Number, offset, limit)
	if err != nil {
		return issuesFail(err, http.MethodGet, numSuffix(req.Number, "/timeline"))
	}
	out := make(v1gen.ListIssueTimeline200JSONResponse, 0, len(list))
	for _, t := range list {
		out = append(out, timelineDTO(t))
	}
	return out, nil
}

func (s *v1Server) ListIssueTemplates(ctx context.Context, req v1gen.ListIssueTemplatesRequestObject) (v1gen.ListIssueTemplatesResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	t, err := s.issues.ListTemplates(ctx, principal(ctx), req.Owner, req.Name)
	if err != nil {
		return issuesFail(err, http.MethodGet, issuesSuffix("/templates"))
	}
	return v1gen.ListIssueTemplates200JSONResponse(templatesDTO(t)), nil
}

// ---- comments ----

func (s *v1Server) ListIssueComments(ctx context.Context, req v1gen.ListIssueCommentsRequestObject) (v1gen.ListIssueCommentsResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit)
	list, err := s.issues.ListComments(ctx, principal(ctx), req.Owner, req.Name, req.Number, offset, limit)
	if err != nil {
		return issuesFail(err, http.MethodGet, numSuffix(req.Number, "/comments"))
	}
	out := make(v1gen.ListIssueComments200JSONResponse, 0, len(list))
	for _, c := range list {
		out = append(out, commentDTO(c))
	}
	return out, nil
}

func (s *v1Server) CreateIssueComment(ctx context.Context, req v1gen.CreateIssueCommentRequestObject) (v1gen.CreateIssueCommentResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	c, err := s.issues.CreateComment(ctx, principal(ctx), req.Owner, req.Name, req.Number, req.Body.Body)
	if err != nil {
		return issuesFail(err, http.MethodPost, numSuffix(req.Number, "/comments"))
	}
	return v1gen.CreateIssueComment201JSONResponse(commentDTO(c)), nil
}

func (s *v1Server) GetIssueComment(ctx context.Context, req v1gen.GetIssueCommentRequestObject) (v1gen.GetIssueCommentResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	c, err := s.issues.GetComment(ctx, principal(ctx), req.Owner, req.Name, req.CommentId)
	if err != nil {
		return issuesFail(err, http.MethodGet, commentSuffix(req.CommentId, ""))
	}
	return v1gen.GetIssueComment200JSONResponse(commentDTO(c)), nil
}

func (s *v1Server) UpdateIssueComment(ctx context.Context, req v1gen.UpdateIssueCommentRequestObject) (v1gen.UpdateIssueCommentResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	c, err := s.issues.UpdateComment(ctx, principal(ctx), req.Owner, req.Name, req.CommentId, req.Body.Body)
	if err != nil {
		return issuesFail(err, http.MethodPatch, commentSuffix(req.CommentId, ""))
	}
	return v1gen.UpdateIssueComment200JSONResponse(commentDTO(c)), nil
}

func (s *v1Server) DeleteIssueComment(ctx context.Context, req v1gen.DeleteIssueCommentRequestObject) (v1gen.DeleteIssueCommentResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	if err := s.issues.DeleteComment(ctx, principal(ctx), req.Owner, req.Name, req.CommentId, clientOf(ctx)); err != nil {
		return issuesFail(err, http.MethodDelete, commentSuffix(req.CommentId, ""))
	}
	return v1gen.DeleteIssueComment204Response{}, nil
}

func (s *v1Server) ListIssueCommentEdits(ctx context.Context, req v1gen.ListIssueCommentEditsRequestObject) (v1gen.ListIssueCommentEditsResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit)
	list, err := s.issues.ListCommentEdits(ctx, principal(ctx), req.Owner, req.Name, req.CommentId, offset, limit)
	if err != nil {
		return issuesFail(err, http.MethodGet, commentSuffix(req.CommentId, "/edits"))
	}
	return v1gen.ListIssueCommentEdits200JSONResponse(editsDTO(list)), nil
}

// ---- reactions ----

func (s *v1Server) ListIssueReactions(ctx context.Context, req v1gen.ListIssueReactionsRequestObject) (v1gen.ListIssueReactionsResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit)
	list, err := s.issues.ListIssueReactions(ctx, principal(ctx), req.Owner, req.Name, req.Number, deref(req.Params.Content), offset, limit)
	if err != nil {
		return issuesFail(err, http.MethodGet, numSuffix(req.Number, "/reactions"))
	}
	return v1gen.ListIssueReactions200JSONResponse(reactionsDTO(list)), nil
}

func (s *v1Server) AddIssueReaction(ctx context.Context, req v1gen.AddIssueReactionRequestObject) (v1gen.AddIssueReactionResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	gs, err := s.issues.AddIssueReaction(ctx, principal(ctx), req.Owner, req.Name, req.Number, req.Body.Content)
	if err != nil {
		return issuesFail(err, http.MethodPost, numSuffix(req.Number, "/reactions"))
	}
	return v1gen.AddIssueReaction200JSONResponse(groupsDTO(gs)), nil
}

func (s *v1Server) RemoveIssueReaction(ctx context.Context, req v1gen.RemoveIssueReactionRequestObject) (v1gen.RemoveIssueReactionResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	gs, err := s.issues.RemoveIssueReaction(ctx, principal(ctx), req.Owner, req.Name, req.Number, req.Content)
	if err != nil {
		return issuesFail(err, http.MethodDelete, numSuffix(req.Number, "/reactions/"+url.PathEscape(req.Content)))
	}
	return v1gen.RemoveIssueReaction200JSONResponse(groupsDTO(gs)), nil
}

func (s *v1Server) ListIssueCommentReactions(ctx context.Context, req v1gen.ListIssueCommentReactionsRequestObject) (v1gen.ListIssueCommentReactionsResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit)
	list, err := s.issues.ListCommentReactions(ctx, principal(ctx), req.Owner, req.Name, req.CommentId, deref(req.Params.Content), offset, limit)
	if err != nil {
		return issuesFail(err, http.MethodGet, commentSuffix(req.CommentId, "/reactions"))
	}
	return v1gen.ListIssueCommentReactions200JSONResponse(reactionsDTO(list)), nil
}

func (s *v1Server) AddIssueCommentReaction(ctx context.Context, req v1gen.AddIssueCommentReactionRequestObject) (v1gen.AddIssueCommentReactionResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	gs, err := s.issues.AddCommentReaction(ctx, principal(ctx), req.Owner, req.Name, req.CommentId, req.Body.Content)
	if err != nil {
		return issuesFail(err, http.MethodPost, commentSuffix(req.CommentId, "/reactions"))
	}
	return v1gen.AddIssueCommentReaction200JSONResponse(groupsDTO(gs)), nil
}

func (s *v1Server) RemoveIssueCommentReaction(ctx context.Context, req v1gen.RemoveIssueCommentReactionRequestObject) (v1gen.RemoveIssueCommentReactionResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	gs, err := s.issues.RemoveCommentReaction(ctx, principal(ctx), req.Owner, req.Name, req.CommentId, req.Content)
	if err != nil {
		return issuesFail(err, http.MethodDelete, commentSuffix(req.CommentId, "/reactions/"+url.PathEscape(req.Content)))
	}
	return v1gen.RemoveIssueCommentReaction200JSONResponse(groupsDTO(gs)), nil
}

// ---- labels ----

func (s *v1Server) ListLabels(ctx context.Context, req v1gen.ListLabelsRequestObject) (v1gen.ListLabelsResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit)
	list, err := s.issues.ListLabels(ctx, principal(ctx), req.Owner, req.Name, offset, limit)
	if err != nil {
		return issuesFail(err, http.MethodGet, "/labels")
	}
	out := make(v1gen.ListLabels200JSONResponse, 0, len(list))
	for _, l := range list {
		out = append(out, labelDTO(l))
	}
	return out, nil
}

func (s *v1Server) CreateLabel(ctx context.Context, req v1gen.CreateLabelRequestObject) (v1gen.CreateLabelResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	b := req.Body
	l, err := s.issues.CreateLabel(ctx, principal(ctx), req.Owner, req.Name,
		issues.LabelInput{Name: &b.Name, Color: &b.Color, Description: b.Description}, clientOf(ctx))
	if err != nil {
		return issuesFail(err, http.MethodPost, "/labels")
	}
	return v1gen.CreateLabel201JSONResponse(labelDTO(l)), nil
}

func (s *v1Server) UpdateLabel(ctx context.Context, req v1gen.UpdateLabelRequestObject) (v1gen.UpdateLabelResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	b := req.Body
	l, err := s.issues.UpdateLabel(ctx, principal(ctx), req.Owner, req.Name, req.Label,
		issues.LabelInput{Name: b.Name, Color: b.Color, Description: b.Description}, clientOf(ctx))
	if err != nil {
		return issuesFail(err, http.MethodPatch, "/labels/"+url.PathEscape(req.Label))
	}
	return v1gen.UpdateLabel200JSONResponse(labelDTO(l)), nil
}

func (s *v1Server) DeleteLabel(ctx context.Context, req v1gen.DeleteLabelRequestObject) (v1gen.DeleteLabelResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	if err := s.issues.DeleteLabel(ctx, principal(ctx), req.Owner, req.Name, req.Label, clientOf(ctx)); err != nil {
		return issuesFail(err, http.MethodDelete, "/labels/"+url.PathEscape(req.Label))
	}
	return v1gen.DeleteLabel204Response{}, nil
}

// ---- milestones ----

func (s *v1Server) ListMilestones(ctx context.Context, req v1gen.ListMilestonesRequestObject) (v1gen.ListMilestonesResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit)
	state := ""
	if req.Params.State != nil {
		state = string(*req.Params.State)
	}
	list, err := s.issues.ListMilestones(ctx, principal(ctx), req.Owner, req.Name, state, offset, limit)
	if err != nil {
		return issuesFail(err, http.MethodGet, "/milestones")
	}
	out := make(v1gen.ListMilestones200JSONResponse, 0, len(list))
	for _, m := range list {
		out = append(out, milestoneDTO(m))
	}
	return out, nil
}

func (s *v1Server) CreateMilestone(ctx context.Context, req v1gen.CreateMilestoneRequestObject) (v1gen.CreateMilestoneResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	b := req.Body
	in := issues.MilestoneInput{Title: &b.Title, Description: b.Description}
	if b.DueOn != nil {
		due := b.DueOn.Format(time.RFC3339)
		in.DueOn = &due
	}
	m, err := s.issues.CreateMilestone(ctx, principal(ctx), req.Owner, req.Name, in, clientOf(ctx))
	if err != nil {
		return issuesFail(err, http.MethodPost, "/milestones")
	}
	return v1gen.CreateMilestone201JSONResponse(milestoneDTO(m)), nil
}

func (s *v1Server) GetMilestone(ctx context.Context, req v1gen.GetMilestoneRequestObject) (v1gen.GetMilestoneResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	m, err := s.issues.GetMilestone(ctx, principal(ctx), req.Owner, req.Name, req.MilestoneId)
	if err != nil {
		return issuesFail(err, http.MethodGet, "/milestones/"+req.MilestoneId.String())
	}
	return v1gen.GetMilestone200JSONResponse(milestoneDTO(m)), nil
}

func (s *v1Server) UpdateMilestone(ctx context.Context, req v1gen.UpdateMilestoneRequestObject) (v1gen.UpdateMilestoneResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	b := req.Body
	in := issues.MilestoneInput{Title: b.Title, Description: b.Description, DueOn: b.DueOn}
	if b.State != nil {
		st := string(*b.State)
		in.State = &st
	}
	m, err := s.issues.UpdateMilestone(ctx, principal(ctx), req.Owner, req.Name, req.MilestoneId, in, clientOf(ctx))
	if err != nil {
		return issuesFail(err, http.MethodPatch, "/milestones/"+req.MilestoneId.String())
	}
	return v1gen.UpdateMilestone200JSONResponse(milestoneDTO(m)), nil
}

func (s *v1Server) DeleteMilestone(ctx context.Context, req v1gen.DeleteMilestoneRequestObject) (v1gen.DeleteMilestoneResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	if err := s.issues.DeleteMilestone(ctx, principal(ctx), req.Owner, req.Name, req.MilestoneId, clientOf(ctx)); err != nil {
		return issuesFail(err, http.MethodDelete, "/milestones/"+req.MilestoneId.String())
	}
	return v1gen.DeleteMilestone204Response{}, nil
}

// ---- attachments ----

func (s *v1Server) DeleteIssueAttachment(ctx context.Context, req v1gen.DeleteIssueAttachmentRequestObject) (v1gen.DeleteIssueAttachmentResponseObject, error) {
	if s.issues == nil {
		return errIssuesUnavailable, nil
	}
	if err := s.issues.DeleteAttachment(ctx, principal(ctx), req.Owner, req.Name, req.AttachmentId, clientOf(ctx)); err != nil {
		return issuesFail(err, http.MethodDelete, issuesSuffix("/attachments/"+req.AttachmentId.String()))
	}
	return v1gen.DeleteIssueAttachment204Response{}, nil
}

// ---- streaming attachment routes (outside OpenAPI and the request timeout) ----

const (
	issueAttachmentUploadPath   = "/api/v1/repos/{owner}/{name}/issues/attachments"
	issueAttachmentDownloadPath = "/api/v1/repos/{owner}/{name}/issues/attachments/{attachmentId}/download"
)

// mountIssues mounts the attachment upload (POST, raw body, Content-Length
// required, ?name=) and download (GET, one byte range supported) routes.
func mountIssues(r chi.Router, d Deps) {
	r.With(d.Auth.LoadSession, d.Auth.CSRF).Post(issueAttachmentUploadPath, func(w http.ResponseWriter, req *http.Request) {
		uploadIssueAttachment(w, req, d)
	})
	r.With(d.Auth.LoadSession).Get(issueAttachmentDownloadPath, func(w http.ResponseWriter, req *http.Request) {
		downloadIssueAttachment(w, req, d)
	})
}

// issueStreamFail answers a service error on an attachment route.
func issueStreamFail(w http.ResponseWriter, req *http.Request, d Deps, err error) {
	r, ferr := issuesFail(err, req.Method, "")
	if ferr != nil {
		d.Log.ErrorContext(req.Context(), "issue attachment request failed", "error", ferr)
		writeProblem(w, http.StatusInternalServerError, "")
		return
	}
	switch v := r.(type) {
	case problemResponse:
		writeProblem(w, v.status, v.detail)
	default:
		writeProblem(w, http.StatusNotFound, "repository not found")
	}
}

func uploadIssueAttachment(w http.ResponseWriter, req *http.Request, d Deps) {
	if d.Issues == nil {
		writeProblem(w, http.StatusServiceUnavailable, "issues are not available")
		return
	}
	p, ok := streamPrincipal(w, req, "repo:write")
	if !ok {
		return
	}
	if p.UserID == uuid.Nil {
		writeProblem(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if req.ContentLength < 0 {
		writeProblem(w, http.StatusLengthRequired, "Content-Length is required")
		return
	}
	owner, name := chi.URLParam(req, "owner"), chi.URLParam(req, "name")
	in := issues.AttachmentInput{Name: req.URL.Query().Get("name"), ContentType: req.Header.Get("Content-Type"),
		Size: req.ContentLength, Body: req.Body}
	a, err := d.Issues.UploadAttachment(req.Context(), p, owner, name, in)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeProblem(w, http.StatusRequestEntityTooLarge, "attachment is too large")
			return
		}
		issueStreamFail(w, req, d, err)
		return
	}
	u := attachmentURL(owner, name, a.ID)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Location", u)
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(issueAttachmentDTO{ID: a.ID, Name: a.Name, Size: a.Size, ContentType: a.ContentType,
		URL: u, CreatedAt: a.CreatedAt})
}

// inlineTypes are served inline (images only, never SVG or HTML); anything
// else is a download.
func inlineType(ct string) bool {
	switch ct {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/avif":
		return true
	}
	return false
}

func downloadIssueAttachment(w http.ResponseWriter, req *http.Request, d Deps) {
	if d.Issues == nil {
		writeProblem(w, http.StatusServiceUnavailable, "issues are not available")
		return
	}
	p, ok := streamPrincipal(w, req, "repo:read")
	if !ok {
		return
	}
	id, ok := uuidParam(req, "attachmentId")
	if !ok {
		writeProblem(w, http.StatusNotFound, "attachment not found")
		return
	}
	owner, name := chi.URLParam(req, "owner"), chi.URLParam(req, "name")
	ctx := req.Context()
	meta, err := d.Issues.GetAttachment(ctx, p, owner, name, id)
	if err != nil {
		issueStreamFail(w, req, d, err)
		return
	}
	var rng *blob.Range
	status := http.StatusOK
	if r, ok, unsat := parseRange(req.Header.Get("Range"), meta.Size); unsat {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", meta.Size))
		writeProblem(w, http.StatusRequestedRangeNotSatisfiable, "")
		return
	} else if ok {
		rng, status = &r, http.StatusPartialContent
	}
	rc, _, err := d.Issues.OpenAttachment(ctx, p, owner, name, id, rng)
	if err != nil {
		issueStreamFail(w, req, d, err)
		return
	}
	defer func() { _ = rc.Close() }()
	h := w.Header()
	disposition := "attachment"
	if inlineType(meta.ContentType) {
		disposition = "inline"
	}
	h.Set("Content-Type", meta.ContentType)
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": meta.Name}))
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Accept-Ranges", "bytes")
	h.Set("ETag", `"`+meta.ID.String()+`"`)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, max-age=0")
	length := meta.Size
	if rng != nil {
		length = rng.Length
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", rng.Offset, rng.Offset+rng.Length-1, meta.Size))
	}
	h.Set("Content-Length", strconv.FormatInt(length, 10))
	w.WriteHeader(status)
	if req.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, rc); err != nil {
		d.Log.WarnContext(ctx, "stream issue attachment", "attachment", id, "error", err)
	}
}

// Visit methods let problemResponse and movedResponse answer every issue
// operation.
func (p problemResponse) VisitListIssueTemplatesResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListIssuesResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitCreateIssueResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitDeleteIssueAttachmentResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitDeleteIssueCommentResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitGetIssueCommentResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitUpdateIssueCommentResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListIssueCommentEditsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListIssueCommentReactionsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitAddIssueCommentReactionResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitRemoveIssueCommentReactionResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitGetIssueResponse(w http.ResponseWriter) error    { return p.write(w) }
func (p problemResponse) VisitUpdateIssueResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitListIssueCommentsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitCreateIssueCommentResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListIssueEditsResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitUnlockIssueResponse(w http.ResponseWriter) error    { return p.write(w) }
func (p problemResponse) VisitLockIssueResponse(w http.ResponseWriter) error      { return p.write(w) }
func (p problemResponse) VisitListIssueReactionsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitAddIssueReactionResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitRemoveIssueReactionResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListIssueTimelineResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListLabelsResponse(w http.ResponseWriter) error      { return p.write(w) }
func (p problemResponse) VisitCreateLabelResponse(w http.ResponseWriter) error     { return p.write(w) }
func (p problemResponse) VisitDeleteLabelResponse(w http.ResponseWriter) error     { return p.write(w) }
func (p problemResponse) VisitUpdateLabelResponse(w http.ResponseWriter) error     { return p.write(w) }
func (p problemResponse) VisitListMilestonesResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitCreateMilestoneResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitDeleteMilestoneResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitGetMilestoneResponse(w http.ResponseWriter) error    { return p.write(w) }
func (p problemResponse) VisitUpdateMilestoneResponse(w http.ResponseWriter) error { return p.write(w) }
func (m movedResponse) VisitListIssueTemplatesResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitListIssuesResponse(w http.ResponseWriter) error  { return m.write(w) }
func (m movedResponse) VisitCreateIssueResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitDeleteIssueAttachmentResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitDeleteIssueCommentResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitGetIssueCommentResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitUpdateIssueCommentResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitListIssueCommentEditsResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitListIssueCommentReactionsResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitAddIssueCommentReactionResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitRemoveIssueCommentReactionResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitGetIssueResponse(w http.ResponseWriter) error          { return m.write(w) }
func (m movedResponse) VisitUpdateIssueResponse(w http.ResponseWriter) error       { return m.write(w) }
func (m movedResponse) VisitListIssueCommentsResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitCreateIssueCommentResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitListIssueEditsResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitUnlockIssueResponse(w http.ResponseWriter) error    { return m.write(w) }
func (m movedResponse) VisitLockIssueResponse(w http.ResponseWriter) error      { return m.write(w) }
func (m movedResponse) VisitListIssueReactionsResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitAddIssueReactionResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitRemoveIssueReactionResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitListIssueTimelineResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitListLabelsResponse(w http.ResponseWriter) error        { return m.write(w) }
func (m movedResponse) VisitCreateLabelResponse(w http.ResponseWriter) error       { return m.write(w) }
func (m movedResponse) VisitDeleteLabelResponse(w http.ResponseWriter) error       { return m.write(w) }
func (m movedResponse) VisitUpdateLabelResponse(w http.ResponseWriter) error       { return m.write(w) }
func (m movedResponse) VisitListMilestonesResponse(w http.ResponseWriter) error    { return m.write(w) }
func (m movedResponse) VisitCreateMilestoneResponse(w http.ResponseWriter) error   { return m.write(w) }
func (m movedResponse) VisitDeleteMilestoneResponse(w http.ResponseWriter) error   { return m.write(w) }
func (m movedResponse) VisitGetMilestoneResponse(w http.ResponseWriter) error      { return m.write(w) }
func (m movedResponse) VisitUpdateMilestoneResponse(w http.ResponseWriter) error   { return m.write(w) }
