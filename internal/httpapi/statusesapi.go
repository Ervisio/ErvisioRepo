package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/statuses"
)

// Statuses is the commit status and checks service used by the checks
// endpoints; it is implemented by *statuses.Service.
type Statuses interface {
	CreateStatus(ctx context.Context, who auth.Principal, owner, name, ref string, in statuses.StatusInput) (statuses.Status, error)
	ListStatuses(ctx context.Context, who auth.Principal, owner, name, ref string, offset, limit int) ([]statuses.Status, string, error)
	CombinedStatus(ctx context.Context, who auth.Principal, owner, name, ref string) (statuses.Combined, error)
	CreateCheckRun(ctx context.Context, who auth.Principal, owner, name string, in statuses.CheckRunInput) (statuses.CheckRun, error)
	UpdateCheckRun(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, p statuses.CheckRunPatch) (statuses.CheckRun, error)
	GetCheckRun(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (statuses.CheckRun, error)
	ListCheckRunAnnotations(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, offset, limit int) ([]statuses.Annotation, error)
	ListCheckRuns(ctx context.Context, who auth.Principal, owner, name, ref string, f statuses.CheckRunFilter) ([]statuses.CheckRun, int, string, error)
	ListSuiteCheckRuns(ctx context.Context, who auth.Principal, owner, name string, suiteID uuid.UUID, f statuses.CheckRunFilter) ([]statuses.CheckRun, int, error)
	GetCheckSuite(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (statuses.CheckSuite, error)
	ListCheckSuites(ctx context.Context, who auth.Principal, owner, name, ref, app string, offset, limit int) ([]statuses.CheckSuite, int, string, error)
}

var _ Statuses = (*statuses.Service)(nil)

var errStatusesUnavailable = problemResponse{status: http.StatusServiceUnavailable, detail: "commit statuses and checks are not available"}

// statusesFail maps a statuses service error to a problem response. An
// unknown commit is 422 on a write (the body named it) and 404 on a read.
func statusesFail(err error, method, suffix string) (statusResponse, error) {
	var ve *statuses.ValidationError
	switch {
	case errors.As(err, &ve):
		return badRequest(err.Error()), nil
	case errors.Is(err, statuses.ErrCommitNotFound):
		if method != http.MethodGet {
			return problemResponse{status: http.StatusUnprocessableEntity, detail: err.Error()}, nil
		}
		return problemResponse{status: http.StatusNotFound, detail: err.Error()}, nil
	case errors.Is(err, statuses.ErrCheckRunNotFound), errors.Is(err, statuses.ErrCheckSuiteNotFound):
		return problemResponse{status: http.StatusNotFound, detail: err.Error()}, nil
	}
	r, err2 := browseFail(err, method, suffix)
	if err2 != nil {
		return nil, err2
	}
	return r.(statusResponse), nil
}

// statusResponse is answered by problemResponse and movedResponse.
type statusResponse interface {
	v1gen.CreateCommitStatusResponseObject
	v1gen.ListCommitStatusesResponseObject
	v1gen.GetCombinedStatusResponseObject
	v1gen.CreateCheckRunResponseObject
	v1gen.GetCheckRunResponseObject
	v1gen.UpdateCheckRunResponseObject
	v1gen.ListCheckRunAnnotationsResponseObject
	v1gen.ListCheckRunsForRefResponseObject
	v1gen.ListCheckSuitesForRefResponseObject
	v1gen.GetCheckSuiteResponseObject
	v1gen.ListCheckSuiteRunsResponseObject
}

var (
	_ statusResponse = problemResponse{}
	_ statusResponse = movedResponse{}
)

func statusDTO(s statuses.Status) v1gen.CommitStatus {
	return v1gen.CommitStatus{Id: s.ID, Sha: s.SHA, State: v1gen.CommitStatusState(s.State), Context: s.Context,
		Description: s.Description, TargetUrl: s.TargetURL, Creator: s.Creator, CreatedAt: s.CreatedAt}
}

func checkRunDTO(r statuses.CheckRun) v1gen.CheckRun {
	return v1gen.CheckRun{Id: r.ID, SuiteId: r.SuiteID, HeadSha: r.HeadSHA, HeadBranch: r.HeadBranch, Name: r.Name,
		App: r.App, Status: v1gen.CheckRunStatus(r.Status), Conclusion: r.Conclusion, ExternalId: r.ExternalID,
		DetailsUrl: r.DetailsURL, Title: r.Title, Summary: r.Summary, Text: r.Text, AnnotationsCount: r.Annotations,
		StartedAt: r.StartedAt, CompletedAt: r.CompletedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func checkRunListDTO(list []statuses.CheckRun, total int) v1gen.CheckRunList {
	out := v1gen.CheckRunList{TotalCount: total, CheckRuns: make([]v1gen.CheckRun, 0, len(list))}
	for _, r := range list {
		out.CheckRuns = append(out.CheckRuns, checkRunDTO(r))
	}
	return out
}

func checkSuiteDTO(s statuses.CheckSuite) v1gen.CheckSuite {
	return v1gen.CheckSuite{Id: s.ID, HeadSha: s.HeadSHA, HeadBranch: s.HeadBranch, App: s.App,
		Status: v1gen.CheckSuiteStatus(s.Status), Conclusion: s.Conclusion, RunsCount: s.Runs,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt}
}

func annotationDTO(a statuses.Annotation) v1gen.CheckAnnotation {
	return v1gen.CheckAnnotation{Path: a.Path, StartLine: a.StartLine, EndLine: a.EndLine, StartColumn: a.StartColumn,
		EndColumn: a.EndColumn, Level: v1gen.CheckAnnotationLevel(a.Level), Message: a.Message,
		Title: optString(a.Title), RawDetails: optString(a.RawDetails)}
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func outputIn(o *v1gen.CheckRunOutput) *statuses.Output {
	if o == nil {
		return nil
	}
	out := &statuses.Output{Title: deref(o.Title), Summary: deref(o.Summary), Text: deref(o.Text)}
	if o.Annotations != nil {
		for _, a := range *o.Annotations {
			out.Annotations = append(out.Annotations, statuses.Annotation{Path: a.Path, StartLine: a.StartLine,
				EndLine: a.EndLine, StartColumn: a.StartColumn, EndColumn: a.EndColumn, Level: string(a.Level),
				Message: a.Message, Title: deref(a.Title), RawDetails: deref(a.RawDetails)})
		}
	}
	return out
}

func strPtr[T ~string](p *T) *string {
	if p == nil {
		return nil
	}
	s := string(*p)
	return &s
}

func refSuffix(kind, ref, tail string) string { return "/" + kind + "/" + url.PathEscape(ref) + tail }

func checkRunSuffix(id uuid.UUID, tail string) string { return "/check-runs/" + id.String() + tail }

func (s *v1Server) CreateCommitStatus(ctx context.Context, req v1gen.CreateCommitStatusRequestObject) (v1gen.CreateCommitStatusResponseObject, error) {
	if s.statuses == nil {
		return errStatusesUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	b := req.Body
	in := statuses.StatusInput{State: string(b.State), Context: deref(b.Context), Description: deref(b.Description),
		TargetURL: deref(b.TargetUrl)}
	st, err := s.statuses.CreateStatus(ctx, principal(ctx), req.Owner, req.Name, req.Sha, in)
	if err != nil {
		return statusesFail(err, http.MethodPost, refSuffix("statuses", req.Sha, ""))
	}
	return v1gen.CreateCommitStatus201JSONResponse(statusDTO(st)), nil
}

func (s *v1Server) ListCommitStatuses(ctx context.Context, req v1gen.ListCommitStatusesRequestObject) (v1gen.ListCommitStatusesResponseObject, error) {
	if s.statuses == nil {
		return errStatusesUnavailable, nil
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit)
	list, _, err := s.statuses.ListStatuses(ctx, principal(ctx), req.Owner, req.Name, req.Sha, offset, limit)
	if err != nil {
		return statusesFail(err, http.MethodGet, refSuffix("statuses", req.Sha, ""))
	}
	out := make(v1gen.ListCommitStatuses200JSONResponse, 0, len(list))
	for _, st := range list {
		out = append(out, statusDTO(st))
	}
	return out, nil
}

func (s *v1Server) GetCombinedStatus(ctx context.Context, req v1gen.GetCombinedStatusRequestObject) (v1gen.GetCombinedStatusResponseObject, error) {
	if s.statuses == nil {
		return errStatusesUnavailable, nil
	}
	c, err := s.statuses.CombinedStatus(ctx, principal(ctx), req.Owner, req.Name, req.Sha)
	if err != nil {
		return statusesFail(err, http.MethodGet, refSuffix("commits", req.Sha, "/status"))
	}
	out := v1gen.GetCombinedStatus200JSONResponse{Sha: c.SHA, State: v1gen.CombinedStatusState(c.State),
		TotalCount: len(c.Statuses), Statuses: make([]v1gen.CommitStatus, 0, len(c.Statuses))}
	for _, st := range c.Statuses {
		out.Statuses = append(out.Statuses, statusDTO(st))
	}
	return out, nil
}

func (s *v1Server) CreateCheckRun(ctx context.Context, req v1gen.CreateCheckRunRequestObject) (v1gen.CreateCheckRunResponseObject, error) {
	if s.statuses == nil {
		return errStatusesUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	b := req.Body
	in := statuses.CheckRunInput{Name: b.Name, HeadSHA: b.HeadSha, Status: deref(strPtr(b.Status)),
		Conclusion: deref(strPtr(b.Conclusion)), ExternalID: deref(b.ExternalId), DetailsURL: deref(b.DetailsUrl),
		StartedAt: b.StartedAt, CompletedAt: b.CompletedAt, Output: outputIn(b.Output), App: deref(b.App)}
	run, err := s.statuses.CreateCheckRun(ctx, principal(ctx), req.Owner, req.Name, in)
	if err != nil {
		return statusesFail(err, http.MethodPost, "/check-runs")
	}
	return v1gen.CreateCheckRun201JSONResponse(checkRunDTO(run)), nil
}

func (s *v1Server) GetCheckRun(ctx context.Context, req v1gen.GetCheckRunRequestObject) (v1gen.GetCheckRunResponseObject, error) {
	if s.statuses == nil {
		return errStatusesUnavailable, nil
	}
	run, err := s.statuses.GetCheckRun(ctx, principal(ctx), req.Owner, req.Name, req.CheckRunId)
	if err != nil {
		return statusesFail(err, http.MethodGet, checkRunSuffix(req.CheckRunId, ""))
	}
	return v1gen.GetCheckRun200JSONResponse(checkRunDTO(run)), nil
}

func (s *v1Server) UpdateCheckRun(ctx context.Context, req v1gen.UpdateCheckRunRequestObject) (v1gen.UpdateCheckRunResponseObject, error) {
	if s.statuses == nil {
		return errStatusesUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	b := req.Body
	p := statuses.CheckRunPatch{Name: b.Name, Status: strPtr(b.Status), Conclusion: strPtr(b.Conclusion),
		ExternalID: b.ExternalId, DetailsURL: b.DetailsUrl, StartedAt: b.StartedAt, CompletedAt: b.CompletedAt,
		Output: outputIn(b.Output)}
	run, err := s.statuses.UpdateCheckRun(ctx, principal(ctx), req.Owner, req.Name, req.CheckRunId, p)
	if err != nil {
		return statusesFail(err, http.MethodPatch, checkRunSuffix(req.CheckRunId, ""))
	}
	return v1gen.UpdateCheckRun200JSONResponse(checkRunDTO(run)), nil
}

func (s *v1Server) ListCheckRunAnnotations(ctx context.Context, req v1gen.ListCheckRunAnnotationsRequestObject) (v1gen.ListCheckRunAnnotationsResponseObject, error) {
	if s.statuses == nil {
		return errStatusesUnavailable, nil
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit)
	list, err := s.statuses.ListCheckRunAnnotations(ctx, principal(ctx), req.Owner, req.Name, req.CheckRunId, offset, limit)
	if err != nil {
		return statusesFail(err, http.MethodGet, checkRunSuffix(req.CheckRunId, "/annotations"))
	}
	out := make(v1gen.ListCheckRunAnnotations200JSONResponse, 0, len(list))
	for _, a := range list {
		out = append(out, annotationDTO(a))
	}
	return out, nil
}

func (s *v1Server) ListCheckRunsForRef(ctx context.Context, req v1gen.ListCheckRunsForRefRequestObject) (v1gen.ListCheckRunsForRefResponseObject, error) {
	if s.statuses == nil {
		return errStatusesUnavailable, nil
	}
	pr := req.Params
	offset, limit := page(pr.Offset, pr.Limit)
	f := statuses.CheckRunFilter{Name: deref(pr.CheckName), Status: deref(strPtr(pr.Status)), App: deref(pr.App),
		All: deref(strPtr(pr.Filter)) == "all", Offset: offset, Limit: limit}
	list, total, _, err := s.statuses.ListCheckRuns(ctx, principal(ctx), req.Owner, req.Name, req.Sha, f)
	if err != nil {
		return statusesFail(err, http.MethodGet, refSuffix("commits", req.Sha, "/check-runs"))
	}
	return v1gen.ListCheckRunsForRef200JSONResponse(checkRunListDTO(list, total)), nil
}

func (s *v1Server) ListCheckSuitesForRef(ctx context.Context, req v1gen.ListCheckSuitesForRefRequestObject) (v1gen.ListCheckSuitesForRefResponseObject, error) {
	if s.statuses == nil {
		return errStatusesUnavailable, nil
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit)
	list, total, _, err := s.statuses.ListCheckSuites(ctx, principal(ctx), req.Owner, req.Name, req.Sha, deref(req.Params.App), offset, limit)
	if err != nil {
		return statusesFail(err, http.MethodGet, refSuffix("commits", req.Sha, "/check-suites"))
	}
	out := v1gen.ListCheckSuitesForRef200JSONResponse{TotalCount: total, CheckSuites: make([]v1gen.CheckSuite, 0, len(list))}
	for _, cs := range list {
		out.CheckSuites = append(out.CheckSuites, checkSuiteDTO(cs))
	}
	return out, nil
}

func (s *v1Server) GetCheckSuite(ctx context.Context, req v1gen.GetCheckSuiteRequestObject) (v1gen.GetCheckSuiteResponseObject, error) {
	if s.statuses == nil {
		return errStatusesUnavailable, nil
	}
	cs, err := s.statuses.GetCheckSuite(ctx, principal(ctx), req.Owner, req.Name, req.CheckSuiteId)
	if err != nil {
		return statusesFail(err, http.MethodGet, "/check-suites/"+req.CheckSuiteId.String())
	}
	return v1gen.GetCheckSuite200JSONResponse(checkSuiteDTO(cs)), nil
}

func (s *v1Server) ListCheckSuiteRuns(ctx context.Context, req v1gen.ListCheckSuiteRunsRequestObject) (v1gen.ListCheckSuiteRunsResponseObject, error) {
	if s.statuses == nil {
		return errStatusesUnavailable, nil
	}
	pr := req.Params
	offset, limit := page(pr.Offset, pr.Limit)
	f := statuses.CheckRunFilter{Name: deref(pr.CheckName), Status: deref(strPtr(pr.Status)),
		All: deref(strPtr(pr.Filter)) == "all", Offset: offset, Limit: limit}
	list, total, err := s.statuses.ListSuiteCheckRuns(ctx, principal(ctx), req.Owner, req.Name, req.CheckSuiteId, f)
	if err != nil {
		return statusesFail(err, http.MethodGet, "/check-suites/"+req.CheckSuiteId.String()+"/check-runs")
	}
	return v1gen.ListCheckSuiteRuns200JSONResponse(checkRunListDTO(list, total)), nil
}

// Visit methods let problemResponse and movedResponse answer every checks
// operation.
func (p problemResponse) VisitCreateCommitStatusResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListCommitStatusesResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitGetCombinedStatusResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitCreateCheckRunResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitGetCheckRunResponse(w http.ResponseWriter) error    { return p.write(w) }
func (p problemResponse) VisitUpdateCheckRunResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitListCheckRunAnnotationsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListCheckRunsForRefResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListCheckSuitesForRefResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitGetCheckSuiteResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitListCheckSuiteRunsResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (m movedResponse) VisitCreateCommitStatusResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitListCommitStatusesResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitGetCombinedStatusResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitCreateCheckRunResponse(w http.ResponseWriter) error    { return m.write(w) }
func (m movedResponse) VisitGetCheckRunResponse(w http.ResponseWriter) error       { return m.write(w) }
func (m movedResponse) VisitUpdateCheckRunResponse(w http.ResponseWriter) error    { return m.write(w) }
func (m movedResponse) VisitListCheckRunAnnotationsResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitListCheckRunsForRefResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitListCheckSuitesForRefResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitGetCheckSuiteResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitListCheckSuiteRunsResponse(w http.ResponseWriter) error {
	return m.write(w)
}
