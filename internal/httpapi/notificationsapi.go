package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/notify"
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

// notifyFail maps an error of a notification endpoint.
func notifyFail(err error) (notifyResponse, error) {
	if p, ok := notifyProblem(err); ok {
		return p, nil
	}
	return nil, err
}

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
	ts, err := s.notify.List(ctx, principal(ctx), notify.ListOptions{All: deref(p.All),
		Participating: deref(p.Participating), Since: p.Since, Offset: offset, Limit: limit})
	if err != nil {
		return notifyFail(err)
	}
	out := make(v1gen.ListNotifications200JSONResponse, 0, len(ts))
	for _, t := range ts {
		out = append(out, threadDTO(t))
	}
	return out, nil
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
