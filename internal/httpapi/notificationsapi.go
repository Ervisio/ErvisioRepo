package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/notify"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

var errNotificationsUnavailable = problemResponse{status: http.StatusServiceUnavailable,
	detail: "notifications are not available"}

// notifyProblem maps a notification service error to a problem response.
func notifyProblem(err error) (problemResponse, bool) {
	var ve *notify.ValidationError
	switch {
	case errors.As(err, &ve):
		return badRequest(err.Error()), true
	case errors.Is(err, notify.ErrUnauthenticated):
		return problemResponse{status: http.StatusUnauthorized, detail: err.Error()}, true
	case errors.Is(err, notify.ErrNotFound):
		return problemResponse{status: http.StatusNotFound, detail: err.Error()}, true
	}
	return problemResponse{}, false
}

// notifyFail maps an error of a notification endpoint, including the
// errors of a repository named by a query parameter.
func notifyFail(err error) (notifyResponse, error) {
	if p, ok := notifyProblem(err); ok {
		return p, nil
	}
	if p, ok := repoParamProblem(err); ok {
		return p, nil
	}
	return nil, err
}

// repoParamProblem maps the error of a repository named by a query
// parameter (owner/name): no redirect for a renamed one.
func repoParamProblem(err error) (problemResponse, bool) {
	var mv *repos.MovedError
	var ve *repos.ValidationError
	switch {
	case errors.As(err, &mv):
		return problemResponse{status: http.StatusNotFound,
			detail: "the repository was renamed to " + mv.Owner + "/" + mv.Name}, true
	case errors.As(err, &ve):
		return badRequest(err.Error()), true
	case errors.Is(err, repos.ErrNotFound):
		return problemResponse{status: http.StatusNotFound, detail: err.Error()}, true
	case errors.Is(err, repos.ErrForbidden):
		return problemResponse{status: http.StatusForbidden, detail: err.Error()}, true
	}
	return problemResponse{}, false
}

// splitRepoParam splits an owner/name query parameter.
func splitRepoParam(v string) (owner, name string, ok bool) {
	owner, name, ok = strings.Cut(v, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	return owner, name, true
}

// hasMoreHeader is the value of the X-Has-More response header.
func hasMoreHeader(more bool) string { return strconv.FormatBool(more) }

// repoSubFail maps an error of a repository subscription endpoint.
func repoSubFail(err error, method string) (repoSubResponse, error) {
	if p, ok := notifyProblem(err); ok {
		return p, nil
	}
	r, err2 := reposFail(err, method, "/subscription")
	if err2 != nil {
		return nil, err2
	}
	return r.(repoSubResponse), nil
}

// notifyResponse is answered by problemResponse.
type notifyResponse interface {
	v1gen.ListNotificationsResponseObject
	v1gen.CountNotificationsResponseObject
	v1gen.MarkNotificationsReadResponseObject
	v1gen.GetNotificationSettingsResponseObject
	v1gen.SetNotificationSettingsResponseObject
	v1gen.GetNotificationThreadResponseObject
	v1gen.MarkNotificationThreadReadResponseObject
	v1gen.MarkNotificationThreadDoneResponseObject
	v1gen.GetThreadSubscriptionResponseObject
	v1gen.SetThreadSubscriptionResponseObject
	v1gen.DeleteThreadSubscriptionResponseObject
	v1gen.ListNotificationReposResponseObject
	v1gen.GetNotificationSubjectResponseObject
	v1gen.SetNotificationSubjectSubscriptionResponseObject
	v1gen.DeleteNotificationSubjectSubscriptionResponseObject
}

// repoSubResponse is answered by problemResponse and movedResponse.
type repoSubResponse interface {
	v1gen.GetRepoSubscriptionResponseObject
	v1gen.SetRepoSubscriptionResponseObject
}

func (p problemResponse) VisitListNotificationsResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitCountNotificationsResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitMarkNotificationsReadResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitGetNotificationSettingsResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitSetNotificationSettingsResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitGetNotificationThreadResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitMarkNotificationThreadReadResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitMarkNotificationThreadDoneResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitGetThreadSubscriptionResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitSetThreadSubscriptionResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitDeleteThreadSubscriptionResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitListNotificationReposResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitGetNotificationSubjectResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitSetNotificationSubjectSubscriptionResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitDeleteNotificationSubjectSubscriptionResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitGetRepoSubscriptionResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitSetRepoSubscriptionResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (m movedResponse) VisitGetRepoSubscriptionResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitSetRepoSubscriptionResponse(w http.ResponseWriter) error {
	return m.write(w)
}

var (
	_ notifyResponse  = problemResponse{}
	_ repoSubResponse = problemResponse{}
	_ repoSubResponse = movedResponse{}
)

func threadDTO(t notify.Thread) v1gen.NotificationThread {
	return v1gen.NotificationThread{Id: t.ID, RepoId: t.RepoID, Owner: t.Owner, Repo: t.Repo,
		SubjectType: v1gen.NotificationThreadSubjectType(t.SubjectType), Number: t.Number, Title: t.Title,
		Reason: v1gen.NotificationReason(t.Reason), Summary: t.Summary, Unread: t.Unread,
		LastReadAt: t.LastReadAt, UpdatedAt: t.UpdatedAt}
}

func threadSubDTO(t notify.ThreadSubscription) v1gen.ThreadSubscription {
	return v1gen.ThreadSubscription{Subscribed: t.Subscribed, Ignored: t.Ignored, Explicit: t.Explicit}
}

func repoSubDTO(r notify.RepoSubscription) v1gen.RepoSubscription {
	return v1gen.RepoSubscription{Level: v1gen.RepoWatchLevel(r.Level), Explicit: r.Explicit}
}

func (s *v1Server) ListNotifications(ctx context.Context, req v1gen.ListNotificationsRequestObject) (v1gen.ListNotificationsResponseObject, error) {
	if s.notify == nil {
		return errNotificationsUnavailable, nil
	}
	p := req.Params
	offset, limit := page(p.Offset, p.Limit)
	o := notify.ListOptions{All: deref(p.All), Participating: deref(p.Participating), Since: p.Since,
		Offset: offset, Limit: limit}
	if p.Repo != nil && *p.Repo != "" {
		var ok bool
		if o.Owner, o.Name, ok = splitRepoParam(*p.Repo); !ok {
			return badRequest("repo must be owner/name"), nil
		}
	}
	ts, more, err := s.notify.List(ctx, principal(ctx), o)
	if err != nil {
		return notifyFail(err)
	}
	out := v1gen.ListNotifications200JSONResponse{Body: make([]v1gen.NotificationThread, 0, len(ts)),
		Headers: v1gen.ListNotifications200ResponseHeaders{XHasMore: hasMoreHeader(more)}}
	for _, t := range ts {
		out.Body = append(out.Body, threadDTO(t))
	}
	return out, nil
}

func (s *v1Server) ListNotificationRepos(ctx context.Context, req v1gen.ListNotificationReposRequestObject) (v1gen.ListNotificationReposResponseObject, error) {
	if s.notify == nil {
		return errNotificationsUnavailable, nil
	}
	cs, err := s.notify.RepoCounts(ctx, principal(ctx), deref(req.Params.All), deref(req.Params.Participating))
	if err != nil {
		return notifyFail(err)
	}
	out := make(v1gen.ListNotificationRepos200JSONResponse, 0, len(cs))
	for _, c := range cs {
		out = append(out, v1gen.NotificationRepoCount{RepoId: c.RepoID, Owner: c.Owner, Repo: c.Repo,
			Count: c.Count, Unread: c.Unread})
	}
	return out, nil
}

// subjectRef builds the subject of the /notifications/subject endpoints.
func subjectRef(repo string, number *int64, release *uuid.UUID) (notify.SubjectRef, *problemResponse) {
	owner, name, ok := splitRepoParam(repo)
	if !ok {
		p := badRequest("repo must be owner/name")
		return notify.SubjectRef{}, &p
	}
	ref := notify.SubjectRef{Owner: owner, Name: name}
	if number != nil {
		ref.Number = *number
	}
	if release != nil {
		ref.ReleaseID = *release
	}
	return ref, nil
}

func subjectDTO(sub notify.Subject) v1gen.NotificationSubject {
	out := v1gen.NotificationSubject{Subscription: threadSubDTO(sub.Subscription)}
	if sub.Thread != nil {
		t := threadDTO(*sub.Thread)
		out.Thread = &t
	}
	return out
}

func (s *v1Server) GetNotificationSubject(ctx context.Context, req v1gen.GetNotificationSubjectRequestObject) (v1gen.GetNotificationSubjectResponseObject, error) {
	if s.notify == nil {
		return errNotificationsUnavailable, nil
	}
	ref, bad := subjectRef(req.Params.Repo, req.Params.Number, req.Params.Release)
	if bad != nil {
		return *bad, nil
	}
	sub, err := s.notify.Subject(ctx, principal(ctx), ref)
	if err != nil {
		return notifyFail(err)
	}
	return v1gen.GetNotificationSubject200JSONResponse(subjectDTO(sub)), nil
}

func (s *v1Server) SetNotificationSubjectSubscription(ctx context.Context, req v1gen.SetNotificationSubjectSubscriptionRequestObject) (v1gen.SetNotificationSubjectSubscriptionResponseObject, error) {
	if s.notify == nil {
		return errNotificationsUnavailable, nil
	}
	ref, bad := subjectRef(req.Params.Repo, req.Params.Number, req.Params.Release)
	if bad != nil {
		return *bad, nil
	}
	ignored := false
	if req.Body != nil {
		ignored = deref(req.Body.Ignored)
	}
	sub, err := s.notify.SetSubjectSubscription(ctx, principal(ctx), ref, ignored)
	if err != nil {
		return notifyFail(err)
	}
	return v1gen.SetNotificationSubjectSubscription200JSONResponse(subjectDTO(sub)), nil
}

func (s *v1Server) DeleteNotificationSubjectSubscription(ctx context.Context, req v1gen.DeleteNotificationSubjectSubscriptionRequestObject) (v1gen.DeleteNotificationSubjectSubscriptionResponseObject, error) {
	if s.notify == nil {
		return errNotificationsUnavailable, nil
	}
	ref, bad := subjectRef(req.Params.Repo, req.Params.Number, req.Params.Release)
	if bad != nil {
		return *bad, nil
	}
	sub, err := s.notify.DeleteSubjectSubscription(ctx, principal(ctx), ref)
	if err != nil {
		return notifyFail(err)
	}
	return v1gen.DeleteNotificationSubjectSubscription200JSONResponse(subjectDTO(sub)), nil
}

func (s *v1Server) CountNotifications(ctx context.Context, _ v1gen.CountNotificationsRequestObject) (v1gen.CountNotificationsResponseObject, error) {
	if s.notify == nil {
		return errNotificationsUnavailable, nil
	}
	n, err := s.notify.Count(ctx, principal(ctx))
	if err != nil {
		return notifyFail(err)
	}
	return v1gen.CountNotifications200JSONResponse{Unread: n}, nil
}

func (s *v1Server) MarkNotificationsRead(ctx context.Context, req v1gen.MarkNotificationsReadRequestObject) (v1gen.MarkNotificationsReadResponseObject, error) {
	if s.notify == nil {
		return errNotificationsUnavailable, nil
	}
	var before *time.Time // nil: now
	if req.Body != nil {
		before = req.Body.LastReadAt
	}
	if err := s.notify.MarkAllRead(ctx, principal(ctx), before); err != nil {
		return notifyFail(err)
	}
	return v1gen.MarkNotificationsRead204Response{}, nil
}

func (s *v1Server) GetNotificationSettings(ctx context.Context, _ v1gen.GetNotificationSettingsRequestObject) (v1gen.GetNotificationSettingsResponseObject, error) {
	if s.notify == nil {
		return errNotificationsUnavailable, nil
	}
	on, err := s.notify.EmailEnabled(ctx, principal(ctx))
	if err != nil {
		return notifyFail(err)
	}
	return v1gen.GetNotificationSettings200JSONResponse{Email: on}, nil
}

func (s *v1Server) SetNotificationSettings(ctx context.Context, req v1gen.SetNotificationSettingsRequestObject) (v1gen.SetNotificationSettingsResponseObject, error) {
	if s.notify == nil {
		return errNotificationsUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing request body"), nil
	}
	who := principal(ctx)
	if err := s.notify.SetEmailEnabled(ctx, who, req.Body.Email); err != nil {
		return notifyFail(err)
	}
	return v1gen.SetNotificationSettings200JSONResponse{Email: req.Body.Email}, nil
}

func (s *v1Server) GetNotificationThread(ctx context.Context, req v1gen.GetNotificationThreadRequestObject) (v1gen.GetNotificationThreadResponseObject, error) {
	if s.notify == nil {
		return errNotificationsUnavailable, nil
	}
	t, err := s.notify.Get(ctx, principal(ctx), req.ThreadId)
	if err != nil {
		return notifyFail(err)
	}
	return v1gen.GetNotificationThread200JSONResponse(threadDTO(t)), nil
}

func (s *v1Server) MarkNotificationThreadRead(ctx context.Context, req v1gen.MarkNotificationThreadReadRequestObject) (v1gen.MarkNotificationThreadReadResponseObject, error) {
	if s.notify == nil {
		return errNotificationsUnavailable, nil
	}
	if err := s.notify.MarkRead(ctx, principal(ctx), req.ThreadId); err != nil {
		return notifyFail(err)
	}
	return v1gen.MarkNotificationThreadRead204Response{}, nil
}

func (s *v1Server) MarkNotificationThreadDone(ctx context.Context, req v1gen.MarkNotificationThreadDoneRequestObject) (v1gen.MarkNotificationThreadDoneResponseObject, error) {
	if s.notify == nil {
		return errNotificationsUnavailable, nil
	}
	if err := s.notify.MarkDone(ctx, principal(ctx), req.ThreadId); err != nil {
		return notifyFail(err)
	}
	return v1gen.MarkNotificationThreadDone204Response{}, nil
}

func (s *v1Server) GetThreadSubscription(ctx context.Context, req v1gen.GetThreadSubscriptionRequestObject) (v1gen.GetThreadSubscriptionResponseObject, error) {
	if s.notify == nil {
		return errNotificationsUnavailable, nil
	}
	sub, err := s.notify.ThreadSubscription(ctx, principal(ctx), req.ThreadId)
	if err != nil {
		return notifyFail(err)
	}
	return v1gen.GetThreadSubscription200JSONResponse(threadSubDTO(sub)), nil
}

func (s *v1Server) SetThreadSubscription(ctx context.Context, req v1gen.SetThreadSubscriptionRequestObject) (v1gen.SetThreadSubscriptionResponseObject, error) {
	if s.notify == nil {
		return errNotificationsUnavailable, nil
	}
	ignored := false
	if req.Body != nil {
		ignored = deref(req.Body.Ignored)
	}
	sub, err := s.notify.SetThreadSubscription(ctx, principal(ctx), req.ThreadId, ignored)
	if err != nil {
		return notifyFail(err)
	}
	return v1gen.SetThreadSubscription200JSONResponse(threadSubDTO(sub)), nil
}

func (s *v1Server) DeleteThreadSubscription(ctx context.Context, req v1gen.DeleteThreadSubscriptionRequestObject) (v1gen.DeleteThreadSubscriptionResponseObject, error) {
	if s.notify == nil {
		return errNotificationsUnavailable, nil
	}
	if err := s.notify.DeleteThreadSubscription(ctx, principal(ctx), req.ThreadId); err != nil {
		return notifyFail(err)
	}
	return v1gen.DeleteThreadSubscription204Response{}, nil
}

func (s *v1Server) GetRepoSubscription(ctx context.Context, req v1gen.GetRepoSubscriptionRequestObject) (v1gen.GetRepoSubscriptionResponseObject, error) {
	if s.notify == nil {
		return errNotificationsUnavailable, nil
	}
	sub, err := s.notify.RepoSubscription(ctx, principal(ctx), req.Owner, req.Name)
	if err != nil {
		return repoSubFail(err, http.MethodGet)
	}
	return v1gen.GetRepoSubscription200JSONResponse(repoSubDTO(sub)), nil
}

func (s *v1Server) SetRepoSubscription(ctx context.Context, req v1gen.SetRepoSubscriptionRequestObject) (v1gen.SetRepoSubscriptionResponseObject, error) {
	if s.notify == nil {
		return errNotificationsUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing request body"), nil
	}
	sub, err := s.notify.SetRepoSubscription(ctx, principal(ctx), req.Owner, req.Name, string(req.Body.Level))
	if err != nil {
		return repoSubFail(err, http.MethodPut)
	}
	return v1gen.SetRepoSubscription200JSONResponse(repoSubDTO(sub)), nil
}
