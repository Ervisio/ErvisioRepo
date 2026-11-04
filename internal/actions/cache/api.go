package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/actions/blobsvc"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// ---- v2 (Twirp) ----

type createEntryReq struct {
	Key     string `json:"key"`
	Version string `json:"version"`
}

type createEntryResp struct {
	Ok              bool   `json:"ok"`
	SignedUploadURL string `json:"signed_upload_url"`
	Message         string `json:"message,omitempty"`
}

func (s *Service) createEntry(w http.ResponseWriter, r *http.Request, j blobsvc.Job) {
	var req createEntryReq
	if !blobsvc.Decode(w, r, &req) {
		return
	}
	if !validKey(req.Key) || !validVersion(req.Version) {
		blobsvc.TwirpError(w, "invalid_argument", "invalid key or version")
		return
	}
	e, ok, err := s.reserve(r.Context(), j, req.Key, req.Version)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if !ok {
		blobsvc.Reply(w, createEntryResp{Message: "cache entry already exists"})
		return
	}
	blobsvc.Reply(w, createEntryResp{Ok: true, SignedUploadURL: s.signer.URL(blobKind, blobsvc.OpUpload, e.ID, urlTTL)})
}

type finalizeEntryReq struct {
	Key       string        `json:"key"`
	SizeBytes blobsvc.Int64 `json:"size_bytes"`
	Version   string        `json:"version"`
}

type finalizeEntryResp struct {
	Ok      bool          `json:"ok"`
	EntryID blobsvc.Int64 `json:"entry_id"`
	Message string        `json:"message,omitempty"`
}

func (s *Service) finalizeEntry(w http.ResponseWriter, r *http.Request, j blobsvc.Job) {
	var req finalizeEntryReq
	if !blobsvc.Decode(w, r, &req) {
		return
	}
	e, err := s.q.GetActionsCacheByKey(r.Context(), dbgen.GetActionsCacheByKeyParams{
		RepoID: j.RepoID, Scope: writeScope(j), Key: req.Key, Version: req.Version})
	if errors.Is(err, pgx.ErrNoRows) {
		blobsvc.TwirpError(w, "not_found", "cache entry not reserved")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if !writable(j, e) {
		blobsvc.Reply(w, finalizeEntryResp{Message: "cache entry not writable by this job"})
		return
	}
	ok, err := s.complete(r.Context(), e, int64(req.SizeBytes))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if !ok {
		blobsvc.Reply(w, finalizeEntryResp{Message: "uploaded size does not match"})
		return
	}
	blobsvc.Reply(w, finalizeEntryResp{Ok: true, EntryID: blobsvc.Int64(e.ID)})
}

type downloadURLReq struct {
	Key         string   `json:"key"`
	RestoreKeys []string `json:"restore_keys"`
	Version     string   `json:"version"`
}

type downloadURLResp struct {
	Ok                bool   `json:"ok"`
	SignedDownloadURL string `json:"signed_download_url"`
	MatchedKey        string `json:"matched_key"`
}

func (s *Service) downloadURL(w http.ResponseWriter, r *http.Request, j blobsvc.Job) {
	var req downloadURLReq
	if !blobsvc.Decode(w, r, &req) {
		return
	}
	keys := append([]string{req.Key}, req.RestoreKeys...)
	if !validKeys(keys) || !validVersion(req.Version) {
		blobsvc.TwirpError(w, "invalid_argument", "invalid keys or version")
		return
	}
	e, ok, err := s.find(r.Context(), j, keys, req.Version)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if !ok {
		blobsvc.Reply(w, downloadURLResp{})
		return
	}
	blobsvc.Reply(w, downloadURLResp{Ok: true, MatchedKey: e.Key,
		SignedDownloadURL: s.signer.URL(blobKind, blobsvc.OpDownload, e.ID, urlTTL)})
}

func validKeys(keys []string) bool {
	if len(keys) == 0 || len(keys) > maxKeys {
		return false
	}
	for _, k := range keys {
		if !validKey(k) {
			return false
		}
	}
	return true
}

func (s *Service) internal(w http.ResponseWriter, r *http.Request, err error) {
	s.log.ErrorContext(r.Context(), "actions cache", "error", err)
	blobsvc.TwirpError(w, "internal", "internal error")
}

// ---- v1 (REST under ACTIONS_CACHE_URL) ----

func (s *Service) v1Error(w http.ResponseWriter, r *http.Request, err error) {
	s.log.ErrorContext(r.Context(), "actions cache v1", "error", err)
	blobsvc.Problem(w, http.StatusInternalServerError, "")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

// v1Get is GET cache?keys=k1,k2&version=v: 200 with the entry, 204 on a
// miss.
func (s *Service) v1Get(w http.ResponseWriter, r *http.Request, j blobsvc.Job) {
	keys := strings.Split(r.URL.Query().Get("keys"), ",")
	version := r.URL.Query().Get("version")
	if !validKeys(keys) || !validVersion(version) {
		blobsvc.Problem(w, http.StatusBadRequest, "invalid keys or version")
		return
	}
	e, ok, err := s.find(r.Context(), j, keys, version)
	if err != nil {
		s.v1Error(w, r, err)
		return
	}
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cacheKey": e.Key, "scope": e.Scope, "cacheVersion": e.Version,
		"creationTime":    blobsvc.RFC3339(e.CreatedAt),
		"archiveLocation": s.signer.URL(blobKind, blobsvc.OpDownload, e.ID, urlTTL)})
}

// v1List is the diagnostic GET caches?key=k; listing is not supported and
// it always answers with an empty list.
func (s *Service) v1List(w http.ResponseWriter, r *http.Request, _ blobsvc.Job) {
	writeJSON(w, http.StatusOK, map[string]any{"totalCount": 0, "artifactCaches": []any{}})
}

type v1ReserveReq struct {
	Key       string `json:"key"`
	Version   string `json:"version"`
	CacheSize *int64 `json:"cacheSize"`
}

func (s *Service) v1Reserve(w http.ResponseWriter, r *http.Request, j blobsvc.Job) {
	var req v1ReserveReq
	if err := readJSON(r, &req); err != nil || !validKey(req.Key) || !validVersion(req.Version) {
		blobsvc.Problem(w, http.StatusBadRequest, "invalid key or version")
		return
	}
	if req.CacheSize != nil && *req.CacheSize > s.max {
		blobsvc.Problem(w, http.StatusBadRequest, "cache size exceeds the repository limit")
		return
	}
	e, ok, err := s.reserve(r.Context(), j, req.Key, req.Version)
	if err != nil {
		s.v1Error(w, r, err)
		return
	}
	if !ok {
		blobsvc.Problem(w, http.StatusConflict, "cache entry already exists")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"cacheId": e.ID})
}

// v1Entry loads the {id} entry the job is uploading.
func (s *Service) v1Entry(w http.ResponseWriter, r *http.Request, j blobsvc.Job) (dbgen.ActionsCache, bool) {
	e, _, err := s.entryByPath(r.Context(), r)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !writable(j, e) {
		blobsvc.Problem(w, http.StatusNotFound, "cache entry not reserved by this job")
		return e, false
	}
	if err != nil {
		s.v1Error(w, r, err)
		return e, false
	}
	return e, true
}

// parseContentRange parses "bytes start-end/*".
func parseContentRange(v string) (start, size int64, err error) {
	rest, ok := strings.CutPrefix(v, "bytes ")
	if !ok {
		return 0, 0, fmt.Errorf("content range %q", v)
	}
	rng, _, _ := strings.Cut(rest, "/")
	a, b, ok := strings.Cut(rng, "-")
	if !ok {
		return 0, 0, fmt.Errorf("content range %q", v)
	}
	start, err1 := strconv.ParseInt(a, 10, 64)
	end, err2 := strconv.ParseInt(b, 10, 64)
	if err1 != nil || err2 != nil || start < 0 || end < start {
		return 0, 0, fmt.Errorf("content range %q", v)
	}
	return start, end - start + 1, nil
}

// v1Upload is PATCH caches/{id}: one chunk at the offset of its
// Content-Range.
func (s *Service) v1Upload(w http.ResponseWriter, r *http.Request, j blobsvc.Job) {
	e, ok := s.v1Entry(w, r, j)
	if !ok {
		return
	}
	start, size, err := parseContentRange(r.Header.Get("Content-Range"))
	if err != nil || start+size > s.max {
		blobsvc.Problem(w, http.StatusBadRequest, "invalid Content-Range")
		return
	}
	block := fmt.Sprintf("%020d", start)
	if err := s.stage.Put(r.Context(), owner(e.ID), block, io.LimitReader(r.Body, size), size); err != nil {
		s.v1Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// v1Commit is POST caches/{id} {"size": n}: the chunks, contiguous from
// offset 0, become the entry.
func (s *Service) v1Commit(w http.ResponseWriter, r *http.Request, j blobsvc.Job) {
	e, ok := s.v1Entry(w, r, j)
	if !ok {
		return
	}
	var req struct {
		Size int64 `json:"size"`
	}
	if err := readJSON(r, &req); err != nil || req.Size < 0 {
		blobsvc.Problem(w, http.StatusBadRequest, "invalid size")
		return
	}
	parts, err := s.stage.Parts(r.Context(), owner(e.ID))
	if err != nil {
		s.v1Error(w, r, err)
		return
	}
	// Block names are zero-padded offsets: sort them and check that they
	// tile [0, size).
	slices.SortFunc(parts, func(a, b dbgen.ActionsUploadPart) int { return strings.Compare(a.Block, b.Block) })
	var off int64
	for _, p := range parts {
		if p.Block != fmt.Sprintf("%020d", off) {
			blobsvc.Problem(w, http.StatusBadRequest, "uploaded chunks are not contiguous")
			return
		}
		off += p.Size
	}
	if off != req.Size {
		blobsvc.Problem(w, http.StatusBadRequest, "uploaded size does not match")
		return
	}
	if _, err := s.stage.Commit(r.Context(), owner(e.ID), parts, storageKey(e), s.max); err != nil {
		s.v1Error(w, r, err)
		return
	}
	done, err := s.complete(r.Context(), e, req.Size)
	if err != nil {
		s.v1Error(w, r, err)
		return
	}
	if !done {
		blobsvc.Problem(w, http.StatusConflict, "cache entry could not be committed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
