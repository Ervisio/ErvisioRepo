// Package cache is the server side of actions/cache (PLAN §6): the cache
// service v2 (Twirp, github.actions.results.api.v1.CacheService) and the
// v1 REST API under ACTIONS_CACHE_URL, which the toolkit falls back to on
// hosts other than github.com. Entries are scoped by repository and git
// ref as on GitHub; blobs live in storage (policy "actions").
package cache

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

// Paths served by Handler.
const (
	TwirpPrefix = "/twirp/github.actions.results.api.v1.CacheService/"
	V1Prefix    = "/api/actions/cache/_apis/artifactcache/"
	blobKind    = "cache"
)

const (
	// staleAfter frees reservations and parts of abandoned uploads.
	staleAfter = 24 * time.Hour
	// urlTTL is the lifetime of signed upload and download URLs.
	urlTTL       = 6 * time.Hour
	maxKeyLen    = 512
	maxKeys      = 10
	sweepEvery   = time.Hour
	sweepBatch   = 500
	evictBatch   = 200
	maxVersionLn = 512
)

// Options configure the cache service.
type Options struct {
	Pool        *pgxpool.Pool
	Storage     *tier.Tier
	Verifier    blobsvc.Verifier
	Signer      *blobsvc.Signer
	Log         *slog.Logger
	MaxRepoSize int64
	TTL         time.Duration
}

// Service serves the cache APIs.
type Service struct {
	q      *dbgen.Queries
	class  *tier.Class
	auth   *blobsvc.Auth
	stage  *blobsvc.Stager
	signer *blobsvc.Signer
	log    *slog.Logger
	max    int64
	ttl    time.Duration
}

// New builds the cache service; it needs the "actions" storage policy.
func New(o Options) (*Service, error) {
	class, err := o.Storage.Class("actions")
	if err != nil {
		return nil, fmt.Errorf("actions cache: %w", err)
	}
	q := dbgen.New(o.Pool)
	log := o.Log.With("component", "actions.cache")
	return &Service{q: q, class: class, auth: &blobsvc.Auth{V: o.Verifier, Q: q, Log: log},
		stage: &blobsvc.Stager{Q: q, Class: class, Log: log}, signer: o.Signer, log: log,
		max: o.MaxRepoSize, ttl: o.TTL}, nil
}

// Handler serves the Twirp service, the v1 REST API and the signed blob
// URLs. Uploads are long: do not wrap it in a request timeout.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+TwirpPrefix+"CreateCacheEntry", s.auth.Twirp(s.createEntry))
	mux.HandleFunc("POST "+TwirpPrefix+"FinalizeCacheEntryUpload", s.auth.Twirp(s.finalizeEntry))
	mux.HandleFunc("POST "+TwirpPrefix+"GetCacheEntryDownloadURL", s.auth.Twirp(s.downloadURL))
	mux.HandleFunc("GET "+V1Prefix+"cache", s.auth.REST(s.v1Get))
	mux.HandleFunc("GET "+V1Prefix+"caches", s.auth.REST(s.v1List))
	mux.HandleFunc("POST "+V1Prefix+"caches", s.auth.REST(s.v1Reserve))
	mux.HandleFunc("PATCH "+V1Prefix+"caches/{id}", s.auth.REST(s.v1Upload))
	mux.HandleFunc("POST "+V1Prefix+"caches/{id}", s.auth.REST(s.v1Commit))
	mux.HandleFunc("PUT "+blobsvc.BlobPrefix+blobKind+"/"+blobsvc.OpUpload+"/{id}", s.blobUpload)
	mux.HandleFunc("GET "+blobsvc.BlobPrefix+blobKind+"/"+blobsvc.OpDownload+"/{id}", s.blobDownload)
	return mux
}

// writeScope is the ref entries of the job are saved under.
func writeScope(j blobsvc.Job) string {
	if j.Ref != "" {
		return j.Ref
	}
	return "refs/heads/" + j.DefaultBranch
}

// readScopes are the refs the job restores from, in order: its own ref,
// the base branch of a pull request, the default branch.
func readScopes(j blobsvc.Job) []string {
	out := []string{writeScope(j)}
	for _, b := range []string{j.BaseRef, j.DefaultBranch} {
		if b == "" {
			continue
		}
		ref := "refs/heads/" + strings.TrimPrefix(b, "refs/heads/")
		if !contains(out, ref) {
			out = append(out, ref)
		}
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func owner(id int64) string { return "cache-" + strconv.FormatInt(id, 10) }

func storageKey(e dbgen.ActionsCache) string {
	return "cache/" + e.RepoID.String() + "/" + strconv.FormatInt(e.ID, 10)
}

func likePrefix(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s) + "%"
}

func validKey(k string) bool {
	return k != "" && len(k) <= maxKeyLen && !strings.Contains(k, ",")
}

func validVersion(v string) bool { return v != "" && len(v) <= maxVersionLn }

// find returns the entry restored for keys: per scope, an exact match of
// the primary key, then the newest entry whose key starts with one of
// the keys, in order.
func (s *Service) find(ctx context.Context, j blobsvc.Job, keys []string, version string) (dbgen.ActionsCache, bool, error) {
	for _, scope := range readScopes(j) {
		e, err := s.q.GetActionsCacheByKey(ctx, dbgen.GetActionsCacheByKeyParams{
			RepoID: j.RepoID, Scope: scope, Key: keys[0], Version: version})
		if err == nil && e.Complete {
			return s.touch(ctx, e)
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return e, false, fmt.Errorf("find cache: %w", err)
		}
		for _, k := range keys {
			e, err := s.q.FindActionsCache(ctx, dbgen.FindActionsCacheParams{
				RepoID: j.RepoID, Scope: scope, Version: version, Pattern: likePrefix(k)})
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return e, false, fmt.Errorf("find cache: %w", err)
			}
			return s.touch(ctx, e)
		}
	}
	return dbgen.ActionsCache{}, false, nil
}

func (s *Service) touch(ctx context.Context, e dbgen.ActionsCache) (dbgen.ActionsCache, bool, error) {
	if err := s.q.TouchActionsCache(ctx, e.ID); err != nil {
		return e, false, fmt.Errorf("touch cache: %w", err)
	}
	return e, true, nil
}

// reserve creates the entry key/version of the job's scope; ok is false
// when it exists already.
func (s *Service) reserve(ctx context.Context, j blobsvc.Job, key, version string) (dbgen.ActionsCache, bool, error) {
	e, err := s.q.ReserveActionsCache(ctx, dbgen.ReserveActionsCacheParams{
		RepoID: j.RepoID, Scope: writeScope(j), Key: key, Version: version,
		RunID: uuid.NullUUID{UUID: j.RunID, Valid: true}, StaleBefore: blobsvc.Timestamp(time.Now().Add(-staleAfter))})
	if errors.Is(err, pgx.ErrNoRows) {
		return e, false, nil
	}
	if err != nil {
		return e, false, fmt.Errorf("reserve cache: %w", err)
	}
	// A taken-over reservation may have left parts behind.
	if err := s.stage.Discard(ctx, owner(e.ID)); err != nil {
		return e, false, err
	}
	return e, true, nil
}

// complete marks e complete once its blob of size bytes is stored, then
// evicts what exceeds the repository limits.
func (s *Service) complete(ctx context.Context, e dbgen.ActionsCache, size int64) (bool, error) {
	obj, err := s.class.Stat(ctx, storageKey(e))
	if errors.Is(err, blob.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat cache: %w", err)
	}
	if obj.Size != size {
		return false, nil
	}
	if _, err := s.q.CompleteActionsCache(ctx, dbgen.CompleteActionsCacheParams{
		Size: size, StorageKey: storageKey(e), ID: e.ID}); errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("complete cache: %w", err)
	}
	if err := s.evict(ctx); err != nil {
		s.log.WarnContext(ctx, "evict caches", "error", err)
	}
	return true, nil
}

// writable reports whether the job may upload e.
func writable(j blobsvc.Job, e dbgen.ActionsCache) bool {
	return e.RepoID == j.RepoID && e.RunID.Valid && e.RunID.UUID == j.RunID && !e.Complete
}

// remove deletes an entry with its blob and staged parts.
func (s *Service) remove(ctx context.Context, e dbgen.ActionsCache) error {
	if e.StorageKey != "" {
		if err := s.class.Delete(ctx, e.StorageKey); err != nil && !errors.Is(err, blob.ErrNotFound) {
			return fmt.Errorf("delete cache blob: %w", err)
		}
	}
	if err := s.stage.Discard(ctx, owner(e.ID)); err != nil {
		return err
	}
	if err := s.q.DeleteActionsCache(ctx, e.ID); err != nil {
		return fmt.Errorf("delete cache: %w", err)
	}
	return nil
}

// evict removes the least recently used entries of every repository over
// its size limit.
func (s *Service) evict(ctx context.Context) error {
	over, err := s.q.ListActionsCacheReposOver(ctx, s.max)
	if err != nil {
		return fmt.Errorf("list repositories over the cache limit: %w", err)
	}
	for _, o := range over {
		excess := o.Total - s.max
		for excess > 0 {
			lru, err := s.q.ListActionsCachesLRU(ctx, dbgen.ListActionsCachesLRUParams{RepoID: o.RepoID, Lim: evictBatch})
			if err != nil {
				return fmt.Errorf("list caches: %w", err)
			}
			if len(lru) == 0 {
				break
			}
			for _, e := range lru {
				if excess <= 0 {
					break
				}
				if err := s.remove(ctx, e); err != nil {
					return err
				}
				excess -= e.Size
			}
		}
	}
	return nil
}

// Sweep evicts entries unused for longer than the TTL, abandoned uploads
// and what exceeds the repository limits.
func (s *Service) Sweep(ctx context.Context) error {
	now := time.Now()
	for {
		list, err := s.q.ListExpiredActionsCaches(ctx, dbgen.ListExpiredActionsCachesParams{
			UsedBefore: blobsvc.Timestamp(now.Add(-s.ttl)), StaleBefore: blobsvc.Timestamp(now.Add(-staleAfter)),
			Lim: sweepBatch})
		if err != nil {
			return fmt.Errorf("list expired caches: %w", err)
		}
		for _, e := range list {
			if err := s.remove(ctx, e); err != nil {
				return err
			}
		}
		if len(list) < sweepBatch {
			break
		}
	}
	if err := s.stage.SweepStale(ctx, now.Add(-staleAfter)); err != nil {
		return err
	}
	return s.evict(ctx)
}

// SweepArgs runs Sweep.
type SweepArgs struct{}

func (SweepArgs) Kind() string { return "actions_cache_sweep" }

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

// entryByPath loads the entry named by the {id} path value.
func (s *Service) entryByPath(ctx context.Context, r *http.Request) (dbgen.ActionsCache, int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return dbgen.ActionsCache{}, 0, pgx.ErrNoRows
	}
	e, err := s.q.GetActionsCache(ctx, id)
	return e, id, err
}

func (s *Service) blobUpload(w http.ResponseWriter, r *http.Request) {
	e, id, err := s.entryByPath(r.Context(), r)
	if err == nil && !s.signer.Valid(r, blobKind, blobsvc.OpUpload, id) {
		err = pgx.ErrNoRows
	}
	if !s.blobEntry(w, r, err) {
		return
	}
	if e.Complete {
		blobsvc.Problem(w, http.StatusConflict, "cache entry already committed")
		return
	}
	s.stage.ServeUpload(w, r, blobsvc.Upload{Owner: owner(e.ID), Key: storageKey(e), Max: s.max})
}

func (s *Service) blobDownload(w http.ResponseWriter, r *http.Request) {
	e, id, err := s.entryByPath(r.Context(), r)
	if err == nil && (!s.signer.Valid(r, blobKind, blobsvc.OpDownload, id) || !e.Complete) {
		err = pgx.ErrNoRows
	}
	if !s.blobEntry(w, r, err) {
		return
	}
	blobsvc.ServeBlob(w, r, s.class, s.log, e.StorageKey, "application/octet-stream", "")
}

func (s *Service) blobEntry(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		blobsvc.Problem(w, http.StatusForbidden, "invalid or expired signature")
		return false
	case err != nil:
		s.log.ErrorContext(r.Context(), "load cache entry", "error", err)
		blobsvc.Problem(w, http.StatusInternalServerError, "")
		return false
	}
	return true
}
