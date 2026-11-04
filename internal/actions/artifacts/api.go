package artifacts

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/actions/blobsvc"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

// ids are the backend ids every request carries.
type ids struct {
	RunID string `json:"workflow_run_backend_id"`
	JobID string `json:"workflow_job_run_backend_id"`
}

type createReq struct {
	ids
	Name      string  `json:"name"`
	ExpiresAt *string `json:"expires_at"`
	Version   int     `json:"version"`
	MimeType  *string `json:"mime_type"`
}

type createResp struct {
	Ok              bool   `json:"ok"`
	SignedUploadURL string `json:"signed_upload_url"`
}

func (s *Service) create(w http.ResponseWriter, r *http.Request, j blobsvc.Job) {
	var req createReq
	if !blobsvc.Decode(w, r, &req) {
		return
	}
	if !sameRun(j, req.RunID, req.JobID, true) {
		blobsvc.TwirpError(w, "permission_denied", "backend ids do not match the runtime token")
		return
	}
	if !validName(req.Name) {
		blobsvc.TwirpError(w, "invalid_argument", "invalid artifact name")
		return
	}
	expires := time.Now().Add(s.retention)
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339Nano, *req.ExpiresAt)
		if err != nil {
			blobsvc.TwirpError(w, "invalid_argument", "invalid expires_at")
			return
		}
		if t.Before(expires) {
			expires = t
		}
	}
	mt := ""
	if req.MimeType != nil && len(*req.MimeType) <= 255 {
		mt = *req.MimeType
	}
	a, err := s.q.CreateActionsArtifact(r.Context(), dbgen.CreateActionsArtifactParams{
		RepoID: j.RepoID, RunID: j.RunID, JobID: uuid.NullUUID{UUID: j.JobID, Valid: true},
		Name: req.Name, MimeType: mt, ExpiresAt: blobsvc.Timestamp(expires)})
	if errors.Is(err, pgx.ErrNoRows) {
		blobsvc.TwirpError(w, "already_exists", "an artifact with this name already exists in the run")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if err := s.stage.Discard(r.Context(), owner(a.ID)); err != nil {
		s.internal(w, r, err)
		return
	}
	blobsvc.Reply(w, createResp{Ok: true, SignedUploadURL: s.signer.URL(blobKind, blobsvc.OpUpload, a.ID, uploadTTL)})
}

type finalizeReq struct {
	ids
	Name string        `json:"name"`
	Size blobsvc.Int64 `json:"size"`
	Hash *string       `json:"hash"`
}

type finalizeResp struct {
	Ok         bool          `json:"ok"`
	ArtifactID blobsvc.Int64 `json:"artifact_id"`
}

func (s *Service) finalize(w http.ResponseWriter, r *http.Request, j blobsvc.Job) {
	ctx := r.Context()
	var req finalizeReq
	if !blobsvc.Decode(w, r, &req) {
		return
	}
	if !sameRun(j, req.RunID, req.JobID, true) {
		blobsvc.TwirpError(w, "permission_denied", "backend ids do not match the runtime token")
		return
	}
	a, ok := s.byName(w, r, j, req.Name)
	if !ok {
		return
	}
	if a.Finalized {
		blobsvc.TwirpError(w, "already_exists", "artifact already finalized")
		return
	}
	if a.StorageKey == "" {
		// Append blob uploads are committed here.
		if _, err := s.stage.CommitAppended(ctx, s.upload(a)); errors.Is(err, blob.ErrNotFound) {
			blobsvc.TwirpError(w, "failed_precondition", "nothing was uploaded")
			return
		} else if err != nil {
			s.internal(w, r, err)
			return
		}
		if a, ok = s.byName(w, r, j, req.Name); !ok {
			return
		}
	}
	if a.Size != int64(req.Size) {
		blobsvc.TwirpError(w, "invalid_argument", "size does not match the upload")
		return
	}
	if req.Hash != nil && *req.Hash != "" && !strings.EqualFold(*req.Hash, a.Digest) {
		blobsvc.TwirpError(w, "invalid_argument", "hash does not match the upload")
		return
	}
	if _, err := s.q.FinalizeActionsArtifact(ctx, a.ID); errors.Is(err, pgx.ErrNoRows) {
		blobsvc.TwirpError(w, "failed_precondition", "artifact cannot be finalized")
		return
	} else if err != nil {
		s.internal(w, r, err)
		return
	}
	blobsvc.Reply(w, finalizeResp{Ok: true, ArtifactID: blobsvc.Int64(a.ID)})
}

type listReq struct {
	ids
	NameFilter *string        `json:"name_filter"`
	IDFilter   *blobsvc.Int64 `json:"id_filter"`
}

type listEntry struct {
	RunID      string        `json:"workflow_run_backend_id"`
	JobID      string        `json:"workflow_job_run_backend_id"`
	DatabaseID blobsvc.Int64 `json:"database_id"`
	Name       string        `json:"name"`
	Size       blobsvc.Int64 `json:"size"`
	CreatedAt  string        `json:"created_at,omitempty"`
	Digest     string        `json:"digest,omitempty"`
}

func (s *Service) list(w http.ResponseWriter, r *http.Request, j blobsvc.Job) {
	var req listReq
	if !blobsvc.Decode(w, r, &req) {
		return
	}
	if !sameRun(j, req.RunID, req.JobID, false) {
		blobsvc.TwirpError(w, "permission_denied", "backend ids do not match the runtime token")
		return
	}
	p := dbgen.ListActionsArtifactsParams{RunID: j.RunID}
	if req.NameFilter != nil {
		p.Name = pgtype.Text{String: *req.NameFilter, Valid: true}
	}
	if req.IDFilter != nil {
		p.ID = pgtype.Int8{Int64: int64(*req.IDFilter), Valid: true}
	}
	list, err := s.q.ListActionsArtifacts(r.Context(), p)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]listEntry, 0, len(list))
	for _, a := range list {
		e := listEntry{RunID: a.RunID.String(), DatabaseID: blobsvc.Int64(a.ID), Name: a.Name,
			Size: blobsvc.Int64(a.Size), CreatedAt: blobsvc.RFC3339(a.CreatedAt), Digest: a.Digest}
		if a.JobID.Valid {
			e.JobID = a.JobID.UUID.String()
		}
		out = append(out, e)
	}
	blobsvc.Reply(w, map[string]any{"artifacts": out})
}

type nameReq struct {
	ids
	Name string `json:"name"`
}

func (s *Service) signedURL(w http.ResponseWriter, r *http.Request, j blobsvc.Job) {
	var req nameReq
	if !blobsvc.Decode(w, r, &req) {
		return
	}
	if !sameRun(j, req.RunID, req.JobID, false) {
		blobsvc.TwirpError(w, "permission_denied", "backend ids do not match the runtime token")
		return
	}
	a, ok := s.byName(w, r, j, req.Name)
	if !ok {
		return
	}
	if !a.Finalized || !a.ExpiresAt.Time.After(time.Now()) {
		blobsvc.TwirpError(w, "not_found", "artifact not found")
		return
	}
	blobsvc.Reply(w, map[string]string{"signed_url": s.signer.URL(blobKind, blobsvc.OpDownload, a.ID, downloadTTL)})
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request, j blobsvc.Job) {
	var req nameReq
	if !blobsvc.Decode(w, r, &req) {
		return
	}
	if !sameRun(j, req.RunID, req.JobID, false) {
		blobsvc.TwirpError(w, "permission_denied", "backend ids do not match the runtime token")
		return
	}
	a, ok := s.byName(w, r, j, req.Name)
	if !ok {
		return
	}
	if err := s.remove(r.Context(), a); err != nil {
		s.internal(w, r, err)
		return
	}
	blobsvc.Reply(w, finalizeResp{Ok: true, ArtifactID: blobsvc.Int64(a.ID)})
}

// byName loads the artifact name of the job's run.
func (s *Service) byName(w http.ResponseWriter, r *http.Request, j blobsvc.Job, name string) (dbgen.ActionsArtifact, bool) {
	a, err := s.q.GetActionsArtifactByName(r.Context(), dbgen.GetActionsArtifactByNameParams{RunID: j.RunID, Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		blobsvc.TwirpError(w, "not_found", "artifact not found")
		return a, false
	}
	if err != nil {
		s.internal(w, r, err)
		return a, false
	}
	return a, true
}

func (s *Service) internal(w http.ResponseWriter, r *http.Request, err error) {
	s.log.ErrorContext(r.Context(), "actions artifacts", "error", err)
	blobsvc.TwirpError(w, "internal", "internal error")
}
