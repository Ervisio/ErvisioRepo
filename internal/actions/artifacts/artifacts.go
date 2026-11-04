// Package artifacts is the server side of actions/upload-artifact and
// download-artifact v4 (PLAN §6): the Twirp service
// github.actions.results.api.v1.ArtifactService and the signed blob URLs
// its uploads and downloads go through. Artifacts belong to a run, live
// in storage (policy "actions") and expire after their retention.
package artifacts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/actions/blobsvc"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/jobs"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// TwirpPrefix is where the Twirp service is served.
const TwirpPrefix = "/twirp/github.actions.results.api.v1.ArtifactService/"

const (
	blobKind = "artifact"
	// MaxSize bounds one artifact.
	MaxSize = 50 << 30
	// staleAfter drops uploads never finalized.
	staleAfter  = 24 * time.Hour
	uploadTTL   = 6 * time.Hour
	downloadTTL = time.Hour
	maxNameLen  = 256
	sweepEvery  = time.Hour
	sweepBatch  = 500
	defaultMIME = "application/zip"
)

// Options configure the artifact service.
type Options struct {
	Pool     *pgxpool.Pool
	Storage  *tier.Tier
	Verifier blobsvc.Verifier
	Signer   *blobsvc.Signer
	Log      *slog.Logger
	// Retention is the default and maximum lifetime of an artifact.
	Retention time.Duration
}

// Service serves the artifact API.
type Service struct {
	q         *dbgen.Queries
	class     *tier.Class
	auth      *blobsvc.Auth
	stage     *blobsvc.Stager
	signer    *blobsvc.Signer
	log       *slog.Logger
	retention time.Duration
}

// New builds the artifact service; it needs the "actions" storage policy.
func New(o Options) (*Service, error) {
	class, err := o.Storage.Class("actions")
	if err != nil {
		return nil, fmt.Errorf("actions artifacts: %w", err)
	}
	q := dbgen.New(o.Pool)
	log := o.Log.With("component", "actions.artifacts")
	return &Service{q: q, class: class, auth: &blobsvc.Auth{V: o.Verifier, Q: q, Log: log},
		stage: &blobsvc.Stager{Q: q, Class: class, Log: log}, signer: o.Signer, log: log,
		retention: o.Retention}, nil
}

// Handler serves the Twirp service and the signed blob URLs. Uploads are
// long: do not wrap it in a request timeout.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+TwirpPrefix+"CreateArtifact", s.auth.Twirp(s.create))
	mux.HandleFunc("POST "+TwirpPrefix+"FinalizeArtifact", s.auth.Twirp(s.finalize))
	mux.HandleFunc("POST "+TwirpPrefix+"ListArtifacts", s.auth.Twirp(s.list))
	mux.HandleFunc("POST "+TwirpPrefix+"GetSignedArtifactURL", s.auth.Twirp(s.signedURL))
	mux.HandleFunc("POST "+TwirpPrefix+"DeleteArtifact", s.auth.Twirp(s.delete))
	mux.HandleFunc("PUT "+blobsvc.BlobPrefix+blobKind+"/"+blobsvc.OpUpload+"/{id}", s.blobUpload)
	mux.HandleFunc("GET "+blobsvc.BlobPrefix+blobKind+"/"+blobsvc.OpDownload+"/{id}", s.blobDownload)
	return mux
}

func owner(id int64) string { return "artifact-" + strconv.FormatInt(id, 10) }

func storageKey(a dbgen.ActionsArtifact) string {
	return "artifacts/" + a.RepoID.String() + "/" + a.RunID.String() + "/" + strconv.FormatInt(a.ID, 10)
}

func validName(n string) bool {
	return n != "" && len(n) <= maxNameLen && !strings.ContainsAny(n, "/\\\x00\r\n\":<>|*?")
}

func mimeOf(a dbgen.ActionsArtifact) string {
	if a.MimeType == "" {
		return defaultMIME
	}
	return a.MimeType
}

// upload is the blob target of a.
func (s *Service) upload(a dbgen.ActionsArtifact) blobsvc.Upload {
	return blobsvc.Upload{Owner: owner(a.ID), Key: storageKey(a), Max: MaxSize,
		Done: func(ctx context.Context, obj tier.Object) error {
			_, err := s.q.SetActionsArtifactBlob(ctx, dbgen.SetActionsArtifactBlobParams{
				StorageKey: storageKey(a), Size: obj.Size, Digest: "sha256:" + obj.SHA256Hex(), ID: a.ID})
			if errors.Is(err, pgx.ErrNoRows) {
				return errors.New("artifact already finalized")
			}
			if err != nil {
				return fmt.Errorf("record artifact blob: %w", err)
			}
			return nil
		}}
}

// remove deletes an artifact with its blob and staged parts.
func (s *Service) remove(ctx context.Context, a dbgen.ActionsArtifact) error {
	if a.StorageKey != "" {
		if err := s.class.Delete(ctx, a.StorageKey); err != nil && !errors.Is(err, blob.ErrNotFound) {
			return fmt.Errorf("delete artifact blob: %w", err)
		}
	}
	if err := s.stage.Discard(ctx, owner(a.ID)); err != nil {
		return err
	}
	if err := s.q.DeleteActionsArtifact(ctx, a.ID); err != nil {
		return fmt.Errorf("delete artifact: %w", err)
	}
	return nil
}

// Sweep deletes expired artifacts and uploads never finalized.
func (s *Service) Sweep(ctx context.Context) error {
	for {
		list, err := s.q.ListExpiredActionsArtifacts(ctx, dbgen.ListExpiredActionsArtifactsParams{
			StaleBefore: blobsvc.Timestamp(time.Now().Add(-staleAfter)), Lim: sweepBatch})
		if err != nil {
			return fmt.Errorf("list expired artifacts: %w", err)
		}
		for _, a := range list {
			if err := s.remove(ctx, a); err != nil {
				return err
			}
		}
		if len(list) < sweepBatch {
			return nil
		}
	}
}

// SweepArgs runs Sweep.
type SweepArgs struct{}

func (SweepArgs) Kind() string { return "actions_artifact_sweep" }

type sweepWorker struct {
	river.WorkerDefaults[SweepArgs]
	s *Service
}

func (w *sweepWorker) Work(ctx context.Context, _ *river.Job[SweepArgs]) error { return w.s.Sweep(ctx) }

// JobsOption registers the periodic sweep.
func (s *Service) JobsOption() jobs.Option {
	return func(r *jobs.Registry) {
		river.AddWorker(r.Workers, &sweepWorker{s: s})
		r.Periodic = append(r.Periodic, river.NewPeriodicJob(river.PeriodicInterval(sweepEvery),
			func() (river.JobArgs, *river.InsertOpts) { return SweepArgs{}, nil }, nil))
	}
}

// byPath loads the artifact named by the {id} path value when r carries a
// valid signature for op.
func (s *Service) byPath(w http.ResponseWriter, r *http.Request, op string) (dbgen.ActionsArtifact, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 || !s.signer.Valid(r, blobKind, op, id) {
		blobsvc.Problem(w, http.StatusForbidden, "invalid or expired signature")
		return dbgen.ActionsArtifact{}, false
	}
	a, err := s.q.GetActionsArtifact(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		blobsvc.Problem(w, http.StatusNotFound, "")
		return a, false
	}
	if err != nil {
		s.log.ErrorContext(r.Context(), "load artifact", "error", err)
		blobsvc.Problem(w, http.StatusInternalServerError, "")
		return a, false
	}
	return a, true
}

func (s *Service) blobUpload(w http.ResponseWriter, r *http.Request) {
	a, ok := s.byPath(w, r, blobsvc.OpUpload)
	if !ok {
		return
	}
	if a.Finalized {
		blobsvc.Problem(w, http.StatusConflict, "artifact already finalized")
		return
	}
	s.stage.ServeUpload(w, r, s.upload(a))
}

func (s *Service) blobDownload(w http.ResponseWriter, r *http.Request) {
	a, ok := s.byPath(w, r, blobsvc.OpDownload)
	if !ok {
		return
	}
	if !a.Finalized || !a.ExpiresAt.Time.After(time.Now()) {
		blobsvc.Problem(w, http.StatusNotFound, "")
		return
	}
	name := a.Name
	if mimeOf(a) == defaultMIME {
		name += ".zip"
	}
	blobsvc.ServeBlob(w, r, s.class, s.log, a.StorageKey, mimeOf(a), name)
}

// sameRun reports whether the request's backend ids name the job's run
// and, when job is true, the job itself.
func sameRun(j blobsvc.Job, runID, jobID string, job bool) bool {
	r, err := uuid.Parse(runID)
	if err != nil || r != j.RunID {
		return false
	}
	if !job {
		return true
	}
	jid, err := uuid.Parse(jobID)
	return err == nil && jid == j.JobID
}
