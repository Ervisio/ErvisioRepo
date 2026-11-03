package tier

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type env struct {
	tier    *Tier
	db      *fakeDB
	clock   *clock
	cache   *memStore
	remote  *memStore
	replica *memStore

	mu     sync.Mutex
	stored []ObjectRef
}

func (e *env) storedRefs() []ObjectRef {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]ObjectRef(nil), e.stored...)
}

// newEnv builds a tier with a local cache, a remote primary and a replica.
// The "lfs" policy uses all three; "git" has no replica and no cache.
func newEnv(t *testing.T, mod func(*config.Storage)) *env {
	t.Helper()
	e := &env{
		clock:   &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		cache:   newMemStore("cache"),
		remote:  newMemStore("remote"),
		replica: newMemStore("replica"),
	}
	e.db = newFakeDB(e.clock.now)
	cfg := config.Storage{
		Targets: []config.Target{
			{Name: "cache", Kind: "local", Path: "/unused", Budget: 1 << 20, HighWatermark: 0.9, LowWatermark: 0.5},
			{Name: "remote", Kind: "s3", Encrypt: true},
			{Name: "replica", Kind: "sftp"},
		},
		Policy: map[string]config.Policy{
			"lfs": {Primary: "remote", Replicas: []string{"replica"}, Cache: "cache", CacheTTL: config.Duration(24 * time.Hour)},
			"git": {Primary: "remote"},
		},
	}
	if mod != nil {
		mod(&cfg)
	}
	tr, err := New(Options{
		Config:  cfg,
		Stores:  map[string]blob.Store{"cache": e.cache, "remote": e.remote, "replica": e.replica},
		Queries: e.db,
		Now:     e.clock.now,
		OnStored: func(_ context.Context, ref ObjectRef) error {
			e.mu.Lock()
			e.stored = append(e.stored, ref)
			e.mu.Unlock()
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	e.tier = tr
	return e
}

func (e *env) class(t *testing.T, name string) *Class {
	t.Helper()
	c, err := e.tier.Class(name)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func content(n int, seed byte) ([]byte, []byte) {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7) + seed
	}
	s := sha256.Sum256(b)
	return b, s[:]
}

func put(t *testing.T, c *Class, key string, data, sum []byte) Object {
	t.Helper()
	obj, err := c.Put(context.Background(), key, bytes.NewReader(data), int64(len(data)), sum)
	if err != nil {
		t.Fatalf("Put %s: %v", key, err)
	}
	return obj
}

func read(t *testing.T, c *Class, key string, rng *blob.Range) []byte {
	t.Helper()
	rc, _, err := c.Get(context.Background(), key, rng)
	if err != nil {
		t.Fatalf("Get %s: %v", key, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	return b
}

func cached(c *Class, full string) bool {
	_, ok := c.cache.lookup(full, time.Time{})
	return ok
}

func TestPutGet(t *testing.T) {
	e := newEnv(t, nil)
	lfs := e.class(t, "lfs")
	data, sum := content(10_000, 1)
	obj := put(t, lfs, "sha256/ab/obj1", data, sum)

	if obj.Key != "lfs/sha256/ab/obj1" || obj.Target != "remote" || obj.Size != 10_000 || !bytes.Equal(obj.SHA256, sum) || obj.ReplicasOK {
		t.Fatalf("object = %+v", obj)
	}
	if !e.remote.has(obj.Key) || !e.cache.has(obj.Key) || e.replica.has(obj.Key) {
		t.Fatal("object must be on primary and cache only")
	}
	if !cached(lfs, obj.Key) {
		t.Fatal("cache entry not registered")
	}
	if refs := e.storedRefs(); len(refs) != 1 || refs[0] != (ObjectRef{Target: "remote", Key: obj.Key}) {
		t.Fatalf("OnStored calls = %v", refs)
	}

	gets := e.remote.gets.Load()
	if got := read(t, lfs, "sha256/ab/obj1", nil); !bytes.Equal(got, data) {
		t.Fatal("cached read returned wrong bytes")
	}
	if e.remote.gets.Load() != gets {
		t.Fatal("cache hit must not read the primary")
	}
	if got := read(t, lfs, "sha256/ab/obj1", &blob.Range{Offset: 10, Length: 5}); !bytes.Equal(got, data[10:15]) {
		t.Fatal("range read returned wrong bytes")
	}
	st, err := lfs.Stat(context.Background(), "sha256/ab/obj1")
	if err != nil || st.Size != 10_000 {
		t.Fatalf("Stat = %+v, %v", st, err)
	}
}

func TestPutWithoutSHA(t *testing.T) {
	e := newEnv(t, nil)
	git := e.class(t, "git")
	data, sum := content(100, 2)
	obj, err := git.Put(context.Background(), "x", bytes.NewReader(data), 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(obj.SHA256, sum) || !obj.ReplicasOK {
		t.Fatalf("object = %+v", obj)
	}
	if len(e.storedRefs()) != 0 {
		t.Fatal("policy without replicas must not enqueue replication")
	}
}

// Acceptance: an object deleted from the cache is served by the primary
// transparently and the cache is refilled.
func TestCacheLossFallsBackToPrimary(t *testing.T) {
	e := newEnv(t, nil)
	lfs := e.class(t, "lfs")
	data, sum := content(50_000, 3)
	obj := put(t, lfs, "o", data, sum)

	// Removed behind the tier's back (disk wiped, manual cleanup...).
	if err := e.cache.Delete(context.Background(), obj.Key); err != nil {
		t.Fatal(err)
	}
	gets := e.remote.gets.Load()
	if got := read(t, lfs, "o", nil); !bytes.Equal(got, data) {
		t.Fatal("fallback read returned wrong bytes")
	}
	if e.remote.gets.Load() != gets+1 {
		t.Fatal("expected one primary read")
	}
	if !e.cache.has(obj.Key) || !cached(lfs, obj.Key) {
		t.Fatal("cache not refilled")
	}
	// Next read is a cache hit again.
	gets = e.remote.gets.Load()
	read(t, lfs, "o", nil)
	if e.remote.gets.Load() != gets {
		t.Fatal("refilled cache not used")
	}

	// Explicit eviction behaves the same way.
	if err := lfs.EvictCached(context.Background(), "o"); err != nil {
		t.Fatal(err)
	}
	if e.cache.has(obj.Key) || cached(lfs, obj.Key) {
		t.Fatal("EvictCached left the object")
	}
	if got := read(t, lfs, "o", nil); !bytes.Equal(got, data) {
		t.Fatal("read after eviction returned wrong bytes")
	}
	if !cached(lfs, obj.Key) {
		t.Fatal("cache not refilled after eviction")
	}
}

func TestCacheReadErrorFallsBack(t *testing.T) {
	e := newEnv(t, nil)
	lfs := e.class(t, "lfs")
	data, sum := content(1000, 4)
	put(t, lfs, "o", data, sum)
	e.cache.setGetErr(func(string) error { return errors.New("disk on fire") })
	if got := read(t, lfs, "o", nil); !bytes.Equal(got, data) {
		t.Fatal("wrong bytes")
	}
}

func TestReadExactSizeFillsCache(t *testing.T) {
	e := newEnv(t, nil)
	lfs := e.class(t, "lfs")
	data, sum := content(4096, 5)
	obj := put(t, lfs, "o", data, sum)
	if err := lfs.EvictCached(context.Background(), "o"); err != nil {
		t.Fatal(err)
	}
	rc, _, err := lfs.Get(context.Background(), "o", nil)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(data))
	if _, err := io.ReadFull(rc, buf); err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	if !cached(lfs, obj.Key) {
		t.Fatal("reading exactly size bytes must fill the cache")
	}
}

func TestPartialReadDoesNotFillCache(t *testing.T) {
	e := newEnv(t, nil)
	lfs := e.class(t, "lfs")
	data, sum := content(4096, 6)
	obj := put(t, lfs, "o", data, sum)
	_ = lfs.EvictCached(context.Background(), "o")
	rc, _, err := lfs.Get(context.Background(), "o", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadFull(rc, make([]byte, 100))
	_ = rc.Close()
	if cached(lfs, obj.Key) || e.cache.has(obj.Key) {
		t.Fatal("partial read must not leave a cache entry")
	}
}

// assertNothingStored checks the invariant of an interrupted upload: no
// object on any target and no row.
func assertNothingStored(t *testing.T, e *env, full string) {
	t.Helper()
	if e.remote.has(full) || e.cache.has(full) || e.replica.has(full) {
		t.Fatalf("partial object left behind: remote=%v cache=%v replica=%v",
			e.remote.has(full), e.cache.has(full), e.replica.has(full))
	}
	if _, ok := e.db.row("remote", full); ok {
		t.Fatal("row recorded for a failed upload")
	}
	if c, _ := e.tier.Class("lfs"); cached(c, full) {
		t.Fatal("cache entry registered for a failed upload")
	}
	if len(e.storedRefs()) != 0 {
		t.Fatal("replication enqueued for a failed upload")
	}
}

// Acceptance: an interrupted upload leaves no corrupt object and no row.
func TestPutInterrupted(t *testing.T) {
	data, sum := content(200_000, 7)
	cases := map[string]func(e *env) (io.Reader, context.Context, []byte){
		"network failure on primary": func(e *env) (io.Reader, context.Context, []byte) {
			e.remote.failAfter.Store(50_000)
			return bytes.NewReader(data), context.Background(), sum
		},
		"client disconnects": func(*env) (io.Reader, context.Context, []byte) {
			return io.MultiReader(bytes.NewReader(data[:100_000]), &errReader{errors.New("client gone")}), context.Background(), sum
		},
		"short body": func(*env) (io.Reader, context.Context, []byte) {
			return bytes.NewReader(data[:100_000]), context.Background(), sum
		},
		"long body": func(*env) (io.Reader, context.Context, []byte) {
			return io.MultiReader(bytes.NewReader(data), bytes.NewReader([]byte("x"))), context.Background(), sum
		},
		"checksum mismatch": func(*env) (io.Reader, context.Context, []byte) {
			bad := bytes.Clone(data)
			bad[len(bad)-1] ^= 1
			return bytes.NewReader(bad), context.Background(), sum
		},
		"context cancelled": func(*env) (io.Reader, context.Context, []byte) {
			ctx, cancel := context.WithCancel(context.Background())
			return io.MultiReader(bytes.NewReader(data[:1000]), &cancelReader{cancel: cancel}, bytes.NewReader(data[1000:])), ctx, sum
		},
		"driver ignores the stream": func(e *env) (io.Reader, context.Context, []byte) {
			e.remote.lazyPut.Store(true)
			return bytes.NewReader(data), context.Background(), nil
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, nil)
			lfs := e.class(t, "lfs")
			r, ctx, want := setup(e)
			if _, err := lfs.Put(ctx, "o", r, int64(len(data)), want); err == nil {
				t.Fatal("Put succeeded")
			}
			assertNothingStored(t, e, "lfs/o")
			if _, err := lfs.Stat(context.Background(), "o"); !errors.Is(err, blob.ErrNotFound) {
				t.Fatalf("Stat = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestPutRecordFailureLeavesNoRow(t *testing.T) {
	e := newEnv(t, nil)
	lfs := e.class(t, "lfs")
	e.db.upsertErr = errors.New("db down")
	data, sum := content(1000, 8)
	if _, err := lfs.Put(context.Background(), "o", bytes.NewReader(data), 1000, sum); err == nil {
		t.Fatal("Put succeeded")
	}
	if e.db.count() != 0 || cached(lfs, "lfs/o") || e.cache.has("lfs/o") {
		t.Fatal("failed record left state behind")
	}
	// A retry overwrites the unrecorded object and succeeds.
	e.db.upsertErr = nil
	put(t, lfs, "o", data, sum)
}

func TestPutDeduplicates(t *testing.T) {
	e := newEnv(t, nil)
	lfs := e.class(t, "lfs")
	data, sum := content(1000, 9)
	put(t, lfs, "o", data, sum)
	puts := e.remote.puts.Load()
	if _, err := lfs.Put(context.Background(), "o", &errReader{errors.New("must not be read")}, 1000, sum); err != nil {
		t.Fatal(err)
	}
	if e.remote.puts.Load() != puts {
		t.Fatal("duplicate content uploaded again")
	}
}

func TestPutRejectsBadInput(t *testing.T) {
	e := newEnv(t, nil)
	lfs := e.class(t, "lfs")
	for _, key := range []string{"", "../x", "a//b", ".hidden", "a b"} {
		if _, err := lfs.Put(context.Background(), key, bytes.NewReader(nil), 0, nil); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("key %q: err = %v", key, err)
		}
	}
	if _, err := lfs.Put(context.Background(), "o", bytes.NewReader(nil), -1, nil); err == nil {
		t.Error("unknown size accepted")
	}
	if _, err := lfs.Put(context.Background(), "o", bytes.NewReader(nil), 0, []byte{1}); err == nil {
		t.Error("short sha256 accepted")
	}
	if _, err := e.tier.Class("nope"); !errors.Is(err, ErrUnknownPolicy) {
		t.Errorf("unknown policy: %v", err)
	}
}

func TestZeroByteObject(t *testing.T) {
	e := newEnv(t, nil)
	lfs := e.class(t, "lfs")
	_, sum := content(0, 0)
	put(t, lfs, "empty", nil, sum)
	if got := read(t, lfs, "empty", nil); len(got) != 0 {
		t.Fatal("non-empty read")
	}
}

func TestGetMissing(t *testing.T) {
	e := newEnv(t, nil)
	if _, _, err := e.class(t, "lfs").Get(context.Background(), "nope", nil); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestGetDetectsCorruptPrimary(t *testing.T) {
	e := newEnv(t, nil)
	git := e.class(t, "git")
	data, sum := content(5000, 10)
	obj := put(t, git, "o", data, sum)
	e.remote.corrupt(obj.Key)
	rc, _, err := git.Get(context.Background(), "o", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if _, err := io.ReadAll(rc); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("read err = %v, want ErrCorrupt", err)
	}
}

func TestCorruptRemoteNotCached(t *testing.T) {
	e := newEnv(t, nil)
	lfs := e.class(t, "lfs")
	data, sum := content(5000, 11)
	obj := put(t, lfs, "o", data, sum)
	_ = lfs.EvictCached(context.Background(), "o")
	e.remote.corrupt(obj.Key)
	rc, _, err := lfs.Get(context.Background(), "o", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, rerr := io.ReadAll(rc)
	_ = rc.Close()
	if !errors.Is(rerr, ErrCorrupt) {
		t.Fatalf("read err = %v", rerr)
	}
	if cached(lfs, obj.Key) || e.cache.has(obj.Key) {
		t.Fatal("corrupt bytes cached")
	}
}

func TestReplicateAndReplicaFallback(t *testing.T) {
	e := newEnv(t, nil)
	lfs := e.class(t, "lfs")
	data, sum := content(30_000, 12)
	obj := put(t, lfs, "o", data, sum)
	ctx := context.Background()

	ref := e.storedRefs()[0]
	if err := e.tier.Replicate(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.db.row("remote", obj.Key); !r.ReplicasOk {
		t.Fatal("row not marked replicated")
	}
	if !e.replica.has(obj.Key) {
		t.Fatal("replica missing")
	}
	// Idempotent: the existing replica is kept.
	puts := e.replica.puts.Load()
	if err := e.tier.Replicate(ctx, ref); err != nil || e.replica.puts.Load() != puts {
		t.Fatalf("second replicate: %v, puts %d -> %d", err, puts, e.replica.puts.Load())
	}
	// Deleted objects are a no-op.
	if err := e.tier.Replicate(ctx, ObjectRef{Target: "remote", Key: "lfs/gone"}); err != nil {
		t.Fatal(err)
	}

	// Primary lost and cache empty: served from the replica.
	_ = lfs.EvictCached(ctx, "o")
	_ = e.remote.Delete(ctx, obj.Key)
	if got := read(t, lfs, "o", nil); !bytes.Equal(got, data) {
		t.Fatal("replica fallback returned wrong bytes")
	}
}

func TestReplicateFailureRecorded(t *testing.T) {
	e := newEnv(t, nil)
	lfs := e.class(t, "lfs")
	data, sum := content(30_000, 13)
	obj := put(t, lfs, "o", data, sum)
	e.replica.failAfter.Store(100)
	if err := e.tier.Replicate(context.Background(), e.storedRefs()[0]); err == nil {
		t.Fatal("Replicate succeeded")
	}
	r, _ := e.db.row("remote", obj.Key)
	if r.ReplicasOk || !r.LastError.Valid {
		t.Fatalf("row = %+v", r)
	}
	if e.replica.has(obj.Key) {
		t.Fatal("partial replica left")
	}

	// Corrupt source: nothing is written to the replica.
	e.replica.failAfter.Store(-1)
	e.remote.corrupt(obj.Key)
	if err := e.tier.Replicate(context.Background(), e.storedRefs()[0]); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}
	if e.replica.has(obj.Key) {
		t.Fatal("corrupt replica written")
	}

	// The sweep finds it.
	e.clock.advance(time.Hour)
	refs, err := e.tier.PendingReplicas(context.Background(), ts(e.clock.now()), 10)
	if err != nil || len(refs) != 1 || refs[0].Key != obj.Key {
		t.Fatalf("PendingReplicas = %v, %v", refs, err)
	}
	st, err := e.tier.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range st.Policies {
		if p.Name == "lfs" && (p.Objects != 1 || p.PendingReplicas != 1 || p.Errors != 1) {
			t.Fatalf("lfs status = %+v", p)
		}
	}
}

func TestVerifySample(t *testing.T) {
	e := newEnv(t, nil)
	lfs := e.class(t, "lfs")
	ctx := context.Background()
	objs := map[string][]byte{}
	for i := range 3 {
		data, sum := content(10_000, byte(20+i))
		obj := put(t, lfs, fmt.Sprintf("o%d", i), data, sum)
		objs[obj.Key] = data
	}
	for _, ref := range e.storedRefs() {
		if err := e.tier.Replicate(ctx, ref); err != nil {
			t.Fatal(err)
		}
	}
	e.mu.Lock()
	e.stored = nil
	e.mu.Unlock()

	e.remote.corrupt("lfs/o0")  // primary repaired from the replica
	e.replica.corrupt("lfs/o1") // replica queued again
	e.clock.advance(8 * 24 * time.Hour)
	cutoff := ts(e.clock.now().Add(-6 * 24 * time.Hour))

	rep, err := e.tier.VerifySample(ctx, cutoff, 10)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Checked != 3 || rep.Repaired != 1 || rep.Replicas != 1 || rep.Failed != 0 || rep.OK != 2 {
		t.Fatalf("report = %+v", rep)
	}
	if err := hashObject(ctx, e.tier.targets["remote"], "lfs/o0", 10_000, mustRow(t, e, "lfs/o0").Sha256); err != nil {
		t.Fatalf("primary not repaired: %v", err)
	}
	if r := mustRow(t, e, "lfs/o1"); r.ReplicasOk || !r.LastError.Valid {
		t.Fatalf("bad replica not flagged: %+v", r)
	}
	if refs := e.storedRefs(); len(refs) != 1 || refs[0].Key != "lfs/o1" {
		t.Fatalf("re-replication enqueued for %v", refs)
	}
	// Every object was verified: the next run with the same cutoff is empty.
	rep, err = e.tier.VerifySample(ctx, cutoff, 10)
	if err != nil || rep.Checked != 0 {
		t.Fatalf("second run = %+v, %v", rep, err)
	}

	// Primary and replica both bad: reported as failed.
	e.remote.corrupt("lfs/o2")
	e.replica.corrupt("lfs/o2")
	e.clock.advance(8 * 24 * time.Hour)
	rep, err = e.tier.VerifySample(ctx, ts(e.clock.now().Add(-6*24*time.Hour)), 10)
	if err != nil || rep.Failed != 1 {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if r := mustRow(t, e, "lfs/o2"); !r.LastError.Valid {
		t.Fatal("failure not recorded")
	}
}

func mustRow(t *testing.T, e *env, key string) (r struct {
	Sha256     []byte
	ReplicasOk bool
	LastError  pgtype.Text
}) {
	t.Helper()
	row, ok := e.db.row("remote", key)
	if !ok {
		t.Fatalf("no row for %s", key)
	}
	r.Sha256, r.ReplicasOk, r.LastError = row.Sha256, row.ReplicasOk, row.LastError
	return r
}

func TestDelete(t *testing.T) {
	e := newEnv(t, nil)
	lfs := e.class(t, "lfs")
	data, sum := content(1000, 30)
	obj := put(t, lfs, "o", data, sum)
	if err := e.tier.Replicate(context.Background(), e.storedRefs()[0]); err != nil {
		t.Fatal(err)
	}
	if err := lfs.Delete(context.Background(), "o"); err != nil {
		t.Fatal(err)
	}
	if e.remote.has(obj.Key) || e.cache.has(obj.Key) || e.replica.has(obj.Key) || e.db.count() != 0 || cached(lfs, obj.Key) {
		t.Fatal("delete left state behind")
	}
	if err := lfs.Delete(context.Background(), "o"); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}

func TestLoadCaches(t *testing.T) {
	e := newEnv(t, nil)
	lfs := e.class(t, "lfs")
	ctx := context.Background()
	data, sum := content(1000, 31)
	obj := put(t, lfs, "o", data, sum)
	// Leftovers of a crash: an unrecorded object and a truncated one.
	_, _ = e.cache.Put(ctx, "lfs/orphan", bytes.NewReader([]byte("x")), 1, blob.PutOpts{})
	data2, sum2 := content(1000, 32)
	obj2 := put(t, lfs, "o2", data2, sum2)
	_, _ = e.cache.Put(ctx, obj2.Key, bytes.NewReader(data2[:10]), 10, blob.PutOpts{})
	// Unrelated data on the same target is ignored.
	_, _ = e.cache.Put(ctx, "other/file", bytes.NewReader([]byte("x")), 1, blob.PutOpts{})

	// A fresh process rebuilds the index.
	e2 := newEnv(t, nil)
	e2.db, e2.cache, e2.remote = e.db, e.cache, e.remote
	tr, err := New(Options{Config: e.tier.cfg, Stores: map[string]blob.Store{"cache": e.cache, "remote": e.remote, "replica": e.replica}, Queries: e.db, Now: e.clock.now})
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.LoadCaches(ctx); err != nil {
		t.Fatal(err)
	}
	c2, _ := tr.Class("lfs")
	if !cached(c2, obj.Key) || cached(c2, obj2.Key) || cached(c2, "lfs/orphan") {
		t.Fatal("wrong entries loaded")
	}
	if e.cache.has("lfs/orphan") || e.cache.has(obj2.Key) || !e.cache.has("other/file") {
		t.Fatal("stale entries not cleaned")
	}
	if st := c2.cache.Stats(); st.Used != 1000 || st.Entries != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestCacheSameAsPrimaryIgnored(t *testing.T) {
	e := newEnv(t, func(s *config.Storage) {
		s.Policy["local"] = config.Policy{Primary: "cache", Cache: "cache"}
	})
	if e.class(t, "local").cache != nil {
		t.Fatal("cache equal to primary must be ignored")
	}
}

func TestNewErrors(t *testing.T) {
	stores := map[string]blob.Store{"a": newMemStore("a")}
	cfg := config.Storage{Targets: []config.Target{{Name: "a", Kind: "local"}}}
	for name, p := range map[string]config.Policy{
		"unknown primary": {Primary: "x"},
		"unknown replica": {Primary: "a", Replicas: []string{"x"}},
		"unknown cache":   {Primary: "a", Cache: "x"},
	} {
		cfg.Policy = map[string]config.Policy{"p": p}
		if _, err := New(Options{Config: cfg, Stores: stores, Queries: newFakeDB(time.Now)}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	cfg.Policy = nil
	if _, err := New(Options{Config: cfg, Stores: map[string]blob.Store{}, Queries: newFakeDB(time.Now)}); err == nil {
		t.Error("missing store accepted")
	}
	if _, err := New(Options{Config: cfg, Stores: stores}); err == nil {
		t.Error("missing queries accepted")
	}
}

func TestConcurrentPutGet(t *testing.T) {
	e := newEnv(t, func(s *config.Storage) { s.Targets[0].Budget = 64 << 10 })
	lfs := e.class(t, "lfs")
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, sum := content(8000, byte(i))
			key := fmt.Sprintf("o%d", i)
			if _, err := lfs.Put(context.Background(), key, bytes.NewReader(data), int64(len(data)), sum); err != nil {
				t.Error(err)
				return
			}
			for range 5 {
				rc, _, err := lfs.Get(context.Background(), key, nil)
				if err != nil {
					t.Error(err)
					return
				}
				got, err := io.ReadAll(rc)
				_ = rc.Close()
				if err != nil || !bytes.Equal(got, data) {
					t.Errorf("%s: %v", key, err)
				}
			}
		}()
	}
	wg.Wait()
	if st := lfs.cache.Stats(); st.Used > 64<<10 {
		t.Fatalf("cache over budget: %+v", st)
	}
}

type errReader struct{ err error }

func (r *errReader) Read([]byte) (int, error) { return 0, r.err }

type cancelReader struct{ cancel func() }

func (r *cancelReader) Read([]byte) (int, error) {
	r.cancel()
	return 0, context.Canceled
}
