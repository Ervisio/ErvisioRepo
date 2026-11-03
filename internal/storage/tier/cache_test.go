package tier

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"
)

// fillCache registers n entries of size bytes for policy, oldest first.
func fillCache(t *testing.T, c *Cache, policy string, n int, size int64, start time.Time) []string {
	t.Helper()
	var keys []string
	for i := range n {
		key := fmt.Sprintf("%s/k%03d", policy, i)
		if _, err := c.store.Put(context.Background(), key, bytes.NewReader(make([]byte, size)), size, blobOpts()); err != nil {
			t.Fatal(err)
		}
		c.add(cacheEntry{key: key, policy: policy, size: size, lastAccess: start.Add(time.Duration(i) * time.Second)})
		keys = append(keys, key)
	}
	return keys
}

func newTestCache(budget int64) (*Cache, *memStore) {
	s := newMemStore("cache")
	return newCache("cache", s, budget, 0.9, 0.5, slog.New(slog.DiscardHandler)), s
}

func TestCacheWatermarks(t *testing.T) {
	c, s := newTestCache(1000)
	now := time.Unix(1000, 0)
	keys := fillCache(t, c, "lfs", 9, 100, now) // 900 = high watermark, no eviction
	if n, _, err := c.Evict(context.Background(), now); err != nil || n != 0 {
		t.Fatalf("evicted %d at the high watermark (%v)", n, err)
	}
	// Touch the oldest one: it becomes the most recently used.
	c.lookup(keys[0], now)
	fillCache(t, c, "lfsx", 1, 100, now) // 1000 > 900
	n, b, err := c.Evict(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 || b != 500 || c.Stats().Used != 500 {
		t.Fatalf("evicted %d objects / %d bytes, used %d", n, b, c.Stats().Used)
	}
	if !s.has(keys[0]) {
		t.Fatal("recently used entry evicted")
	}
	for _, k := range keys[1:6] {
		if s.has(k) {
			t.Fatalf("LRU entry %s kept", k)
		}
	}
}

func TestCacheTTL(t *testing.T) {
	c, s := newTestCache(0) // unlimited budget: only the TTL applies
	c.ttls["lfs"] = time.Hour
	now := time.Unix(10_000, 0)
	old := fillCache(t, c, "lfs", 2, 10, now.Add(-2*time.Hour))
	fresh := fillCache(t, c, "git", 1, 10, now.Add(-2*time.Hour)) // no TTL for git
	recent := "lfs/recent"
	_, _ = s.Put(context.Background(), recent, bytes.NewReader(make([]byte, 10)), 10, blobOpts())
	c.add(cacheEntry{key: recent, policy: "lfs", size: 10, lastAccess: now.Add(-time.Minute)})

	n, _, err := c.Evict(context.Background(), now)
	if err != nil || n != 2 {
		t.Fatalf("evicted %d (%v)", n, err)
	}
	for _, k := range old {
		if s.has(k) {
			t.Fatalf("expired %s kept", k)
		}
	}
	if !s.has(fresh[0]) || !s.has(recent) {
		t.Fatal("live entries evicted")
	}
}

func TestCacheShares(t *testing.T) {
	c, s := newTestCache(1000)
	c.shares["registry"] = 0.2 // 200 bytes of its own; the pool gets 800
	c.shares["lfs"] = 0
	now := time.Unix(1000, 0)
	reg := fillCache(t, c, "registry", 3, 100, now) // 300 > 0.9*200
	lfs := fillCache(t, c, "lfs", 6, 100, now)      // 600 <= 0.9*800

	if _, _, err := c.Evict(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	st := c.Stats()
	if st.UsedBy["registry"] != 100 || st.UsedBy["lfs"] != 600 {
		t.Fatalf("usage = %v", st.UsedBy)
	}
	if !s.has(reg[2]) || s.has(reg[0]) || s.has(reg[1]) {
		t.Fatal("registry: wrong victims")
	}
	for _, k := range lfs {
		if !s.has(k) {
			t.Fatal("lfs entries must not pay for registry")
		}
	}
	// Pool above its watermark: only pool entries go.
	fillCache(t, c, "git", 2, 100, now.Add(time.Hour)) // pool 800 > 720
	if _, _, err := c.Evict(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	st = c.Stats()
	if st.UsedBy["registry"] != 100 || st.UsedBy["lfs"]+st.UsedBy["git"] > 400 {
		t.Fatalf("usage after pool eviction = %v", st.UsedBy)
	}
}

func TestCacheAdmits(t *testing.T) {
	c, _ := newTestCache(1000)
	c.shares["registry"] = 0.2
	if !c.admits("lfs", 400) || c.admits("lfs", 401) {
		t.Fatal("pool admission wrong (low watermark of 800)")
	}
	if !c.admits("registry", 100) || c.admits("registry", 101) {
		t.Fatal("share admission wrong (low watermark of 200)")
	}
	u, _ := newTestCache(0)
	if !u.admits("x", 1<<40) {
		t.Fatal("unlimited cache must admit everything")
	}
}

func TestCacheMisconfiguredShares(t *testing.T) {
	c, _ := newTestCache(1000)
	c.shares["a"], c.shares["b"] = 0.7, 0.6
	if c.poolBudget() != 0 || c.policyBudget("other") != 0 {
		t.Fatal("shares over 1 leave no pool")
	}
	// The global budget still applies.
	fillCache(t, c, "other", 10, 100, time.Unix(0, 0))
	if _, _, err := c.Evict(context.Background(), time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	if c.Stats().Used > 500 {
		t.Fatalf("used %d", c.Stats().Used)
	}
}

func TestFillerAbort(t *testing.T) {
	c, s := newTestCache(0)
	f := c.startFill(context.Background(), cacheEntry{key: "lfs/x", policy: "lfs", size: 10})
	_, _ = f.Write([]byte("01234"))
	f.finish(context.Background(), false, time.Now())
	if s.has("lfs/x") || c.Stats().Entries != 0 {
		t.Fatal("aborted fill left state")
	}
	// Complete stream but not ok (verification failed after the last byte).
	f = c.startFill(context.Background(), cacheEntry{key: "lfs/y", policy: "lfs", size: 3})
	_, _ = f.Write([]byte("abc"))
	f.finish(context.Background(), false, time.Now())
	f.finish(context.Background(), true, time.Now()) // idempotent
	if s.has("lfs/y") || c.Stats().Entries != 0 {
		t.Fatal("unverified fill kept")
	}
}

func TestFillerStoreFailure(t *testing.T) {
	c, s := newTestCache(0)
	s.failAfter.Store(2)
	f := c.startFill(context.Background(), cacheEntry{key: "lfs/x", policy: "lfs", size: 10})
	for range 10 {
		if _, err := f.Write([]byte("0")); err != nil {
			t.Fatal("cache failure must not fail the main stream")
		}
	}
	f.finish(context.Background(), true, time.Now())
	if s.has("lfs/x") || c.Stats().Entries != 0 {
		t.Fatal("failed fill registered")
	}
}
