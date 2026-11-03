package jobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbtest"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

type fakeTier struct {
	mu         sync.Mutex
	replicated []tier.ObjectRef
	pending    []tier.ObjectRef
	cutoff     time.Time
	limit      int32
	err        error
	evicted    int
}

func (f *fakeTier) Replicate(_ context.Context, ref tier.ObjectRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replicated = append(f.replicated, ref)
	return f.err
}

func (f *fakeTier) PendingReplicas(_ context.Context, before pgtype.Timestamptz, limit int32) ([]tier.ObjectRef, error) {
	f.cutoff, f.limit = before.Time, limit
	return f.pending, f.err
}

func (f *fakeTier) VerifySample(_ context.Context, before pgtype.Timestamptz, limit int32) (tier.VerifyReport, error) {
	f.cutoff, f.limit = before.Time, limit
	return tier.VerifyReport{Checked: 3, OK: 2, Repaired: 1}, f.err
}

func (f *fakeTier) Evict(context.Context) (int, int64, error) {
	f.evicted++
	return 2, 10, f.err
}

type fakeInserter struct{ got []river.InsertManyParams }

func (f *fakeInserter) InsertMany(_ context.Context, p []river.InsertManyParams) ([]*rivertype.JobInsertResult, error) {
	f.got = append(f.got, p...)
	return nil, nil
}

func TestStorageWorkers(t *testing.T) {
	ctx := context.Background()
	ft := &fakeTier{}
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	ref := tier.ObjectRef{Target: "box", Key: "lfs/x"}

	rw := &ReplicateWorker{Tier: ft}
	if err := rw.Work(ctx, &river.Job[ReplicateArgs]{Args: ReplicateArgs{Target: ref.Target, Key: ref.Key}}); err != nil {
		t.Fatal(err)
	}
	if len(ft.replicated) != 1 || ft.replicated[0] != ref {
		t.Fatalf("replicated %v", ft.replicated)
	}

	ft.pending = []tier.ObjectRef{ref, {Target: "box", Key: "lfs/y"}}
	ins := &fakeInserter{}
	sw := &ReplicaSweepWorker{Tier: ft, Inserter: ins, Now: func() time.Time { return now }}
	if err := sw.Work(ctx, &river.Job[ReplicaSweepArgs]{}); err != nil {
		t.Fatal(err)
	}
	if len(ins.got) != 2 || ins.got[0].Args.(ReplicateArgs).Key != "lfs/x" || !ft.cutoff.Equal(now.Add(-ReplicaSweepGrace)) {
		t.Fatalf("sweep inserted %v (cutoff %v)", ins.got, ft.cutoff)
	}
	if opts := (ReplicateArgs{}).InsertOpts(); !opts.UniqueOpts.ByArgs {
		t.Fatal("replication jobs must be unique by args")
	}

	vw := &StorageVerifyWorker{Tier: ft, Now: func() time.Time { return now }}
	if err := vw.Work(ctx, &river.Job[StorageVerifyArgs]{}); err != nil {
		t.Fatal(err)
	}
	if ft.limit != VerifySampleSize || !ft.cutoff.Equal(now.Add(-VerifyAge)) {
		t.Fatalf("verify limit %d cutoff %v", ft.limit, ft.cutoff)
	}

	ew := &StorageEvictWorker{Tier: ft, Log: slog.New(slog.DiscardHandler)}
	if err := ew.Work(ctx, &river.Job[StorageEvictArgs]{}); err != nil || ft.evicted != 1 {
		t.Fatalf("evict: %v", err)
	}

	ft.err = errors.New("boom")
	for name, err := range map[string]error{
		"replicate": rw.Work(ctx, &river.Job[ReplicateArgs]{}),
		"sweep":     sw.Work(ctx, &river.Job[ReplicaSweepArgs]{}),
		"verify":    vw.Work(ctx, &river.Job[StorageVerifyArgs]{}),
		"evict":     ew.Work(ctx, &river.Job[StorageEvictArgs]{}),
	} {
		if err == nil {
			t.Errorf("%s: error not returned", name)
		}
	}
	if err := (*Runner)(nil).EnqueueReplication(ctx, ref); err == nil {
		t.Error("nil runner accepted")
	}
}

// TestRunnerReplication runs the whole path on River and PostgreSQL: a Put
// enqueues replication through OnStored and the job copies the object.
func TestRunnerReplication(t *testing.T) {
	ctx := context.Background()
	pool, err := db.Open(ctx, config.Database{URL: dbtest.URL(t), MaxConns: 8})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	log := slog.New(slog.DiscardHandler)
	if err := db.Migrate(ctx, pool, log); err != nil {
		t.Fatal(err)
	}

	cfg := config.Storage{
		Targets: []config.Target{{Name: "main", Kind: "local", Path: t.TempDir()}, {Name: "copy", Kind: "local", Path: t.TempDir()}},
		Policy:  map[string]config.Policy{"lfs": {Primary: "main", Replicas: []string{"copy"}}},
	}
	stores, err := tier.BuildStores(ctx, cfg, tier.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var runner *Runner
	tr, err := tier.New(tier.Options{Config: cfg, Stores: stores, Queries: dbgen.New(pool),
		OnStored: func(ctx context.Context, ref tier.ObjectRef) error { return runner.EnqueueReplication(ctx, ref) }})
	if err != nil {
		t.Fatal(err)
	}
	runner, err = New(pool, log, WithStorage(tr, log))
	if err != nil {
		t.Fatal(err)
	}
	events, cancel := runner.Client().Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
	defer cancel()
	if err := runner.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, stop := context.WithTimeout(ctx, 10*time.Second)
		defer stop()
		_ = runner.Stop(stopCtx)
	})

	lfs, _ := tr.Class("lfs")
	data := bytes.Repeat([]byte("replica"), 5000)
	sum := sha256.Sum256(data)
	obj, err := lfs.Put(ctx, "sha256/x", bytes.NewReader(data), int64(len(data)), sum[:])
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.After(30 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Job.Kind != (ReplicateArgs{}).Kind() {
				continue
			}
			if ev.Kind != river.EventKindJobCompleted {
				t.Fatalf("replication job failed: %v", ev.Job.Errors)
			}
			got, err := lfs.Stat(ctx, "sha256/x")
			if err != nil || !got.ReplicasOK {
				t.Fatalf("object = %+v, %v", got, err)
			}
			if _, err := stores["copy"].Stat(ctx, obj.Key); err != nil {
				t.Fatalf("replica missing: %v", err)
			}
			return
		case <-deadline:
			t.Fatal("replication job did not run")
		}
	}
}
