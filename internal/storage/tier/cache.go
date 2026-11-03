package tier

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

// Cache is the local LRU cache kept on one local target (PLAN §4.2). It is
// shared by every policy that names the target as its cache.
//
// Only objects already confirmed on their primary are ever registered, so
// any entry can be evicted at any time without losing data. The index lives
// in memory and is rebuilt from the target and the storage_objects table at
// start (see Tier.LoadCaches).
type Cache struct {
	name   string
	store  blob.Store
	budget int64 // 0 means unlimited (only the TTL applies)
	high   float64
	low    float64
	log    *slog.Logger

	shares map[string]float64       // policy -> share of the budget (0 = common pool)
	ttls   map[string]time.Duration // policy -> cache_ttl (0 = no TTL)

	mu      sync.Mutex
	lru     *list.List // front is the most recently used
	entries map[string]*list.Element
	used    int64
	usedBy  map[string]int64 // policy -> bytes
}

type cacheEntry struct {
	key        string
	policy     string
	size       int64
	sha256     []byte
	lastAccess time.Time
}

func newCache(name string, store blob.Store, budget int64, high, low float64, log *slog.Logger) *Cache {
	return &Cache{
		name: name, store: store, budget: budget, high: high, low: low, log: log,
		shares: map[string]float64{}, ttls: map[string]time.Duration{},
		lru: list.New(), entries: map[string]*list.Element{}, usedBy: map[string]int64{},
	}
}

// Name is the cache target name.
func (c *Cache) Name() string { return c.name }

// policyBudget is the byte budget of one policy: its share of the budget, or
// what the shares leave to the common pool. 0 means unlimited.
func (c *Cache) policyBudget(policy string) int64 {
	if c.budget == 0 {
		return 0
	}
	if s := c.shares[policy]; s > 0 {
		return int64(s * float64(c.budget))
	}
	return c.poolBudget()
}

func (c *Cache) poolBudget() int64 {
	sum := 0.0
	for _, s := range c.shares {
		sum += s
	}
	if sum >= 1 {
		return 0 // misconfigured shares leave nothing to the pool; fall back to the global budget
	}
	return int64((1 - sum) * float64(c.budget))
}

// admits reports whether an object of size bytes may be cached for policy.
func (c *Cache) admits(policy string, size int64) bool {
	b := c.policyBudget(policy)
	if b == 0 {
		b = c.budget
	}
	return b == 0 || size <= int64(c.low*float64(b))
}

// lookup returns the entry for key and marks it used.
func (c *Cache) lookup(key string, now time.Time) (cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		return cacheEntry{}, false
	}
	e := el.Value.(*cacheEntry)
	e.lastAccess = now
	c.lru.MoveToFront(el)
	return *e, true
}

// add registers an object written to the cache store.
func (c *Cache) add(e cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[e.key]; ok {
		c.removeLocked(el)
	}
	el := c.lru.PushFront(&e)
	c.entries[e.key] = el
	c.used += e.size
	c.usedBy[e.policy] += e.size
}

// addLoaded registers an entry found at start, keeping LRU order by time.
func (c *Cache) addLoaded(entries []cacheEntry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].lastAccess.Before(entries[j].lastAccess) })
	for _, e := range entries {
		c.add(e)
	}
}

func (c *Cache) forget(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		c.removeLocked(el)
	}
}

func (c *Cache) removeLocked(el *list.Element) {
	e := el.Value.(*cacheEntry)
	c.lru.Remove(el)
	delete(c.entries, e.key)
	c.used -= e.size
	c.usedBy[e.policy] -= e.size
}

// CacheStats is a snapshot of the cache usage.
type CacheStats struct {
	Target        string
	Budget        int64
	Used          int64
	Entries       int
	HighWatermark float64
	LowWatermark  float64
	UsedBy        map[string]int64
}

// Stats returns the current usage.
func (c *Cache) Stats() CacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	by := make(map[string]int64, len(c.usedBy))
	for p, n := range c.usedBy {
		if n != 0 {
			by[p] = n
		}
	}
	return CacheStats{Target: c.name, Budget: c.budget, Used: c.used, Entries: len(c.entries),
		HighWatermark: c.high, LowWatermark: c.low, UsedBy: by}
}

// victims selects and unregisters the entries to evict: every entry past its
// policy TTL, then, for each budget above its high watermark, least recently
// used entries until usage is at or below the low watermark.
func (c *Cache) victims(now time.Time) []cacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []cacheEntry
	take := func(el *list.Element) {
		e := el.Value.(*cacheEntry)
		out = append(out, *e)
		c.removeLocked(el)
	}

	for el := c.lru.Back(); el != nil; {
		prev := el.Prev()
		e := el.Value.(*cacheEntry)
		if ttl := c.ttls[e.policy]; ttl > 0 && now.Sub(e.lastAccess) > ttl {
			take(el)
		}
		el = prev
	}
	if c.budget == 0 {
		return out
	}

	// Policies with their own share.
	for policy, share := range c.shares {
		if share <= 0 {
			continue
		}
		b := float64(c.policyBudget(policy))
		if float64(c.usedBy[policy]) <= c.high*b {
			continue
		}
		for el := c.lru.Back(); el != nil && float64(c.usedBy[policy]) > c.low*b; {
			prev := el.Prev()
			if el.Value.(*cacheEntry).policy == policy {
				take(el)
			}
			el = prev
		}
	}
	// The common pool, shared by policies without a share.
	if pool := float64(c.poolBudget()); pool > 0 {
		inPool := func(p string) bool { return c.shares[p] <= 0 }
		poolUsed := func() (n int64) {
			for p, u := range c.usedBy {
				if inPool(p) {
					n += u
				}
			}
			return n
		}
		if float64(poolUsed()) > c.high*pool {
			for el := c.lru.Back(); el != nil && float64(poolUsed()) > c.low*pool; {
				prev := el.Prev()
				if inPool(el.Value.(*cacheEntry).policy) {
					take(el)
				}
				el = prev
			}
		}
	}
	// The whole budget, whatever the shares say.
	if float64(c.used) > c.high*float64(c.budget) {
		for el := c.lru.Back(); el != nil && float64(c.used) > c.low*float64(c.budget); {
			prev := el.Prev()
			take(el)
			el = prev
		}
	}
	return out
}

// Evict removes expired entries and enforces the watermarks. It returns the
// number of objects and bytes removed from the cache store.
func (c *Cache) Evict(ctx context.Context, now time.Time) (int, int64, error) {
	var (
		n     int
		bytes int64
		errs  []error
	)
	for _, e := range c.victims(now) {
		if err := c.store.Delete(ctx, e.key); err != nil && !errors.Is(err, blob.ErrNotFound) {
			errs = append(errs, fmt.Errorf("evict %s from %s: %w", e.key, c.name, err))
			continue
		}
		n++
		bytes += e.size
	}
	if n > 0 {
		c.log.DebugContext(ctx, "cache eviction", "target", c.name, "objects", n, "bytes", bytes)
	}
	return n, bytes, errors.Join(errs...)
}

// drop removes one object from the cache index and store.
func (c *Cache) drop(ctx context.Context, key string) error {
	c.forget(key)
	if err := c.store.Delete(ctx, key); err != nil && !errors.Is(err, blob.ErrNotFound) {
		return fmt.Errorf("delete %s from cache %s: %w", key, c.name, err)
	}
	return nil
}

// errFillAborted cancels a cache fill whose source did not verify.
var errFillAborted = errors.New("cache fill aborted")

// filler streams a copy of an object into the cache while it is being read
// or written elsewhere. A failed cache write never fails the main stream.
type filler struct {
	c      *Cache
	entry  cacheEntry
	pw     *io.PipeWriter
	done   chan error
	failed bool
	closed bool
}

func (c *Cache) startFill(ctx context.Context, e cacheEntry) *filler {
	pr, pw := io.Pipe()
	f := &filler{c: c, entry: e, pw: pw, done: make(chan error, 1)}
	go func() {
		_, err := c.store.Put(ctx, e.key, pr, e.size, blob.PutOpts{})
		// Unblock the writer if the store stopped reading early.
		_ = pr.CloseWithError(errors.Join(err, errFillAborted))
		f.done <- err
	}()
	return f
}

func (f *filler) Write(p []byte) (int, error) {
	if f.failed || len(p) == 0 {
		return len(p), nil
	}
	if _, err := f.pw.Write(p); err != nil {
		f.failed = true
	}
	return len(p), nil
}

// finish completes the fill when ok, and registers the entry; otherwise it
// aborts the write and removes anything the store may have committed.
func (f *filler) finish(ctx context.Context, ok bool, now time.Time) {
	if f.closed {
		return
	}
	f.closed = true
	if ok && !f.failed {
		_ = f.pw.Close()
	} else {
		_ = f.pw.CloseWithError(errFillAborted)
	}
	err := <-f.done
	switch {
	case ok && !f.failed && err == nil:
		f.entry.lastAccess = now
		f.c.add(f.entry)
		if _, _, err := f.c.Evict(ctx, now); err != nil {
			f.c.log.WarnContext(ctx, "cache eviction failed", "target", f.c.name, "error", err)
		}
	case err == nil:
		// The store committed although the source did not verify.
		if derr := f.c.store.Delete(context.WithoutCancel(ctx), f.entry.key); derr != nil && !errors.Is(derr, blob.ErrNotFound) {
			f.c.log.WarnContext(ctx, "remove aborted cache fill", "target", f.c.name, "key", f.entry.key, "error", derr)
		}
	default:
		if ok {
			f.c.log.WarnContext(ctx, "cache fill failed", "target", f.c.name, "key", f.entry.key, "error", err)
		}
	}
}
