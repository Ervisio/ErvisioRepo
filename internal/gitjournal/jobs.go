package gitjournal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/jobs"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// Job schedule and retention.
const (
	// SweepInterval and SweepAge bound the replication delay of async
	// pushes (and of failed sync flushes): RPO < SweepInterval + SweepAge.
	SweepInterval = 30 * time.Second
	SweepAge      = 20 * time.Second
	GCInterval    = 6 * time.Hour
	// OrphanGrace protects objects uploaded by a push or a compaction that
	// has not committed its manifest yet.
	OrphanGrace = 24 * time.Hour
	// KeepGenerations previous generations are kept up to Retention, so
	// that readers and backups of an older manifest still find their packs.
	KeepGenerations = 2
	Retention       = 7 * 24 * time.Hour

	sweepBatch  = 500
	gcBatch     = 200
	repackLimit = 4 * time.Hour
)

// SyncArgs replicates the hot refs of a repository to the journal.
type SyncArgs struct {
	RepoID uuid.UUID `json:"repo_id"`
	Sweep  bool      `json:"sweep,omitempty"`
}

// Kind implements river.JobArgs.
func (SyncArgs) Kind() string { return "gitjournal_sync" }

// InsertOpts deduplicates sweep enqueues; push enqueues are never dropped,
// since a running sync may predate the push.
func (a SyncArgs) InsertOpts() river.InsertOpts {
	o := river.InsertOpts{MaxAttempts: 20}
	if a.Sweep {
		o.UniqueOpts = river.UniqueOpts{ByArgs: true, ByPeriod: SweepInterval}
	}
	return o
}

// SweepArgs enqueues a sync for every repository left dirty.
type SweepArgs struct{}

// Kind implements river.JobArgs.
func (SweepArgs) Kind() string { return "gitjournal_sweep" }

// CompactArgs rewrites the journal of a repository as one base pack.
type CompactArgs struct {
	RepoID uuid.UUID `json:"repo_id"`
}

// Kind implements river.JobArgs.
func (CompactArgs) Kind() string { return "gitjournal_compact" }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (CompactArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: time.Hour}}
}

// MaintenanceArgs runs git maintenance on the hot copy of a repository.
type MaintenanceArgs struct {
	RepoID uuid.UUID `json:"repo_id"`
}

// Kind implements river.JobArgs.
func (MaintenanceArgs) Kind() string { return "gitjournal_maintenance" }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (MaintenanceArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: time.Hour}}
}

// GCArgs deletes orphan and expired journal objects.
type GCArgs struct{}

// Kind implements river.JobArgs.
func (GCArgs) Kind() string { return "gitjournal_gc" }

// JobsOption registers the journal workers and periodic jobs.
func JobsOption(j *Journal) jobs.Option {
	return func(r *jobs.Registry) {
		river.AddWorker(r.Workers, &syncWorker{j: j})
		river.AddWorker(r.Workers, &sweepWorker{j: j})
		river.AddWorker(r.Workers, &compactWorker{j: j})
		river.AddWorker(r.Workers, &maintenanceWorker{j: j})
		river.AddWorker(r.Workers, &gcWorker{j: j})
		r.Periodic = append(r.Periodic,
			river.NewPeriodicJob(river.PeriodicInterval(SweepInterval),
				func() (river.JobArgs, *river.InsertOpts) { return SweepArgs{}, nil },
				&river.PeriodicJobOpts{RunOnStart: true}),
			river.NewPeriodicJob(river.PeriodicInterval(GCInterval),
				func() (river.JobArgs, *river.InsertOpts) { return GCArgs{}, nil }, nil))
	}
}

// hot reports whether the repository is hot, so that a job does not
// hydrate a cold one just to find nothing to do.
func (j *Journal) hot(ctx context.Context, repo uuid.UUID) (bool, error) {
	row, err := j.q.GetRepoStorage(ctx, repo)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load repository state: %w", err)
	}
	return row.StorageState == gitstore.StateHot, nil
}

// acquire takes a Write lease, or nil when the repository is gone.
func (j *Journal) acquire(ctx context.Context, repo uuid.UUID) (*gitstore.Lease, error) {
	l, err := j.o.Tier.Acquire(ctx, repo, gitstore.Write)
	if errors.Is(err, gitstore.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("acquire %s: %w", repo, err)
	}
	return l, nil
}

type syncWorker struct {
	river.WorkerDefaults[SyncArgs]
	j *Journal
}

func (w *syncWorker) Work(ctx context.Context, job *river.Job[SyncArgs]) error {
	return w.j.Sync(ctx, job.Args.RepoID)
}

// Sync replicates a dirty repository to the journal.
func (j *Journal) Sync(ctx context.Context, repo uuid.UUID) error {
	row, err := j.q.GetRepoJournal(ctx, repo)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load repository: %w", err)
	}
	if !row.JournalDirtySince.Valid {
		return nil
	}
	l, err := j.acquire(ctx, repo)
	if err != nil || l == nil {
		return err
	}
	defer l.Release()
	if err := j.flush(ctx, repo, l.Path, nil); err != nil {
		return fmt.Errorf("sync %s: %w", repo, err)
	}
	return nil
}

type sweepWorker struct {
	river.WorkerDefaults[SweepArgs]
	j *Journal
}

func (w *sweepWorker) Work(ctx context.Context, _ *river.Job[SweepArgs]) error {
	before := pgtype.Timestamptz{Time: w.j.o.Now().Add(-SweepAge), Valid: true}
	ids, err := w.j.q.ListDirtyRepos(ctx, dbgen.ListDirtyReposParams{Before: before, Lim: sweepBatch})
	if err != nil {
		return fmt.Errorf("list dirty repositories: %w", err)
	}
	for _, id := range ids {
		w.j.enqueue(ctx, SyncArgs{RepoID: id, Sweep: true})
	}
	return nil
}

type compactWorker struct {
	river.WorkerDefaults[CompactArgs]
	j *Journal
}

func (w *compactWorker) Work(ctx context.Context, job *river.Job[CompactArgs]) error {
	return w.j.Compact(ctx, job.Args.RepoID, false)
}

// Compact repacks the hot copy and replaces the base and segments of the
// journal with one base pack of a new generation. Unless force is set it
// does nothing below the thresholds. It holds the write lease: pushes wait,
// fetches do not.
func (j *Journal) Compact(ctx context.Context, repo uuid.UUID, force bool) error {
	if ok, err := j.hot(ctx, repo); err != nil || !ok {
		return err
	}
	l, err := j.acquire(ctx, repo)
	if err != nil || l == nil {
		return err
	}
	defer l.Release()
	if err := j.flush(ctx, repo, l.Path, nil); err != nil {
		return fmt.Errorf("compact %s: %w", repo, err)
	}
	h, err := j.loadHead(ctx, repo)
	if err != nil {
		return err
	}
	if h.refs == nil || (!force && !needsCompaction(h.m)) {
		return nil
	}
	if err := git.Exec(ctx, git.Cmd{RepoPath: l.Path, Args: []string{"repack", "-a", "-d", "-q"}, Timeout: repackLimit}); err != nil {
		return fmt.Errorf("compact %s: repack: %w", repo, err)
	}
	gen := h.m.Generation + 1
	p, dir, err := writePack(ctx, l.Path, nil, h.tips(), nil)
	if dir != "" {
		defer func() { _ = os.RemoveAll(dir) }()
	}
	if err != nil {
		return fmt.Errorf("compact %s: %w", repo, err)
	}
	m := h.m.Clone()
	m.Generation = gen
	m.Segments = []gitstore.Segment{}
	m.Base = nil
	if p != nil {
		b := &gitstore.Base{Gen: gen, SHA: p.SHA, Size: p.Size, SHA256: p.SHA256, IdxSHA256: p.IdxSHA256,
			Pack: gitstore.BaseKey(repo, gen, gitstore.ExtPack), Idx: gitstore.BaseKey(repo, gen, gitstore.ExtIdx)}
		if err := j.upload(ctx, p, b.Pack, b.Idx); err != nil {
			return fmt.Errorf("compact %s: %w", repo, err)
		}
		m.Base = b
	}
	// Retire the old packs before the manifest stops referencing them, so
	// that the garbage collector always sees them as protected.
	var old []string
	for _, pk := range h.m.Packs() {
		old = append(old, pk.Pack, pk.Idx)
	}
	if err := j.q.InsertRetiredGeneration(ctx, dbgen.InsertRetiredGenerationParams{
		RepoID: repo, Generation: h.m.Generation, Keys: old}); err != nil {
		return fmt.Errorf("compact %s: retire generation: %w", repo, err)
	}
	if err := j.store.SaveManifest(ctx, m); err != nil {
		return fmt.Errorf("compact %s: %w", repo, err)
	}
	j.log.InfoContext(ctx, "git journal compacted", "repo", repo, "generation", gen,
		"segments", len(h.m.Segments), "size", size(m))
	if err := j.recordSize(ctx, repo, m); err != nil {
		return err
	}
	return j.pruneGenerations(ctx, repo, m)
}

type maintenanceWorker struct {
	river.WorkerDefaults[MaintenanceArgs]
	j *Journal
}

func (w *maintenanceWorker) Work(ctx context.Context, job *river.Job[MaintenanceArgs]) error {
	return w.j.Maintain(ctx, job.Args.RepoID)
}

// Maintain runs git maintenance (commit-graph, loose objects) on the hot
// copy and resets the push counter.
func (j *Journal) Maintain(ctx context.Context, repo uuid.UUID) error {
	if ok, err := j.hot(ctx, repo); err != nil || !ok {
		return err
	}
	l, err := j.acquire(ctx, repo)
	if err != nil || l == nil {
		return err
	}
	defer l.Release()
	if err := git.Exec(ctx, git.Cmd{RepoPath: l.Path, Timeout: repackLimit,
		Args: []string{"maintenance", "run", "--task=commit-graph", "--task=loose-objects"}}); err != nil {
		return fmt.Errorf("maintenance %s: %w", repo, err)
	}
	if err := j.q.ResetRepoPushes(ctx, repo); err != nil {
		return fmt.Errorf("maintenance %s: reset counter: %w", repo, err)
	}
	return nil
}

type gcWorker struct {
	river.WorkerDefaults[GCArgs]
	j *Journal
}

func (w *gcWorker) Work(ctx context.Context, _ *river.Job[GCArgs]) error {
	return w.j.GC(ctx)
}

// GC deletes, for every repository, the packs no manifest or retained
// generation references (after OrphanGrace), the ref snapshots older than
// the manifest (after Retention) and the expired generations.
func (j *Journal) GC(ctx context.Context) error {
	var after uuid.UUID
	var errs []error
	for {
		ids, err := j.q.ListRepoIDsAfter(ctx, dbgen.ListRepoIDsAfterParams{After: after, Lim: gcBatch})
		if err != nil {
			return fmt.Errorf("gc: list repositories: %w", err)
		}
		for _, id := range ids {
			if err := j.gcRepo(ctx, id); err != nil {
				j.log.WarnContext(ctx, "git journal gc failed", "repo", id, "error", err)
				errs = append(errs, err)
			}
		}
		if len(ids) < gcBatch {
			break
		}
		after = ids[len(ids)-1]
	}
	if len(errs) > 0 {
		return fmt.Errorf("gc: %d repositories failed: %w", len(errs), errors.Join(errs...))
	}
	return nil
}

func (j *Journal) gcRepo(ctx context.Context, repo uuid.UUID) error {
	// Manifest first, retired generations second: a compaction retires the
	// old packs before it saves the manifest, so nothing live is missed.
	m, err := j.store.LoadManifest(ctx, repo)
	if errors.Is(err, gitstore.ErrNoManifest) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := j.pruneGenerations(ctx, repo, m); err != nil {
		return err
	}
	keep := liveKeys(m)
	retired, err := j.q.ListRetiredGenerations(ctx, repo)
	if err != nil {
		return fmt.Errorf("list retired generations: %w", err)
	}
	for _, r := range retired {
		for _, k := range r.Keys {
			keep[k] = true
		}
	}
	class := j.store.Class()
	now := j.o.Now()
	prefix := class.FullKey(gitstore.RepoPrefix(repo))
	var doomed []string
	err = class.Primary().Store.List(ctx, prefix, func(info blob.Info) error {
		key, ok := strings.CutPrefix(info.Key, class.FullKey(""))
		if !ok || keep[key] {
			return nil
		}
		k, err := gitstore.ParseKey(key)
		if err != nil || k.Repo != repo {
			return nil //nolint:nilerr // not a journal key: leave it alone
		}
		age := now.Sub(info.ModTime)
		switch k.Kind {
		case gitstore.KindBase, gitstore.KindSegment:
			if age > OrphanGrace {
				doomed = append(doomed, key)
			}
		case gitstore.KindRefs:
			if k.Seq < m.RefsSeq && age > Retention {
				doomed = append(doomed, key)
			}
		default:
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("list journal objects: %w", err)
	}
	return deleteKeys(ctx, class, doomed)
}

// liveKeys are the keys the manifest references.
func liveKeys(m *gitstore.Manifest) map[string]bool {
	keep := map[string]bool{gitstore.ManifestKey(m.Repo): true, gitstore.RefsKey(m.Repo, m.RefsSeq): true}
	for _, p := range m.Packs() {
		keep[p.Pack], keep[p.Idx] = true, true
	}
	return keep
}

// pruneGenerations deletes the retired generations beyond the retention:
// beyond the KeepGenerations newest (after OrphanGrace) or older than
// Retention. Keys of the current manifest are never deleted.
func (j *Journal) pruneGenerations(ctx context.Context, repo uuid.UUID, m *gitstore.Manifest) error {
	rows, err := j.q.ListRetiredGenerations(ctx, repo)
	if err != nil {
		return fmt.Errorf("list retired generations: %w", err)
	}
	live := liveKeys(m)
	now := j.o.Now()
	for i, r := range rows {
		age := now.Sub(r.RetiredAt.Time)
		if (i < KeepGenerations || age <= OrphanGrace) && age <= Retention {
			continue
		}
		var keys []string
		for _, k := range r.Keys {
			if !live[k] {
				keys = append(keys, k)
			}
		}
		if err := deleteKeys(ctx, j.store.Class(), keys); err != nil {
			return err
		}
		if err := j.q.DeleteRetiredGeneration(ctx, dbgen.DeleteRetiredGenerationParams{RepoID: repo, Generation: r.Generation}); err != nil {
			return fmt.Errorf("delete retired generation: %w", err)
		}
		j.log.InfoContext(ctx, "git journal generation deleted", "repo", repo, "generation", r.Generation)
	}
	return nil
}

func deleteKeys(ctx context.Context, class *tier.Class, keys []string) error {
	var errs []error
	for _, k := range keys {
		if err := class.Delete(ctx, k); err != nil && !errors.Is(err, blob.ErrNotFound) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
