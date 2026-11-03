package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/webhooks"
)

var errWebhooksUnavailable = problemResponse{status: http.StatusServiceUnavailable,
	detail: "webhooks are not available"}

// hooksProblem maps a webhook service error to a problem response.
func hooksProblem(err error) (problemResponse, bool) {
	var ve *webhooks.ValidationError
	switch {
	case errors.As(err, &ve):
		return badRequest(err.Error()), true
	case errors.Is(err, webhooks.ErrNotFound):
		return problemResponse{status: http.StatusNotFound, detail: err.Error()}, true
	case errors.Is(err, webhooks.ErrForbidden):
		return problemResponse{status: http.StatusForbidden, detail: err.Error()}, true
	case errors.Is(err, webhooks.ErrTooMany):
		return problemResponse{status: http.StatusConflict, detail: err.Error()}, true
	case errors.Is(err, webhooks.ErrNoMasterKey):
		return problemResponse{status: http.StatusServiceUnavailable, detail: err.Error()}, true
	}
	return problemResponse{}, false
}

// repoHooksFail maps an error of a repository hook endpoint; suffix is the
// path after the repository, kept on a rename redirect.
func repoHooksFail(err error, method, suffix string) (repoHookResponse, error) {
	if p, ok := hooksProblem(err); ok {
		return p, nil
	}
	r, err2 := reposFail(err, method, suffix)
	if err2 != nil {
		return nil, err2
	}
	return r.(repoHookResponse), nil
}

// orgHooksFail maps an error of an organization hook endpoint.
func orgHooksFail(err error) (orgHookResponse, error) {
	if p, ok := hooksProblem(err); ok {
		return p, nil
	}
	return nil, err
}

// repoHookResponse is answered by problemResponse and movedResponse.
type repoHookResponse interface {
	v1gen.ListRepoWebhooksResponseObject
	v1gen.CreateRepoWebhookResponseObject
	v1gen.GetRepoWebhookResponseObject
	v1gen.UpdateRepoWebhookResponseObject
	v1gen.DeleteRepoWebhookResponseObject
	v1gen.PingRepoWebhookResponseObject
	v1gen.ListRepoWebhookDeliveriesResponseObject
	v1gen.GetRepoWebhookDeliveryResponseObject
	v1gen.RedeliverRepoWebhookDeliveryResponseObject
}

// orgHookResponse is answered by problemResponse.
type orgHookResponse interface {
	v1gen.ListOrgWebhooksResponseObject
	v1gen.CreateOrgWebhookResponseObject
	v1gen.GetOrgWebhookResponseObject
	v1gen.UpdateOrgWebhookResponseObject
	v1gen.DeleteOrgWebhookResponseObject
	v1gen.PingOrgWebhookResponseObject
	v1gen.ListOrgWebhookDeliveriesResponseObject
	v1gen.GetOrgWebhookDeliveryResponseObject
	v1gen.RedeliverOrgWebhookDeliveryResponseObject
}

func (p problemResponse) VisitListRepoWebhooksResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitCreateRepoWebhookResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitGetRepoWebhookResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitUpdateRepoWebhookResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitDeleteRepoWebhookResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitPingRepoWebhookResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitListRepoWebhookDeliveriesResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitGetRepoWebhookDeliveryResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitRedeliverRepoWebhookDeliveryResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitListOrgWebhooksResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitCreateOrgWebhookResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitGetOrgWebhookResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitUpdateOrgWebhookResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitDeleteOrgWebhookResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitPingOrgWebhookResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitListOrgWebhookDeliveriesResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitGetOrgWebhookDeliveryResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitRedeliverOrgWebhookDeliveryResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (m movedResponse) VisitListRepoWebhooksResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitCreateRepoWebhookResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitGetRepoWebhookResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitUpdateRepoWebhookResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitDeleteRepoWebhookResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitPingRepoWebhookResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitListRepoWebhookDeliveriesResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitGetRepoWebhookDeliveryResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitRedeliverRepoWebhookDeliveryResponse(w http.ResponseWriter) error {
	return m.write(w)
}

var (
	_ repoHookResponse = problemResponse{}
	_ repoHookResponse = movedResponse{}
	_ orgHookResponse  = problemResponse{}
)

func hookPath(id uuid.UUID) string { return "/hooks/" + id.String() }

func hookDTO(h webhooks.Hook) v1gen.Webhook {
	out := v1gen.Webhook{Id: h.ID, Url: h.URL, ContentType: v1gen.WebhookContentType(h.ContentType),
		Format: v1gen.WebhookFormat(h.Format), Events: h.Events, Active: h.Active, HasSecret: h.HasSecret,
		CreatedAt: h.CreatedAt, UpdatedAt: h.UpdatedAt}
	if h.Last != nil {
		out.LastDelivery = &v1gen.WebhookLastDelivery{Status: v1gen.WebhookDeliveryStatus(h.Last.Status),
			StatusCode: h.Last.StatusCode, DeliveredAt: h.Last.DeliveredAt}
	}
	return out
}

func hooksDTO(hs []webhooks.Hook) []v1gen.Webhook {
	out := make([]v1gen.Webhook, 0, len(hs))
	for _, h := range hs {
		out = append(out, hookDTO(h))
	}
	return out
}

func deliveryDTO(d webhooks.Delivery) v1gen.WebhookDelivery {
	return v1gen.WebhookDelivery{Id: d.ID, Guid: d.GUID, Event: d.Event, Action: d.Action, Redelivery: d.Redelivery,
		Status: v1gen.WebhookDeliveryStatus(d.Status), Attempts: d.Attempts, StatusCode: d.StatusCode,
		DurationMs: d.DurationMs, Error: d.Error, CreatedAt: d.CreatedAt, DeliveredAt: d.DeliveredAt}
}

func deliveriesDTO(ds []webhooks.Delivery) []v1gen.WebhookDelivery {
	out := make([]v1gen.WebhookDelivery, 0, len(ds))
	for _, d := range ds {
		out = append(out, deliveryDTO(d))
	}
	return out
}

func deliveryDetailDTO(d webhooks.DeliveryDetail) v1gen.WebhookDeliveryDetail {
	return v1gen.WebhookDeliveryDetail{Delivery: deliveryDTO(d.Delivery), Url: d.URL,
		RequestHeaders: nonNilMap(d.RequestHeaders), RequestBody: d.RequestBody,
		ResponseHeaders: nonNilMap(d.ResponseHeaders), ResponseBody: d.ResponseBody}
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func hookCreateInput(b *v1gen.WebhookCreate) webhooks.Input {
	return webhooks.Input{URL: &b.Url, ContentType: (*string)(b.ContentType), Format: (*string)(b.Format),
		Events: b.Events, Secret: b.Secret, Active: b.Active}
}

func hookUpdateInput(b *v1gen.WebhookUpdate) webhooks.Input {
	if b == nil {
		return webhooks.Input{}
	}
	return webhooks.Input{URL: b.Url, ContentType: (*string)(b.ContentType), Format: (*string)(b.Format),
		Events: b.Events, Secret: b.Secret, Active: b.Active}
}

// repoHooks resolves the repository scope of a request.
func (s *v1Server) repoHooks(ctx context.Context, owner, name string) (auth.Principal, webhooks.Scope, error) {
	who := principal(ctx)
	sc, err := s.webhooks.RepoScope(ctx, who, owner, name)
	return who, sc, err
}

// orgHooks resolves the organization scope of a request.
func (s *v1Server) orgHooks(ctx context.Context, org string) (auth.Principal, webhooks.Scope, error) {
	who := principal(ctx)
	sc, err := s.webhooks.OrgScope(ctx, who, org)
	return who, sc, err
}

// ---- repository hooks ----

func (s *v1Server) ListRepoWebhooks(ctx context.Context, req v1gen.ListRepoWebhooksRequestObject) (v1gen.ListRepoWebhooksResponseObject, error) {
	if s.webhooks == nil {
		return errWebhooksUnavailable, nil
	}
	_, sc, err := s.repoHooks(ctx, req.Owner, req.Name)
	if err != nil {
		return repoHooksFail(err, http.MethodGet, "/hooks")
	}
	hs, err := s.webhooks.List(ctx, sc)
	if err != nil {
		return repoHooksFail(err, http.MethodGet, "/hooks")
	}
	return v1gen.ListRepoWebhooks200JSONResponse(hooksDTO(hs)), nil
}

func (s *v1Server) CreateRepoWebhook(ctx context.Context, req v1gen.CreateRepoWebhookRequestObject) (v1gen.CreateRepoWebhookResponseObject, error) {
	if s.webhooks == nil {
		return errWebhooksUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing request body"), nil
	}
	who, sc, err := s.repoHooks(ctx, req.Owner, req.Name)
	if err != nil {
		return repoHooksFail(err, http.MethodPost, "/hooks")
	}
	h, err := s.webhooks.Create(ctx, who, sc, hookCreateInput(req.Body))
	if err != nil {
		return repoHooksFail(err, http.MethodPost, "/hooks")
	}
	return v1gen.CreateRepoWebhook201JSONResponse(hookDTO(h)), nil
}

func (s *v1Server) GetRepoWebhook(ctx context.Context, req v1gen.GetRepoWebhookRequestObject) (v1gen.GetRepoWebhookResponseObject, error) {
	if s.webhooks == nil {
		return errWebhooksUnavailable, nil
	}
	suffix := hookPath(req.HookId)
	_, sc, err := s.repoHooks(ctx, req.Owner, req.Name)
	if err != nil {
		return repoHooksFail(err, http.MethodGet, suffix)
	}
	h, err := s.webhooks.Get(ctx, sc, req.HookId)
	if err != nil {
		return repoHooksFail(err, http.MethodGet, suffix)
	}
	return v1gen.GetRepoWebhook200JSONResponse(hookDTO(h)), nil
}

func (s *v1Server) UpdateRepoWebhook(ctx context.Context, req v1gen.UpdateRepoWebhookRequestObject) (v1gen.UpdateRepoWebhookResponseObject, error) {
	if s.webhooks == nil {
		return errWebhooksUnavailable, nil
	}
	suffix := hookPath(req.HookId)
	_, sc, err := s.repoHooks(ctx, req.Owner, req.Name)
	if err != nil {
		return repoHooksFail(err, http.MethodPatch, suffix)
	}
	h, err := s.webhooks.Update(ctx, sc, req.HookId, hookUpdateInput(req.Body))
	if err != nil {
		return repoHooksFail(err, http.MethodPatch, suffix)
	}
	return v1gen.UpdateRepoWebhook200JSONResponse(hookDTO(h)), nil
}

func (s *v1Server) DeleteRepoWebhook(ctx context.Context, req v1gen.DeleteRepoWebhookRequestObject) (v1gen.DeleteRepoWebhookResponseObject, error) {
	if s.webhooks == nil {
		return errWebhooksUnavailable, nil
	}
	suffix := hookPath(req.HookId)
	_, sc, err := s.repoHooks(ctx, req.Owner, req.Name)
	if err != nil {
		return repoHooksFail(err, http.MethodDelete, suffix)
	}
	if err := s.webhooks.Delete(ctx, sc, req.HookId); err != nil {
		return repoHooksFail(err, http.MethodDelete, suffix)
	}
	return v1gen.DeleteRepoWebhook204Response{}, nil
}

func (s *v1Server) PingRepoWebhook(ctx context.Context, req v1gen.PingRepoWebhookRequestObject) (v1gen.PingRepoWebhookResponseObject, error) {
	if s.webhooks == nil {
		return errWebhooksUnavailable, nil
	}
	suffix := hookPath(req.HookId) + "/pings"
	who, sc, err := s.repoHooks(ctx, req.Owner, req.Name)
	if err != nil {
		return repoHooksFail(err, http.MethodPost, suffix)
	}
	d, err := s.webhooks.Ping(ctx, who, sc, req.HookId)
	if err != nil {
		return repoHooksFail(err, http.MethodPost, suffix)
	}
	return v1gen.PingRepoWebhook202JSONResponse(deliveryDTO(d)), nil
}

func (s *v1Server) ListRepoWebhookDeliveries(ctx context.Context, req v1gen.ListRepoWebhookDeliveriesRequestObject) (v1gen.ListRepoWebhookDeliveriesResponseObject, error) {
	if s.webhooks == nil {
		return errWebhooksUnavailable, nil
	}
	suffix := hookPath(req.HookId) + "/deliveries"
	_, sc, err := s.repoHooks(ctx, req.Owner, req.Name)
	if err != nil {
		return repoHooksFail(err, http.MethodGet, suffix)
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit)
	ds, err := s.webhooks.Deliveries(ctx, sc, req.HookId, offset, limit)
	if err != nil {
		return repoHooksFail(err, http.MethodGet, suffix)
	}
	return v1gen.ListRepoWebhookDeliveries200JSONResponse(deliveriesDTO(ds)), nil
}

func (s *v1Server) GetRepoWebhookDelivery(ctx context.Context, req v1gen.GetRepoWebhookDeliveryRequestObject) (v1gen.GetRepoWebhookDeliveryResponseObject, error) {
	if s.webhooks == nil {
		return errWebhooksUnavailable, nil
	}
	suffix := hookPath(req.HookId) + "/deliveries/" + req.DeliveryId.String()
	_, sc, err := s.repoHooks(ctx, req.Owner, req.Name)
	if err != nil {
		return repoHooksFail(err, http.MethodGet, suffix)
	}
	d, err := s.webhooks.Delivery(ctx, sc, req.HookId, req.DeliveryId)
	if err != nil {
		return repoHooksFail(err, http.MethodGet, suffix)
	}
	return v1gen.GetRepoWebhookDelivery200JSONResponse(deliveryDetailDTO(d)), nil
}

func (s *v1Server) RedeliverRepoWebhookDelivery(ctx context.Context, req v1gen.RedeliverRepoWebhookDeliveryRequestObject) (v1gen.RedeliverRepoWebhookDeliveryResponseObject, error) {
	if s.webhooks == nil {
		return errWebhooksUnavailable, nil
	}
	suffix := hookPath(req.HookId) + "/deliveries/" + req.DeliveryId.String() + "/attempts"
	_, sc, err := s.repoHooks(ctx, req.Owner, req.Name)
	if err != nil {
		return repoHooksFail(err, http.MethodPost, suffix)
	}
	d, err := s.webhooks.Redeliver(ctx, sc, req.HookId, req.DeliveryId)
	if err != nil {
		return repoHooksFail(err, http.MethodPost, suffix)
	}
	return v1gen.RedeliverRepoWebhookDelivery202JSONResponse(deliveryDTO(d)), nil
}

// ---- organization hooks ----

func (s *v1Server) ListOrgWebhooks(ctx context.Context, req v1gen.ListOrgWebhooksRequestObject) (v1gen.ListOrgWebhooksResponseObject, error) {
	if s.webhooks == nil {
		return errWebhooksUnavailable, nil
	}
	_, sc, err := s.orgHooks(ctx, req.Org)
	if err != nil {
		return orgHooksFail(err)
	}
	hs, err := s.webhooks.List(ctx, sc)
	if err != nil {
		return orgHooksFail(err)
	}
	return v1gen.ListOrgWebhooks200JSONResponse(hooksDTO(hs)), nil
}

func (s *v1Server) CreateOrgWebhook(ctx context.Context, req v1gen.CreateOrgWebhookRequestObject) (v1gen.CreateOrgWebhookResponseObject, error) {
	if s.webhooks == nil {
		return errWebhooksUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing request body"), nil
	}
	who, sc, err := s.orgHooks(ctx, req.Org)
	if err != nil {
		return orgHooksFail(err)
	}
	h, err := s.webhooks.Create(ctx, who, sc, hookCreateInput(req.Body))
	if err != nil {
		return orgHooksFail(err)
	}
	return v1gen.CreateOrgWebhook201JSONResponse(hookDTO(h)), nil
}

func (s *v1Server) GetOrgWebhook(ctx context.Context, req v1gen.GetOrgWebhookRequestObject) (v1gen.GetOrgWebhookResponseObject, error) {
	if s.webhooks == nil {
		return errWebhooksUnavailable, nil
	}
	_, sc, err := s.orgHooks(ctx, req.Org)
	if err != nil {
		return orgHooksFail(err)
	}
	h, err := s.webhooks.Get(ctx, sc, req.HookId)
	if err != nil {
		return orgHooksFail(err)
	}
	return v1gen.GetOrgWebhook200JSONResponse(hookDTO(h)), nil
}

func (s *v1Server) UpdateOrgWebhook(ctx context.Context, req v1gen.UpdateOrgWebhookRequestObject) (v1gen.UpdateOrgWebhookResponseObject, error) {
	if s.webhooks == nil {
		return errWebhooksUnavailable, nil
	}
	_, sc, err := s.orgHooks(ctx, req.Org)
	if err != nil {
		return orgHooksFail(err)
	}
	h, err := s.webhooks.Update(ctx, sc, req.HookId, hookUpdateInput(req.Body))
	if err != nil {
		return orgHooksFail(err)
	}
	return v1gen.UpdateOrgWebhook200JSONResponse(hookDTO(h)), nil
}

func (s *v1Server) DeleteOrgWebhook(ctx context.Context, req v1gen.DeleteOrgWebhookRequestObject) (v1gen.DeleteOrgWebhookResponseObject, error) {
	if s.webhooks == nil {
		return errWebhooksUnavailable, nil
	}
	_, sc, err := s.orgHooks(ctx, req.Org)
	if err != nil {
		return orgHooksFail(err)
	}
	if err := s.webhooks.Delete(ctx, sc, req.HookId); err != nil {
		return orgHooksFail(err)
	}
	return v1gen.DeleteOrgWebhook204Response{}, nil
}

func (s *v1Server) PingOrgWebhook(ctx context.Context, req v1gen.PingOrgWebhookRequestObject) (v1gen.PingOrgWebhookResponseObject, error) {
	if s.webhooks == nil {
		return errWebhooksUnavailable, nil
	}
	who, sc, err := s.orgHooks(ctx, req.Org)
	if err != nil {
		return orgHooksFail(err)
	}
	d, err := s.webhooks.Ping(ctx, who, sc, req.HookId)
	if err != nil {
		return orgHooksFail(err)
	}
	return v1gen.PingOrgWebhook202JSONResponse(deliveryDTO(d)), nil
}

func (s *v1Server) ListOrgWebhookDeliveries(ctx context.Context, req v1gen.ListOrgWebhookDeliveriesRequestObject) (v1gen.ListOrgWebhookDeliveriesResponseObject, error) {
	if s.webhooks == nil {
		return errWebhooksUnavailable, nil
	}
	_, sc, err := s.orgHooks(ctx, req.Org)
	if err != nil {
		return orgHooksFail(err)
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit)
	ds, err := s.webhooks.Deliveries(ctx, sc, req.HookId, offset, limit)
	if err != nil {
		return orgHooksFail(err)
	}
	return v1gen.ListOrgWebhookDeliveries200JSONResponse(deliveriesDTO(ds)), nil
}

func (s *v1Server) GetOrgWebhookDelivery(ctx context.Context, req v1gen.GetOrgWebhookDeliveryRequestObject) (v1gen.GetOrgWebhookDeliveryResponseObject, error) {
	if s.webhooks == nil {
		return errWebhooksUnavailable, nil
	}
	_, sc, err := s.orgHooks(ctx, req.Org)
	if err != nil {
		return orgHooksFail(err)
	}
	d, err := s.webhooks.Delivery(ctx, sc, req.HookId, req.DeliveryId)
	if err != nil {
		return orgHooksFail(err)
	}
	return v1gen.GetOrgWebhookDelivery200JSONResponse(deliveryDetailDTO(d)), nil
}

func (s *v1Server) RedeliverOrgWebhookDelivery(ctx context.Context, req v1gen.RedeliverOrgWebhookDeliveryRequestObject) (v1gen.RedeliverOrgWebhookDeliveryResponseObject, error) {
	if s.webhooks == nil {
		return errWebhooksUnavailable, nil
	}
	_, sc, err := s.orgHooks(ctx, req.Org)
	if err != nil {
		return orgHooksFail(err)
	}
	d, err := s.webhooks.Redeliver(ctx, sc, req.HookId, req.DeliveryId)
	if err != nil {
		return orgHooksFail(err)
	}
	return v1gen.RedeliverOrgWebhookDelivery202JSONResponse(deliveryDTO(d)), nil
}
