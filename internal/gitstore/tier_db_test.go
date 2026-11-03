package gitstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbtest"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob/local"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

type fixture struct {
	pool  *pgxpool.Pool
	q     *dbgen.Queries
	store *Store
	tier  *Tier
	owner uuid.UUID
}

func newFixture(t *testing.T, hyd Hydrator) *fixture {
	t.Helper()
	ctx := context.Background()
	pool, err := db.Open(ctx, config.Database{URL: dbtest.URL(t), MaxConns: 8})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	q := dbgen.New(pool)
	dir := t.TempDir()
	ls, err := local.New("hot", filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Storage{
		Targets: []config.Target{{Name: "hot", Kind: "local", Path: filepath.Join(dir, "blobs")}},
		Policy:  map[string]config.Policy{"git": {Primary: "hot", Durability: "sync"}},
	}
	tr, err := tier.New(tier.Options{Config: cfg, Stores: map[string]blob.Store{"hot": ls}, Queries: q})
	if err != nil {
		t.Fatal(err)
	}
	st, err := NewStore(tr)
	if err != nil {
		t.Fatal(err)
	}
	pc := git.NewPool(git.PoolOptions{})
	t.Cleanup(pc.Close)
	gt, err := NewTier(Options{Pool: pool, HotPath: filepath.Join(dir, "blobs", ".hot"), Store: st, Hydrator: hyd, CatFile: pc})
	if err != nil {
		t.Fatal(err)
	}
	owner := uuid.Must(uuid.NewV7())
	if err := q.InsertNamespace(ctx, dbgen.InsertNamespaceParams{ID: owner, Kind: "org", Name: "o" + owner.String()[:8]}); err != nil {
		t.Fatal(err)
	}
	return &fixture{pool: pool, q: q, store: st, tier: gt, owner: owner}
}

func (f *fixture) repo(t *testing.T, state string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if err := f.q.InsertRepo(context.Background(), dbgen.InsertRepoParams{ID: id, OwnerID: f.owner, Name: "r" + id.String()[24:],
		Visibility: "private", DefaultBranch: "main", StorageState: state}); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *fixture) setState(t *testing.T, id uuid.UUID, from, to string) {
	t.Helper()
	n, err := f.q.SetRepoStorageState(context.Background(), dbgen.SetRepoStorageStateParams{ID: id, FromState: from, ToState: to})
	if err != nil || n != 1 {
		t.Fatalf("set state %s->%s: %d %v", from, to, n, err)
	}
}

func TestStoreManifestCAS(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	id := uuid.New()
	if _, err := f.store.LoadManifest(ctx, id); !errors.Is(err, ErrNoManifest) {
		t.Fatalf("missing manifest: %v", err)
	}
	m := NewManifest(id)
	if err := f.store.SaveManifest(ctx, m); err != nil || m.ETag == "" {
		t.Fatalf("create: %v %q", err, m.ETag)
	}
	if err := f.store.SaveManifest(ctx, NewManifest(id)); !errors.Is(err, ErrConflict) {
		t.Fatalf("second create: %v", err)
	}
	a, err := f.store.LoadManifest(ctx, id)
	if err != nil || a.ETag != m.ETag {
		t.Fatalf("load: %+v %v", a, err)
	}
	b := a.Clone()
	a.RefsSeq = 1
	if err := f.store.SaveManifest(ctx, a); err != nil {
		t.Fatal(err)
	}
	b.RefsSeq = 2
	if err := f.store.SaveManifest(ctx, b); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale etag: %v", err)
	}
	if got, _ := f.store.LoadManifest(ctx, id); got.RefsSeq != 1 {
		t.Fatalf("lost update: %+v", got)
	}
	bad := NewManifest(id)
	bad.Version = 9
	if err := f.store.SaveManifest(ctx, bad); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("invalid manifest saved: %v", err)
	}

	// Journal objects and snapshots go through the class.
	pack := []byte("PACK fake")
	sum := sha256.Sum256(pack)
	key := SegmentKey(id, 1, sha1, ExtPack)
	if _, err := f.store.PutObject(ctx, key, bytes.NewReader(pack), int64(len(pack)), sum[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PutObject(ctx, ManifestKey(id), bytes.NewReader(pack), int64(len(pack)), nil); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("manifest through PutObject: %v", err)
	}
	if _, err := f.store.PutObject(ctx, "../x", bytes.NewReader(pack), int64(len(pack)), nil); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("bad key: %v", err)
	}
	snap := &RefsSnapshot{Version: 1, Seq: 1, Head: "refs/heads/main", Refs: map[string]string{"refs/heads/main": sha1}}
	if err := f.store.PutRefs(ctx, id, snap); err != nil {
		t.Fatal(err)
	}
	got, err := f.store.LoadRefs(ctx, id, 1)
	if err != nil || got.Refs["refs/heads/main"] != sha1 {
		t.Fatalf("refs %+v %v", got, err)
	}
	if _, err := f.store.LoadRefs(ctx, id, 2); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("missing refs: %v", err)
	}

	if err := f.store.DeleteRepo(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.LoadManifest(ctx, id); !errors.Is(err, ErrNoManifest) {
		t.Fatalf("manifest after delete: %v", err)
	}
	if _, _, err := f.store.OpenObject(ctx, key, nil); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("segment after delete: %v", err)
	}
}

func TestStoreRejectsForeignManifest(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	// Copy a's manifest under b's key.
	if err := f.store.SaveManifest(ctx, NewManifest(a)); err != nil {
		t.Fatal(err)
	}
	p := f.store.primary
	rc, _, err := p.Get(ctx, f.store.class.FullKey(ManifestKey(a)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Put(ctx, f.store.class.FullKey(ManifestKey(b)), rc, -1, blob.PutOpts{}); err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	if _, err := f.store.LoadManifest(ctx, b); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("foreign manifest: %v", err)
	}
}

func TestTierInitAcquire(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	id := f.repo(t, StateHot)
	if _, err := f.tier.Acquire(ctx, id, Read); !errors.Is(err, ErrHotMissing) {
		t.Fatalf("before init: %v", err)
	}
	if err := f.tier.Init(ctx, id, "main"); err != nil {
		t.Fatal(err)
	}
	if err := f.tier.Init(ctx, id, "main"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second init: %v", err)
	}
	if m, err := f.store.LoadManifest(ctx, id); err != nil || m.RefsSeq != 0 {
		t.Fatalf("initial manifest %+v %v", m, err)
	}
	want := filepath.Join(f.tier.HotRoot(), "git", id.String()[34:], id.String()+".git")
	if f.tier.HotPath(id) != want {
		t.Fatalf("hot path %s, want %s", f.tier.HotPath(id), want)
	}

	l, err := f.tier.Acquire(ctx, id, Read)
	if err != nil || l.Path != want || l.Mode != Read {
		t.Fatalf("acquire: %+v %v", l, err)
	}
	if out, err := git.Run(ctx, l.Path, "symbolic-ref", "HEAD"); err != nil || strings.TrimSpace(string(out)) != "refs/heads/main" {
		t.Fatalf("HEAD %q %v", out, err)
	}
	l.Release()
	l.Release() // idempotent
	var nilLease *Lease
	nilLease.Release()

	row, err := f.q.GetRepoStorage(ctx, id)
	if err != nil || !row.LastAccessAt.Valid || time.Since(row.LastAccessAt.Time) > time.Minute {
		t.Fatalf("last access not touched: %+v %v", row, err)
	}

	if _, err := f.tier.Acquire(ctx, uuid.New(), Read); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown repo: %v", err)
	}
	if _, err := f.tier.Acquire(ctx, id, Exclusive); err == nil {
		t.Fatal("Acquire accepted Exclusive")
	}
	for _, tc := range []struct {
		state string
		want  error
	}{{StateCold, ErrCold}, {StateHydrating, ErrBusy}, {StateEvicting, ErrBusy}, {StateDegraded, ErrDegraded}} {
		f.setState(t, id, StateHot, tc.state)
		if _, err := f.tier.Acquire(ctx, id, Write); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v", tc.state, err)
		}
		f.setState(t, id, tc.state, StateHot)
	}

	// Trash and purge.
	if err := f.tier.Trash(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(want); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("still hot: %v", err)
	}
	if err := f.tier.Trash(ctx, id); err != nil {
		t.Fatalf("trash twice: %v", err)
	}
	if n, err := f.tier.PurgeTrash(ctx, time.Hour); err != nil || n != 0 {
		t.Fatalf("purge young: %d %v", n, err)
	}
	if n, err := f.tier.PurgeTrash(ctx, -time.Second); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	// The hot root is invisible to the blob layer (dot directory).
	var keys []string
	if err := f.store.primary.List(ctx, "", func(i blob.Info) error { keys = append(keys, i.Key); return nil }); err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if !strings.HasPrefix(k, "git/"+id.String()+"/") {
			t.Fatalf("unexpected blob %q", k)
		}
	}
}

func TestTierLocks(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	id := f.repo(t, StateHot)
	if err := f.tier.Init(ctx, id, "main"); err != nil {
		t.Fatal(err)
	}
	short := func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, 150*time.Millisecond) }

	w, err := f.tier.Acquire(ctx, id, Write)
	if err != nil {
		t.Fatal(err)
	}
	// Readers run alongside the writer.
	r, err := f.tier.Acquire(ctx, id, Read)
	if err != nil {
		t.Fatal(err)
	}
	r.Release()
	// A second writer waits.
	c, cancel := short()
	if _, err := f.tier.Acquire(c, id, Write); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second writer: %v", err)
	}
	cancel()
	// So does any other process: the advisory lock is held.
	if ok, err := f.q.TryLockRepoSession(ctx, id); err != nil || ok {
		t.Fatalf("advisory lock free during write: %v %v", ok, err)
	}
	// And a transaction-scoped LockRepoWrite.
	// lock_timeout rather than a cancelled context: pgx abandons a cancelled
	// connection while the backend may still be queued for the lock.
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '100ms'"); err != nil {
		t.Fatal(err)
	}
	if err := f.q.WithTx(tx).LockRepoWrite(ctx, id); err == nil {
		t.Fatal("LockRepoWrite granted during write")
	}
	_ = tx.Rollback(ctx)
	w.Release()

	// After release another session can take it.
	conn, err := f.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	cq := dbgen.New(conn)
	if ok, err := cq.TryLockRepoSession(ctx, id); err != nil || !ok {
		t.Fatalf("advisory lock not released: %v %v", ok, err)
	}
	// ... and Write leases wait for it.
	c, cancel = short()
	if _, err := f.tier.Acquire(c, id, Write); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("writer with foreign lock: %v", err)
	}
	cancel()
	if ok, err := cq.UnlockRepoSession(ctx, id); err != nil || !ok {
		t.Fatal(err)
	}

	// Exclusive waits for readers, and blocks new readers while waiting.
	r, err = f.tier.Acquire(ctx, id, Read)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan *Lease)
	go func() {
		l, err := f.tier.Lock(ctx, id)
		if err != nil {
			t.Error(err)
		}
		got <- l
	}()
	time.Sleep(50 * time.Millisecond)
	select {
	case <-got:
		t.Fatal("exclusive lease granted while a reader holds the repo")
	default:
	}
	c, cancel = short()
	if _, err := f.tier.Acquire(c, id, Read); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("reader overtook a waiting exclusive lease: %v", err)
	}
	cancel()
	r.Release()
	ex := <-got
	if ex == nil || ex.Mode != Exclusive {
		t.Fatalf("exclusive %+v", ex)
	}
	ex.Release()
	if len(f.tier.entries) != 0 {
		t.Fatalf("%d lock entries leaked", len(f.tier.entries))
	}
}

type fakeHydrator struct {
	f     *fixture
	t     *testing.T
	calls atomic.Int32
	fail  bool
}

func (h *fakeHydrator) Hydrate(ctx context.Context, id uuid.UUID) error {
	h.calls.Add(1)
	if h.fail {
		return errors.New("boom")
	}
	l, err := h.f.tier.Lock(ctx, id)
	if err != nil {
		return err
	}
	defer l.Release()
	if err := git.InitBare(ctx, l.Path, "main"); err != nil {
		return err
	}
	_, err = h.f.q.SetRepoStorageState(ctx, dbgen.SetRepoStorageStateParams{ID: id, FromState: StateCold, ToState: StateHot})
	return err
}

func TestTierHydrator(t *testing.T) {
	h := &fakeHydrator{t: t}
	f := newFixture(t, h)
	h.f = f
	ctx := context.Background()
	id := f.repo(t, StateCold)
	l, err := f.tier.Acquire(ctx, id, Read)
	if err != nil {
		t.Fatal(err)
	}
	l.Release()
	if h.calls.Load() != 1 {
		t.Fatalf("%d hydrations", h.calls.Load())
	}
	h.fail = true
	id2 := f.repo(t, StateCold)
	if _, err := f.tier.Acquire(ctx, id2, Read); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("failed hydration: %v", err)
	}
}
