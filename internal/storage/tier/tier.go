// Package tier places content-addressed objects on the storage targets
// according to the per-data-type policies of PLAN §4.2.
//
// Rules enforced here:
//
//   - The primary target holds the canonical copy. A Put returns only after
//     the object is written and verified on the primary; only then is its row
//     inserted in storage_objects. A Put interrupted at any point leaves
//     neither a partial object (blob.Store.Put is atomic and the stream is
//     verified before its last byte) nor a row.
//   - The local cache is a read-through LRU: a miss is served from the primary
//     (or a replica) and fills the cache on the way; a cache entry that has
//     disappeared is ignored transparently.
//   - Replicas are filled asynchronously by the replication job, which copies
//     from the primary and checks the sha256; a weekly job re-hashes a sample.
//
// Durability "async" (PLAN §4.3) applies to the git pack journal of Phase 3;
// content-addressed objects are always written synchronously.
package tier

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

// Queries is the subset of dbgen.Querier used by the tier.
type Queries interface {
	GetStorageObject(ctx context.Context, arg dbgen.GetStorageObjectParams) (dbgen.StorageObject, error)
	UpsertStorageObject(ctx context.Context, arg dbgen.UpsertStorageObjectParams) (dbgen.StorageObject, error)
	DeleteStorageObject(ctx context.Context, arg dbgen.DeleteStorageObjectParams) error
	MarkStorageObjectReplicated(ctx context.Context, arg dbgen.MarkStorageObjectReplicatedParams) error
	MarkStorageObjectVerified(ctx context.Context, arg dbgen.MarkStorageObjectVerifiedParams) error
	SetStorageObjectError(ctx context.Context, arg dbgen.SetStorageObjectErrorParams) error
	SetStorageObjectReplicasPending(ctx context.Context, arg dbgen.SetStorageObjectReplicasPendingParams) error
	ListStorageObjectsPendingReplicas(ctx context.Context, arg dbgen.ListStorageObjectsPendingReplicasParams) ([]dbgen.StorageObject, error)
	SampleStorageObjectsForVerify(ctx context.Context, arg dbgen.SampleStorageObjectsForVerifyParams) ([]dbgen.StorageObject, error)
	ListStorageObjectsByKeys(ctx context.Context, arg dbgen.ListStorageObjectsByKeysParams) ([]dbgen.StorageObject, error)
	StorageObjectStats(ctx context.Context) ([]dbgen.StorageObjectStatsRow, error)
}

// Target is one configured physical store.
type Target struct {
	Name      string
	Kind      string
	Encrypted bool
	Store     blob.Store
}

// Options configure New.
type Options struct {
	Config  config.Storage
	Stores  map[string]blob.Store // by target name, see BuildStores
	Queries Queries
	Log     *slog.Logger
	// OnStored is called after an object is committed on its primary when
	// the policy has replicas; it should enqueue a replication job. Errors
	// are only logged: the replica sweep catches anything missed.
	OnStored func(ctx context.Context, ref ObjectRef) error
	Now      func() time.Time
}

// Tier is the storage engine. It is safe for concurrent use.
type Tier struct {
	targets  map[string]*Target
	classes  map[string]*Class
	caches   map[string]*Cache
	cfg      config.Storage
	q        Queries
	log      *slog.Logger
	onStored func(ctx context.Context, ref ObjectRef) error
	now      func() time.Time
}

// Class is the handle for one data type (git, lfs, registry, ...). Keys
// passed to its methods are relative: the policy name is prepended, so
// Class("lfs").Put("sha256/ab/cd/<oid>") stores "lfs/sha256/ab/cd/<oid>".
type Class struct {
	t        *Tier
	name     string
	primary  *Target
	replicas []*Target
	cache    *Cache // nil when the policy has no cache
	policy   config.Policy
}

// ObjectRef identifies a row of storage_objects.
type ObjectRef struct {
	Target string `json:"target"`
	Key    string `json:"key"`
}

// Object is the metadata of a stored object.
type Object struct {
	Key        string // full key, including the policy prefix
	Policy     string
	Target     string // primary target
	Size       int64
	SHA256     []byte
	ReplicasOK bool
	VerifiedAt time.Time
}

// SHA256Hex returns the digest as lowercase hex.
func (o Object) SHA256Hex() string { return hex.EncodeToString(o.SHA256) }

// ErrUnknownPolicy is returned by Class for a data type without a policy.
var ErrUnknownPolicy = errors.New("storage: no policy for data type")

// New builds the engine. Stores must contain every configured target.
func New(o Options) (*Tier, error) {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Queries == nil {
		return nil, errors.New("storage tier: queries are required")
	}
	t := &Tier{
		targets: map[string]*Target{}, classes: map[string]*Class{}, caches: map[string]*Cache{},
		cfg: o.Config, q: o.Queries, log: o.Log, onStored: o.OnStored, now: o.Now,
	}
	for _, tc := range o.Config.Targets {
		s, ok := o.Stores[tc.Name]
		if !ok || s == nil {
			return nil, fmt.Errorf("storage tier: no store for target %q", tc.Name)
		}
		t.targets[tc.Name] = &Target{Name: tc.Name, Kind: tc.Kind, Encrypted: tc.Encrypt, Store: s}
	}
	for _, name := range slices.Sorted(maps.Keys(o.Config.Policy)) {
		p := o.Config.Policy[name]
		c := &Class{t: t, name: name, policy: p, primary: t.targets[p.Primary]}
		if c.primary == nil {
			return nil, fmt.Errorf("storage tier: policy %s: unknown primary %q", name, p.Primary)
		}
		for _, r := range p.Replicas {
			rt := t.targets[r]
			if rt == nil {
				return nil, fmt.Errorf("storage tier: policy %s: unknown replica %q", name, r)
			}
			c.replicas = append(c.replicas, rt)
		}
		// A cache on the primary itself would only duplicate the data.
		if p.Cache != "" && p.Cache != p.Primary {
			c.cache = t.cacheFor(p.Cache)
			if c.cache == nil {
				return nil, fmt.Errorf("storage tier: policy %s: unknown cache %q", name, p.Cache)
			}
			c.cache.shares[name] = p.CacheShare
			c.cache.ttls[name] = time.Duration(p.CacheTTL)
		}
		t.classes[name] = c
	}
	return t, nil
}

func (t *Tier) cacheFor(name string) *Cache {
	if c, ok := t.caches[name]; ok {
		return c
	}
	tgt := t.targets[name]
	if tgt == nil {
		return nil
	}
	var tc config.Target
	for _, x := range t.cfg.Targets {
		if x.Name == name {
			tc = x
		}
	}
	high, low := tc.HighWatermark, tc.LowWatermark
	if high == 0 {
		high = 0.90
	}
	if low == 0 {
		low = 0.75
	}
	c := newCache(name, tgt.Store, int64(tc.Budget), high, low, t.log)
	t.caches[name] = c
	return c
}

// Class returns the handle of a data type.
func (t *Tier) Class(policy string) (*Class, error) {
	c, ok := t.classes[policy]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownPolicy, policy)
	}
	return c, nil
}

// Target returns a configured target by name.
func (t *Tier) Target(name string) (*Target, bool) {
	tgt, ok := t.targets[name]
	return tgt, ok
}

// Targets returns every configured target in configuration order.
func (t *Tier) Targets() []*Target {
	out := make([]*Target, 0, len(t.cfg.Targets))
	for _, tc := range t.cfg.Targets {
		out = append(out, t.targets[tc.Name])
	}
	return out
}

// Name is the data type of the class.
func (c *Class) Name() string { return c.name }

// Primary returns the target holding the canonical copies of the class.
// Mutable metadata that cannot be content-addressed (the git pack journal
// manifest) is written there directly, with conditional puts.
func (c *Class) Primary() *Target { return c.primary }

// Replicas returns the replica targets of the class, in policy order.
func (c *Class) Replicas() []*Target { return slices.Clone(c.replicas) }

// FullKey returns the key used on the targets for a relative key.
func (c *Class) FullKey(key string) string { return c.name + "/" + key }

// validKey rejects keys that are not portable blob keys (see blob.ValidateKey).
func validKey(key string) error { return blob.ValidateKey(key) }

func objectFromRow(r dbgen.StorageObject) Object {
	o := Object{Key: r.Key, Policy: r.Policy, Target: r.Target, Size: r.Size, SHA256: r.Sha256, ReplicasOK: r.ReplicasOk}
	if r.VerifiedAt.Valid {
		o.VerifiedAt = r.VerifiedAt.Time
	}
	return o
}

func (c *Class) row(ctx context.Context, full string) (dbgen.StorageObject, error) {
	r, err := c.t.q.GetStorageObject(ctx, dbgen.GetStorageObjectParams{Target: c.primary.Name, Key: full})
	if errors.Is(err, pgx.ErrNoRows) {
		return r, fmt.Errorf("%s: %w", full, blob.ErrNotFound)
	}
	if err != nil {
		return r, fmt.Errorf("load storage object %s: %w", full, err)
	}
	return r, nil
}

// Stat returns the metadata of a committed object.
func (c *Class) Stat(ctx context.Context, key string) (Object, error) {
	if err := validKey(key); err != nil {
		return Object{}, err
	}
	r, err := c.row(ctx, c.FullKey(key))
	if err != nil {
		return Object{}, err
	}
	return objectFromRow(r), nil
}

// Put stores size bytes from r under key. When sha256 is not nil the content
// must match it; an object already committed with the same digest is not
// uploaded again (r is not read). Put returns once the object is verified on
// the primary and recorded.
func (c *Class) Put(ctx context.Context, key string, r io.Reader, size int64, sha256 []byte) (Object, error) {
	if err := validKey(key); err != nil {
		return Object{}, err
	}
	if size < 0 {
		return Object{}, fmt.Errorf("put %s: size must be known", key)
	}
	if sha256 != nil && len(sha256) != 32 {
		return Object{}, fmt.Errorf("put %s: sha256 must be 32 bytes", key)
	}
	full := c.FullKey(key)

	if sha256 != nil {
		if row, err := c.row(ctx, full); err == nil && row.Size == size && bytes.Equal(row.Sha256, sha256) {
			return objectFromRow(row), nil
		} else if err != nil && !errors.Is(err, blob.ErrNotFound) {
			return Object{}, err
		}
	}

	vr := newVerifyReader(r, size, sha256)
	src := io.Reader(vr)
	var fill *filler
	if c.cache != nil && c.cache.admits(c.name, size) {
		fill = c.cache.startFill(ctx, cacheEntry{key: full, policy: c.name, size: size})
		src = io.TeeReader(vr, fill)
	}
	opts := blob.PutOpts{}
	if sha256 != nil {
		opts.SHA256 = hex.EncodeToString(sha256)
	}
	_, err := c.primary.Store.Put(ctx, full, src, size, opts)
	if err == nil && !vr.Verified() {
		// The driver returned without consuming the whole stream: never
		// trust what it stored.
		err = fmt.Errorf("%w: primary stored %s without reading it in full", ErrCorrupt, full)
		if derr := c.primary.Store.Delete(context.WithoutCancel(ctx), full); derr != nil {
			c.t.log.ErrorContext(ctx, "remove unverified object", "target", c.primary.Name, "key", full, "error", derr)
		}
	}
	if err != nil {
		if fill != nil {
			fill.finish(ctx, false, c.t.now())
		}
		return Object{}, fmt.Errorf("put %s on %s: %w", full, c.primary.Name, err)
	}
	sum := vr.Sum()

	row, err := c.t.q.UpsertStorageObject(context.WithoutCancel(ctx), dbgen.UpsertStorageObjectParams{
		Target: c.primary.Name, Key: full, Policy: c.name, Size: size, Sha256: sum,
		ReplicasOk: len(c.replicas) == 0,
	})
	if err != nil {
		// The object is on the primary but unrecorded: it stays invisible and
		// a retried Put overwrites it.
		if fill != nil {
			fill.finish(ctx, false, c.t.now())
		}
		return Object{}, fmt.Errorf("record %s: %w", full, err)
	}
	if fill != nil {
		fill.entry.sha256 = sum
		fill.finish(ctx, true, c.t.now())
	}
	if !row.ReplicasOk && c.t.onStored != nil {
		if err := c.t.onStored(ctx, ObjectRef{Target: row.Target, Key: row.Key}); err != nil {
			c.t.log.WarnContext(ctx, "enqueue replication", "key", full, "error", err)
		}
	}
	return objectFromRow(row), nil
}

// Get opens a committed object, or the part selected by rng. Full reads are
// verified against the recorded sha256: a mismatch surfaces as ErrCorrupt
// instead of io.EOF at the end of the stream.
func (c *Class) Get(ctx context.Context, key string, rng *blob.Range) (io.ReadCloser, Object, error) {
	if err := validKey(key); err != nil {
		return nil, Object{}, err
	}
	full := c.FullKey(key)
	now := c.t.now()

	if c.cache != nil {
		if e, ok := c.cache.lookup(full, now); ok {
			rc, _, err := c.cache.store.Get(ctx, full, rng)
			switch {
			case err == nil:
				obj := Object{Key: full, Policy: c.name, Target: c.primary.Name, Size: e.size, SHA256: e.sha256}
				if rng == nil {
					return &readCloser{Reader: newVerifyReader(rc, e.size, e.sha256), closer: rc}, obj, nil
				}
				return rc, obj, nil
			case errors.Is(err, blob.ErrNotFound):
				c.cache.forget(full)
			default:
				c.t.log.WarnContext(ctx, "cache read failed, using primary", "target", c.cache.name, "key", full, "error", err)
			}
		}
	}

	row, err := c.row(ctx, full)
	if err != nil {
		return nil, Object{}, err
	}
	obj := objectFromRow(row)
	rc, err := c.openRemote(ctx, row, rng)
	if err != nil {
		return nil, Object{}, err
	}
	if rng != nil {
		return rc, obj, nil
	}
	vr := newVerifyReader(rc, row.Size, row.Sha256)
	out := &readCloser{Reader: vr, closer: rc}
	if c.cache != nil && c.cache.admits(c.name, row.Size) {
		fill := c.cache.startFill(ctx, cacheEntry{key: full, policy: c.name, size: row.Size, sha256: row.Sha256})
		out.Reader = &fillingReader{vr: vr, fill: fill, ctx: ctx, now: c.t.now}
		out.onClose = func() { fill.finish(ctx, false, c.t.now()) } // no-op once finished
	}
	return out, obj, nil
}

// openRemote reads from the primary, falling back to replicas holding a
// verified copy when the primary has lost the object or cannot be reached.
func (c *Class) openRemote(ctx context.Context, row dbgen.StorageObject, rng *blob.Range) (io.ReadCloser, error) {
	src := c.t.targets[row.Target]
	if src == nil {
		src = c.primary
	}
	rc, _, err := src.Store.Get(ctx, row.Key, rng)
	if err == nil {
		return rc, nil
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("get %s from %s: %w", row.Key, src.Name, err)
	}
	c.t.log.ErrorContext(ctx, "primary read failed", "target", src.Name, "key", row.Key, "error", err)
	if row.ReplicasOk {
		for _, rep := range c.replicas {
			rrc, _, rerr := rep.Store.Get(ctx, row.Key, rng)
			if rerr == nil {
				c.t.log.WarnContext(ctx, "serving object from replica", "target", rep.Name, "key", row.Key)
				return rrc, nil
			}
		}
	}
	return nil, fmt.Errorf("get %s from %s: %w", row.Key, src.Name, err)
}

// Delete removes the object from the database first (so it is no longer
// visible), then from every target. Leftover blobs are harmless orphans.
func (c *Class) Delete(ctx context.Context, key string) error {
	if err := validKey(key); err != nil {
		return err
	}
	full := c.FullKey(key)
	if err := c.t.q.DeleteStorageObject(ctx, dbgen.DeleteStorageObjectParams{Target: c.primary.Name, Key: full}); err != nil {
		return fmt.Errorf("delete record %s: %w", full, err)
	}
	var errs []error
	if c.cache != nil {
		errs = append(errs, c.cache.drop(ctx, full))
	}
	for _, tgt := range append([]*Target{c.primary}, c.replicas...) {
		if err := tgt.Store.Delete(ctx, full); err != nil {
			errs = append(errs, fmt.Errorf("delete %s from %s: %w", full, tgt.Name, err))
		}
	}
	return errors.Join(errs...)
}

// EvictCached drops a cached copy, if any. The object stays available.
func (c *Class) EvictCached(ctx context.Context, key string) error {
	if c.cache == nil {
		return nil
	}
	if err := validKey(key); err != nil {
		return err
	}
	return c.cache.drop(ctx, c.FullKey(key))
}

// readCloser pairs a wrapped reader with the underlying closer.
type readCloser struct {
	io.Reader
	closer  io.Closer
	onClose func()
}

func (r *readCloser) Close() error {
	if r.onClose != nil {
		r.onClose()
	}
	return r.closer.Close()
}

// fillingReader tees a verified remote stream into the cache and registers
// the cache entry only once the whole object has verified.
type fillingReader struct {
	vr   *verifyReader
	fill *filler
	ctx  context.Context
	now  func() time.Time
}

func (f *fillingReader) Read(p []byte) (int, error) {
	n, err := f.vr.Read(p)
	if n > 0 {
		_, _ = f.fill.Write(p[:n])
	}
	// Finish as soon as the stream has verified, so a reader that stops at
	// exactly size bytes and closes still fills the cache.
	if f.vr.Verified() || err != nil {
		f.fill.finish(f.ctx, f.vr.Verified(), f.now())
	}
	return n, err
}

// LoadCaches rebuilds the in-memory cache indexes from the cache targets.
// Entries without a committed row (for example from a Put interrupted by a
// crash) or whose size differs from the row are deleted.
func (t *Tier) LoadCaches(ctx context.Context) error {
	for _, name := range slices.Sorted(maps.Keys(t.classes)) {
		c := t.classes[name]
		if c.cache == nil {
			continue
		}
		var batch []blob.Info
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			keys := make([]string, len(batch))
			for i, b := range batch {
				keys[i] = b.Key
			}
			rows, err := t.q.ListStorageObjectsByKeys(ctx, dbgen.ListStorageObjectsByKeysParams{Target: c.primary.Name, Keys: keys})
			if err != nil {
				return fmt.Errorf("load cache index: %w", err)
			}
			byKey := make(map[string]dbgen.StorageObject, len(rows))
			for _, r := range rows {
				byKey[r.Key] = r
			}
			var entries []cacheEntry
			for _, b := range batch {
				r, ok := byKey[b.Key]
				if !ok || r.Size != b.Size {
					if err := c.cache.store.Delete(ctx, b.Key); err != nil {
						t.log.WarnContext(ctx, "remove stale cache entry", "target", c.cache.name, "key", b.Key, "error", err)
					}
					continue
				}
				entries = append(entries, cacheEntry{key: b.Key, policy: name, size: b.Size, sha256: r.Sha256, lastAccess: b.ModTime})
			}
			c.cache.addLoaded(entries)
			batch = batch[:0]
			return nil
		}
		err := c.cache.store.List(ctx, name+"/", func(i blob.Info) error {
			batch = append(batch, i)
			if len(batch) >= 500 {
				return flush()
			}
			return nil
		})
		if err == nil {
			err = flush()
		}
		if err != nil {
			return fmt.Errorf("scan cache %s for %s: %w", c.cache.name, name, err)
		}
	}
	return nil
}

// Evict runs eviction on every cache.
func (t *Tier) Evict(ctx context.Context) (objects int, bytes int64, err error) {
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(t.caches)) {
		n, b, err := t.caches[name].Evict(ctx, t.now())
		objects += n
		bytes += b
		errs = append(errs, err)
	}
	return objects, bytes, errors.Join(errs...)
}

// Caches returns the cache of every local target used as a cache.
func (t *Tier) Caches() []*Cache {
	out := make([]*Cache, 0, len(t.caches))
	for _, name := range slices.Sorted(maps.Keys(t.caches)) {
		out = append(out, t.caches[name])
	}
	return out
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
