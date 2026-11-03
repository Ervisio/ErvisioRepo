package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// Storage job schedule (PLAN §4.2).
const (
	// ReplicaSweepInterval is how often objects still missing a replica are
	// re-enqueued, covering enqueue failures and exhausted retries.
	ReplicaSweepInterval = 10 * time.Minute
	// ReplicaSweepGrace skips objects younger than this: their replication
	// job is most likely still queued.
	ReplicaSweepGrace = 5 * time.Minute
	// VerifyInterval is how often a sample of objects is re-hashed.
	VerifyInterval = 7 * 24 * time.Hour
	// VerifyAge is how old a verification must be to be repeated.
	VerifyAge = 6 * 24 * time.Hour
	// VerifySampleSize is how many objects one verification run checks.
	VerifySampleSize = 500
	// EvictInterval is how often the cache watermarks and TTLs are enforced.
	EvictInterval = 15 * time.Minute

	sweepBatch = 500
)

// StorageTier is the subset of *tier.Tier used by the storage jobs.
type StorageTier interface {
	Replicate(ctx context.Context, ref tier.ObjectRef) error
	PendingReplicas(ctx context.Context, createdBefore pgtype.Timestamptz, limit int32) ([]tier.ObjectRef, error)
	VerifySample(ctx context.Context, verifiedBefore pgtype.Timestamptz, limit int32) (tier.VerifyReport, error)
	Evict(ctx context.Context) (objects int, bytes int64, err error)
}

// WithStorage registers the replication, sweep, verification and eviction
// workers for t.
func WithStorage(t StorageTier, log *slog.Logger) Option {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return func(r *Registry) {
		river.AddWorker(r.Workers, &ReplicateWorker{Tier: t})
		river.AddWorker(r.Workers, &ReplicaSweepWorker{Tier: t})
		river.AddWorker(r.Workers, &StorageVerifyWorker{Tier: t, Log: log})
		river.AddWorker(r.Workers, &StorageEvictWorker{Tier: t, Log: log})
		r.Periodic = append(r.Periodic,
			river.NewPeriodicJob(river.PeriodicInterval(ReplicaSweepInterval),
				func() (river.JobArgs, *river.InsertOpts) { return ReplicaSweepArgs{}, nil },
				&river.PeriodicJobOpts{RunOnStart: true}),
			river.NewPeriodicJob(river.PeriodicInterval(VerifyInterval),
				func() (river.JobArgs, *river.InsertOpts) { return StorageVerifyArgs{}, nil }, nil),
			river.NewPeriodicJob(river.PeriodicInterval(EvictInterval),
				func() (river.JobArgs, *river.InsertOpts) { return StorageEvictArgs{}, nil },
				&river.PeriodicJobOpts{RunOnStart: true}),
		)
	}
}

// ReplicateArgs asks for one object to be copied to its policy's replicas.
type ReplicateArgs struct {
	Target string `json:"target"`
	Key    string `json:"key"`
}

func (ReplicateArgs) Kind() string { return "storage_replicate" }

// InsertOpts deduplicates replication jobs for the same object.
func (ReplicateArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 12, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// ReplicateWorker runs tier.Replicate.
type ReplicateWorker struct {
	river.WorkerDefaults[ReplicateArgs]
	Tier StorageTier
}

// Timeout allows copying large objects over slow links.
func (*ReplicateWorker) Timeout(*river.Job[ReplicateArgs]) time.Duration { return 4 * time.Hour }

func (w *ReplicateWorker) Work(ctx context.Context, job *river.Job[ReplicateArgs]) error {
	ref := tier.ObjectRef{Target: job.Args.Target, Key: job.Args.Key}
	if err := w.Tier.Replicate(ctx, ref); err != nil {
		return fmt.Errorf("replicate: %w", err)
	}
	return nil
}

// Inserter is the subset of the River client used to enqueue jobs.
type Inserter interface {
	InsertMany(ctx context.Context, params []river.InsertManyParams) ([]*rivertype.JobInsertResult, error)
}

// EnqueueReplication inserts replication jobs; duplicates of a job still
// pending are skipped.
func EnqueueReplication(ctx context.Context, c Inserter, refs ...tier.ObjectRef) error {
	if len(refs) == 0 {
		return nil
	}
	params := make([]river.InsertManyParams, len(refs))
	for i, r := range refs {
		params[i] = river.InsertManyParams{Args: ReplicateArgs{Target: r.Target, Key: r.Key}}
	}
	if _, err := c.InsertMany(ctx, params); err != nil {
		return fmt.Errorf("enqueue replication: %w", err)
	}
	return nil
}

// EnqueueReplication enqueues a replication job on the runner's client. It
// fits tier.Options.OnStored.
func (r *Runner) EnqueueReplication(ctx context.Context, ref tier.ObjectRef) error {
	if r == nil || r.client == nil {
		return errors.New("enqueue replication: job runner not configured")
	}
	return EnqueueReplication(ctx, r.client, ref)
}

// ReplicaSweepArgs is the periodic sweep of objects missing a replica.
type ReplicaSweepArgs struct{}

func (ReplicaSweepArgs) Kind() string { return "storage_replica_sweep" }

// ReplicaSweepWorker re-enqueues replication for objects left pending.
type ReplicaSweepWorker struct {
	river.WorkerDefaults[ReplicaSweepArgs]
	Tier StorageTier
	// Inserter defaults to the client running the job.
	Inserter Inserter
	Now      func() time.Time
}

func (w *ReplicaSweepWorker) Work(ctx context.Context, _ *river.Job[ReplicaSweepArgs]) error {
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	refs, err := w.Tier.PendingReplicas(ctx, pgtype.Timestamptz{Time: now().Add(-ReplicaSweepGrace), Valid: true}, sweepBatch)
	if err != nil {
		return fmt.Errorf("replica sweep: %w", err)
	}
	ins := w.Inserter
	if ins == nil {
		c, err := river.ClientFromContextSafely[pgx.Tx](ctx)
		if err != nil {
			return fmt.Errorf("replica sweep: %w", err)
		}
		ins = c
	}
	return EnqueueReplication(ctx, ins, refs...)
}

// StorageVerifyArgs is the weekly sample verification.
type StorageVerifyArgs struct{}

func (StorageVerifyArgs) Kind() string { return "storage_verify" }

// StorageVerifyWorker re-hashes a sample of objects on every copy.
type StorageVerifyWorker struct {
	river.WorkerDefaults[StorageVerifyArgs]
	Tier   StorageTier
	Log    *slog.Logger
	Sample int32 // defaults to VerifySampleSize
	Now    func() time.Time
}

// Timeout leaves room for downloading the whole sample from every target.
func (*StorageVerifyWorker) Timeout(*river.Job[StorageVerifyArgs]) time.Duration {
	return 12 * time.Hour
}

func (w *StorageVerifyWorker) Work(ctx context.Context, _ *river.Job[StorageVerifyArgs]) error {
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	n := w.Sample
	if n <= 0 {
		n = VerifySampleSize
	}
	rep, err := w.Tier.VerifySample(ctx, pgtype.Timestamptz{Time: now().Add(-VerifyAge), Valid: true}, n)
	log := w.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	level := slog.LevelInfo
	if rep.Failed > 0 || rep.Repaired > 0 || rep.Replicas > 0 {
		level = slog.LevelWarn
	}
	log.Log(ctx, level, "storage verification", "checked", rep.Checked, "ok", rep.OK,
		"repaired", rep.Repaired, "failed", rep.Failed, "bad_replicas", rep.Replicas)
	if err != nil {
		return fmt.Errorf("storage verification: %w", err)
	}
	return nil
}

// StorageEvictArgs is the periodic cache eviction.
type StorageEvictArgs struct{}

func (StorageEvictArgs) Kind() string { return "storage_evict" }

// StorageEvictWorker enforces cache TTLs and watermarks.
type StorageEvictWorker struct {
	river.WorkerDefaults[StorageEvictArgs]
	Tier StorageTier
	Log  *slog.Logger
}

func (w *StorageEvictWorker) Work(ctx context.Context, _ *river.Job[StorageEvictArgs]) error {
	n, b, err := w.Tier.Evict(ctx)
	if n > 0 && w.Log != nil {
		w.Log.InfoContext(ctx, "storage cache eviction", "objects", n, "bytes", b)
	}
	if err != nil {
		return fmt.Errorf("cache eviction: %w", err)
	}
	return nil
}
