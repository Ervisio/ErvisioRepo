package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/presence"
)

// Presence serves the presence and file lock endpoints; it is implemented
// by *presence.Service.
type Presence interface {
	State(ctx context.Context, who auth.Principal, owner, name string) (presence.State, error)
	SetEnabled(ctx context.Context, who auth.Principal, owner, name string, enabled bool, c identity.Client) (presence.State, error)
	Heartbeat(ctx context.Context, who auth.Principal, owner, name string, in presence.Heartbeat) (presence.State, error)
	Leave(ctx context.Context, who auth.Principal, owner, name string, client uuid.UUID) error
	CreateLock(ctx context.Context, who auth.Principal, owner, name, path, branch string) (presence.Lock, error)
	Unlock(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, force bool) (presence.Lock, error)
	Subscribe(ctx context.Context, who auth.Principal, owner, name string) (*presence.Subscription, presence.State, error)
	Check(ctx context.Context, who auth.Principal, sub *presence.Subscription) error
}

var _ Presence = (*presence.Service)(nil)

const presencePath = "/api/v1/repos/{owner}/{name}/presence"

var errPresenceUnavailable = problemResponse{status: http.StatusServiceUnavailable, detail: presence.ErrUnavailable.Error()}

// presenceResponse is answered by problemResponse and movedResponse.
type presenceResponse interface {
	v1gen.GetPresenceResponseObject
	v1gen.SetPresenceEnabledResponseObject
	v1gen.PutPresenceHeartbeatResponseObject
	v1gen.DeletePresenceHeartbeatResponseObject
	v1gen.CreatePresenceLockResponseObject
	v1gen.DeletePresenceLockResponseObject
}

var (
	_ presenceResponse = problemResponse{}
	_ presenceResponse = movedResponse{}
)

func (p problemResponse) VisitGetPresenceResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitSetPresenceEnabledResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitPutPresenceHeartbeatResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitDeletePresenceHeartbeatResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitCreatePresenceLockResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitDeletePresenceLockResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (m movedResponse) VisitGetPresenceResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitSetPresenceEnabledResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitPutPresenceHeartbeatResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitDeletePresenceHeartbeatResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitCreatePresenceLockResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitDeletePresenceLockResponse(w http.ResponseWriter) error {
	return m.write(w)
}

// presenceFail maps a presence error to a response. suffix is the part of
// the path after /repos/{owner}/{name}.
func presenceFail(err error, method, suffix string) (presenceResponse, error) {
	var ve *presence.ValidationError
	switch {
	case errors.As(err, &ve):
		return badRequest(err.Error()), nil
	case errors.Is(err, presence.ErrForbidden):
		return problemResponse{status: http.StatusForbidden, detail: err.Error()}, nil
	case errors.Is(err, presence.ErrDisabled):
		return problemResponse{status: http.StatusConflict, detail: err.Error()}, nil
	case errors.Is(err, presence.ErrLockNotFound):
		return problemResponse{status: http.StatusNotFound, detail: err.Error()}, nil
	case errors.Is(err, presence.ErrUnavailable):
		return errPresenceUnavailable, nil
	}
	r, err2 := reposFail(err, method, suffix)
	if err2 != nil {
		return nil, err2
	}
	return r.(presenceResponse), nil
}

func presenceUserDTO(u presence.User) v1gen.PresenceUser {
	return v1gen.PresenceUser{Login: u.Login, Name: u.Name, AvatarUrl: u.AvatarURL}
}

func presenceDTO(p presence.Presence) v1gen.Presence {
	dirty := make([]v1gen.PresenceDirtyFile, len(p.Dirty))
	for i, f := range p.Dirty {
		dirty[i] = v1gen.PresenceDirtyFile{Path: f.Path, State: v1gen.PresenceFileState(f.State)}
	}
	return v1gen.Presence{User: presenceUserDTO(p.User), ClientId: p.ClientID, Branch: p.Branch, Head: p.Head, Dirty: dirty,
		Truncated: p.Truncated, Ahead: p.Ahead, Unpublished: p.Unpublished, Active: p.Active, UpdatedAt: p.UpdatedAt}
}

func presenceLockDTO(l presence.Lock) v1gen.PresenceLock {
	return v1gen.PresenceLock{Id: l.ID, Path: l.Path, Branch: l.Branch, Owner: presenceUserDTO(l.Owner), LockedAt: l.LockedAt}
}

func presenceStateDTO(st presence.State) v1gen.PresenceState {
	out := v1gen.PresenceState{Enabled: st.Enabled, CanUse: st.CanUse, CanManage: st.CanManage, CanForceUnlock: st.CanForceUnlock,
		Interval: int(st.Interval / time.Second), Ttl: int(st.TTL / time.Second),
		Others: make([]v1gen.Presence, len(st.Others)), Locks: make([]v1gen.PresenceLock, len(st.Locks))}
	for i, p := range st.Others {
		out.Others[i] = presenceDTO(p)
	}
	for i, l := range st.Locks {
		out.Locks[i] = presenceLockDTO(l)
	}
	return out
}

func (s *v1Server) GetPresence(ctx context.Context, req v1gen.GetPresenceRequestObject) (v1gen.GetPresenceResponseObject, error) {
	if s.presence == nil {
		return errPresenceUnavailable, nil
	}
	st, err := s.presence.State(ctx, principal(ctx), req.Owner, req.Name)
	if err != nil {
		return presenceFail(err, http.MethodGet, "/presence")
	}
	return v1gen.GetPresence200JSONResponse(presenceStateDTO(st)), nil
}

func (s *v1Server) SetPresenceEnabled(ctx context.Context, req v1gen.SetPresenceEnabledRequestObject) (v1gen.SetPresenceEnabledResponseObject, error) {
	if s.presence == nil {
		return errPresenceUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	st, err := s.presence.SetEnabled(ctx, principal(ctx), req.Owner, req.Name, req.Body.Enabled, clientOf(ctx))
	if err != nil {
		return presenceFail(err, http.MethodPut, "/presence/enabled")
	}
	return v1gen.SetPresenceEnabled200JSONResponse(presenceStateDTO(st)), nil
}

func (s *v1Server) PutPresenceHeartbeat(ctx context.Context, req v1gen.PutPresenceHeartbeatRequestObject) (v1gen.PutPresenceHeartbeatResponseObject, error) {
	if s.presence == nil {
		return errPresenceUnavailable, nil
	}
	b := req.Body
	if b == nil {
		return badRequest("missing body"), nil
	}
	in := presence.Heartbeat{ClientID: b.ClientId}
	if b.Branch != nil {
		in.Branch = *b.Branch
	}
	if b.Head != nil {
		in.Head = *b.Head
	}
	if b.Dirty != nil {
		in.Dirty = make([]presence.DirtyFile, len(*b.Dirty))
		for i, f := range *b.Dirty {
			in.Dirty[i] = presence.DirtyFile{Path: f.Path, State: string(f.State)}
		}
	}
	in.Truncated = b.Truncated != nil && *b.Truncated
	if b.Ahead != nil {
		in.Ahead = *b.Ahead
	}
	in.Unpublished = b.Unpublished != nil && *b.Unpublished
	in.Active = b.Active != nil && *b.Active
	st, err := s.presence.Heartbeat(ctx, principal(ctx), req.Owner, req.Name, in)
	if err != nil {
		return presenceFail(err, http.MethodPut, "/presence/heartbeat")
	}
	return v1gen.PutPresenceHeartbeat200JSONResponse(presenceStateDTO(st)), nil
}

func (s *v1Server) DeletePresenceHeartbeat(ctx context.Context, req v1gen.DeletePresenceHeartbeatRequestObject) (v1gen.DeletePresenceHeartbeatResponseObject, error) {
	if s.presence == nil {
		return errPresenceUnavailable, nil
	}
	if err := s.presence.Leave(ctx, principal(ctx), req.Owner, req.Name, req.Params.ClientId); err != nil {
		return presenceFail(err, http.MethodDelete, "/presence/heartbeat")
	}
	return v1gen.DeletePresenceHeartbeat204Response{}, nil
}

func (s *v1Server) CreatePresenceLock(ctx context.Context, req v1gen.CreatePresenceLockRequestObject) (v1gen.CreatePresenceLockResponseObject, error) {
	if s.presence == nil {
		return errPresenceUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	l, err := s.presence.CreateLock(ctx, principal(ctx), req.Owner, req.Name, req.Body.Path, req.Body.Branch)
	if errors.Is(err, presence.ErrLockExists) {
		return v1gen.CreatePresenceLock409JSONResponse{Lock: presenceLockDTO(l)}, nil
	}
	if err != nil {
		return presenceFail(err, http.MethodPost, "/presence/locks")
	}
	return v1gen.CreatePresenceLock201JSONResponse(presenceLockDTO(l)), nil
}

func (s *v1Server) DeletePresenceLock(ctx context.Context, req v1gen.DeletePresenceLockRequestObject) (v1gen.DeletePresenceLockResponseObject, error) {
	if s.presence == nil {
		return errPresenceUnavailable, nil
	}
	force := req.Params.Force != nil && *req.Params.Force
	l, err := s.presence.Unlock(ctx, principal(ctx), req.Owner, req.Name, req.LockId, force)
	if err != nil {
		return presenceFail(err, http.MethodDelete, "/presence/locks/"+req.LockId.String())
	}
	return v1gen.DeletePresenceLock200JSONResponse(presenceLockDTO(l)), nil
}

// mountPresence mounts the presence event stream outside the request
// timeout. It accepts sessions and access tokens (repo:read).
func mountPresence(r chi.Router, d Deps) {
	r.With(d.Auth.LoadSession).Get(presencePath+"/stream", func(w http.ResponseWriter, req *http.Request) {
		streamPresence(w, req, d)
	})
}

// streamPresence sends the presence events of a repository as server-sent
// events: a snapshot first, then the changes, with a ping comment every
// PingInterval. Access is checked again at every ping.
func streamPresence(w http.ResponseWriter, req *http.Request, d Deps) {
	if d.Presence == nil {
		writeProblem(w, http.StatusServiceUnavailable, presence.ErrUnavailable.Error())
		return
	}
	p, ok := streamPrincipal(w, req, auth.ScopeReadRepo)
	if !ok {
		return
	}
	if p.UserID == uuid.Nil {
		writeProblem(w, http.StatusUnauthorized, "sign in required")
		return
	}
	ctx := req.Context()
	sub, st, err := d.Presence.Subscribe(ctx, p, chi.URLParam(req, "owner"), chi.URLParam(req, "name"))
	if err != nil {
		presenceStreamFail(w, req, d, err)
		return
	}
	defer sub.Close()
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if writeSSE(w, presence.KindSnapshot, presenceStateDTO(st)) != nil || rc.Flush() != nil {
		return
	}
	ping := time.NewTicker(presence.PingInterval)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-sub.Events():
			if !ok {
				return
			}
			if writePresenceEvent(w, ev) != nil || rc.Flush() != nil || ev.Kind == presence.KindDisabled {
				return
			}
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
			if err := d.Presence.Check(ctx, p, sub); err != nil {
				if errors.Is(err, presence.ErrDisabled) {
					_ = writeSSE(w, presence.KindDisabled, struct{}{})
					_ = rc.Flush()
				} else if ctx.Err() == nil && !errors.Is(err, presence.ErrForbidden) {
					d.Log.InfoContext(ctx, "presence stream closed", "repo", sub.RepoID, "reason", err)
				}
				return
			}
		}
	}
}

// presenceGoneDTO and presenceUnlockDTO are the PresenceGone and
// PresenceUnlock schemas, which no generated operation references.
type presenceGoneDTO struct {
	Login    string    `json:"login"`
	ClientID uuid.UUID `json:"clientId"`
}

type presenceUnlockDTO struct {
	ID uuid.UUID `json:"id"`
}

func writePresenceEvent(w io.Writer, ev presence.Event) error {
	switch ev.Kind {
	case presence.KindPresence:
		return writeSSE(w, ev.Kind, presenceDTO(*ev.Presence))
	case presence.KindGone:
		return writeSSE(w, ev.Kind, presenceGoneDTO{Login: ev.Gone.Login, ClientID: ev.Gone.ClientID})
	case presence.KindLock:
		return writeSSE(w, ev.Kind, presenceLockDTO(*ev.Lock))
	case presence.KindUnlock:
		return writeSSE(w, ev.Kind, presenceUnlockDTO{ID: ev.LockID})
	case presence.KindDisabled:
		return writeSSE(w, ev.Kind, struct{}{})
	}
	return nil
}

func writeSSE(w io.Writer, event string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode %s event: %w", event, err)
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	return err
}

// presenceStreamFail answers a service error on the stream route.
func presenceStreamFail(w http.ResponseWriter, req *http.Request, d Deps, err error) {
	r, ferr := presenceFail(err, req.Method, "/presence/stream")
	if ferr != nil {
		d.Log.ErrorContext(req.Context(), "presence stream request failed", "error", ferr)
		writeProblem(w, http.StatusInternalServerError, "")
		return
	}
	switch v := r.(type) {
	case problemResponse:
		_ = v.write(w)
	case movedResponse:
		_ = v.write(w)
	default:
		writeProblem(w, http.StatusNotFound, "repository not found")
	}
}
