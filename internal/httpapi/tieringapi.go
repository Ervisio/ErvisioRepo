package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/gittier"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// Tiering serves the admin pin/unpin/evict endpoints (satisfied by
// *gittier.Admin).
type Tiering interface {
	Resolve(ctx context.Context, owner, name string) (uuid.UUID, error)
	SetPinned(ctx context.Context, actor uuid.UUID, ip string, id uuid.UUID, pinned bool) (gittier.RepoState, error)
	Evict(ctx context.Context, actor uuid.UUID, ip string, id uuid.UUID) (gittier.RepoState, error)
}

// Hydration streams the hydration progress (satisfied by *gittier.Hydrator).
type Hydration interface {
	Hub() *gittier.Hub
	Start(repoID uuid.UUID)
}

var (
	_ Tiering   = (*gittier.Admin)(nil)
	_ Hydration = (*gittier.Hydrator)(nil)
)

var errTieringUnavailable = problem(http.StatusServiceUnavailable, "repository tiering is not configured")

func tieringDTO(st gittier.RepoState) v1gen.RepoTiering {
	return v1gen.RepoTiering{State: v1gen.RepoTieringState(st.State), Pinned: st.Pinned, SizeBytes: st.SizeBytes, ManifestSeq: st.ManifestSeq}
}

func repoNotFound() v1gen.ProblemApplicationProblemPlusJSONResponse {
	return v1gen.ProblemApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "repository not found"))
}

func (s *v1Server) setPinned(ctx context.Context, owner, name string, pinned bool) (gittier.RepoState, error) {
	id, err := s.tiering.Resolve(ctx, owner, name)
	if err != nil {
		return gittier.RepoState{}, err
	}
	return s.tiering.SetPinned(ctx, principal(ctx).UserID, clientOf(ctx).IP, id, pinned)
}

func (s *v1Server) PinAdminRepo(ctx context.Context, req v1gen.PinAdminRepoRequestObject) (v1gen.PinAdminRepoResponseObject, error) {
	if s.tiering == nil {
		return v1gen.PinAdminRepodefaultApplicationProblemPlusJSONResponse{Body: errTieringUnavailable, StatusCode: http.StatusServiceUnavailable}, nil
	}
	st, err := s.setPinned(ctx, req.Owner, req.Name, true)
	if errors.Is(err, gitstore.ErrNotFound) {
		return v1gen.PinAdminRepo404ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: repoNotFound()}, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.PinAdminRepo200JSONResponse(tieringDTO(st)), nil
}

func (s *v1Server) UnpinAdminRepo(ctx context.Context, req v1gen.UnpinAdminRepoRequestObject) (v1gen.UnpinAdminRepoResponseObject, error) {
	if s.tiering == nil {
		return v1gen.UnpinAdminRepodefaultApplicationProblemPlusJSONResponse{Body: errTieringUnavailable, StatusCode: http.StatusServiceUnavailable}, nil
	}
	st, err := s.setPinned(ctx, req.Owner, req.Name, false)
	if errors.Is(err, gitstore.ErrNotFound) {
		return v1gen.UnpinAdminRepo404ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: repoNotFound()}, nil
	}
	if err != nil {
		return nil, err
	}
	return v1gen.UnpinAdminRepo200JSONResponse(tieringDTO(st)), nil
}

func (s *v1Server) EvictAdminRepo(ctx context.Context, req v1gen.EvictAdminRepoRequestObject) (v1gen.EvictAdminRepoResponseObject, error) {
	if s.tiering == nil {
		return v1gen.EvictAdminRepodefaultApplicationProblemPlusJSONResponse{Body: errTieringUnavailable, StatusCode: http.StatusServiceUnavailable}, nil
	}
	id, err := s.tiering.Resolve(ctx, req.Owner, req.Name)
	if err == nil {
		var st gittier.RepoState
		if st, err = s.tiering.Evict(ctx, principal(ctx).UserID, clientOf(ctx).IP, id); err == nil {
			return v1gen.EvictAdminRepo200JSONResponse(tieringDTO(st)), nil
		}
	}
	switch {
	case errors.Is(err, gitstore.ErrNotFound):
		return v1gen.EvictAdminRepo404ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse: repoNotFound()}, nil
	case errors.Is(err, gittier.ErrNotEvictable):
		return v1gen.EvictAdminRepo409ApplicationProblemPlusJSONResponse(problem(http.StatusConflict, err.Error())), nil
	case errors.Is(err, gitstore.ErrBusy):
		return v1gen.EvictAdminRepo409ApplicationProblemPlusJSONResponse(problem(http.StatusConflict, "repository is in use, retry later")), nil
	}
	return nil, err
}

// hydrationPath is the SSE endpoint of the hydration progress. It is
// mounted outside the request timeout and outside the generated router (a
// stream has no OpenAPI response body).
const hydrationPath = "/api/v1/repos/{owner}/{name}/hydration"

// hydrationPoll is how often the stream re-reads the state, for a
// hydration run by another process (the hub only sees local ones).
const (
	hydrationPoll      = 5 * time.Second
	hydrationKeepalive = 15 * time.Second
)

// mountHydration mounts GET hydrationPath: an event stream of
// gittier.Event ("event: progress"), ending with a done or failed event.
// Opening it on a cold repository starts the hydration.
func mountHydration(r chi.Router, d Deps) {
	r.With(d.Auth.LoadSession).Get(hydrationPath, func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		if d.Repos == nil || d.Hydration == nil {
			writeProblem(w, http.StatusServiceUnavailable, "repository tiering is not configured")
			return
		}
		p, _ := auth.PrincipalFrom(ctx)
		if p.Method == auth.MethodToken && !p.HasScope("repo:read") {
			writeProblem(w, http.StatusForbidden, "the token lacks the repo:read scope")
			return
		}
		repo, err := d.Repos.Get(ctx, p, chi.URLParam(req, "owner"), chi.URLParam(req, "name"))
		switch {
		case errors.Is(err, repos.ErrNotFound), errors.Is(err, repos.ErrForbidden), errors.As(err, new(*repos.MovedError)):
			writeProblem(w, http.StatusNotFound, "repository not found")
			return
		case err != nil:
			d.Log.ErrorContext(ctx, "hydration stream: load repository", "error", err)
			writeProblem(w, http.StatusInternalServerError, "")
			return
		}
		streamHydration(ctx, w, d, p, repo)
	})
}

func streamHydration(ctx context.Context, w http.ResponseWriter, d Deps, p auth.Principal, repo repos.Repo) {
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	send := func(e gittier.Event) bool {
		data, err := json.Marshal(e)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: progress\ndata: %s\n\n", data); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	final := func(state string) (gittier.Event, bool) {
		switch state {
		case gitstore.StateHot:
			return gittier.Event{Repo: repo.ID, Phase: gittier.PhaseDone, Percent: 100, State: state}, true
		case gitstore.StateDegraded:
			return gittier.Event{Repo: repo.ID, Phase: gittier.PhaseFailed, State: state, Error: "repository is degraded"}, true
		}
		return gittier.Event{}, false
	}
	if e, ok := final(repo.StorageState); ok {
		send(e)
		return
	}
	ch, unsubscribe := d.Hydration.Hub().Subscribe(repo.ID)
	defer unsubscribe()
	if repo.StorageState == gitstore.StateCold {
		d.Hydration.Start(repo.ID)
	}
	if _, running := d.Hydration.Hub().Last(repo.ID); !running {
		// The subscription replays the last event of a run in progress;
		// otherwise tell the client the stream is live.
		send(gittier.Event{Repo: repo.ID, Phase: gittier.PhaseStart, State: repo.StorageState})
	}
	poll := time.NewTicker(hydrationPoll)
	defer poll.Stop()
	keepalive := time.NewTicker(hydrationKeepalive)
	defer keepalive.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-ch:
			if !send(e) || e.Terminal() {
				return
			}
		case <-poll.C:
			r, err := d.Repos.GetByID(ctx, p, repo.ID)
			if err != nil {
				return
			}
			if e, ok := final(r.StorageState); ok {
				send(e)
				return
			}
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}
