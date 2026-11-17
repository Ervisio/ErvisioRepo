package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Ervisio/ErvisioRepo/internal/avatars"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/storage/storagecfg"
)

var errAvatarsUnavailable = problemResponse{status: http.StatusServiceUnavailable, detail: "avatar uploads are not available"}

func (p problemResponse) VisitUploadAvatarResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitDeleteAvatarResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitGetAdminAvatarStorageResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitPutAdminAvatarStorageResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (s *v1Server) UploadAvatar(ctx context.Context, req v1gen.UploadAvatarRequestObject) (v1gen.UploadAvatarResponseObject, error) {
	if s.avatars == nil || s.identity == nil {
		return errAvatarsUnavailable, nil
	}
	if !signedIn(ctx) {
		return unauthenticated, nil
	}
	if req.Body == nil {
		return badRequest("an image body is required"), nil
	}
	id := principal(ctx).UserID
	_, err := s.avatars.Upload(ctx, id, clientOf(ctx).IP, req.Body)
	if pr, ok := avatarProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	u, err := s.identity.User(ctx, id)
	if err != nil {
		return nil, err
	}
	return v1gen.UploadAvatar200JSONResponse(userDTO(u)), nil
}

func (s *v1Server) DeleteAvatar(ctx context.Context, _ v1gen.DeleteAvatarRequestObject) (v1gen.DeleteAvatarResponseObject, error) {
	if s.avatars == nil || s.identity == nil {
		return errAvatarsUnavailable, nil
	}
	if !signedIn(ctx) {
		return unauthenticated, nil
	}
	id := principal(ctx).UserID
	if err := s.avatars.Remove(ctx, id, clientOf(ctx).IP); err != nil {
		return nil, err
	}
	u, err := s.identity.User(ctx, id)
	if err != nil {
		return nil, err
	}
	return v1gen.DeleteAvatar200JSONResponse(userDTO(u)), nil
}

func (p problemResponse) VisitUploadOrgAvatarResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitDeleteOrgAvatarResponse(w http.ResponseWriter) error { return p.write(w) }

// avatarProblem maps an error of avatars.Service.Store to a response.
func avatarProblem(err error) (problemResponse, bool) {
	switch {
	case errors.Is(err, avatars.ErrTooLarge):
		return problemResponse{status: http.StatusRequestEntityTooLarge, detail: err.Error()}, true
	case errors.Is(err, avatars.ErrInvalidImage):
		return badRequest(avatars.ErrInvalidImage.Error()), true
	case errors.Is(err, avatars.ErrUnavailable):
		return problemResponse{status: http.StatusServiceUnavailable, detail: err.Error()}, true
	}
	return problemResponse{}, false
}

// UploadOrgAvatar stores the image only after checking that the caller
// manages the organization, so that nobody else can fill the avatar storage.
func (s *v1Server) UploadOrgAvatar(ctx context.Context, req v1gen.UploadOrgAvatarRequestObject) (v1gen.UploadOrgAvatarResponseObject, error) {
	if s.avatars == nil {
		return errAvatarsUnavailable, nil
	}
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("an image body is required"), nil
	}
	who := principal(ctx)
	if _, err := s.orgs.AuthorizeManage(ctx, who, req.Org); err != nil {
		return orgsFail(err)
	}
	u, err := s.avatars.Store(ctx, req.Body)
	if pr, ok := avatarProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	o, err := s.orgs.SetAvatar(ctx, who, req.Org, u, clientOf(ctx))
	if err != nil {
		return orgsFail(err)
	}
	return v1gen.UploadOrgAvatar200JSONResponse(orgDTO(o)), nil
}

func (s *v1Server) DeleteOrgAvatar(ctx context.Context, req v1gen.DeleteOrgAvatarRequestObject) (v1gen.DeleteOrgAvatarResponseObject, error) {
	if s.orgs == nil {
		return errOrgsUnavailable, nil
	}
	o, err := s.orgs.SetAvatar(ctx, principal(ctx), req.Org, "", clientOf(ctx))
	if err != nil {
		return orgsFail(err)
	}
	return v1gen.DeleteOrgAvatar200JSONResponse(orgDTO(o)), nil
}

// UploadRepoAvatar stores the image only after checking that the caller
// administers the repository.
func (s *v1Server) UploadRepoAvatar(ctx context.Context, req v1gen.UploadRepoAvatarRequestObject) (v1gen.UploadRepoAvatarResponseObject, error) {
	if s.avatars == nil {
		return errAvatarsUnavailable, nil
	}
	if s.repos == nil {
		return errReposUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("an image body is required"), nil
	}
	who := principal(ctx)
	if _, err := s.repos.AuthorizeAdmin(ctx, who, req.Owner, req.Name); err != nil {
		return reposFail(err, http.MethodPut, "/avatar")
	}
	u, err := s.avatars.Store(ctx, req.Body)
	if pr, ok := avatarProblem(err); ok {
		return pr, nil
	}
	if err != nil {
		return nil, err
	}
	r, err := s.repos.SetAvatar(ctx, who, req.Owner, req.Name, u, clientOf(ctx))
	if err != nil {
		return reposFail(err, http.MethodPut, "/avatar")
	}
	out, err := s.repoOut(ctx, r)
	if err != nil {
		return nil, err
	}
	return v1gen.UploadRepoAvatar200JSONResponse(out), nil
}

func (s *v1Server) DeleteRepoAvatar(ctx context.Context, req v1gen.DeleteRepoAvatarRequestObject) (v1gen.DeleteRepoAvatarResponseObject, error) {
	if s.repos == nil {
		return errReposUnavailable, nil
	}
	r, err := s.repos.SetAvatar(ctx, principal(ctx), req.Owner, req.Name, "", clientOf(ctx))
	if err != nil {
		return reposFail(err, http.MethodDelete, "/avatar")
	}
	out, err := s.repoOut(ctx, r)
	if err != nil {
		return nil, err
	}
	return v1gen.DeleteRepoAvatar200JSONResponse(out), nil
}

func (s *v1Server) GetAdminAvatarStorage(ctx context.Context, _ v1gen.GetAdminAvatarStorageRequestObject) (v1gen.GetAdminAvatarStorageResponseObject, error) {
	if s.avatars == nil {
		return errAvatarsUnavailable, nil
	}
	v, err := s.avatars.Config(ctx)
	if err != nil {
		return nil, err
	}
	return v1gen.GetAdminAvatarStorage200JSONResponse(avatarStorageDTO(v)), nil
}

func (s *v1Server) PutAdminAvatarStorage(ctx context.Context, req v1gen.PutAdminAvatarStorageRequestObject) (v1gen.PutAdminAvatarStorageResponseObject, error) {
	if s.avatars == nil {
		return errAvatarsUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("avatar storage settings are required"), nil
	}
	v, err := s.avatars.PutConfig(ctx, principal(ctx).UserID, clientOf(ctx).IP,
		avatars.Settings{Target: req.Body.Target, PublicURL: req.Body.PublicUrl})
	var verr *storagecfg.ValidationError
	switch {
	case errors.As(err, &verr):
		return v1gen.PutAdminAvatarStorage422ApplicationProblemPlusJSONResponse(storageProblem(verr)), nil
	case err != nil:
		return nil, err
	}
	return v1gen.PutAdminAvatarStorage200JSONResponse(avatarStorageDTO(v)), nil
}

func avatarStorageDTO(v avatars.View) v1gen.AvatarStorage {
	out := v1gen.AvatarStorage{Target: v.Target, PublicUrl: v.PublicURL, LocalTarget: v.LocalTarget,
		Targets: make([]v1gen.AvatarStorageTarget, 0, len(v.Targets))}
	for _, t := range v.Targets {
		out.Targets = append(out.Targets, v1gen.AvatarStorageTarget{Name: t.Name, Kind: t.Kind, Encrypted: t.Encrypted})
	}
	return out
}

// mountAvatars serves the stored avatar images at /avatars/{file}: public,
// content-addressed, so cached as immutable.
func mountAvatars(r chi.Router, d Deps) {
	if d.Avatars == nil {
		return
	}
	r.Get("/"+avatars.Prefix+"{file}", func(w http.ResponseWriter, req *http.Request) {
		rc, info, err := d.Avatars.Open(req.Context(), chi.URLParam(req, "file"))
		if err != nil {
			writeProblem(w, http.StatusNotFound, "avatar not found")
			return
		}
		defer func() { _ = rc.Close() }()
		h := w.Header()
		h.Set("Content-Type", avatars.ContentType)
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
		h.Set("ETag", `"`+chi.URLParam(req, "file")+`"`)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
		h.Set("Cross-Origin-Resource-Policy", "cross-origin")
		if req.Header.Get("If-None-Match") == h.Get("ETag") {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		h.Set("Content-Length", strconv.FormatInt(info.Size, 10))
		_, _ = io.Copy(w, rc)
	})
}
