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
	switch {
	case errors.Is(err, avatars.ErrTooLarge):
		return problemResponse{status: http.StatusRequestEntityTooLarge, detail: err.Error()}, nil
	case errors.Is(err, avatars.ErrInvalidImage):
		return badRequest(avatars.ErrInvalidImage.Error()), nil
	case errors.Is(err, avatars.ErrUnavailable):
		return problemResponse{status: http.StatusServiceUnavailable, detail: err.Error()}, nil
	case err != nil:
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
