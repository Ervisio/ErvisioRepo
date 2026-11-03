package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/protection"
)

// Protection serves the branch protection and tag rule endpoints; it is
// implemented by *protection.Engine.
type Protection interface {
	ListBranchRules(ctx context.Context, who auth.Principal, owner, name string) ([]protection.BranchRule, error)
	GetBranchRule(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (protection.BranchRule, error)
	CreateBranchRule(ctx context.Context, who auth.Principal, owner, name string, in protection.BranchRuleInput) (protection.BranchRule, error)
	UpdateBranchRule(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, in protection.BranchRuleInput) (protection.BranchRule, error)
	DeleteBranchRule(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) error
	ListTagRules(ctx context.Context, who auth.Principal, owner, name string) ([]protection.TagRule, error)
	GetTagRule(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (protection.TagRule, error)
	CreateTagRule(ctx context.Context, who auth.Principal, owner, name string, in protection.TagRuleInput) (protection.TagRule, error)
	UpdateTagRule(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, in protection.TagRuleInput) (protection.TagRule, error)
	DeleteTagRule(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) error
	Evaluate(ctx context.Context, who auth.Principal, owner, name, ref, oldID, newID, user string) (protection.Result, error)
}

var _ Protection = (*protection.Engine)(nil)

var errProtectionUnavailable = problemResponse{status: http.StatusServiceUnavailable, detail: "branch protection is not available"}

// protectionFail maps a protection error to a problem response.
func protectionFail(err error, method, suffix string) (protectionResponse, error) {
	switch {
	case errors.Is(err, protection.ErrInvalid), errors.Is(err, git.ErrInvalid):
		return badRequest(err.Error()), nil
	case errors.Is(err, protection.ErrNotFound), errors.Is(err, gitstore.ErrNotFound):
		return problemResponse{status: http.StatusNotFound, detail: err.Error()}, nil
	case errors.Is(err, protection.ErrConflict):
		return problemResponse{status: http.StatusConflict, detail: err.Error()}, nil
	case errors.Is(err, protection.ErrUnavailable):
		return errProtectionUnavailable, nil
	case errors.Is(err, gitstore.ErrBusy), errors.Is(err, gitstore.ErrCold):
		return problemResponse{status: http.StatusServiceUnavailable, detail: "repository is being prepared, retry later",
			retryAfter: retryHydrating}, nil
	case errors.Is(err, gitstore.ErrDegraded), errors.Is(err, gitstore.ErrHotMissing):
		return problemResponse{status: http.StatusServiceUnavailable, detail: "repository is temporarily unavailable"}, nil
	}
	r, err2 := reposFail(err, method, suffix)
	if err2 != nil {
		return nil, err2
	}
	return r.(protectionResponse), nil
}

// protectionResponse is answered by problemResponse and movedResponse.
type protectionResponse interface {
	v1gen.ListBranchProtectionsResponseObject
	v1gen.CreateBranchProtectionResponseObject
	v1gen.EvaluateRefUpdateResponseObject
	v1gen.GetBranchProtectionResponseObject
	v1gen.UpdateBranchProtectionResponseObject
	v1gen.DeleteBranchProtectionResponseObject
	v1gen.ListTagRulesResponseObject
	v1gen.CreateTagRuleResponseObject
	v1gen.GetTagRuleResponseObject
	v1gen.UpdateTagRuleResponseObject
	v1gen.DeleteTagRuleResponseObject
}

var (
	_ protectionResponse = problemResponse{}
	_ protectionResponse = movedResponse{}
)

func (p problemResponse) VisitListBranchProtectionsResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitCreateBranchProtectionResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitEvaluateRefUpdateResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitGetBranchProtectionResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitUpdateBranchProtectionResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitDeleteBranchProtectionResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListTagRulesResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitCreateTagRuleResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitGetTagRuleResponse(w http.ResponseWriter) error    { return p.write(w) }
func (p problemResponse) VisitUpdateTagRuleResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitDeleteTagRuleResponse(w http.ResponseWriter) error { return p.write(w) }

func (m movedResponse) VisitListBranchProtectionsResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitCreateBranchProtectionResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitEvaluateRefUpdateResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitGetBranchProtectionResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitUpdateBranchProtectionResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitDeleteBranchProtectionResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitListTagRulesResponse(w http.ResponseWriter) error  { return m.write(w) }
func (m movedResponse) VisitCreateTagRuleResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitGetTagRuleResponse(w http.ResponseWriter) error    { return m.write(w) }
func (m movedResponse) VisitUpdateTagRuleResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitDeleteTagRuleResponse(w http.ResponseWriter) error { return m.write(w) }

const (
	branchRulesSuffix = "/branch-protections"
	tagRulesSuffix    = "/tag-rules"
)

func boolOf(p *bool) bool { return p != nil && *p }

func listOf(p *[]string) []string {
	if p == nil {
		return nil
	}
	return *p
}

func branchRuleInput(b *v1gen.BranchProtectionInput) protection.BranchRuleInput {
	in := protection.BranchRuleInput{
		Pattern:                 b.Pattern,
		RequireCodeOwnerReviews: boolOf(b.RequireCodeOwnerReviews),
		DismissStaleReviews:     boolOf(b.DismissStaleReviews),
		RequiredStatusChecks:    listOf(b.RequiredStatusChecks),
		StrictStatusChecks:      boolOf(b.StrictStatusChecks),
		RequireLinearHistory:    boolOf(b.RequireLinearHistory),
		AllowForcePushes:        boolOf(b.AllowForcePushes),
		AllowDeletions:          boolOf(b.AllowDeletions),
		RequireSignedCommits:    boolOf(b.RequireSignedCommits),
		RestrictPushes:          boolOf(b.RestrictPushes),
		PushUsers:               listOf(b.PushUsers),
		PushTeams:               listOf(b.PushTeams),
		AdminsBypass:            boolOf(b.AdminsBypass),
	}
	if b.RequiredApprovals != nil {
		in.RequiredApprovals = *b.RequiredApprovals
	}
	return in
}

func branchRuleDTO(r protection.BranchRule) v1gen.BranchProtection {
	checks := r.RequiredStatusChecks
	if checks == nil {
		checks = []string{}
	}
	return v1gen.BranchProtection{
		Id: r.ID, Pattern: r.Pattern, RequiredApprovals: int(r.RequiredApprovals),
		RequireCodeOwnerReviews: r.RequireCodeOwnerReviews, DismissStaleReviews: r.DismissStaleReviews,
		RequiredStatusChecks: checks, StrictStatusChecks: r.StrictStatusChecks,
		RequireLinearHistory: r.RequireLinearHistory, AllowForcePushes: r.AllowForcePushes,
		AllowDeletions: r.AllowDeletions, RequireSignedCommits: r.RequireSignedCommits,
		RestrictPushes: r.RestrictPushes, PushUsers: r.PushUsers, PushTeams: r.PushTeams,
		AdminsBypass: r.AdminsBypass, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time,
	}
}

func levelOf(p *v1gen.TagRuleLevel) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

func tagRuleInput(b *v1gen.TagRuleInput) protection.TagRuleInput {
	return protection.TagRuleInput{
		Pattern: b.Pattern, Create: levelOf(b.Create), Update: levelOf(b.Update), Delete: levelOf(b.Delete),
		AllowedUsers: listOf(b.AllowedUsers), AllowedTeams: listOf(b.AllowedTeams), AdminsBypass: boolOf(b.AdminsBypass),
	}
}

func tagRuleDTO(r protection.TagRule) v1gen.TagRule {
	return v1gen.TagRule{
		Id: r.ID, Pattern: r.Pattern, Create: v1gen.TagRuleLevel(r.CreateMin), Update: v1gen.TagRuleLevel(r.UpdateMin),
		Delete: v1gen.TagRuleLevel(r.DeleteMin), AllowedUsers: r.AllowedUsers, AllowedTeams: r.AllowedTeams,
		AdminsBypass: r.AdminsBypass, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time,
	}
}

func (s *v1Server) ListBranchProtections(ctx context.Context, req v1gen.ListBranchProtectionsRequestObject) (v1gen.ListBranchProtectionsResponseObject, error) {
	if s.protection == nil {
		return errProtectionUnavailable, nil
	}
	list, err := s.protection.ListBranchRules(ctx, principal(ctx), req.Owner, req.Name)
	if err != nil {
		return protectionFail(err, http.MethodGet, branchRulesSuffix)
	}
	out := make(v1gen.ListBranchProtections200JSONResponse, 0, len(list))
	for _, r := range list {
		out = append(out, branchRuleDTO(r))
	}
	return out, nil
}

func (s *v1Server) CreateBranchProtection(ctx context.Context, req v1gen.CreateBranchProtectionRequestObject) (v1gen.CreateBranchProtectionResponseObject, error) {
	if s.protection == nil {
		return errProtectionUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	r, err := s.protection.CreateBranchRule(ctx, principal(ctx), req.Owner, req.Name, branchRuleInput(req.Body))
	if err != nil {
		return protectionFail(err, http.MethodPost, branchRulesSuffix)
	}
	return v1gen.CreateBranchProtection201JSONResponse(branchRuleDTO(r)), nil
}

func (s *v1Server) GetBranchProtection(ctx context.Context, req v1gen.GetBranchProtectionRequestObject) (v1gen.GetBranchProtectionResponseObject, error) {
	if s.protection == nil {
		return errProtectionUnavailable, nil
	}
	r, err := s.protection.GetBranchRule(ctx, principal(ctx), req.Owner, req.Name, req.RuleId)
	if err != nil {
		return protectionFail(err, http.MethodGet, branchRulesSuffix+"/"+req.RuleId.String())
	}
	return v1gen.GetBranchProtection200JSONResponse(branchRuleDTO(r)), nil
}

func (s *v1Server) UpdateBranchProtection(ctx context.Context, req v1gen.UpdateBranchProtectionRequestObject) (v1gen.UpdateBranchProtectionResponseObject, error) {
	if s.protection == nil {
		return errProtectionUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	r, err := s.protection.UpdateBranchRule(ctx, principal(ctx), req.Owner, req.Name, req.RuleId, branchRuleInput(req.Body))
	if err != nil {
		return protectionFail(err, http.MethodPut, branchRulesSuffix+"/"+req.RuleId.String())
	}
	return v1gen.UpdateBranchProtection200JSONResponse(branchRuleDTO(r)), nil
}

func (s *v1Server) DeleteBranchProtection(ctx context.Context, req v1gen.DeleteBranchProtectionRequestObject) (v1gen.DeleteBranchProtectionResponseObject, error) {
	if s.protection == nil {
		return errProtectionUnavailable, nil
	}
	if err := s.protection.DeleteBranchRule(ctx, principal(ctx), req.Owner, req.Name, req.RuleId); err != nil {
		return protectionFail(err, http.MethodDelete, branchRulesSuffix+"/"+req.RuleId.String())
	}
	return v1gen.DeleteBranchProtection204Response{}, nil
}

func (s *v1Server) EvaluateRefUpdate(ctx context.Context, req v1gen.EvaluateRefUpdateRequestObject) (v1gen.EvaluateRefUpdateResponseObject, error) {
	if s.protection == nil {
		return errProtectionUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	b := req.Body
	res, err := s.protection.Evaluate(ctx, principal(ctx), req.Owner, req.Name, b.Ref, str(b.Old), str(b.New), str(b.User))
	if err != nil {
		return protectionFail(err, http.MethodPost, branchRulesSuffix+"/evaluate")
	}
	out := v1gen.EvaluateRefUpdate200JSONResponse{Ref: res.Ref, Kind: v1gen.RefUpdateEvaluationKind(res.Kind),
		Allowed: res.Allowed(), Rule: res.Rule, Violations: make([]v1gen.ProtectionViolation, 0, len(res.Violations))}
	for _, v := range res.Violations {
		pv := v1gen.ProtectionViolation{Code: v.Code, Message: v.Message}
		if v.Commit != "" {
			pv.Commit = &v.Commit
		}
		out.Violations = append(out.Violations, pv)
	}
	return out, nil
}

func (s *v1Server) ListTagRules(ctx context.Context, req v1gen.ListTagRulesRequestObject) (v1gen.ListTagRulesResponseObject, error) {
	if s.protection == nil {
		return errProtectionUnavailable, nil
	}
	list, err := s.protection.ListTagRules(ctx, principal(ctx), req.Owner, req.Name)
	if err != nil {
		return protectionFail(err, http.MethodGet, tagRulesSuffix)
	}
	out := make(v1gen.ListTagRules200JSONResponse, 0, len(list))
	for _, r := range list {
		out = append(out, tagRuleDTO(r))
	}
	return out, nil
}

func (s *v1Server) CreateTagRule(ctx context.Context, req v1gen.CreateTagRuleRequestObject) (v1gen.CreateTagRuleResponseObject, error) {
	if s.protection == nil {
		return errProtectionUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	r, err := s.protection.CreateTagRule(ctx, principal(ctx), req.Owner, req.Name, tagRuleInput(req.Body))
	if err != nil {
		return protectionFail(err, http.MethodPost, tagRulesSuffix)
	}
	return v1gen.CreateTagRule201JSONResponse(tagRuleDTO(r)), nil
}

func (s *v1Server) GetTagRule(ctx context.Context, req v1gen.GetTagRuleRequestObject) (v1gen.GetTagRuleResponseObject, error) {
	if s.protection == nil {
		return errProtectionUnavailable, nil
	}
	r, err := s.protection.GetTagRule(ctx, principal(ctx), req.Owner, req.Name, req.RuleId)
	if err != nil {
		return protectionFail(err, http.MethodGet, tagRulesSuffix+"/"+req.RuleId.String())
	}
	return v1gen.GetTagRule200JSONResponse(tagRuleDTO(r)), nil
}

func (s *v1Server) UpdateTagRule(ctx context.Context, req v1gen.UpdateTagRuleRequestObject) (v1gen.UpdateTagRuleResponseObject, error) {
	if s.protection == nil {
		return errProtectionUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	r, err := s.protection.UpdateTagRule(ctx, principal(ctx), req.Owner, req.Name, req.RuleId, tagRuleInput(req.Body))
	if err != nil {
		return protectionFail(err, http.MethodPut, tagRulesSuffix+"/"+req.RuleId.String())
	}
	return v1gen.UpdateTagRule200JSONResponse(tagRuleDTO(r)), nil
}

func (s *v1Server) DeleteTagRule(ctx context.Context, req v1gen.DeleteTagRuleRequestObject) (v1gen.DeleteTagRuleResponseObject, error) {
	if s.protection == nil {
		return errProtectionUnavailable, nil
	}
	if err := s.protection.DeleteTagRule(ctx, principal(ctx), req.Owner, req.Name, req.RuleId); err != nil {
		return protectionFail(err, http.MethodDelete, tagRulesSuffix+"/"+req.RuleId.String())
	}
	return v1gen.DeleteTagRule204Response{}, nil
}
