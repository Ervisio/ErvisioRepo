package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/Ervisio/ErvisioRepo/internal/actions"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

// Runner pause/resume at the three scopes and the approval side of the
// runner device registration (/runner/approve). The installer side lives
// on the runner protocol (RequestDevice, PollDevice).

func pausedOf(b *v1gen.ActionsRunnerPause) (bool, bool) {
	if b == nil {
		return false, false
	}
	return b.Paused, true
}

func (s *v1Server) SetRepoActionsRunnerPaused(ctx context.Context, req v1gen.SetRepoActionsRunnerPausedRequestObject) (v1gen.SetRepoActionsRunnerPausedResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	paused, ok := pausedOf(req.Body)
	if !ok {
		return badRequest("missing body"), nil
	}
	suffix := "/actions/runners/" + req.RunnerId.String() + "/paused"
	sc, err := s.repoScope(ctx, req.Owner, req.Name, nil)
	if err != nil {
		return actionsFail(err, http.MethodPut, suffix)
	}
	r, err := s.actions.SetRunnerPaused(ctx, principal(ctx), sc, req.RunnerId, paused, clientOf(ctx).IP)
	if err != nil {
		return actionsFail(err, http.MethodPut, suffix)
	}
	return v1gen.SetRepoActionsRunnerPaused200JSONResponse(actionsDTORunner(r)), nil
}

func (s *v1Server) SetOrgActionsRunnerPaused(ctx context.Context, req v1gen.SetOrgActionsRunnerPausedRequestObject) (v1gen.SetOrgActionsRunnerPausedResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	paused, ok := pausedOf(req.Body)
	if !ok {
		return badRequest("missing body"), nil
	}
	who := principal(ctx)
	sc, err := s.actions.OwnerScope(ctx, who, req.Org)
	if err != nil {
		return actionsFail(err, http.MethodPut, "")
	}
	r, err := s.actions.SetRunnerPaused(ctx, who, sc, req.RunnerId, paused, clientOf(ctx).IP)
	if err != nil {
		return actionsFail(err, http.MethodPut, "")
	}
	return v1gen.SetOrgActionsRunnerPaused200JSONResponse(actionsDTORunner(r)), nil
}

func (s *v1Server) SetInstanceActionsRunnerPaused(ctx context.Context, req v1gen.SetInstanceActionsRunnerPausedRequestObject) (v1gen.SetInstanceActionsRunnerPausedResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	paused, ok := pausedOf(req.Body)
	if !ok {
		return badRequest("missing body"), nil
	}
	who := principal(ctx)
	sc, err := s.actions.InstanceScope(who)
	if err != nil {
		return actionsFail(err, http.MethodPut, "")
	}
	r, err := s.actions.SetRunnerPaused(ctx, who, sc, req.RunnerId, paused, clientOf(ctx).IP)
	if err != nil {
		return actionsFail(err, http.MethodPut, "")
	}
	return v1gen.SetInstanceActionsRunnerPaused200JSONResponse(actionsDTORunner(r)), nil
}

// UpdateInstanceActionsRunner asks one instance runner to update itself
// to the server version.
func (s *v1Server) UpdateInstanceActionsRunner(ctx context.Context, req v1gen.UpdateInstanceActionsRunnerRequestObject) (v1gen.UpdateInstanceActionsRunnerResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	who := principal(ctx)
	sc, err := s.actions.InstanceScope(who)
	if err != nil {
		return actionsFail(err, http.MethodPost, "")
	}
	r, err := s.actions.RequestRunnerUpdate(ctx, who, sc, req.RunnerId, clientOf(ctx).IP)
	if err != nil {
		return actionsFail(err, http.MethodPost, "")
	}
	return v1gen.UpdateInstanceActionsRunner202JSONResponse(actionsDTORunner(r)), nil
}

// UpdateInstanceActionsRunners asks every outdated online instance runner
// to update itself.
func (s *v1Server) UpdateInstanceActionsRunners(ctx context.Context, _ v1gen.UpdateInstanceActionsRunnersRequestObject) (v1gen.UpdateInstanceActionsRunnersResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	who := principal(ctx)
	sc, err := s.actions.InstanceScope(who)
	if err != nil {
		return actionsFail(err, http.MethodPost, "")
	}
	n, version, err := s.actions.RequestRunnerUpdates(ctx, who, sc, clientOf(ctx).IP)
	if err != nil {
		return actionsFail(err, http.MethodPost, "")
	}
	return v1gen.UpdateInstanceActionsRunners202JSONResponse{Requested: n, Version: version}, nil
}

// SetInstanceActionsRunnerWake makes an instance runner on-demand.
func (s *v1Server) SetInstanceActionsRunnerWake(ctx context.Context, req v1gen.SetInstanceActionsRunnerWakeRequestObject) (v1gen.SetInstanceActionsRunnerWakeResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	who := principal(ctx)
	sc, err := s.actions.InstanceScope(who)
	if err != nil {
		return actionsFail(err, http.MethodPut, "")
	}
	b := req.Body
	in := actions.RunnerWakeSettings{Enabled: b.Enabled, URL: b.Url, Token: b.Token}
	if b.Header != nil {
		in.Header = *b.Header
	}
	if b.CooldownSeconds != nil {
		in.Cooldown = *b.CooldownSeconds
	}
	r, err := s.actions.SetRunnerWake(ctx, who, sc, req.RunnerId, in, clientOf(ctx).IP)
	if err != nil {
		return actionsFail(err, http.MethodPut, "")
	}
	return v1gen.SetInstanceActionsRunnerWake200JSONResponse(actionsDTORunner(r)), nil
}

func (s *v1Server) DeleteInstanceActionsRunnerWake(ctx context.Context, req v1gen.DeleteInstanceActionsRunnerWakeRequestObject) (v1gen.DeleteInstanceActionsRunnerWakeResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	who := principal(ctx)
	sc, err := s.actions.InstanceScope(who)
	if err != nil {
		return actionsFail(err, http.MethodDelete, "")
	}
	r, err := s.actions.DeleteRunnerWake(ctx, who, sc, req.RunnerId, clientOf(ctx).IP)
	if err != nil {
		return actionsFail(err, http.MethodDelete, "")
	}
	return v1gen.DeleteInstanceActionsRunnerWake200JSONResponse(actionsDTORunner(r)), nil
}

// WakeInstanceActionsRunner calls the start URL of an instance runner now.
func (s *v1Server) WakeInstanceActionsRunner(ctx context.Context, req v1gen.WakeInstanceActionsRunnerRequestObject) (v1gen.WakeInstanceActionsRunnerResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	who := principal(ctx)
	sc, err := s.actions.InstanceScope(who)
	if err != nil {
		return actionsFail(err, http.MethodPost, "")
	}
	r, err := s.actions.WakeRunner(ctx, who, sc, req.RunnerId, clientOf(ctx).IP)
	if err != nil {
		return actionsFail(err, http.MethodPost, "")
	}
	return v1gen.WakeInstanceActionsRunner200JSONResponse(actionsDTORunner(r)), nil
}

// runnerDeviceFail maps the errors of the device approval.
func runnerDeviceFail(err error) (actionsResponse, error) {
	var lim *actions.ErrDeviceLimited
	switch {
	case errors.As(err, &lim):
		return problemResponse{status: http.StatusTooManyRequests, detail: lim.Error(),
			retryAfter: retryAfterSeconds(lim.Wait)}, nil
	case errors.Is(err, actions.ErrNotFound):
		return problemResponse{status: http.StatusNotFound, detail: "unknown or expired code"}, nil
	}
	return actionsFail(err, http.MethodPost, "")
}

func (s *v1Server) GetActionsRunnerDevice(ctx context.Context, req v1gen.GetActionsRunnerDeviceRequestObject) (v1gen.GetActionsRunnerDeviceResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	d, err := s.actions.LookupRunnerDevice(ctx, p, req.UserCode, clientOf(ctx).IP)
	if err != nil {
		return runnerDeviceFail(err)
	}
	return v1gen.GetActionsRunnerDevice200JSONResponse{UserCode: d.UserCode, Hostname: d.Hostname, Os: d.OS,
		Arch: d.Arch, Cpus: d.CPUs, MemoryBytes: d.MemoryBytes, Docker: d.Docker, Version: d.Version, Name: d.Name,
		Labels: nonNil(d.Labels), Capacity: d.Capacity, RequestIp: d.RequestIP, CanInstance: d.CanInstance,
		CreatedAt: d.CreatedAt, ExpiresAt: d.ExpiresAt}, nil
}

func (s *v1Server) DecideActionsRunnerDevice(ctx context.Context, req v1gen.DecideActionsRunnerDeviceRequestObject) (v1gen.DecideActionsRunnerDeviceResponseObject, error) {
	if s.actions == nil {
		return errActionsUnavailable, nil
	}
	p, ok := caller(ctx)
	if !ok {
		return errAuthRequired, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	b := req.Body
	d := actions.RunnerDeviceDecision{Approve: b.Approve}
	if b.Scope != nil {
		d.Scope = *b.Scope
	}
	if b.Name != nil {
		d.Name = *b.Name
	}
	if b.Labels != nil {
		d.Labels = nonNil(*b.Labels)
	}
	if b.Capacity != nil {
		d.Capacity = *b.Capacity
	}
	if err := s.actions.DecideRunnerDevice(ctx, p, req.UserCode, d, clientOf(ctx).IP); err != nil {
		return runnerDeviceFail(err)
	}
	return v1gen.DecideActionsRunnerDevice204Response{}, nil
}
