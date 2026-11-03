package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/ghsync"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
)

// webhookPath receives the GitHub webhook deliveries of a connection. It is
// mounted outside the REST API: GitHub authenticates with the payload
// signature (X-Hub-Signature-256), not with a session or a token.
const webhookPath = "/api/v1/github/webhook/{connectionId}"

var errGitHubUnavailable = problemResponse{status: http.StatusServiceUnavailable,
	detail: "GitHub sync is not available"}

// ghsyncFail maps a GitHub sync error to a problem response.
func ghsyncFail(err error, method, suffix string) (ghsyncResponse, error) {
	var ve *ghsync.ValidationError
	var ae *ghsync.APIError
	switch {
	case errors.As(err, &ve):
		return badRequest(err.Error()), nil
	case errors.Is(err, ghsync.ErrNotFound), errors.Is(err, gitstore.ErrNotFound):
		return problemResponse{status: http.StatusNotFound, detail: err.Error()}, nil
	case errors.Is(err, ghsync.ErrForbidden):
		return problemResponse{status: http.StatusForbidden, detail: err.Error()}, nil
	case errors.Is(err, ghsync.ErrInUse), errors.Is(err, ghsync.ErrNameTaken),
		errors.Is(err, ghsync.ErrImported), ghsync.IsConflict(err):
		return problemResponse{status: http.StatusConflict, detail: err.Error()}, nil
	case errors.Is(err, ghsync.ErrNoMasterKey):
		return problemResponse{status: http.StatusServiceUnavailable, detail: err.Error()}, nil
	case errors.Is(err, gitstore.ErrBusy), errors.Is(err, gitstore.ErrCold):
		return problemResponse{status: http.StatusServiceUnavailable, detail: "repository is being prepared, retry later",
			retryAfter: retryHydrating}, nil
	case errors.As(err, &ae):
		// API errors carry the status and message of GitHub, never a credential.
		return problemResponse{status: http.StatusBadGateway, detail: ae.Error()}, nil
	}
	r, err2 := reposFail(err, method, suffix)
	if err2 != nil {
		return nil, err2
	}
	return r.(ghsyncResponse), nil
}

// ghsyncResponse is answered by problemResponse and movedResponse.
type ghsyncResponse interface {
	v1gen.ListGitHubConnectionsResponseObject
	v1gen.CreateGitHubConnectionResponseObject
	v1gen.GetGitHubConnectionResponseObject
	v1gen.UpdateGitHubConnectionResponseObject
	v1gen.DeleteGitHubConnectionResponseObject
	v1gen.CheckGitHubConnectionResponseObject
	v1gen.ImportGitHubRepoResponseObject
	v1gen.GetGitHubImportResponseObject
	v1gen.GetGitHubSyncLinkResponseObject
	v1gen.PutGitHubSyncLinkResponseObject
	v1gen.DeleteGitHubSyncLinkResponseObject
	v1gen.ListGitHubSyncRefsResponseObject
	v1gen.RunGitHubSyncResponseObject
	v1gen.ResolveGitHubSyncConflictResponseObject
}

var (
	_ ghsyncResponse = problemResponse{}
	_ ghsyncResponse = movedResponse{}
)

func (p problemResponse) VisitListGitHubConnectionsResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitCreateGitHubConnectionResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitGetGitHubConnectionResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitUpdateGitHubConnectionResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitDeleteGitHubConnectionResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitCheckGitHubConnectionResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitImportGitHubRepoResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitGetGitHubImportResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitGetGitHubSyncLinkResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitPutGitHubSyncLinkResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitDeleteGitHubSyncLinkResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitListGitHubSyncRefsResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (p problemResponse) VisitRunGitHubSyncResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitResolveGitHubSyncConflictResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (m movedResponse) VisitListGitHubConnectionsResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitCreateGitHubConnectionResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitGetGitHubConnectionResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitUpdateGitHubConnectionResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitDeleteGitHubConnectionResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitCheckGitHubConnectionResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitImportGitHubRepoResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitGetGitHubImportResponse(w http.ResponseWriter) error  { return m.write(w) }
func (m movedResponse) VisitGetGitHubSyncLinkResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitPutGitHubSyncLinkResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitDeleteGitHubSyncLinkResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitListGitHubSyncRefsResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func (m movedResponse) VisitRunGitHubSyncResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitResolveGitHubSyncConflictResponse(w http.ResponseWriter) error {
	return m.write(w)
}

func connectionDTO(c ghsync.Connection) v1gen.GitHubConnection {
	out := v1gen.GitHubConnection{Id: c.ID, Name: c.Name, Kind: v1gen.GitHubConnectionKind(c.Kind),
		BaseUrl: c.BaseURL, Owner: c.Owner, OwnerKind: v1gen.GitHubConnectionOwnerKind(c.OwnerKind),
		WebhookUrl: c.WebhookURL, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
	if c.AppID > 0 {
		out.AppId, out.InstallationId = &c.AppID, &c.InstallationID
	}
	if c.WebhookSecret != "" {
		out.WebhookSecret = &c.WebhookSecret
	}
	return out
}

func importDTO(i ghsync.Import) v1gen.GitHubImport {
	d := v1gen.GitHubImport{Id: i.ID, RepoId: i.RepoID, RepoFullName: i.RepoFullName,
		RemoteOwner: i.RemoteOwner, RemoteName: i.RemoteName, Status: v1gen.GitHubImportStatus(i.Status),
		LastError: i.LastError, CreatedAt: i.CreatedAt, StartedAt: i.StartedAt, FinishedAt: i.FinishedAt}
	if len(i.Include) > 0 {
		inc := make([]v1gen.GitHubImportKind, len(i.Include))
		for n, k := range i.Include {
			inc[n] = v1gen.GitHubImportKind(k)
		}
		d.Include = &inc
	}
	return d
}

func importKinds(in *[]v1gen.GitHubImportKind) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(*in))
	for n, k := range *in {
		out[n] = string(k)
	}
	return out
}

func linkDTO(l ghsync.Link) v1gen.GitHubSyncLink {
	return v1gen.GitHubSyncLink{ConnectionId: l.ConnectionID, Mode: v1gen.GitHubSyncMode(l.Mode),
		RemoteOwner: l.RemoteOwner, RemoteName: l.RemoteName, Prune: l.Prune, TrustGithub: l.TrustGitHub,
		CreateMissing: l.CreateMissing, IntervalMinutes: l.IntervalMinutes, Enabled: l.Enabled,
		ReadOnly: l.Mode == "mirror", Status: v1gen.GitHubSyncLinkStatus(l.Status), LastError: l.LastError,
		Failures: l.Failures, NextAttemptAt: l.NextAttemptAt, LastRunAt: l.LastRunAt,
		LastSuccessAt: l.LastSuccessAt, LfsSkipped: l.LFSSkipped, CreatedAt: l.CreatedAt, UpdatedAt: l.UpdatedAt}
}

func syncRefDTO(r ghsync.Ref) v1gen.GitHubSyncRef {
	out := v1gen.GitHubSyncRef{Ref: r.Ref, LocalSha: r.LocalSHA, RemoteSha: r.RemoteSHA,
		Status: v1gen.GitHubSyncRefStatus(r.Status), LastError: r.LastError, Retries: r.Retries,
		Resolution: v1gen.GitHubSyncRefResolution(r.Resolution), UpdatedAt: r.UpdatedAt}
	if r.ConflictRef != "" {
		out.ConflictRef, out.ConflictSha = &r.ConflictRef, &r.ConflictSHA
	}
	return out
}

func (s *v1Server) ListGitHubConnections(ctx context.Context, req v1gen.ListGitHubConnectionsRequestObject) (v1gen.ListGitHubConnectionsResponseObject, error) {
	if s.github == nil {
		return errGitHubUnavailable, nil
	}
	cs, err := s.github.ListConnections(ctx, principal(ctx), deref(req.Params.Owner))
	if err != nil {
		return ghsyncFail(err, http.MethodGet, "")
	}
	out := make(v1gen.ListGitHubConnections200JSONResponse, 0, len(cs))
	for _, c := range cs {
		out = append(out, connectionDTO(c))
	}
	return out, nil
}

func (s *v1Server) CreateGitHubConnection(ctx context.Context, req v1gen.CreateGitHubConnectionRequestObject) (v1gen.CreateGitHubConnectionResponseObject, error) {
	if s.github == nil {
		return errGitHubUnavailable, nil
	}
	b := req.Body
	c, err := s.github.CreateConnection(ctx, principal(ctx), ghsync.ConnectionInput{Owner: deref(b.Owner),
		Name: b.Name, Kind: string(b.Kind), BaseURL: deref(b.BaseUrl), AppID: deref(b.AppId),
		InstallationID: deref(b.InstallationId), PrivateKey: deref(b.PrivateKey), Token: deref(b.Token),
		WebhookSecret: deref(b.WebhookSecret)})
	if err != nil {
		return ghsyncFail(err, http.MethodPost, "")
	}
	return v1gen.CreateGitHubConnection201JSONResponse(connectionDTO(c)), nil
}

func (s *v1Server) GetGitHubConnection(ctx context.Context, req v1gen.GetGitHubConnectionRequestObject) (v1gen.GetGitHubConnectionResponseObject, error) {
	if s.github == nil {
		return errGitHubUnavailable, nil
	}
	c, err := s.github.GetConnection(ctx, principal(ctx), req.ConnectionId)
	if err != nil {
		return ghsyncFail(err, http.MethodGet, "")
	}
	return v1gen.GetGitHubConnection200JSONResponse(connectionDTO(c)), nil
}

func (s *v1Server) UpdateGitHubConnection(ctx context.Context, req v1gen.UpdateGitHubConnectionRequestObject) (v1gen.UpdateGitHubConnectionResponseObject, error) {
	if s.github == nil {
		return errGitHubUnavailable, nil
	}
	b := req.Body
	c, err := s.github.UpdateConnection(ctx, principal(ctx), req.ConnectionId, ghsync.ConnectionUpdate{
		Name: b.Name, AppID: b.AppId, InstallationID: b.InstallationId, PrivateKey: b.PrivateKey,
		Token: b.Token, RotateWebhookSecret: deref(b.RotateWebhookSecret)})
	if err != nil {
		return ghsyncFail(err, http.MethodPatch, "")
	}
	return v1gen.UpdateGitHubConnection200JSONResponse(connectionDTO(c)), nil
}

func (s *v1Server) DeleteGitHubConnection(ctx context.Context, req v1gen.DeleteGitHubConnectionRequestObject) (v1gen.DeleteGitHubConnectionResponseObject, error) {
	if s.github == nil {
		return errGitHubUnavailable, nil
	}
	if err := s.github.DeleteConnection(ctx, principal(ctx), req.ConnectionId); err != nil {
		return ghsyncFail(err, http.MethodDelete, "")
	}
	return v1gen.DeleteGitHubConnection204Response{}, nil
}

func (s *v1Server) CheckGitHubConnection(ctx context.Context, req v1gen.CheckGitHubConnectionRequestObject) (v1gen.CheckGitHubConnectionResponseObject, error) {
	if s.github == nil {
		return errGitHubUnavailable, nil
	}
	ok, detail, err := s.github.CheckConnection(ctx, principal(ctx), req.ConnectionId)
	if err != nil {
		return ghsyncFail(err, http.MethodPost, "")
	}
	out := v1gen.CheckGitHubConnection200JSONResponse{Ok: ok}
	if detail != "" {
		out.Detail = &detail
	}
	return out, nil
}

func (s *v1Server) ImportGitHubRepo(ctx context.Context, req v1gen.ImportGitHubRepoRequestObject) (v1gen.ImportGitHubRepoResponseObject, error) {
	if s.github == nil {
		return errGitHubUnavailable, nil
	}
	b := req.Body
	i, err := s.github.Import(ctx, principal(ctx), ghsync.ImportInput{ConnectionID: b.ConnectionId,
		RemoteOwner: b.RemoteOwner, RemoteName: b.RemoteName, Owner: deref(b.Owner), Name: deref(b.Name),
		Description: deref(b.Description), Visibility: string(deref(b.Visibility)), Include: importKinds(b.Include)},
		clientOf(ctx))
	if err != nil {
		return ghsyncFail(err, http.MethodPost, "")
	}
	return v1gen.ImportGitHubRepo202JSONResponse(importDTO(i)), nil
}

func (s *v1Server) GetGitHubImport(ctx context.Context, req v1gen.GetGitHubImportRequestObject) (v1gen.GetGitHubImportResponseObject, error) {
	if s.github == nil {
		return errGitHubUnavailable, nil
	}
	i, err := s.github.GetImport(ctx, principal(ctx), req.Owner, req.Name)
	if err != nil {
		return ghsyncFail(err, http.MethodGet, "/github/import")
	}
	return v1gen.GetGitHubImport200JSONResponse(importDTO(i)), nil
}

func (s *v1Server) GetGitHubSyncLink(ctx context.Context, req v1gen.GetGitHubSyncLinkRequestObject) (v1gen.GetGitHubSyncLinkResponseObject, error) {
	if s.github == nil {
		return errGitHubUnavailable, nil
	}
	l, err := s.github.GetLink(ctx, principal(ctx), req.Owner, req.Name)
	if err != nil {
		return ghsyncFail(err, http.MethodGet, "/github/sync")
	}
	return v1gen.GetGitHubSyncLink200JSONResponse(linkDTO(l)), nil
}

func (s *v1Server) PutGitHubSyncLink(ctx context.Context, req v1gen.PutGitHubSyncLinkRequestObject) (v1gen.PutGitHubSyncLinkResponseObject, error) {
	if s.github == nil {
		return errGitHubUnavailable, nil
	}
	b := req.Body
	l, err := s.github.PutLink(ctx, principal(ctx), req.Owner, req.Name, ghsync.LinkInput{
		ConnectionID: b.ConnectionId, Mode: string(b.Mode), RemoteOwner: b.RemoteOwner, RemoteName: b.RemoteName,
		Prune: b.Prune, TrustGitHub: deref(b.TrustGithub), CreateMissing: b.CreateMissing,
		IntervalMinutes: b.IntervalMinutes, Enabled: b.Enabled})
	if err != nil {
		return ghsyncFail(err, http.MethodPut, "/github/sync")
	}
	return v1gen.PutGitHubSyncLink200JSONResponse(linkDTO(l)), nil
}

func (s *v1Server) DeleteGitHubSyncLink(ctx context.Context, req v1gen.DeleteGitHubSyncLinkRequestObject) (v1gen.DeleteGitHubSyncLinkResponseObject, error) {
	if s.github == nil {
		return errGitHubUnavailable, nil
	}
	if err := s.github.DeleteLink(ctx, principal(ctx), req.Owner, req.Name); err != nil {
		return ghsyncFail(err, http.MethodDelete, "/github/sync")
	}
	return v1gen.DeleteGitHubSyncLink204Response{}, nil
}

func (s *v1Server) ListGitHubSyncRefs(ctx context.Context, req v1gen.ListGitHubSyncRefsRequestObject) (v1gen.ListGitHubSyncRefsResponseObject, error) {
	if s.github == nil {
		return errGitHubUnavailable, nil
	}
	rs, err := s.github.ListRefs(ctx, principal(ctx), req.Owner, req.Name)
	if err != nil {
		return ghsyncFail(err, http.MethodGet, "/github/sync/refs")
	}
	out := make(v1gen.ListGitHubSyncRefs200JSONResponse, 0, len(rs))
	for _, r := range rs {
		out = append(out, syncRefDTO(r))
	}
	return out, nil
}

func (s *v1Server) RunGitHubSync(ctx context.Context, req v1gen.RunGitHubSyncRequestObject) (v1gen.RunGitHubSyncResponseObject, error) {
	if s.github == nil {
		return errGitHubUnavailable, nil
	}
	if err := s.github.Run(ctx, principal(ctx), req.Owner, req.Name); err != nil {
		return ghsyncFail(err, http.MethodPost, "/github/sync/run")
	}
	return v1gen.RunGitHubSync202Response{}, nil
}

func (s *v1Server) ResolveGitHubSyncConflict(ctx context.Context, req v1gen.ResolveGitHubSyncConflictRequestObject) (v1gen.ResolveGitHubSyncConflictResponseObject, error) {
	if s.github == nil {
		return errGitHubUnavailable, nil
	}
	r, err := s.github.Resolve(ctx, principal(ctx), req.Owner, req.Name, req.Body.Ref, string(req.Body.Resolution))
	if err != nil {
		return ghsyncFail(err, http.MethodPost, "/github/sync/resolve")
	}
	return v1gen.ResolveGitHubSyncConflict200JSONResponse(syncRefDTO(r)), nil
}

// mountGitHubWebhook mounts the webhook receiver. It has no session, CSRF
// check or timeout of the REST API: the signature is the authentication.
func mountGitHubWebhook(r chi.Router, d Deps) {
	r.Post(webhookPath, func(w http.ResponseWriter, req *http.Request) {
		if d.GitHub == nil {
			writeProblem(w, http.StatusServiceUnavailable, "GitHub sync is not available")
			return
		}
		id, err := uuid.Parse(chi.URLParam(req, "connectionId"))
		if err != nil {
			writeProblem(w, http.StatusNotFound, "")
			return
		}
		status, err := d.GitHub.Webhook(req, id)
		if err != nil {
			if we, ok := ghsync.IsWebhookError(err); ok {
				writeProblem(w, we.Status, we.Msg)
				return
			}
			d.Log.ErrorContext(req.Context(), "github webhook failed", "connection", id, "error", err)
			writeProblem(w, http.StatusInternalServerError, "")
			return
		}
		w.WriteHeader(status)
	})
}
