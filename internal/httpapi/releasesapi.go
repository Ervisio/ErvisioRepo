package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/httpapi/v1gen"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/quota"
	"github.com/Ervisio/ErvisioRepo/internal/releases"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

// Releases is the release service used by the release endpoints; it is
// implemented by *releases.Service.
type Releases interface {
	List(ctx context.Context, who auth.Principal, owner, name, tag string, offset, limit int) ([]releases.Release, error)
	Get(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID) (releases.Release, error)
	Latest(ctx context.Context, who auth.Principal, owner, name string) (releases.Release, error)
	Create(ctx context.Context, who auth.Principal, owner, name string, in releases.CreateInput, c identity.Client) (releases.Release, error)
	Update(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, in releases.UpdateInput, c identity.Client) (releases.Release, error)
	Delete(ctx context.Context, who auth.Principal, owner, name string, id uuid.UUID, c identity.Client) error
	UploadAsset(ctx context.Context, who auth.Principal, owner, name string, releaseID uuid.UUID, in releases.UploadInput, c identity.Client) (releases.Asset, error)
	UpdateAsset(ctx context.Context, who auth.Principal, owner, name string, releaseID, assetID uuid.UUID, newName, label *string, c identity.Client) (releases.Asset, error)
	DeleteAsset(ctx context.Context, who auth.Principal, owner, name string, releaseID, assetID uuid.UUID, c identity.Client) error
	GetAsset(ctx context.Context, who auth.Principal, owner, name string, releaseID, assetID uuid.UUID) (releases.Asset, error)
	OpenAsset(ctx context.Context, who auth.Principal, owner, name string, releaseID, assetID uuid.UUID, rng *blob.Range) (io.ReadCloser, releases.Asset, error)
}

var errReleasesUnavailable = problemResponse{status: http.StatusServiceUnavailable, detail: "releases are not available"}

// releasesFail maps a release service error to a problem response.
func releasesFail(err error, method, suffix string) (releaseResponse, error) {
	var ve *releases.ValidationError
	switch {
	case errors.As(err, &ve):
		return badRequest(err.Error()), nil
	case errors.Is(err, releases.ErrNotFound):
		return problemResponse{status: http.StatusNotFound, detail: err.Error()}, nil
	case errors.Is(err, releases.ErrTagTaken), errors.Is(err, releases.ErrNameTaken), quota.IsExceeded(err):
		return problemResponse{status: http.StatusConflict, detail: err.Error()}, nil
	case errors.Is(err, releases.ErrNoStorage):
		return problemResponse{status: http.StatusServiceUnavailable, detail: err.Error()}, nil
	}
	r, err2 := reposFail(err, method, suffix)
	if err2 != nil {
		return nil, err2
	}
	return r.(releaseResponse), nil
}

// releaseResponse is answered by problemResponse and movedResponse.
type releaseResponse interface {
	v1gen.ListReleasesResponseObject
	v1gen.CreateReleaseResponseObject
	v1gen.GetLatestReleaseResponseObject
	v1gen.GetReleaseResponseObject
	v1gen.UpdateReleaseResponseObject
	v1gen.DeleteReleaseResponseObject
	v1gen.UpdateReleaseAssetResponseObject
	v1gen.DeleteReleaseAssetResponseObject
}

var (
	_ releaseResponse = problemResponse{}
	_ releaseResponse = movedResponse{}
)

func releasesSuffix(tail string) string { return "/releases" + tail }

func assetDTO(a releases.Asset) v1gen.ReleaseAsset {
	return v1gen.ReleaseAsset{Id: a.ID, Name: a.Name, Label: a.Label, ContentType: a.ContentType, Size: a.Size,
		Sha256: a.SHA256, DownloadCount: a.DownloadCount, CreatedAt: a.CreatedAt}
}

func releaseDTO(r releases.Release) v1gen.Release {
	assets := make([]v1gen.ReleaseAsset, 0, len(r.Assets))
	for _, a := range r.Assets {
		assets = append(assets, assetDTO(a))
	}
	return v1gen.Release{Id: r.ID, Tag: r.Tag, Name: r.Name, Body: r.Body, Draft: r.Draft, Prerelease: r.Prerelease,
		Latest: r.Latest, Target: r.Target, Commit: r.Commit, Author: r.Author, CreatedAt: r.CreatedAt,
		PublishedAt: r.PublishedAt, Assets: assets}
}

func (s *v1Server) ListReleases(ctx context.Context, req v1gen.ListReleasesRequestObject) (v1gen.ListReleasesResponseObject, error) {
	if s.releases == nil {
		return errReleasesUnavailable, nil
	}
	offset, limit := page(req.Params.Offset, req.Params.Limit)
	tag := ""
	if req.Params.Tag != nil {
		tag = *req.Params.Tag
	}
	list, err := s.releases.List(ctx, principal(ctx), req.Owner, req.Name, tag, offset, limit)
	if err != nil {
		return releasesFail(err, http.MethodGet, releasesSuffix(""))
	}
	out := make(v1gen.ListReleases200JSONResponse, 0, len(list))
	for _, r := range list {
		out = append(out, releaseDTO(r))
	}
	return out, nil
}

func (s *v1Server) CreateRelease(ctx context.Context, req v1gen.CreateReleaseRequestObject) (v1gen.CreateReleaseResponseObject, error) {
	if s.releases == nil {
		return errReleasesUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	b := req.Body
	in := releases.CreateInput{Tag: b.Tag, Name: deref(b.Name), Body: deref(b.Body), Draft: deref(b.Draft),
		Prerelease: deref(b.Prerelease), Target: deref(b.Target)}
	rel, err := s.releases.Create(ctx, principal(ctx), req.Owner, req.Name, in, clientOf(ctx))
	if err != nil {
		return releasesFail(err, http.MethodPost, releasesSuffix(""))
	}
	return v1gen.CreateRelease201JSONResponse(releaseDTO(rel)), nil
}

func (s *v1Server) GetLatestRelease(ctx context.Context, req v1gen.GetLatestReleaseRequestObject) (v1gen.GetLatestReleaseResponseObject, error) {
	if s.releases == nil {
		return errReleasesUnavailable, nil
	}
	rel, err := s.releases.Latest(ctx, principal(ctx), req.Owner, req.Name)
	if err != nil {
		return releasesFail(err, http.MethodGet, releasesSuffix("/latest"))
	}
	return v1gen.GetLatestRelease200JSONResponse(releaseDTO(rel)), nil
}

func (s *v1Server) GetRelease(ctx context.Context, req v1gen.GetReleaseRequestObject) (v1gen.GetReleaseResponseObject, error) {
	if s.releases == nil {
		return errReleasesUnavailable, nil
	}
	rel, err := s.releases.Get(ctx, principal(ctx), req.Owner, req.Name, req.ReleaseId)
	if err != nil {
		return releasesFail(err, http.MethodGet, releasesSuffix("/"+req.ReleaseId.String()))
	}
	return v1gen.GetRelease200JSONResponse(releaseDTO(rel)), nil
}

func (s *v1Server) UpdateRelease(ctx context.Context, req v1gen.UpdateReleaseRequestObject) (v1gen.UpdateReleaseResponseObject, error) {
	if s.releases == nil {
		return errReleasesUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	b := req.Body
	in := releases.UpdateInput{Tag: b.Tag, Name: b.Name, Body: b.Body, Draft: b.Draft, Prerelease: b.Prerelease, Target: b.Target}
	rel, err := s.releases.Update(ctx, principal(ctx), req.Owner, req.Name, req.ReleaseId, in, clientOf(ctx))
	if err != nil {
		return releasesFail(err, http.MethodPatch, releasesSuffix("/"+req.ReleaseId.String()))
	}
	return v1gen.UpdateRelease200JSONResponse(releaseDTO(rel)), nil
}

func (s *v1Server) DeleteRelease(ctx context.Context, req v1gen.DeleteReleaseRequestObject) (v1gen.DeleteReleaseResponseObject, error) {
	if s.releases == nil {
		return errReleasesUnavailable, nil
	}
	if err := s.releases.Delete(ctx, principal(ctx), req.Owner, req.Name, req.ReleaseId, clientOf(ctx)); err != nil {
		return releasesFail(err, http.MethodDelete, releasesSuffix("/"+req.ReleaseId.String()))
	}
	return v1gen.DeleteRelease204Response{}, nil
}

func (s *v1Server) UpdateReleaseAsset(ctx context.Context, req v1gen.UpdateReleaseAssetRequestObject) (v1gen.UpdateReleaseAssetResponseObject, error) {
	if s.releases == nil {
		return errReleasesUnavailable, nil
	}
	if req.Body == nil {
		return badRequest("missing body"), nil
	}
	a, err := s.releases.UpdateAsset(ctx, principal(ctx), req.Owner, req.Name, req.ReleaseId, req.AssetId, req.Body.Name, req.Body.Label, clientOf(ctx))
	if err != nil {
		return releasesFail(err, http.MethodPatch, releasesSuffix("/"+req.ReleaseId.String()+"/assets/"+req.AssetId.String()))
	}
	return v1gen.UpdateReleaseAsset200JSONResponse(assetDTO(a)), nil
}

func (s *v1Server) DeleteReleaseAsset(ctx context.Context, req v1gen.DeleteReleaseAssetRequestObject) (v1gen.DeleteReleaseAssetResponseObject, error) {
	if s.releases == nil {
		return errReleasesUnavailable, nil
	}
	if err := s.releases.DeleteAsset(ctx, principal(ctx), req.Owner, req.Name, req.ReleaseId, req.AssetId, clientOf(ctx)); err != nil {
		return releasesFail(err, http.MethodDelete, releasesSuffix("/"+req.ReleaseId.String()+"/assets/"+req.AssetId.String()))
	}
	return v1gen.DeleteReleaseAsset204Response{}, nil
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// Visit methods let problemResponse and movedResponse answer every release
// operation.
func (p problemResponse) VisitListReleasesResponse(w http.ResponseWriter) error  { return p.write(w) }
func (p problemResponse) VisitCreateReleaseResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitGetLatestReleaseResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitGetReleaseResponse(w http.ResponseWriter) error    { return p.write(w) }
func (p problemResponse) VisitUpdateReleaseResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitDeleteReleaseResponse(w http.ResponseWriter) error { return p.write(w) }
func (p problemResponse) VisitUpdateReleaseAssetResponse(w http.ResponseWriter) error {
	return p.write(w)
}
func (p problemResponse) VisitDeleteReleaseAssetResponse(w http.ResponseWriter) error {
	return p.write(w)
}

func (m movedResponse) VisitListReleasesResponse(w http.ResponseWriter) error     { return m.write(w) }
func (m movedResponse) VisitCreateReleaseResponse(w http.ResponseWriter) error    { return m.write(w) }
func (m movedResponse) VisitGetLatestReleaseResponse(w http.ResponseWriter) error { return m.write(w) }
func (m movedResponse) VisitGetReleaseResponse(w http.ResponseWriter) error       { return m.write(w) }
func (m movedResponse) VisitUpdateReleaseResponse(w http.ResponseWriter) error    { return m.write(w) }
func (m movedResponse) VisitDeleteReleaseResponse(w http.ResponseWriter) error    { return m.write(w) }
func (m movedResponse) VisitUpdateReleaseAssetResponse(w http.ResponseWriter) error {
	return m.write(w)
}
func (m movedResponse) VisitDeleteReleaseAssetResponse(w http.ResponseWriter) error {
	return m.write(w)
}

// ---- streaming asset routes (outside OpenAPI and the request timeout) ----

const (
	assetUploadPath   = "/api/v1/repos/{owner}/{name}/releases/{releaseId}/assets"
	assetDownloadPath = "/api/v1/repos/{owner}/{name}/releases/{releaseId}/assets/{assetId}/download"
)

// mountReleases mounts the asset upload (POST, raw body, Content-Length
// required, ?name= and optional ?label=) and download (GET, one byte range
// supported) routes. They stream, so they sit outside the request timeout.
func mountReleases(r chi.Router, d Deps) {
	r.With(d.Auth.LoadSession, d.Auth.CSRF).Post(assetUploadPath, func(w http.ResponseWriter, req *http.Request) {
		uploadAsset(w, req, d)
	})
	r.With(d.Auth.LoadSession).Get(assetDownloadPath, func(w http.ResponseWriter, req *http.Request) {
		downloadAsset(w, req, d)
	})
}

// streamPrincipal checks the token scope for a streaming route.
func streamPrincipal(w http.ResponseWriter, req *http.Request, scope string) (auth.Principal, bool) {
	p, _ := auth.PrincipalFrom(req.Context())
	if p.Method == auth.MethodToken && !p.HasScope(scope) {
		writeProblem(w, http.StatusForbidden, "the token lacks the "+scope+" scope")
		return p, false
	}
	return p, true
}

// streamFail answers a service error on a streaming route.
func streamFail(w http.ResponseWriter, req *http.Request, d Deps, err error) {
	r, ferr := releasesFail(err, req.Method, "")
	if ferr != nil {
		d.Log.ErrorContext(req.Context(), "release asset request failed", "error", ferr)
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

func uuidParam(req *http.Request, key string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(req, key))
	return id, err == nil
}

func uploadAsset(w http.ResponseWriter, req *http.Request, d Deps) {
	if d.Releases == nil {
		writeProblem(w, http.StatusServiceUnavailable, "releases are not available")
		return
	}
	p, ok := streamPrincipal(w, req, "repo:write")
	if !ok {
		return
	}
	if p.UserID == uuid.Nil {
		writeProblem(w, http.StatusUnauthorized, "authentication required")
		return
	}
	relID, ok := uuidParam(req, "releaseId")
	if !ok {
		writeProblem(w, http.StatusNotFound, "release not found")
		return
	}
	if req.ContentLength < 0 {
		writeProblem(w, http.StatusLengthRequired, "Content-Length is required")
		return
	}
	q := req.URL.Query()
	in := releases.UploadInput{Name: q.Get("name"), Label: q.Get("label"), ContentType: req.Header.Get("Content-Type"),
		Size: req.ContentLength, Body: req.Body}
	a, err := d.Releases.UploadAsset(req.Context(), p, chi.URLParam(req, "owner"), chi.URLParam(req, "name"), relID, in, clientOf(req.Context()))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeProblem(w, http.StatusRequestEntityTooLarge, "asset is too large")
			return
		}
		streamFail(w, req, d, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Location", "/api/v1/repos/"+url.PathEscape(chi.URLParam(req, "owner"))+"/"+url.PathEscape(chi.URLParam(req, "name"))+
		"/releases/"+relID.String()+"/assets/"+a.ID.String()+"/download")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(assetDTO(a))
}

// parseRange reads a single "bytes=" range for an object of size bytes.
// ok is false when the header is absent or not a single range (the whole
// object is served); unsat is true when the range cannot be satisfied.
func parseRange(h string, size int64) (rng blob.Range, ok, unsat bool) {
	spec, found := strings.CutPrefix(h, "bytes=")
	if !found || strings.Contains(spec, ",") {
		return blob.Range{}, false, false
	}
	a, b, found := strings.Cut(strings.TrimSpace(spec), "-")
	if !found {
		return blob.Range{}, false, false
	}
	switch a {
	case "":
		n, err := strconv.ParseInt(b, 10, 64)
		if err != nil || n <= 0 {
			return blob.Range{}, false, err == nil
		}
		if n > size {
			n = size
		}
		return blob.Range{Offset: size - n, Length: n}, size > 0, size == 0
	default:
		start, err := strconv.ParseInt(a, 10, 64)
		if err != nil || start < 0 {
			return blob.Range{}, false, false
		}
		if start >= size {
			return blob.Range{}, false, true
		}
		end := size - 1
		if b != "" {
			e, err := strconv.ParseInt(b, 10, 64)
			if err != nil || e < start {
				return blob.Range{}, false, false
			}
			end = min(e, size-1)
		}
		return blob.Range{Offset: start, Length: end - start + 1}, true, false
	}
}

func downloadAsset(w http.ResponseWriter, req *http.Request, d Deps) {
	if d.Releases == nil {
		writeProblem(w, http.StatusServiceUnavailable, "releases are not available")
		return
	}
	p, ok := streamPrincipal(w, req, "repo:read")
	if !ok {
		return
	}
	relID, ok1 := uuidParam(req, "releaseId")
	assetID, ok2 := uuidParam(req, "assetId")
	if !ok1 || !ok2 {
		writeProblem(w, http.StatusNotFound, "asset not found")
		return
	}
	owner, name := chi.URLParam(req, "owner"), chi.URLParam(req, "name")
	ctx := req.Context()
	meta, err := d.Releases.GetAsset(ctx, p, owner, name, relID, assetID)
	if err != nil {
		streamFail(w, req, d, err)
		return
	}
	var rng *blob.Range
	status := http.StatusOK
	if r, ok, unsat := parseRange(req.Header.Get("Range"), meta.Size); unsat {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", meta.Size))
		writeProblem(w, http.StatusRequestedRangeNotSatisfiable, "")
		return
	} else if ok {
		rng, status = &r, http.StatusPartialContent
	}
	rc, _, err := d.Releases.OpenAsset(ctx, p, owner, name, relID, assetID, rng)
	if err != nil {
		streamFail(w, req, d, err)
		return
	}
	defer func() { _ = rc.Close() }()
	h := w.Header()
	h.Set("Content-Type", meta.ContentType)
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": meta.Name}))
	h.Set("Accept-Ranges", "bytes")
	h.Set("ETag", `"`+meta.SHA256+`"`)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, max-age=0")
	length := meta.Size
	if rng != nil {
		length = rng.Length
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", rng.Offset, rng.Offset+rng.Length-1, meta.Size))
	}
	h.Set("Content-Length", strconv.FormatInt(length, 10))
	w.WriteHeader(status)
	if req.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, rc); err != nil {
		d.Log.WarnContext(ctx, "stream release asset", "asset", assetID, "error", err)
	}
}
