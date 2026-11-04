package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/actions"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

// Actions is the workflow service used by the Actions endpoints; it is
// implemented by *actions.Service.
type Actions interface {
	ListWorkflows(ctx context.Context, who auth.Principal, owner, name string) ([]actions.Workflow, error)
	ListRuns(ctx context.Context, who auth.Principal, owner, name string, f actions.RunFilter) ([]actions.Run, int64, error)
	GetRun(ctx context.Context, who auth.Principal, owner, name string, runID uuid.UUID) (actions.Run, error)
	ListJobs(ctx context.Context, who auth.Principal, owner, name string, runID uuid.UUID, attempt int32) ([]actions.Job, error)
	GetJob(ctx context.Context, who auth.Principal, owner, name string, jobID uuid.UUID) (actions.Job, []actions.Step, error)
	ReadLog(ctx context.Context, who auth.Principal, owner, name string, jobID uuid.UUID, from int64, limit int) ([]actions.LogLine, bool, error)
	WaitLog(ctx context.Context, who auth.Principal, owner, name string, jobID uuid.UUID, from int64, d time.Duration) error
	RawLog(ctx context.Context, who auth.Principal, owner, name string, jobID uuid.UUID) (io.ReadCloser, error)
	CancelRun(ctx context.Context, who auth.Principal, owner, name string, runID uuid.UUID) error
	CancelJob(ctx context.Context, who auth.Principal, owner, name string, jobID uuid.UUID) error
	RerunRun(ctx context.Context, who auth.Principal, owner, name string, runID uuid.UUID) (actions.Run, error)
	ApproveRun(ctx context.Context, who auth.Principal, owner, name string, runID uuid.UUID) error
	ApproveDeployment(ctx context.Context, who auth.Principal, owner, name string, jobID uuid.UUID, approve bool) error
	Badge(ctx context.Context, who auth.Principal, owner, name, workflow, branch string) ([]byte, error)
	Dispatch(ctx context.Context, who auth.Principal, owner, name, workflow, ref string, inputs map[string]any) (dbgen.ActionsRun, error)

	InstanceScope(who auth.Principal) (actions.Scope, error)
	OwnerScope(ctx context.Context, who auth.Principal, owner string) (actions.Scope, error)
	RepoScope(ctx context.Context, who auth.Principal, owner, name, environment string) (actions.Scope, error)
	ListSecrets(ctx context.Context, sc actions.Scope) ([]actions.Secret, error)
	SetSecret(ctx context.Context, who auth.Principal, sc actions.Scope, name, value string) (actions.Secret, error)
	DeleteSecret(ctx context.Context, sc actions.Scope, name string) error
	ListVariables(ctx context.Context, sc actions.Scope) ([]actions.Variable, error)
	SetVariable(ctx context.Context, who auth.Principal, sc actions.Scope, name, value string) (actions.Variable, error)
	DeleteVariable(ctx context.Context, sc actions.Scope, name string) error
	ListEnvironments(ctx context.Context, who auth.Principal, owner, name string) ([]actions.Environment, error)
	PutEnvironment(ctx context.Context, who auth.Principal, owner, name, env string, in actions.EnvironmentInput) (actions.Environment, error)
	DeleteEnvironment(ctx context.Context, who auth.Principal, owner, name, env string) error
	ListRunners(ctx context.Context, sc actions.Scope) ([]actions.Runner, error)
	ListRepoRunners(ctx context.Context, sc actions.Scope) ([]actions.Runner, error)
	DeleteRunner(ctx context.Context, sc actions.Scope, id uuid.UUID) error
	CreateRegistrationToken(ctx context.Context, who auth.Principal, sc actions.Scope) (actions.RegistrationToken, error)
}

var _ Actions = (*actions.Service)(nil)

var errActionsUnavailable = problemResponse{status: http.StatusServiceUnavailable, detail: "actions are not available"}

// actionsFail maps an actions service error to a problem response, or
// passes it on as a 500. suffix is the part of the request path after
// /repos/{owner}/{name}, kept in the Location of a redirect.
func actionsFail(err error, method, suffix string) (actionsResponse, error) {
	var ve *actions.ValidationError
	switch {
	case errors.As(err, &ve):
		return badRequest(err.Error()), nil
	case actions.IsNotFound(err):
		return problemResponse{status: http.StatusNotFound, detail: err.Error()}, nil
	case errors.Is(err, actions.ErrForbidden):
		return problemResponse{status: http.StatusForbidden, detail: err.Error()}, nil
	case errors.Is(err, actions.ErrConflict):
		return problemResponse{status: http.StatusConflict, detail: err.Error()}, nil
	case errors.Is(err, actions.ErrDisabled), errors.Is(err, actions.ErrNotWired), errors.Is(err, actions.ErrNoMasterKey):
		return problemResponse{status: http.StatusServiceUnavailable, detail: err.Error()}, nil
	}
	r, err2 := reposFail(err, method, suffix)
	if err2 != nil {
		return nil, err2
	}
	return r.(actionsResponse), nil
}

// actionsResponse is answered by problemResponse and movedResponse.
type actionsResponse interface {
	v1gen.ListActionsWorkflowsResponseObject
	v1gen.DispatchActionsWorkflowResponseObject
	v1gen.GetActionsBadgeResponseObject
	v1gen.ListActionsRunsResponseObject
	v1gen.GetActionsRunResponseObject
	v1gen.ListActionsRunJobsResponseObject
	v1gen.RerunActionsRunResponseObject
	v1gen.CancelActionsRunResponseObject
	v1gen.ApproveActionsRunResponseObject
	v1gen.GetActionsJobResponseObject
	v1gen.CancelActionsJobResponseObject
	v1gen.ReviewActionsDeploymentResponseObject
	v1gen.GetActionsJobLogResponseObject
	v1gen.ListRepoActionsSecretsResponseObject
	v1gen.PutRepoActionsSecretResponseObject
	v1gen.DeleteRepoActionsSecretResponseObject
	v1gen.ListRepoActionsVariablesResponseObject
	v1gen.PutRepoActionsVariableResponseObject
	v1gen.DeleteRepoActionsVariableResponseObject
	v1gen.ListActionsEnvironmentsResponseObject
	v1gen.PutActionsEnvironmentResponseObject
	v1gen.DeleteActionsEnvironmentResponseObject
	v1gen.ListRepoActionsRunnersResponseObject
	v1gen.DeleteRepoActionsRunnerResponseObject
	v1gen.CreateRepoActionsRegistrationTokenResponseObject
	v1gen.ListOrgActionsSecretsResponseObject
	v1gen.PutOrgActionsSecretResponseObject
	v1gen.DeleteOrgActionsSecretResponseObject
	v1gen.ListOrgActionsVariablesResponseObject
	v1gen.PutOrgActionsVariableResponseObject
	v1gen.DeleteOrgActionsVariableResponseObject
	v1gen.ListOrgActionsRunnersResponseObject
	v1gen.DeleteOrgActionsRunnerResponseObject
	v1gen.CreateOrgActionsRegistrationTokenResponseObject
	v1gen.ListInstanceActionsRunnersResponseObject
	v1gen.DeleteInstanceActionsRunnerResponseObject
	v1gen.CreateInstanceActionsRegistrationTokenResponseObject
}

var (
	_ actionsResponse = problemResponse{}
	_ actionsResponse = movedResponse{}
)

func actionsDTORun(r actions.Run) v1gen.ActionsRun {
	return v1gen.ActionsRun{Id: r.ID, RunNumber: r.RunNumber, Attempt: int(r.Attempt), WorkflowPath: r.WorkflowPath,
		WorkflowName: r.WorkflowName, Event: r.Event, EventAction: r.EventAction, Ref: r.Ref, Sha: r.Sha, Title: r.Title,
		Status: v1gen.ActionsRunStatus(r.Status), Conclusion: r.Conclusion, NeedsApproval: r.NeedsApproval, Fork: r.Fork,
		Actor: r.Actor, TriggerActor: r.TriggerActor, Url: r.URL, StartedAt: r.StartedAt, CompletedAt: r.CompletedAt,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func actionsDTOJob(j actions.Job) v1gen.ActionsJob {
	return v1gen.ActionsJob{Id: j.ID, RunId: j.RunID, Key: j.Key, Name: j.Name, Needs: nonNil(j.Needs),
		Status: v1gen.ActionsJobStatus(j.Status), Conclusion: j.Conclusion, Environment: j.Environment,
		CancelRequested: j.CancelRequested, Approved: j.Approved, RunsOn: nonNil(j.RunsOn), Runner: j.Runner,
		Reusable: j.Reusable, Caller: j.Caller, LogLength: j.LogLength, Url: j.URL, StartedAt: j.StartedAt,
		CompletedAt: j.CompletedAt}
}

func actionsDTOStep(s actions.Step) v1gen.ActionsStep {
	return v1gen.ActionsStep{Index: int(s.Index), Name: s.Name, Status: s.Status, Conclusion: s.Conclusion,
		LogIndex: s.LogIndex, LogLength: s.LogLength, StartedAt: s.StartedAt, CompletedAt: s.CompletedAt}
}

func actionsDTOSecret(s actions.Secret) v1gen.ActionsSecret {
	return v1gen.ActionsSecret{Name: s.Name, Scope: s.Scope, Environment: s.Environment, CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt}
}

func actionsDTOVariable(v actions.Variable) v1gen.ActionsVariable {
	return v1gen.ActionsVariable{Name: v.Name, Value: v.Value, Scope: v.Scope, Environment: v.Environment,
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}

func actionsDTOEnvironment(e actions.Environment) v1gen.ActionsEnvironment {
	return v1gen.ActionsEnvironment{Name: e.Name, RequireApproval: e.RequireApproval, Reviewers: emptyIfNil(e.Reviewers),
		CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt}
}

func actionsDTORunner(r actions.Runner) v1gen.ActionsRunner {
	return v1gen.ActionsRunner{Id: r.ID, Name: r.Name, Scope: r.Scope, Labels: nonNil(r.Labels), Version: r.Version,
		Ephemeral: r.Ephemeral, Online: r.Online, Busy: r.Busy, LastSeenAt: r.LastSeenAt, CreatedAt: r.CreatedAt}
}

func actionsDTOToken(t actions.RegistrationToken) v1gen.ActionsRegistrationToken {
	return v1gen.ActionsRegistrationToken{Token: t.Token, Scope: t.Scope, ExpiresAt: t.ExpiresAt}
}

func actionsDTOLogPage(lines []actions.LogLine, from int64, complete bool) v1gen.ActionsLogPage {
	out := v1gen.ActionsLogPage{Lines: make([]v1gen.ActionsLogLine, 0, len(lines)), Next: max(from, 0), Complete: complete}
	for _, l := range lines {
		out.Lines = append(out.Lines, v1gen.ActionsLogLine{Index: l.Index, Time: l.Time, Content: l.Content})
	}
	if n := len(lines); n > 0 {
		out.Next = lines[n-1].Index + 1
	}
	return out
}

func mapList[T, U any](in []T, f func(T) U) []U {
	out := make([]U, 0, len(in))
	for _, v := range in {
		out = append(out, f(v))
	}
	return out
}

func emptyIfNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func runPath(id uuid.UUID) string { return "/actions/runs/" + id.String() }
func jobPath(id uuid.UUID) string { return "/actions/jobs/" + id.String() }

// --- workflows and runs ---

func (s *v1Server) ListActionsWorkflows(ctx context.Context, req v1gen.ListActionsWorkflowsRequestObject) (v1gen.ListActionsWorkflowsResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	list, err := s.actions.ListWorkflows(ctx, principal(ctx), req.Owner, req.Name)
	if err != nil {
		return actionsFail(err, http.MethodGet, "/actions/workflows")
	}
	return v1gen.ListActionsWorkflows200JSONResponse(mapList(list, func(w actions.Workflow) v1gen.ActionsWorkflow {
		return v1gen.ActionsWorkflow{Path: w.Path, Name: w.Name, LastRunAt: w.LastRun}
	})), nil
}

func (s *v1Server) DispatchActionsWorkflow(ctx context.Context, req v1gen.DispatchActionsWorkflowRequestObject) (v1gen.DispatchActionsWorkflowResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	var ref string
	var inputs map[string]any
	if req.Body != nil {
		ref = deref(req.Body.Ref)
		inputs = deref(req.Body.Inputs)
	}
	suffix := "/actions/workflows/" + req.Workflow + "/dispatches"
	who := principal(ctx)
	row, err := s.actions.Dispatch(ctx, who, req.Owner, req.Name, req.Workflow, ref, inputs)
	if err != nil {
		return actionsFail(err, http.MethodPost, suffix)
	}
	run, err := s.actions.GetRun(ctx, who, req.Owner, req.Name, row.ID)
	if err != nil {
		return actionsFail(err, http.MethodPost, suffix)
	}
	return v1gen.DispatchActionsWorkflow201JSONResponse(actionsDTORun(run)), nil
}

func (s *v1Server) GetActionsBadge(ctx context.Context, req v1gen.GetActionsBadgeRequestObject) (v1gen.GetActionsBadgeResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	svg, err := s.actions.Badge(ctx, principal(ctx), req.Owner, req.Name, req.Workflow, deref(req.Params.Branch))
	if err != nil {
		return actionsFail(err, http.MethodGet, "/actions/workflows/"+req.Workflow+"/badge.svg")
	}
	return v1gen.GetActionsBadge200ImagesvgXmlResponse{Body: bytes.NewReader(svg), ContentLength: int64(len(svg))}, nil
}

func (s *v1Server) ListActionsRuns(ctx context.Context, req v1gen.ListActionsRunsRequestObject) (v1gen.ListActionsRunsResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	p := req.Params
	f := actions.RunFilter{Workflow: deref(p.Workflow), Event: deref(p.Event), Status: deref(p.Status),
		Ref: deref(p.Branch), Sha: deref(p.Sha), Page: deref(p.Page), PerPage: deref(p.PerPage)}
	list, total, err := s.actions.ListRuns(ctx, principal(ctx), req.Owner, req.Name, f)
	if err != nil {
		return actionsFail(err, http.MethodGet, "/actions/runs")
	}
	return v1gen.ListActionsRuns200JSONResponse{TotalCount: total, Runs: mapList(list, actionsDTORun)}, nil
}

func (s *v1Server) GetActionsRun(ctx context.Context, req v1gen.GetActionsRunRequestObject) (v1gen.GetActionsRunResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	run, err := s.actions.GetRun(ctx, principal(ctx), req.Owner, req.Name, req.RunId)
	if err != nil {
		return actionsFail(err, http.MethodGet, runPath(req.RunId))
	}
	return v1gen.GetActionsRun200JSONResponse(actionsDTORun(run)), nil
}

func (s *v1Server) ListActionsRunJobs(ctx context.Context, req v1gen.ListActionsRunJobsRequestObject) (v1gen.ListActionsRunJobsResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	attempt := deref(req.Params.Attempt)
	if attempt < 0 || attempt > 1<<30 {
		return badRequest("attempt is out of range"), nil
	}
	list, err := s.actions.ListJobs(ctx, principal(ctx), req.Owner, req.Name, req.RunId, int32(attempt))
	if err != nil {
		return actionsFail(err, http.MethodGet, runPath(req.RunId)+"/jobs")
	}
	return v1gen.ListActionsRunJobs200JSONResponse(mapList(list, actionsDTOJob)), nil
}

func (s *v1Server) RerunActionsRun(ctx context.Context, req v1gen.RerunActionsRunRequestObject) (v1gen.RerunActionsRunResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	run, err := s.actions.RerunRun(ctx, principal(ctx), req.Owner, req.Name, req.RunId)
	if err != nil {
		return actionsFail(err, http.MethodPost, runPath(req.RunId)+"/rerun")
	}
	return v1gen.RerunActionsRun201JSONResponse(actionsDTORun(run)), nil
}

func (s *v1Server) CancelActionsRun(ctx context.Context, req v1gen.CancelActionsRunRequestObject) (v1gen.CancelActionsRunResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	if err := s.actions.CancelRun(ctx, principal(ctx), req.Owner, req.Name, req.RunId); err != nil {
		return actionsFail(err, http.MethodPost, runPath(req.RunId)+"/cancel")
	}
	return v1gen.CancelActionsRun202Response{}, nil
}

func (s *v1Server) ApproveActionsRun(ctx context.Context, req v1gen.ApproveActionsRunRequestObject) (v1gen.ApproveActionsRunResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	if err := s.actions.ApproveRun(ctx, principal(ctx), req.Owner, req.Name, req.RunId); err != nil {
		return actionsFail(err, http.MethodPost, runPath(req.RunId)+"/approve")
	}
	return v1gen.ApproveActionsRun204Response{}, nil
}

// --- jobs and logs ---

func (s *v1Server) GetActionsJob(ctx context.Context, req v1gen.GetActionsJobRequestObject) (v1gen.GetActionsJobResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	job, steps, err := s.actions.GetJob(ctx, principal(ctx), req.Owner, req.Name, req.JobId)
	if err != nil {
		return actionsFail(err, http.MethodGet, jobPath(req.JobId))
	}
	return v1gen.GetActionsJob200JSONResponse{Job: actionsDTOJob(job), Steps: mapList(steps, actionsDTOStep)}, nil
}

func (s *v1Server) CancelActionsJob(ctx context.Context, req v1gen.CancelActionsJobRequestObject) (v1gen.CancelActionsJobResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	if err := s.actions.CancelJob(ctx, principal(ctx), req.Owner, req.Name, req.JobId); err != nil {
		return actionsFail(err, http.MethodPost, jobPath(req.JobId)+"/cancel")
	}
	return v1gen.CancelActionsJob202Response{}, nil
}

func (s *v1Server) ReviewActionsDeployment(ctx context.Context, req v1gen.ReviewActionsDeploymentRequestObject) (v1gen.ReviewActionsDeploymentResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("a request body is required"), nil
	}
	err := s.actions.ApproveDeployment(ctx, principal(ctx), req.Owner, req.Name, req.JobId, req.Body.Approve)
	if err != nil {
		return actionsFail(err, http.MethodPost, jobPath(req.JobId)+"/deployment")
	}
	return v1gen.ReviewActionsDeployment204Response{}, nil
}

func (s *v1Server) GetActionsJobLog(ctx context.Context, req v1gen.GetActionsJobLogRequestObject) (v1gen.GetActionsJobLogResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	from := deref(req.Params.From)
	lines, complete, err := s.actions.ReadLog(ctx, principal(ctx), req.Owner, req.Name, req.JobId, from, deref(req.Params.Limit))
	if err != nil {
		return actionsFail(err, http.MethodGet, jobPath(req.JobId)+"/logs")
	}
	return v1gen.GetActionsJobLog200JSONResponse(actionsDTOLogPage(lines, from, complete)), nil
}

// --- repository secrets, variables and environments ---

func (s *v1Server) repoScope(ctx context.Context, owner, name string, env *string) (actions.Scope, error) {
	return s.actions.RepoScope(ctx, principal(ctx), owner, name, deref(env))
}

func (s *v1Server) ListRepoActionsSecrets(ctx context.Context, req v1gen.ListRepoActionsSecretsRequestObject) (v1gen.ListRepoActionsSecretsResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	sc, err := s.repoScope(ctx, req.Owner, req.Name, req.Params.Environment)
	if err != nil {
		return actionsFail(err, http.MethodGet, "/actions/secrets")
	}
	list, err := s.actions.ListSecrets(ctx, sc)
	if err != nil {
		return actionsFail(err, http.MethodGet, "/actions/secrets")
	}
	return v1gen.ListRepoActionsSecrets200JSONResponse(mapList(list, actionsDTOSecret)), nil
}

func (s *v1Server) PutRepoActionsSecret(ctx context.Context, req v1gen.PutRepoActionsSecretRequestObject) (v1gen.PutRepoActionsSecretResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	if req.Body == nil || req.Body.Value == nil {
		return badRequest("value is required"), nil
	}
	suffix := "/actions/secrets/" + req.SecretName
	sc, err := s.repoScope(ctx, req.Owner, req.Name, req.Params.Environment)
	if err != nil {
		return actionsFail(err, http.MethodPut, suffix)
	}
	sec, err := s.actions.SetSecret(ctx, principal(ctx), sc, req.SecretName, *req.Body.Value)
	if err != nil {
		return actionsFail(err, http.MethodPut, suffix)
	}
	return v1gen.PutRepoActionsSecret200JSONResponse(actionsDTOSecret(sec)), nil
}

func (s *v1Server) DeleteRepoActionsSecret(ctx context.Context, req v1gen.DeleteRepoActionsSecretRequestObject) (v1gen.DeleteRepoActionsSecretResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	suffix := "/actions/secrets/" + req.SecretName
	sc, err := s.repoScope(ctx, req.Owner, req.Name, req.Params.Environment)
	if err != nil {
		return actionsFail(err, http.MethodDelete, suffix)
	}
	if err := s.actions.DeleteSecret(ctx, sc, req.SecretName); err != nil {
		return actionsFail(err, http.MethodDelete, suffix)
	}
	return v1gen.DeleteRepoActionsSecret204Response{}, nil
}

func (s *v1Server) ListRepoActionsVariables(ctx context.Context, req v1gen.ListRepoActionsVariablesRequestObject) (v1gen.ListRepoActionsVariablesResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	sc, err := s.repoScope(ctx, req.Owner, req.Name, req.Params.Environment)
	if err != nil {
		return actionsFail(err, http.MethodGet, "/actions/variables")
	}
	list, err := s.actions.ListVariables(ctx, sc)
	if err != nil {
		return actionsFail(err, http.MethodGet, "/actions/variables")
	}
	return v1gen.ListRepoActionsVariables200JSONResponse(mapList(list, actionsDTOVariable)), nil
}

func (s *v1Server) PutRepoActionsVariable(ctx context.Context, req v1gen.PutRepoActionsVariableRequestObject) (v1gen.PutRepoActionsVariableResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("a request body is required"), nil
	}
	suffix := "/actions/variables/" + req.VariableName
	sc, err := s.repoScope(ctx, req.Owner, req.Name, req.Params.Environment)
	if err != nil {
		return actionsFail(err, http.MethodPut, suffix)
	}
	v, err := s.actions.SetVariable(ctx, principal(ctx), sc, req.VariableName, req.Body.Value)
	if err != nil {
		return actionsFail(err, http.MethodPut, suffix)
	}
	return v1gen.PutRepoActionsVariable200JSONResponse(actionsDTOVariable(v)), nil
}

func (s *v1Server) DeleteRepoActionsVariable(ctx context.Context, req v1gen.DeleteRepoActionsVariableRequestObject) (v1gen.DeleteRepoActionsVariableResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	suffix := "/actions/variables/" + req.VariableName
	sc, err := s.repoScope(ctx, req.Owner, req.Name, req.Params.Environment)
	if err != nil {
		return actionsFail(err, http.MethodDelete, suffix)
	}
	if err := s.actions.DeleteVariable(ctx, sc, req.VariableName); err != nil {
		return actionsFail(err, http.MethodDelete, suffix)
	}
	return v1gen.DeleteRepoActionsVariable204Response{}, nil
}

func (s *v1Server) ListActionsEnvironments(ctx context.Context, req v1gen.ListActionsEnvironmentsRequestObject) (v1gen.ListActionsEnvironmentsResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	list, err := s.actions.ListEnvironments(ctx, principal(ctx), req.Owner, req.Name)
	if err != nil {
		return actionsFail(err, http.MethodGet, "/actions/environments")
	}
	return v1gen.ListActionsEnvironments200JSONResponse(mapList(list, actionsDTOEnvironment)), nil
}

func (s *v1Server) PutActionsEnvironment(ctx context.Context, req v1gen.PutActionsEnvironmentRequestObject) (v1gen.PutActionsEnvironmentResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	var in actions.EnvironmentInput
	if req.Body != nil {
		in.RequireApproval = deref(req.Body.RequireApproval)
		in.Reviewers = deref(req.Body.Reviewers)
	}
	e, err := s.actions.PutEnvironment(ctx, principal(ctx), req.Owner, req.Name, req.Environment, in)
	if err != nil {
		return actionsFail(err, http.MethodPut, "/actions/environments/"+req.Environment)
	}
	return v1gen.PutActionsEnvironment200JSONResponse(actionsDTOEnvironment(e)), nil
}

func (s *v1Server) DeleteActionsEnvironment(ctx context.Context, req v1gen.DeleteActionsEnvironmentRequestObject) (v1gen.DeleteActionsEnvironmentResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	if err := s.actions.DeleteEnvironment(ctx, principal(ctx), req.Owner, req.Name, req.Environment); err != nil {
		return actionsFail(err, http.MethodDelete, "/actions/environments/"+req.Environment)
	}
	return v1gen.DeleteActionsEnvironment204Response{}, nil
}

// --- runners ---

func (s *v1Server) ListRepoActionsRunners(ctx context.Context, req v1gen.ListRepoActionsRunnersRequestObject) (v1gen.ListRepoActionsRunnersResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	sc, err := s.repoScope(ctx, req.Owner, req.Name, nil)
	if err != nil {
		return actionsFail(err, http.MethodGet, "/actions/runners")
	}
	list, err := s.actions.ListRepoRunners(ctx, sc)
	if err != nil {
		return actionsFail(err, http.MethodGet, "/actions/runners")
	}
	return v1gen.ListRepoActionsRunners200JSONResponse(mapList(list, actionsDTORunner)), nil
}

func (s *v1Server) DeleteRepoActionsRunner(ctx context.Context, req v1gen.DeleteRepoActionsRunnerRequestObject) (v1gen.DeleteRepoActionsRunnerResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	suffix := "/actions/runners/" + req.RunnerId.String()
	sc, err := s.repoScope(ctx, req.Owner, req.Name, nil)
	if err != nil {
		return actionsFail(err, http.MethodDelete, suffix)
	}
	if err := s.actions.DeleteRunner(ctx, sc, req.RunnerId); err != nil {
		return actionsFail(err, http.MethodDelete, suffix)
	}
	return v1gen.DeleteRepoActionsRunner204Response{}, nil
}

func (s *v1Server) CreateRepoActionsRegistrationToken(ctx context.Context, req v1gen.CreateRepoActionsRegistrationTokenRequestObject) (v1gen.CreateRepoActionsRegistrationTokenResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	sc, err := s.repoScope(ctx, req.Owner, req.Name, nil)
	if err != nil {
		return actionsFail(err, http.MethodPost, "/actions/runners/registration-token")
	}
	t, err := s.actions.CreateRegistrationToken(ctx, principal(ctx), sc)
	if err != nil {
		return actionsFail(err, http.MethodPost, "/actions/runners/registration-token")
	}
	return v1gen.CreateRepoActionsRegistrationToken201JSONResponse(actionsDTOToken(t)), nil
}

// --- organization scope ---

func (s *v1Server) ListOrgActionsSecrets(ctx context.Context, req v1gen.ListOrgActionsSecretsRequestObject) (v1gen.ListOrgActionsSecretsResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	sc, err := s.actions.OwnerScope(ctx, principal(ctx), req.Org)
	if err != nil {
		return actionsFail(err, http.MethodGet, "")
	}
	list, err := s.actions.ListSecrets(ctx, sc)
	if err != nil {
		return actionsFail(err, http.MethodGet, "")
	}
	return v1gen.ListOrgActionsSecrets200JSONResponse(mapList(list, actionsDTOSecret)), nil
}

func (s *v1Server) PutOrgActionsSecret(ctx context.Context, req v1gen.PutOrgActionsSecretRequestObject) (v1gen.PutOrgActionsSecretResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	if req.Body == nil || req.Body.Value == nil {
		return badRequest("value is required"), nil
	}
	who := principal(ctx)
	sc, err := s.actions.OwnerScope(ctx, who, req.Org)
	if err != nil {
		return actionsFail(err, http.MethodPut, "")
	}
	sec, err := s.actions.SetSecret(ctx, who, sc, req.SecretName, *req.Body.Value)
	if err != nil {
		return actionsFail(err, http.MethodPut, "")
	}
	return v1gen.PutOrgActionsSecret200JSONResponse(actionsDTOSecret(sec)), nil
}

func (s *v1Server) DeleteOrgActionsSecret(ctx context.Context, req v1gen.DeleteOrgActionsSecretRequestObject) (v1gen.DeleteOrgActionsSecretResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	sc, err := s.actions.OwnerScope(ctx, principal(ctx), req.Org)
	if err != nil {
		return actionsFail(err, http.MethodDelete, "")
	}
	if err := s.actions.DeleteSecret(ctx, sc, req.SecretName); err != nil {
		return actionsFail(err, http.MethodDelete, "")
	}
	return v1gen.DeleteOrgActionsSecret204Response{}, nil
}

func (s *v1Server) ListOrgActionsVariables(ctx context.Context, req v1gen.ListOrgActionsVariablesRequestObject) (v1gen.ListOrgActionsVariablesResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	sc, err := s.actions.OwnerScope(ctx, principal(ctx), req.Org)
	if err != nil {
		return actionsFail(err, http.MethodGet, "")
	}
	list, err := s.actions.ListVariables(ctx, sc)
	if err != nil {
		return actionsFail(err, http.MethodGet, "")
	}
	return v1gen.ListOrgActionsVariables200JSONResponse(mapList(list, actionsDTOVariable)), nil
}

func (s *v1Server) PutOrgActionsVariable(ctx context.Context, req v1gen.PutOrgActionsVariableRequestObject) (v1gen.PutOrgActionsVariableResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("a request body is required"), nil
	}
	who := principal(ctx)
	sc, err := s.actions.OwnerScope(ctx, who, req.Org)
	if err != nil {
		return actionsFail(err, http.MethodPut, "")
	}
	v, err := s.actions.SetVariable(ctx, who, sc, req.VariableName, req.Body.Value)
	if err != nil {
		return actionsFail(err, http.MethodPut, "")
	}
	return v1gen.PutOrgActionsVariable200JSONResponse(actionsDTOVariable(v)), nil
}

func (s *v1Server) DeleteOrgActionsVariable(ctx context.Context, req v1gen.DeleteOrgActionsVariableRequestObject) (v1gen.DeleteOrgActionsVariableResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	sc, err := s.actions.OwnerScope(ctx, principal(ctx), req.Org)
	if err != nil {
		return actionsFail(err, http.MethodDelete, "")
	}
	if err := s.actions.DeleteVariable(ctx, sc, req.VariableName); err != nil {
		return actionsFail(err, http.MethodDelete, "")
	}
	return v1gen.DeleteOrgActionsVariable204Response{}, nil
}

func (s *v1Server) ListOrgActionsRunners(ctx context.Context, req v1gen.ListOrgActionsRunnersRequestObject) (v1gen.ListOrgActionsRunnersResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	sc, err := s.actions.OwnerScope(ctx, principal(ctx), req.Org)
	if err != nil {
		return actionsFail(err, http.MethodGet, "")
	}
	list, err := s.actions.ListRunners(ctx, sc)
	if err != nil {
		return actionsFail(err, http.MethodGet, "")
	}
	return v1gen.ListOrgActionsRunners200JSONResponse(mapList(list, actionsDTORunner)), nil
}

func (s *v1Server) DeleteOrgActionsRunner(ctx context.Context, req v1gen.DeleteOrgActionsRunnerRequestObject) (v1gen.DeleteOrgActionsRunnerResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	sc, err := s.actions.OwnerScope(ctx, principal(ctx), req.Org)
	if err != nil {
		return actionsFail(err, http.MethodDelete, "")
	}
	if err := s.actions.DeleteRunner(ctx, sc, req.RunnerId); err != nil {
		return actionsFail(err, http.MethodDelete, "")
	}
	return v1gen.DeleteOrgActionsRunner204Response{}, nil
}

func (s *v1Server) CreateOrgActionsRegistrationToken(ctx context.Context, req v1gen.CreateOrgActionsRegistrationTokenRequestObject) (v1gen.CreateOrgActionsRegistrationTokenResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	who := principal(ctx)
	sc, err := s.actions.OwnerScope(ctx, who, req.Org)
	if err != nil {
		return actionsFail(err, http.MethodPost, "")
	}
	t, err := s.actions.CreateRegistrationToken(ctx, who, sc)
	if err != nil {
		return actionsFail(err, http.MethodPost, "")
	}
	return v1gen.CreateOrgActionsRegistrationToken201JSONResponse(actionsDTOToken(t)), nil
}

// --- instance scope ---

func (s *v1Server) ListInstanceActionsRunners(ctx context.Context, _ v1gen.ListInstanceActionsRunnersRequestObject) (v1gen.ListInstanceActionsRunnersResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	sc, err := s.actions.InstanceScope(principal(ctx))
	if err != nil {
		return actionsFail(err, http.MethodGet, "")
	}
	list, err := s.actions.ListRunners(ctx, sc)
	if err != nil {
		return actionsFail(err, http.MethodGet, "")
	}
	return v1gen.ListInstanceActionsRunners200JSONResponse(mapList(list, actionsDTORunner)), nil
}

func (s *v1Server) DeleteInstanceActionsRunner(ctx context.Context, req v1gen.DeleteInstanceActionsRunnerRequestObject) (v1gen.DeleteInstanceActionsRunnerResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	sc, err := s.actions.InstanceScope(principal(ctx))
	if err != nil {
		return actionsFail(err, http.MethodDelete, "")
	}
	if err := s.actions.DeleteRunner(ctx, sc, req.RunnerId); err != nil {
		return actionsFail(err, http.MethodDelete, "")
	}
	return v1gen.DeleteInstanceActionsRunner204Response{}, nil
}

func (s *v1Server) CreateInstanceActionsRegistrationToken(ctx context.Context, _ v1gen.CreateInstanceActionsRegistrationTokenRequestObject) (v1gen.CreateInstanceActionsRegistrationTokenResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	who := principal(ctx)
	sc, err := s.actions.InstanceScope(who)
	if err != nil {
		return actionsFail(err, http.MethodPost, "")
	}
	t, err := s.actions.CreateRegistrationToken(ctx, who, sc)
	if err != nil {
		return actionsFail(err, http.MethodPost, "")
	}
	return v1gen.CreateInstanceActionsRegistrationToken201JSONResponse(actionsDTOToken(t)), nil
}

// --- streaming routes (outside the request timeout) ---

const (
	actionsJobPath       = "/api/v1/repos/{owner}/{name}/actions/jobs/{jobId}"
	actionsLogWait       = 15 * time.Second
	actionsLogStreamPage = 1000
)

// mountActions mounts the raw log download and the live log stream.
func mountActions(r chi.Router, d Deps) {
	r.With(d.Auth.LoadSession).Get(actionsJobPath+"/logs/raw", func(w http.ResponseWriter, req *http.Request) {
		rawActionsLog(w, req, d)
	})
	r.With(d.Auth.LoadSession).Get(actionsJobPath+"/logs/stream", func(w http.ResponseWriter, req *http.Request) {
		streamActionsLog(w, req, d)
	})
}

// actionsStreamFail answers a service error on a streaming route.
func actionsStreamFail(w http.ResponseWriter, req *http.Request, d Deps, err error) {
	r, ferr := actionsFail(err, req.Method, "")
	if ferr != nil {
		d.Log.ErrorContext(req.Context(), "actions log request failed", "error", ferr)
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

func actionsStreamArgs(w http.ResponseWriter, req *http.Request, d Deps) (auth.Principal, uuid.UUID, bool) {
	if d.Actions == nil {
		writeProblem(w, http.StatusServiceUnavailable, errActionsUnavailable.detail)
		return auth.Principal{}, uuid.Nil, false
	}
	p, ok := streamPrincipal(w, req, "repo:read")
	if !ok {
		return p, uuid.Nil, false
	}
	id, ok := uuidParam(req, "jobId")
	if !ok {
		writeProblem(w, http.StatusBadRequest, "jobId must be a UUID")
		return p, uuid.Nil, false
	}
	return p, id, true
}

func rawActionsLog(w http.ResponseWriter, req *http.Request, d Deps) {
	p, id, ok := actionsStreamArgs(w, req, d)
	if !ok {
		return
	}
	ctx := req.Context()
	rc, err := d.Actions.RawLog(ctx, p, chi.URLParam(req, "owner"), chi.URLParam(req, "name"), id)
	if err != nil {
		actionsStreamFail(w, req, d, err)
		return
	}
	defer func() { _ = rc.Close() }()
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, rc); err != nil && ctx.Err() == nil {
		d.Log.WarnContext(ctx, "actions raw log copy", "job", id, "error", err)
	}
}

// streamActionsLog sends the log of a job as server-sent events: "log"
// events carry an ActionsLogPage with the next index as event id, "end"
// closes a completed log; comments keep idle connections open.
func streamActionsLog(w http.ResponseWriter, req *http.Request, d Deps) {
	p, id, ok := actionsStreamArgs(w, req, d)
	if !ok {
		return
	}
	ctx := req.Context()
	owner, name := chi.URLParam(req, "owner"), chi.URLParam(req, "name")
	from, err := logStreamFrom(req)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error())
		return
	}
	lines, complete, err := d.Actions.ReadLog(ctx, p, owner, name, id, from, actionsLogStreamPage)
	if err != nil {
		actionsStreamFail(w, req, d, err)
		return
	}
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	for {
		if len(lines) > 0 || complete {
			page := actionsDTOLogPage(lines, from, complete)
			data, err := json.Marshal(page)
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "event: log\nid: %d\ndata: %s\n\n", page.Next, data); err != nil {
				return
			}
			from = page.Next
		}
		if complete {
			_, _ = io.WriteString(w, "event: end\ndata: {}\n\n")
			_ = rc.Flush()
			return
		}
		if rc.Flush() != nil {
			return
		}
		if len(lines) == 0 {
			start := time.Now()
			if err := d.Actions.WaitLog(ctx, p, owner, name, id, from, actionsLogWait); err != nil {
				if ctx.Err() == nil {
					d.Log.WarnContext(ctx, "actions log stream wait", "job", id, "error", err)
				}
				return
			}
			if time.Since(start) >= actionsLogWait {
				if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil || rc.Flush() != nil {
					return
				}
			}
		}
		if ctx.Err() != nil {
			return
		}
		var again []actions.LogLine
		again, complete, err = d.Actions.ReadLog(ctx, p, owner, name, id, from, actionsLogStreamPage)
		if err != nil {
			if ctx.Err() == nil {
				d.Log.WarnContext(ctx, "actions log stream read", "job", id, "error", err)
			}
			return
		}
		if len(again) == 0 && len(lines) == 0 && !complete {
			// WaitLog returned early but no row is readable yet: back off
			// briefly instead of spinning.
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
		lines = again
	}
}

// logStreamFrom reads the start index from Last-Event-ID (a resumed
// stream) or the from query parameter.
func logStreamFrom(req *http.Request) (int64, error) {
	v := req.Header.Get("Last-Event-ID")
	if v == "" {
		v = req.URL.Query().Get("from")
	}
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, errors.New("from must be a non-negative integer")
	}
	return n, nil
}

// Visit methods let problemResponse and movedResponse answer every Actions
// operation.
func (p problemResponse) VisitListActionsWorkflowsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitDispatchActionsWorkflowResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitGetActionsBadgeResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitListActionsRunsResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitGetActionsRunResponse(w http.ResponseWriter) error   { return p.write(w) }
func (p problemResponse) VisitListActionsRunJobsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitRerunActionsRunResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitCancelActionsRunResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitApproveActionsRunResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitGetActionsJobResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitCancelActionsJobResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitReviewActionsDeploymentResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitGetActionsJobLogResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListRepoActionsSecretsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitPutRepoActionsSecretResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitDeleteRepoActionsSecretResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListRepoActionsVariablesResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitPutRepoActionsVariableResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitDeleteRepoActionsVariableResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListActionsEnvironmentsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitPutActionsEnvironmentResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitDeleteActionsEnvironmentResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListRepoActionsRunnersResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitDeleteRepoActionsRunnerResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitCreateRepoActionsRegistrationTokenResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListOrgActionsSecretsResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitPutOrgActionsSecretResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitDeleteOrgActionsSecretResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListOrgActionsVariablesResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitPutOrgActionsVariableResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitDeleteOrgActionsVariableResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListOrgActionsRunnersResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitDeleteOrgActionsRunnerResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitCreateOrgActionsRegistrationTokenResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitListInstanceActionsRunnersResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitDeleteInstanceActionsRunnerResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitCreateInstanceActionsRegistrationTokenResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (m movedResponse) VisitListActionsWorkflowsResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitDispatchActionsWorkflowResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitGetActionsBadgeResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitListActionsRunsResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitGetActionsRunResponse(w http.ResponseWriter) error   { return m.write(w) }
func (m movedResponse) VisitListActionsRunJobsResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitRerunActionsRunResponse(w http.ResponseWriter) error   { return m.write(w) }
func (m movedResponse) VisitCancelActionsRunResponse(w http.ResponseWriter) error  { return m.write(w) }
func (m movedResponse) VisitApproveActionsRunResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitGetActionsJobResponse(w http.ResponseWriter) error     { return m.write(w) }
func (m movedResponse) VisitCancelActionsJobResponse(w http.ResponseWriter) error  { return m.write(w) }
func (m movedResponse) VisitReviewActionsDeploymentResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitGetActionsJobLogResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitListRepoActionsSecretsResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitPutRepoActionsSecretResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitDeleteRepoActionsSecretResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitListRepoActionsVariablesResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitPutRepoActionsVariableResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitDeleteRepoActionsVariableResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitListActionsEnvironmentsResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitPutActionsEnvironmentResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitDeleteActionsEnvironmentResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitListRepoActionsRunnersResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitDeleteRepoActionsRunnerResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitCreateRepoActionsRegistrationTokenResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitListOrgActionsSecretsResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitPutOrgActionsSecretResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitDeleteOrgActionsSecretResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitListOrgActionsVariablesResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitPutOrgActionsVariableResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitDeleteOrgActionsVariableResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitListOrgActionsRunnersResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitDeleteOrgActionsRunnerResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitCreateOrgActionsRegistrationTokenResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitListInstanceActionsRunnersResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitDeleteInstanceActionsRunnerResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitCreateInstanceActionsRegistrationTokenResponse(w http.ResponseWriter) error {
	return m.write(w)
}
