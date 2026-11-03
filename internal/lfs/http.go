package lfs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/githttp"
	"github.com/Ervisio/ErvisioRepo/internal/quota"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

const (
	mediaType    = "application/vnd.git-lfs+json"
	maxJSONBody  = 1 << 20
	opDownload   = "download"
	opUpload     = "upload"
	hashSHA256   = "sha256"
	basicAdapter = "basic"
)

// ServeLFS implements githttp.LFS.
func (s *Server) ServeLFS(w http.ResponseWriter, r *http.Request, req githttp.LFSRequest) {
	p := req.Path
	switch {
	case p == "objects/batch" && r.Method == http.MethodPost:
		s.batch(w, r, req)
	case p == "verify" && r.Method == http.MethodPost:
		s.verifyHTTP(w, r, req)
	case strings.HasPrefix(p, "objects/") && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		s.download(w, r, req, strings.TrimPrefix(p, "objects/"))
	case strings.HasPrefix(p, "objects/") && r.Method == http.MethodPut:
		s.upload(w, r, req, strings.TrimPrefix(p, "objects/"))
	case p == "locks" && r.Method == http.MethodGet:
		s.listLocksHTTP(w, r, req)
	case p == "locks" && r.Method == http.MethodPost:
		s.createLockHTTP(w, r, req)
	case p == "locks/verify" && r.Method == http.MethodPost:
		s.verifyLocksHTTP(w, r, req)
	case strings.HasPrefix(p, "locks/") && strings.HasSuffix(p, "/unlock") && r.Method == http.MethodPost:
		s.unlockHTTP(w, r, req, strings.TrimSuffix(strings.TrimPrefix(p, "locks/"), "/unlock"))
	default:
		githttp.LFSError(w, http.StatusNotFound, "not found")
	}
}

// deny answers a caller that lacks permission: anonymous callers are asked
// for credentials.
func deny(w http.ResponseWriter, req githttp.LFSRequest) {
	if req.Caller.UserID == uuid.Nil {
		githttp.LFSUnauthorized(w, "authentication required")
		return
	}
	githttp.LFSError(w, http.StatusForbidden, "you do not have permission for this operation")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody))
	if err := dec.Decode(v); err != nil {
		githttp.LFSError(w, http.StatusUnprocessableEntity, "invalid request body")
		return false
	}
	return true
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error, what string) {
	st := status(err)
	if st == http.StatusInternalServerError {
		s.o.Log.ErrorContext(r.Context(), "git lfs: "+what, "error", err)
	}
	githttp.LFSError(w, st, message(err))
}

type batchRef struct {
	Name string `json:"name"`
}

type batchObject struct {
	OID  string `json:"oid"`
	Size int64  `json:"size"`
}

type batchRequest struct {
	Operation string        `json:"operation"`
	Transfers []string      `json:"transfers"`
	Ref       *batchRef     `json:"ref"`
	Objects   []batchObject `json:"objects"`
	HashAlgo  string        `json:"hash_algo"`
}

type action struct {
	Href      string            `json:"href"`
	Header    map[string]string `json:"header,omitempty"`
	ExpiresIn int64             `json:"expires_in,omitempty"`
}

type objectError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type batchResult struct {
	OID           string             `json:"oid"`
	Size          int64              `json:"size"`
	Authenticated bool               `json:"authenticated,omitempty"`
	Actions       map[string]*action `json:"actions,omitempty"`
	Error         *objectError       `json:"error,omitempty"`
}

type batchResponse struct {
	Transfer string        `json:"transfer"`
	Objects  []batchResult `json:"objects"`
	HashAlgo string        `json:"hash_algo"`
}

// authHeader returns the header that authenticates the server hrefs of a
// batch response and its lifetime (0 when unknown): the caller's LFS token
// when it used one, otherwise a fresh short-lived token. Anonymous callers
// get none.
func (s *Server) authHeader(r *http.Request, req githttp.LFSRequest, upload bool) (map[string]string, int64, error) {
	if req.Token != nil {
		return map[string]string{"Authorization": r.Header.Get("Authorization")}, 0, nil
	}
	if req.Caller.UserID == uuid.Nil {
		return nil, 0, nil
	}
	tok, exp, err := s.issueToken(r.Context(), req.Repo.ID, req.Caller.UserID, upload)
	if err != nil {
		return nil, 0, err
	}
	return map[string]string{"Authorization": githttp.LFSAuthScheme + " " + tok}, int64(time.Until(exp) / time.Second), nil
}

func (s *Server) batch(w http.ResponseWriter, r *http.Request, req githttp.LFSRequest) {
	ctx := r.Context()
	var in batchRequest
	if !readJSON(w, r, &in) {
		return
	}
	if in.HashAlgo != "" && in.HashAlgo != hashSHA256 {
		githttp.LFSError(w, http.StatusConflict, "unsupported hash algorithm "+strconv.Quote(in.HashAlgo))
		return
	}
	upload := false
	switch in.Operation {
	case opDownload:
		if !canRead(req.Repo, req.Caller) {
			deny(w, req)
			return
		}
	case opUpload:
		upload = true
		if !canWrite(req.Repo, req.Caller) {
			deny(w, req)
			return
		}
	default:
		githttp.LFSError(w, http.StatusUnprocessableEntity, "unknown operation "+strconv.Quote(in.Operation))
		return
	}
	if len(in.Objects) > maxBatchObjects {
		githttp.LFSError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("at most %d objects per batch", maxBatchObjects))
		return
	}

	oids := make([]string, 0, len(in.Objects))
	for _, o := range in.Objects {
		if validOID(o.OID) {
			oids = append(oids, o.OID)
		}
	}
	have, err := s.has(ctx, req.Repo.ID, oids)
	if err != nil {
		s.fail(w, r, err, "batch")
		return
	}
	if upload {
		var missing int64
		seen := map[string]bool{}
		for _, o := range in.Objects {
			if _, ok := have[o.OID]; !ok && validOID(o.OID) && o.Size > 0 && !seen[o.OID] {
				seen[o.OID] = true
				missing += o.Size
			}
		}
		if missing > 0 {
			if err := quota.Check(ctx, s.q, req.Repo.OwnerID, quota.Delta{Bytes: missing}); err != nil {
				s.fail(w, r, err, "batch quota")
				return
			}
		}
	}

	hdr, hdrTTL, err := s.authHeader(r, req, upload)
	if err != nil {
		s.fail(w, r, err, "batch token")
		return
	}
	presign := s.o.Storage.CanPresign()
	out := batchResponse{Transfer: basicAdapter, HashAlgo: hashSHA256, Objects: make([]batchResult, 0, len(in.Objects))}
	for _, o := range in.Objects {
		res := batchResult{OID: o.OID, Size: o.Size, Authenticated: hdr != nil}
		if !validOID(o.OID) || o.Size < 0 {
			res.Error = &objectError{Code: http.StatusUnprocessableEntity, Message: "invalid object id or size"}
			out.Objects = append(out.Objects, res)
			continue
		}
		row, ok := have[o.OID]
		if ok && row.Size != o.Size {
			res.Error = &objectError{Code: http.StatusUnprocessableEntity, Message: fmt.Sprintf("object is recorded with %d bytes", row.Size)}
			out.Objects = append(out.Objects, res)
			continue
		}
		server := &action{Href: s.href(req.Repo, "objects/"+o.OID), Header: hdr, ExpiresIn: hdrTTL}
		switch {
		case !upload && !ok:
			res.Error = &objectError{Code: http.StatusNotFound, Message: "object does not exist"}
		case !upload:
			a := server
			key := objectKey(o.OID)
			if presign && !s.o.Storage.Cached(key) {
				if u, err := s.o.Storage.PresignGet(ctx, key, PresignTTL); err == nil {
					a = &action{Href: u, ExpiresIn: int64(PresignTTL / time.Second)}
				} else {
					s.o.Log.WarnContext(ctx, "git lfs: presign download", "error", err)
				}
			}
			res.Actions = map[string]*action{"download": a}
		case ok:
			// Already referenced by the repository: nothing to upload.
		default:
			res.Actions = map[string]*action{"upload": s.uploadAction(r, req, o, server, presign)}
			if res.Actions["upload"] != server {
				res.Actions["verify"] = &action{Href: s.href(req.Repo, "verify"), Header: hdr, ExpiresIn: hdrTTL}
			}
		}
		out.Objects = append(out.Objects, res)
	}
	writeJSON(w, http.StatusOK, out)
}

// uploadAction returns a presigned upload for objects not stored yet when
// the backend supports it, otherwise the server action.
func (s *Server) uploadAction(r *http.Request, req githttp.LFSRequest, o batchObject, server *action, presign bool) *action {
	if !presign {
		return server
	}
	ctx := r.Context()
	key := objectKey(o.OID)
	if _, err := s.o.Storage.Stat(ctx, key); !errors.Is(err, blob.ErrNotFound) {
		// Stored already (possession must be proven through the server) or
		// unknown state.
		return server
	}
	u, h, err := s.o.Storage.PresignPut(ctx, key, o.Size, o.OID, PresignTTL)
	if err != nil {
		s.o.Log.WarnContext(ctx, "git lfs: presign upload", "repo", req.Repo.ID, "error", err)
		return server
	}
	hdr := make(map[string]string, len(h))
	for k := range h {
		hdr[k] = h.Get(k)
	}
	return &action{Href: u, Header: hdr, ExpiresIn: int64(PresignTTL / time.Second)}
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request, req githttp.LFSRequest, oid string) {
	if !validOID(oid) {
		githttp.LFSError(w, http.StatusUnprocessableEntity, "invalid object id")
		return
	}
	if !canWrite(req.Repo, req.Caller) {
		deny(w, req)
		return
	}
	if r.ContentLength < 0 {
		githttp.LFSError(w, http.StatusLengthRequired, "Content-Length is required")
		return
	}
	if err := s.receive(r.Context(), req.Repo, oid, r.ContentLength, r.Body); err != nil {
		s.fail(w, r, err, "upload")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) download(w http.ResponseWriter, r *http.Request, req githttp.LFSRequest, oid string) {
	if !validOID(oid) {
		githttp.LFSError(w, http.StatusUnprocessableEntity, "invalid object id")
		return
	}
	if !canRead(req.Repo, req.Caller) {
		deny(w, req)
		return
	}
	ctx := r.Context()
	row, err := s.lookup(ctx, req.Repo, oid)
	if err != nil {
		s.fail(w, r, err, "download")
		return
	}
	var rng *blob.Range
	if h := r.Header.Get("Range"); h != "" {
		var ok bool
		if rng, ok = parseRange(h, row.Size); !ok {
			w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(row.Size, 10))
			githttp.LFSError(w, http.StatusRequestedRangeNotSatisfiable, "invalid range")
			return
		}
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Accept-Ranges", "bytes")
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.FormatInt(row.Size, 10))
		w.WriteHeader(http.StatusOK)
		return
	}
	rc, size, err := s.open(ctx, req.Repo, oid, rng)
	if err != nil {
		s.fail(w, r, err, "download")
		return
	}
	defer func() { _ = rc.Close() }()
	if rng != nil {
		w.Header().Set("Content-Length", strconv.FormatInt(rng.Length, 10))
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", rng.Offset, rng.Offset+rng.Length-1, size))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		w.WriteHeader(http.StatusOK)
	}
	if _, err := io.Copy(w, rc); err != nil && ctx.Err() == nil {
		s.o.Log.WarnContext(ctx, "git lfs: download interrupted", "repo", req.Repo.ID, "oid", oid, "error", err)
	}
}

// parseRange parses a single "bytes=N-[M]" range against size. The result
// always has a non-negative Length.
func parseRange(h string, size int64) (*blob.Range, bool) {
	spec, ok := strings.CutPrefix(h, "bytes=")
	if !ok || strings.Contains(spec, ",") {
		return nil, false
	}
	a, b, ok := strings.Cut(spec, "-")
	if !ok || a == "" {
		return nil, false
	}
	start, err := strconv.ParseInt(a, 10, 64)
	if err != nil || start < 0 || start >= size {
		return nil, false
	}
	end := size - 1
	if b != "" {
		e, err := strconv.ParseInt(b, 10, 64)
		if err != nil || e < start {
			return nil, false
		}
		end = min(e, size-1)
	}
	return &blob.Range{Offset: start, Length: end - start + 1}, true
}

func (s *Server) verifyHTTP(w http.ResponseWriter, r *http.Request, req githttp.LFSRequest) {
	var in batchObject
	if !readJSON(w, r, &in) {
		return
	}
	if !validOID(in.OID) || in.Size < 0 {
		githttp.LFSError(w, http.StatusUnprocessableEntity, "invalid object id or size")
		return
	}
	if !canWrite(req.Repo, req.Caller) {
		deny(w, req)
		return
	}
	if err := s.verify(r.Context(), req.Repo, in.OID, in.Size); err != nil {
		s.fail(w, r, err, "verify")
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}
