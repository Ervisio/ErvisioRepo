package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/instancesettings"
)

// AdminIdentity is the instance admin part of the identity service
// (satisfied by *identity.Service).
type AdminIdentity interface {
	AdminListUsers(ctx context.Context, f identity.AdminUserFilter) ([]identity.AdminUser, int64, error)
	AdminGetUser(ctx context.Context, username string) (identity.AdminUser, error)
	AdminUpdateUser(ctx context.Context, actor uuid.UUID, username string, in identity.AdminUserUpdate, c identity.Client) (identity.AdminUser, error)
	AdminReset2FA(ctx context.Context, actor uuid.UUID, username string, c identity.Client) error
	AdminListInvites(ctx context.Context, offset, limit int) ([]identity.AdminInvite, int64, error)
	AdminRevokeInvite(ctx context.Context, actor, id uuid.UUID, c identity.Client) error
}

// InstanceSettings stores the instance settings edited from the web admin
// (satisfied by *instancesettings.Service).
type InstanceSettings interface {
	Document(ctx context.Context) (instancesettings.Document, error)
	Update(ctx context.Context, actor uuid.UUID, ip string, values map[string]json.RawMessage) (instancesettings.Document, error)
}

const (
	adminPageDefault = 30
	adminPageMax     = 100
)

// adminPage clamps the offset and limit query parameters.
func adminPage(offset, limit *int) (int, int) {
	o, l := page(offset, limit)
	if o < 0 {
		o = 0
	}
	if l <= 0 {
		l = adminPageDefault
	}
	return o, min(l, adminPageMax)
}

func (s *v1Server) adminIdentity() (AdminIdentity, bool) {
	a, ok := s.identity.(AdminIdentity)
	return a, ok && s.identity != nil
}

var errAdminUnavailable = problemResponse{status: http.StatusServiceUnavailable, detail: "user administration is not available"}

// adminProblem maps the identity errors of the admin operations.
func adminProblem(err error) (problemResponse, bool) {
	switch {
	case errors.Is(err, identity.ErrNotFound):
		return problemResponse{status: http.StatusNotFound, detail: "no such user"}, true
	case errors.Is(err, identity.ErrSelfChange), errors.Is(err, identity.ErrLastAdmin):
		return problemResponse{status: http.StatusConflict, detail: err.Error()}, true
	case errors.Is(err, identity.ErrInvalidRole):
		return badRequest(err.Error()), true
	}
	return problemResponse{}, false
}

func adminUserDTO(u identity.AdminUser) v1gen.AdminUser {
	out := v1gen.AdminUser{
		Id: u.ID, Username: u.Username, DisplayName: u.DisplayName, Role: v1gen.AdminUserRole(u.Role),
		SuspendedAt: u.SuspendedAt, Has2fa: u.Has2FA, CreatedAt: u.CreatedAt, LastActivity: u.LastActivity,
	}
	if u.Email != "" {
		out.Email = &u.Email
	}
	return out
}

func adminInviteDTO(i identity.AdminInvite) v1gen.AdminInvite {
	out := v1gen.AdminInvite{
		Id: i.ID, Status: v1gen.AdminInviteStatus(i.Status), CreatedAt: i.CreatedAt, ExpiresAt: i.ExpiresAt, UsedAt: i.UsedAt,
	}
	if i.Email != "" {
		out.Email = &i.Email
	}
	if i.CreatedBy != "" {
		out.CreatedBy = &i.CreatedBy
	}
	if i.UsedBy != "" {
		out.UsedBy = &i.UsedBy
	}
	return out
}

func (s *v1Server) ListAdminUsers(ctx context.Context, req v1gen.ListAdminUsersRequestObject) (v1gen.ListAdminUsersResponseObject, error) {
	a, ok := s.adminIdentity()
	if !ok {
		return errAdminUnavailable, nil
	}
	p := req.Params
	f := identity.AdminUserFilter{Suspended: p.Suspended}
	f.Offset, f.Limit = adminPage(p.Offset, p.Limit)
	if p.Q != nil {
		f.Search = *p.Q
	}
	if p.Role != nil {
		f.Role = string(*p.Role)
	}
	users, total, err := a.AdminListUsers(ctx, f)
	if pr, ok := adminProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	out := v1gen.AdminUserPage{Items: make([]v1gen.AdminUser, 0, len(users)), Total: total}
	for _, u := range users {
		out.Items = append(out.Items, adminUserDTO(u))
	}
	return v1gen.ListAdminUsers200JSONResponse(out), nil
}

func (s *v1Server) GetAdminUser(ctx context.Context, req v1gen.GetAdminUserRequestObject) (v1gen.GetAdminUserResponseObject, error) {
	a, ok := s.adminIdentity()
	if !ok {
		return errAdminUnavailable, nil
	}
	u, err := a.AdminGetUser(ctx, req.Login)
	if pr, ok := adminProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.GetAdminUser200JSONResponse(adminUserDTO(u)), nil
}

func (s *v1Server) UpdateAdminUser(ctx context.Context, req v1gen.UpdateAdminUserRequestObject) (v1gen.UpdateAdminUserResponseObject, error) {
	a, ok := s.adminIdentity()
	if !ok {
		return errAdminUnavailable, nil
	}
	if req.Body == nil || (req.Body.Role == nil && req.Body.Suspended == nil) {
		return badRequest("nothing to change: set role or suspended"), nil
	}
	in := identity.AdminUserUpdate{Suspended: req.Body.Suspended}
	if req.Body.Role != nil {
		r := string(*req.Body.Role)
		in.Role = &r
	}
	u, err := a.AdminUpdateUser(ctx, principal(ctx).UserID, req.Login, in, clientOf(ctx))
	if pr, ok := adminProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.UpdateAdminUser200JSONResponse(adminUserDTO(u)), nil
}

func (s *v1Server) ResetAdminUser2fa(ctx context.Context, req v1gen.ResetAdminUser2faRequestObject) (v1gen.ResetAdminUser2faResponseObject, error) {
	a, ok := s.adminIdentity()
	if !ok {
		return errAdminUnavailable, nil
	}
	err := a.AdminReset2FA(ctx, principal(ctx).UserID, req.Login, clientOf(ctx))
	if pr, ok := adminProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.ResetAdminUser2fa204Response{}, nil
}

func (s *v1Server) ListAdminInvites(ctx context.Context, req v1gen.ListAdminInvitesRequestObject) (v1gen.ListAdminInvitesResponseObject, error) {
	a, ok := s.adminIdentity()
	if !ok {
		return errAdminUnavailable, nil
	}
	offset, limit := adminPage(req.Params.Offset, req.Params.Limit)
	invites, total, err := a.AdminListInvites(ctx, offset, limit)
	if err != nil {
		return nil, err
	}
	out := v1gen.AdminInvitePage{Items: make([]v1gen.AdminInvite, 0, len(invites)), Total: total}
	for _, i := range invites {
		out.Items = append(out.Items, adminInviteDTO(i))
	}
	return v1gen.ListAdminInvites200JSONResponse(out), nil
}

func (s *v1Server) RevokeInvite(ctx context.Context, req v1gen.RevokeInviteRequestObject) (v1gen.RevokeInviteResponseObject, error) {
	a, ok := s.adminIdentity()
	if !ok {
		return errAdminUnavailable, nil
	}
	err := a.AdminRevokeInvite(ctx, principal(ctx).UserID, req.InviteId, clientOf(ctx))
	if errors.Is(err, identity.ErrNotFound) {
		return problemResponse{status: http.StatusNotFound, detail: "no pending invite with this id"}, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.RevokeInvite204Response{}, nil
}

var errSettingsUnavailable = problemResponse{status: http.StatusServiceUnavailable, detail: "instance settings are not available"}

func instanceSettingsDTO(d instancesettings.Document) v1gen.InstanceSettings {
	out := v1gen.InstanceSettings{RestartRequired: d.RestartRequired, Settings: make([]v1gen.InstanceSetting, 0, len(d.Settings))}
	for _, x := range d.Settings {
		out.Settings = append(out.Settings, v1gen.InstanceSetting{
			Key: x.Key, Value: x.Value, FileValue: x.FileValue, Source: v1gen.InstanceSettingSource(x.Source),
			Locked: x.Locked, RestartPending: x.RestartPending,
		})
	}
	return out
}

func (s *v1Server) GetInstanceSettings(ctx context.Context, _ v1gen.GetInstanceSettingsRequestObject) (v1gen.GetInstanceSettingsResponseObject, error) {
	if s.settings == nil {
		return errSettingsUnavailable, nil
	}
	d, err := s.settings.Document(ctx)
	if err != nil {
		return nil, err
	}
	return v1gen.GetInstanceSettings200JSONResponse(instanceSettingsDTO(d)), nil
}

func (s *v1Server) UpdateInstanceSettings(ctx context.Context, req v1gen.UpdateInstanceSettingsRequestObject) (v1gen.UpdateInstanceSettingsResponseObject, error) {
	if s.settings == nil {
		return errSettingsUnavailable, nil
	}
	if req.Body == nil || len(req.Body.Values) == 0 {
		return badRequest(instancesettings.ErrEmpty.Error()), nil
	}
	values := make(map[string]json.RawMessage, len(req.Body.Values))
	for k, v := range req.Body.Values {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("encode setting %s: %w", k, err)
		}
		values[k] = raw
	}
	d, err := s.settings.Update(ctx, principal(ctx).UserID, clientOf(ctx).IP, values)
	var verr *instancesettings.ValidationError
	switch {
	case errors.As(err, &verr):
		return validationProblem(verr), nil
	case errors.Is(err, instancesettings.ErrEmpty):
		return badRequest(err.Error()), nil
	case err != nil:
		return nil, err
	}
	return v1gen.UpdateInstanceSettings200JSONResponse(instanceSettingsDTO(d)), nil
}

func validationProblem(verr *instancesettings.ValidationError) v1gen.UpdateInstanceSettings422ApplicationProblemPlusJSONResponse {
	detail := "some settings are not valid"
	out := v1gen.ValidationProblem{
		Type: "about:blank", Title: http.StatusText(http.StatusUnprocessableEntity), Status: http.StatusUnprocessableEntity,
		Detail: &detail, Errors: make([]v1gen.FieldProblem, 0, len(verr.Errors)),
	}
	for _, e := range verr.Errors {
		out.Errors = append(out.Errors, v1gen.FieldProblem{Path: e.Path, Msg: e.Msg})
	}
	return v1gen.UpdateInstanceSettings422ApplicationProblemPlusJSONResponse(out)
}

// Visit methods let a problemResponse answer the admin user, invite and
// settings operations.
func (p problemResponse) VisitListAdminUsersResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitGetAdminUserResponse(w http.ResponseWriter) error    { return p.write(w) }
func (p problemResponse) VisitUpdateAdminUserResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitResetAdminUser2faResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListAdminInvitesResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitRevokeInviteResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitGetInstanceSettingsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitUpdateInstanceSettingsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
