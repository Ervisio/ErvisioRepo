package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/audit"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/orgs"
	"github.com/Ervisio/ErvisioRepo/internal/quota"
)

// Orgs is the organization service used by the /orgs endpoints; it is
// implemented by *orgs.Service.
type Orgs interface {
	CreateOrg(ctx context.Context, who auth.Principal, in orgs.CreateOrgInput, c identity.Client) (orgs.Org, error)
	GetOrg(ctx context.Context, who auth.Principal, name string) (orgs.Org, error)
	ListMyOrgs(ctx context.Context, who auth.Principal) ([]orgs.Org, error)
	UpdateOrg(ctx context.Context, who auth.Principal, name string, in orgs.UpdateOrgInput, c identity.Client) (orgs.Org, error)
	DeleteOrg(ctx context.Context, who auth.Principal, name string, c identity.Client) error
	ListMembers(ctx context.Context, who auth.Principal, name string) ([]orgs.Member, error)
	SetMemberRole(ctx context.Context, who auth.Principal, name, username, role string, c identity.Client) (orgs.Member, error)
	RemoveMember(ctx context.Context, who auth.Principal, name, username string, c identity.Client) error
	AuditLog(ctx context.Context, who auth.Principal, name string, f audit.Filter) (audit.Page, error)
	Quota(ctx context.Context, who auth.Principal, name string) (quota.Status, error)

	ListTeams(ctx context.Context, who auth.Principal, org string) ([]orgs.Team, error)
	GetTeam(ctx context.Context, who auth.Principal, org, team string) (orgs.Team, error)
	CreateTeam(ctx context.Context, who auth.Principal, org string, in orgs.TeamInput, c identity.Client) (orgs.Team, error)
	UpdateTeam(ctx context.Context, who auth.Principal, org, team string, in orgs.TeamInput, c identity.Client) (orgs.Team, error)
	DeleteTeam(ctx context.Context, who auth.Principal, org, team string, c identity.Client) error
	ListTeamMembers(ctx context.Context, who auth.Principal, org, team string) ([]orgs.Member, error)
	SetTeamMember(ctx context.Context, who auth.Principal, org, team, username, role string, c identity.Client) error
	RemoveTeamMember(ctx context.Context, who auth.Principal, org, team, username string, c identity.Client) error

	CreateInvite(ctx context.Context, who auth.Principal, org string, in orgs.InviteInput, c identity.Client) (orgs.Invite, error)
	ListInvites(ctx context.Context, who auth.Principal, org string) ([]orgs.Invite, error)
	RevokeInvite(ctx context.Context, who auth.Principal, org string, id uuid.UUID, c identity.Client) error
	AcceptInvite(ctx context.Context, who auth.Principal, token string, c identity.Client) (orgs.Accepted, error)
}

var _ Orgs = (*orgs.Service)(nil)

var errOrgsUnavailable = problemResponse{status: http.StatusServiceUnavailable, detail: "organizations are not available"}

// orgsProblem turns an orgs error into a problem; ok is false for errors
// that are not the caller's fault (they become a 500).
func orgsProblem(err error) (problemResponse, bool) {
	var ve *orgs.ValidationError
	switch {
	case errors.As(err, &ve), errors.Is(err, orgs.ErrInvalidToken):
		return badRequest(err.Error()), true
	case errors.Is(err, orgs.ErrNotFound), errors.Is(err, orgs.ErrNotMember):
		return problemResponse{status: http.StatusNotFound, detail: err.Error()}, true
	case errors.Is(err, orgs.ErrForbidden), errors.Is(err, orgs.ErrEmailMismatch), errors.Is(err, orgs.ErrEmailNotVerified):
		return problemResponse{status: http.StatusForbidden, detail: err.Error()}, true
	case errors.Is(err, orgs.ErrNameTaken), errors.Is(err, orgs.ErrInvitePending), errors.Is(err, orgs.ErrLastOwner):
		return problemResponse{status: http.StatusConflict, detail: err.Error()}, true
	}
	return identityProblem(err)
}

// orgsFail maps a service error to a problem, or passes it on as a 500.
func orgsFail(err error) (problemResponse, error) {
	if pr, ok := orgsProblem(err); ok {
		return pr, nil
	}
	return problemResponse{}, err
}

func orgDTO(o orgs.Org) v1gen.Org {
	out := v1gen.Org{
		Id: o.ID, Name: o.Name, DisplayName: o.DisplayName, Description: o.Description,
		BasePermission: v1gen.Permission(o.BasePermission), Require2fa: o.Require2FA, Verified: o.Verified,
		CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt,
	}
	if o.Role != "" {
		r := v1gen.OrgRole(o.Role)
		out.Role = &r
	}
	return out
}

func memberDTO(m orgs.Member) v1gen.OrgMember {
	return v1gen.OrgMember{Username: m.Username, DisplayName: m.DisplayName, Role: v1gen.OrgRole(m.Role), Since: m.Since}
}

func teamDTO(t orgs.Team) v1gen.Team {
	return v1gen.Team{Id: t.ID, Name: t.Name, Description: t.Description, Members: t.Members,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}
}

func inviteDTO(i orgs.Invite) v1gen.OrgInvite {
	out := v1gen.OrgInvite{Id: i.ID, Email: i.Email, Role: v1gen.OrgRole(i.Role), Teams: i.Teams,
		ExpiresAt: i.ExpiresAt, CreatedAt: i.CreatedAt}
	if out.Teams == nil {
		out.Teams = []string{}
	}
	if i.InvitedBy != "" {
		out.InvitedBy = &i.InvitedBy
	}
	if i.Token != "" {
		out.Token = &i.Token
	}
	return out
}

func auditPageDTO(p audit.Page) v1gen.AuditPage {
	out := v1gen.AuditPage{Entries: make([]v1gen.AuditEntry, 0, len(p.Entries))}
	for _, e := range p.Entries {
		d := e.Data
		if d == nil {
			d = map[string]any{}
		}
		ae := v1gen.AuditEntry{Id: e.ID, At: e.At, Action: e.Action, TargetType: e.TargetType,
			TargetId: e.TargetID, Ip: e.IP, Data: d}
		if e.Actor != uuid.Nil {
			a := e.Actor
			ae.ActorId = &a
		}
		if e.ActorName != "" {
			n := e.ActorName
			ae.Actor = &n
		}
		if e.OrgID != uuid.Nil {
			o := e.OrgID
			ae.OrgId = &o
		}
		out.Entries = append(out.Entries, ae)
	}
	if p.Next != uuid.Nil {
		n := p.Next
		out.Next = &n
	}
	return out
}

func auditFilter(before *uuid.UUID, limit *int, action *string) audit.Filter {
	var f audit.Filter
	if before != nil {
		f.Before = *before
	}
	if limit != nil {
		f.Limit = *limit
	}
	if action != nil {
		f.Action = *action
	}
	return f
}

func principal(ctx context.Context) auth.Principal {
	p, _ := auth.PrincipalFrom(ctx)
	return p
}

func (s *v1Server) ListMyOrgs(ctx context.Context, _ v1gen.ListMyOrgsRequestObject) (v1gen.ListMyOrgsResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	list, err := s.orgs.ListMyOrgs(ctx, principal(ctx))
	if err != nil {
		return orgsFail(err)
	}
	out := make(v1gen.ListMyOrgs200JSONResponse, 0, len(list))
	for _, o := range list {
		out = append(out, orgDTO(o))
	}
	return out, nil
}

func (s *v1Server) CreateOrg(ctx context.Context, req v1gen.CreateOrgRequestObject) (v1gen.CreateOrgResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	in := orgs.CreateOrgInput{Name: req.Body.Name}
	if req.Body.DisplayName != nil {
		in.DisplayName = *req.Body.DisplayName
	}
	if req.Body.Description != nil {
		in.Description = *req.Body.Description
	}
	o, err := s.orgs.CreateOrg(ctx, principal(ctx), in, clientOf(ctx))
	if err != nil {
		return orgsFail(err)
	}
	return v1gen.CreateOrg201JSONResponse(orgDTO(o)), nil
}

func (s *v1Server) AcceptOrgInvite(ctx context.Context, req v1gen.AcceptOrgInviteRequestObject) (v1gen.AcceptOrgInviteResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	a, err := s.orgs.AcceptInvite(ctx, principal(ctx), req.Body.Token, clientOf(ctx))
	if err != nil {
		return orgsFail(err)
	}
	return v1gen.AcceptOrgInvite200JSONResponse{Org: a.Org, Role: v1gen.OrgRole(a.Role)}, nil
}

func (s *v1Server) GetOrg(ctx context.Context, req v1gen.GetOrgRequestObject) (v1gen.GetOrgResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	o, err := s.orgs.GetOrg(ctx, principal(ctx), req.Org)
	if err != nil {
		return orgsFail(err)
	}
	return v1gen.GetOrg200JSONResponse(orgDTO(o)), nil
}

func (s *v1Server) UpdateOrg(ctx context.Context, req v1gen.UpdateOrgRequestObject) (v1gen.UpdateOrgResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	in := orgs.UpdateOrgInput{DisplayName: req.Body.DisplayName, Description: req.Body.Description,
		Require2FA: req.Body.Require2fa}
	if req.Body.BasePermission != nil {
		bp := string(*req.Body.BasePermission)
		in.BasePermission = &bp
	}
	o, err := s.orgs.UpdateOrg(ctx, principal(ctx), req.Org, in, clientOf(ctx))
	if err != nil {
		return orgsFail(err)
	}
	return v1gen.UpdateOrg200JSONResponse(orgDTO(o)), nil
}

func (s *v1Server) DeleteOrg(ctx context.Context, req v1gen.DeleteOrgRequestObject) (v1gen.DeleteOrgResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	if err := s.orgs.DeleteOrg(ctx, principal(ctx), req.Org, clientOf(ctx)); err != nil {
		return orgsFail(err)
	}
	return v1gen.DeleteOrg204Response{}, nil
}

func (s *v1Server) ListOrgMembers(ctx context.Context, req v1gen.ListOrgMembersRequestObject) (v1gen.ListOrgMembersResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	list, err := s.orgs.ListMembers(ctx, principal(ctx), req.Org)
	if err != nil {
		return orgsFail(err)
	}
	out := make(v1gen.ListOrgMembers200JSONResponse, 0, len(list))
	for _, m := range list {
		out = append(out, memberDTO(m))
	}
	return out, nil
}

func (s *v1Server) SetOrgMemberRole(ctx context.Context, req v1gen.SetOrgMemberRoleRequestObject) (v1gen.SetOrgMemberRoleResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	m, err := s.orgs.SetMemberRole(ctx, principal(ctx), req.Org, req.Username, string(req.Body.Role), clientOf(ctx))
	if err != nil {
		return orgsFail(err)
	}
	return v1gen.SetOrgMemberRole200JSONResponse(memberDTO(m)), nil
}

func (s *v1Server) RemoveOrgMember(ctx context.Context, req v1gen.RemoveOrgMemberRequestObject) (v1gen.RemoveOrgMemberResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	if err := s.orgs.RemoveMember(ctx, principal(ctx), req.Org, req.Username, clientOf(ctx)); err != nil {
		return orgsFail(err)
	}
	return v1gen.RemoveOrgMember204Response{}, nil
}

func (s *v1Server) ListTeams(ctx context.Context, req v1gen.ListTeamsRequestObject) (v1gen.ListTeamsResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	list, err := s.orgs.ListTeams(ctx, principal(ctx), req.Org)
	if err != nil {
		return orgsFail(err)
	}
	out := make(v1gen.ListTeams200JSONResponse, 0, len(list))
	for _, t := range list {
		out = append(out, teamDTO(t))
	}
	return out, nil
}

func (s *v1Server) CreateTeam(ctx context.Context, req v1gen.CreateTeamRequestObject) (v1gen.CreateTeamResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	t, err := s.orgs.CreateTeam(ctx, principal(ctx), req.Org, orgs.TeamInput{Name: &req.Body.Name, Description: req.Body.Description}, clientOf(ctx))
	if err != nil {
		return orgsFail(err)
	}
	return v1gen.CreateTeam201JSONResponse(teamDTO(t)), nil
}

func (s *v1Server) GetTeam(ctx context.Context, req v1gen.GetTeamRequestObject) (v1gen.GetTeamResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	t, err := s.orgs.GetTeam(ctx, principal(ctx), req.Org, req.Team)
	if err != nil {
		return orgsFail(err)
	}
	return v1gen.GetTeam200JSONResponse(teamDTO(t)), nil
}

func (s *v1Server) UpdateTeam(ctx context.Context, req v1gen.UpdateTeamRequestObject) (v1gen.UpdateTeamResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	t, err := s.orgs.UpdateTeam(ctx, principal(ctx), req.Org, req.Team, orgs.TeamInput{Name: req.Body.Name, Description: req.Body.Description}, clientOf(ctx))
	if err != nil {
		return orgsFail(err)
	}
	return v1gen.UpdateTeam200JSONResponse(teamDTO(t)), nil
}

func (s *v1Server) DeleteTeam(ctx context.Context, req v1gen.DeleteTeamRequestObject) (v1gen.DeleteTeamResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	if err := s.orgs.DeleteTeam(ctx, principal(ctx), req.Org, req.Team, clientOf(ctx)); err != nil {
		return orgsFail(err)
	}
	return v1gen.DeleteTeam204Response{}, nil
}

func (s *v1Server) ListTeamMembers(ctx context.Context, req v1gen.ListTeamMembersRequestObject) (v1gen.ListTeamMembersResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	list, err := s.orgs.ListTeamMembers(ctx, principal(ctx), req.Org, req.Team)
	if err != nil {
		return orgsFail(err)
	}
	out := make(v1gen.ListTeamMembers200JSONResponse, 0, len(list))
	for _, m := range list {
		out = append(out, v1gen.TeamMember{Username: m.Username, DisplayName: m.DisplayName,
			Role: v1gen.TeamMemberRole(m.Role), Since: m.Since})
	}
	return out, nil
}

func (s *v1Server) SetTeamMember(ctx context.Context, req v1gen.SetTeamMemberRequestObject) (v1gen.SetTeamMemberResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	if err := s.orgs.SetTeamMember(ctx, principal(ctx), req.Org, req.Team, req.Username, string(req.Body.Role), clientOf(ctx)); err != nil {
		return orgsFail(err)
	}
	return v1gen.SetTeamMember204Response{}, nil
}

func (s *v1Server) RemoveTeamMember(ctx context.Context, req v1gen.RemoveTeamMemberRequestObject) (v1gen.RemoveTeamMemberResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	if err := s.orgs.RemoveTeamMember(ctx, principal(ctx), req.Org, req.Team, req.Username, clientOf(ctx)); err != nil {
		return orgsFail(err)
	}
	return v1gen.RemoveTeamMember204Response{}, nil
}

func (s *v1Server) ListOrgInvites(ctx context.Context, req v1gen.ListOrgInvitesRequestObject) (v1gen.ListOrgInvitesResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	list, err := s.orgs.ListInvites(ctx, principal(ctx), req.Org)
	if err != nil {
		return orgsFail(err)
	}
	out := make(v1gen.ListOrgInvites200JSONResponse, 0, len(list))
	for _, i := range list {
		out = append(out, inviteDTO(i))
	}
	return out, nil
}

func (s *v1Server) CreateOrgInvite(ctx context.Context, req v1gen.CreateOrgInviteRequestObject) (v1gen.CreateOrgInviteResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	in := orgs.InviteInput{Email: req.Body.Email, Role: string(req.Body.Role)}
	if req.Body.Teams != nil {
		in.Teams = *req.Body.Teams
	}
	inv, err := s.orgs.CreateInvite(ctx, principal(ctx), req.Org, in, clientOf(ctx))
	if err != nil {
		return orgsFail(err)
	}
	return v1gen.CreateOrgInvite201JSONResponse(inviteDTO(inv)), nil
}

func (s *v1Server) RevokeOrgInvite(ctx context.Context, req v1gen.RevokeOrgInviteRequestObject) (v1gen.RevokeOrgInviteResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	if err := s.orgs.RevokeInvite(ctx, principal(ctx), req.Org, req.Id, clientOf(ctx)); err != nil {
		return orgsFail(err)
	}
	return v1gen.RevokeOrgInvite204Response{}, nil
}

func (s *v1Server) GetOrgQuota(ctx context.Context, req v1gen.GetOrgQuotaRequestObject) (v1gen.GetOrgQuotaResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	st, err := s.orgs.Quota(ctx, principal(ctx), req.Org)
	if err != nil {
		return orgsFail(err)
	}
	return v1gen.GetOrgQuota200JSONResponse{
		MaxRepos: clampInt32(st.MaxRepos), MaxStorageBytes: st.MaxStorage,
		Repos: clampInt32(st.Repos), StorageBytes: st.StorageBytes,
	}, nil
}

func clampInt32(v int64) int32 {
	return int32(min(max(v, -1<<31), 1<<31-1)) //nolint:gosec // clamped
}

func (s *v1Server) ListOrgAudit(ctx context.Context, req v1gen.ListOrgAuditRequestObject) (v1gen.ListOrgAuditResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	p := req.Params
	page, err := s.orgs.AuditLog(ctx, principal(ctx), req.Org, auditFilter(p.Before, p.Limit, p.Action))
	if err != nil {
		return orgsFail(err)
	}
	return v1gen.ListOrgAudit200JSONResponse(auditPageDTO(page)), nil
}

// ListAdminAudit serves the instance audit log; the router and the
// operation's security requirement restrict it to instance admins.
func (s *v1Server) ListAdminAudit(ctx context.Context, req v1gen.ListAdminAuditRequestObject) (v1gen.ListAdminAuditResponseObject, error) {
	if s.audit == nil {
		return problemResponse{status: http.StatusServiceUnavailable, detail: "the audit log is not available"}, nil
	}
	if !principal(ctx).IsAdmin() {
		return problemResponse{status: http.StatusForbidden, detail: "instance admin required"}, nil
	}
	p := req.Params
	f := auditFilter(p.Before, p.Limit, p.Action)
	if p.Actor != nil {
		f.Actor = *p.Actor
	}
	if p.Org != nil {
		f.OrgID = *p.Org
	}
	page, err := audit.List(ctx, s.audit, f)
	if err != nil {
		return nil, err
	}
	return v1gen.ListAdminAudit200JSONResponse(auditPageDTO(page)), nil
}

// Visit methods let a problemResponse answer every /orgs operation.
func (p problemResponse) VisitListMyOrgsResponse(w http.ResponseWriter) error      { return p.write(w) }
func (p problemResponse) VisitCreateOrgResponse(w http.ResponseWriter) error       { return p.write(w) }
func (p problemResponse) VisitAcceptOrgInviteResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitGetOrgResponse(w http.ResponseWriter) error          { return p.write(w) }
func (p problemResponse) VisitUpdateOrgResponse(w http.ResponseWriter) error       { return p.write(w) }
func (p problemResponse) VisitDeleteOrgResponse(w http.ResponseWriter) error       { return p.write(w) }
func (p problemResponse) VisitListOrgMembersResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitSetOrgMemberRoleResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitRemoveOrgMemberResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitListTeamsResponse(w http.ResponseWriter) error       { return p.write(w) }
func (p problemResponse) VisitCreateTeamResponse(w http.ResponseWriter) error      { return p.write(w) }
func (p problemResponse) VisitGetTeamResponse(w http.ResponseWriter) error         { return p.write(w) }
func (p problemResponse) VisitUpdateTeamResponse(w http.ResponseWriter) error      { return p.write(w) }
func (p problemResponse) VisitDeleteTeamResponse(w http.ResponseWriter) error      { return p.write(w) }
func (p problemResponse) VisitListTeamMembersResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitSetTeamMemberResponse(w http.ResponseWriter) error   { return p.write(w) }
func (p problemResponse) VisitRemoveTeamMemberResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListOrgInvitesResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitCreateOrgInviteResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitRevokeOrgInviteResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitGetOrgQuotaResponse(w http.ResponseWriter) error     { return p.write(w) }
func (p problemResponse) VisitListOrgAuditResponse(w http.ResponseWriter) error    { return p.write(w) }
func (p problemResponse) VisitListAdminAuditResponse(w http.ResponseWriter) error  { return p.write(w) }
