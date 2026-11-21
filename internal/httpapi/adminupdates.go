package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/updates"
)

var errUpdatesUnavailable = problemResponse{status: http.StatusServiceUnavailable, detail: "updates are not available"}

func updateReleaseDTO(r updates.Release) v1gen.AvailableRelease {
	return v1gen.AvailableRelease{Version: r.Version, Name: r.Name, Url: r.URL, Notes: r.Notes, PublishedAt: r.PublishedAt}
}

func updateStatusDTO(st updates.Status) v1gen.UpdateStatus {
	u := st.Updater
	log := u.Log
	if log == nil {
		log = []string{}
	}
	out := v1gen.UpdateStatus{
		Current: st.Current, Release: st.Release, Check: st.Check, Repository: st.Repository,
		CheckedAt: st.CheckedAt, CheckError: st.CheckError, Newer: make([]v1gen.AvailableRelease, 0, len(st.Newer)),
		Auto: st.Auto, Deploy: v1gen.UpdateStatusDeploy(st.Deploy), DeployDir: st.DeployDir,
		CanApply: st.CanApply, Command: st.Command,
		Updater: v1gen.Updater{Available: u.Available, State: v1gen.UpdaterState(u.State), Version: u.Version, From: u.From,
			StartedAt: u.StartedAt, FinishedAt: u.FinishedAt, Step: u.Step, Log: log, Pending: u.Pending},
	}
	if st.Latest != nil {
		l := updateReleaseDTO(*st.Latest)
		out.Latest = &l
	}
	for _, r := range st.Newer {
		out.Newer = append(out.Newer, updateReleaseDTO(r))
	}
	return out
}

func (s *v1Server) GetUpdateStatus(ctx context.Context, _ v1gen.GetUpdateStatusRequestObject) (v1gen.GetUpdateStatusResponseObject, error) {
	if s.updates == nil {
		return errUpdatesUnavailable, nil
	}
	st, err := s.updates.Status(ctx)
	if err != nil {
		return nil, err
	}
	return v1gen.GetUpdateStatus200JSONResponse(updateStatusDTO(st)), nil
}

func (s *v1Server) CheckUpdates(ctx context.Context, _ v1gen.CheckUpdatesRequestObject) (v1gen.CheckUpdatesResponseObject, error) {
	if s.updates == nil {
		return errUpdatesUnavailable, nil
	}
	if err := s.updates.Refresh(ctx); err != nil {
		return nil, err
	}
	st, err := s.updates.Status(ctx)
	if err != nil {
		return nil, err
	}
	return v1gen.CheckUpdates200JSONResponse(updateStatusDTO(st)), nil
}

func (s *v1Server) SetAutoUpdate(ctx context.Context, req v1gen.SetAutoUpdateRequestObject) (v1gen.SetAutoUpdateResponseObject, error) {
	if s.updates == nil {
		return errUpdatesUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("enabled is required"), nil
	}
	if err := s.updates.SetAuto(ctx, principal(ctx).UserID, clientOf(ctx).IP, req.Body.Enabled); err != nil {
		return nil, err
	}
	st, err := s.updates.Status(ctx)
	if err != nil {
		return nil, err
	}
	return v1gen.SetAutoUpdate200JSONResponse(updateStatusDTO(st)), nil
}

func (s *v1Server) ApplyUpdate(ctx context.Context, req v1gen.ApplyUpdateRequestObject) (v1gen.ApplyUpdateResponseObject, error) {
	if s.updates == nil {
		return errUpdatesUnavailable, nil
	}
	if req.Body == nil || req.Body.Version == "" {
		return badRequest("version is required"), nil
	}
	p := principal(ctx)
	err := s.updates.Apply(ctx, p.UserID, p.Username, clientOf(ctx).IP, req.Body.Version)
	switch {
	case errors.Is(err, updates.ErrConflict):
		return problemResponse{status: http.StatusConflict, detail: err.Error()}, nil
	case errors.Is(err, updates.ErrUnavailable):
		return problemResponse{status: http.StatusServiceUnavailable, detail: err.Error()}, nil
	case err != nil:
		return nil, err
	}
	st, err := s.updates.Status(ctx)
	if err != nil {
		return nil, err
	}
	return v1gen.ApplyUpdate202JSONResponse(updateStatusDTO(st)), nil
}

func (p problemResponse) VisitGetUpdateStatusResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitCheckUpdatesResponse(w http.ResponseWriter) error    { return p.write(w) }
func (p problemResponse) VisitSetAutoUpdateResponse(w http.ResponseWriter) error   { return p.write(w) }
func (p problemResponse) VisitApplyUpdateResponse(w http.ResponseWriter) error     { return p.write(w) }
