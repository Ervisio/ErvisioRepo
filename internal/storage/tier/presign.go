package tier

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

// ErrNoPresign is returned by the presign helpers when the primary of the
// class cannot hand out direct URLs (no driver support, or client-side
// encryption, which requires the data to pass through the server).
var ErrNoPresign = errors.New("storage tier: primary does not support presigned URLs")

// presigner returns the presigner of the primary, or nil.
func (c *Class) presigner() blob.Presigner {
	p := c.primary
	if p.Encrypted || !p.Store.Caps().Presign {
		return nil
	}
	ps, ok := p.Store.(blob.Presigner)
	if !ok {
		return nil
	}
	return ps
}

// CanPresign reports whether clients can read and write objects of the class
// directly on the primary with presigned URLs.
func (c *Class) CanPresign() bool { return c.presigner() != nil }

// PresignPut returns a URL (and the headers to send with it) that uploads
// exactly size bytes with the given sha256 under key on the primary. The
// object stays invisible until Adopt records it.
func (c *Class) PresignPut(ctx context.Context, key string, size int64, sha256Hex string, ttl time.Duration) (string, http.Header, error) {
	if err := validKey(key); err != nil {
		return "", nil, err
	}
	ps := c.presigner()
	if ps == nil {
		return "", nil, ErrNoPresign
	}
	u, h, err := ps.PresignPut(ctx, c.FullKey(key), size, sha256Hex, ttl)
	if err != nil {
		return "", nil, fmt.Errorf("presign put %s: %w", key, err)
	}
	return u, h, nil
}

// PresignGet returns a URL that downloads the committed object key from the
// primary.
func (c *Class) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if err := validKey(key); err != nil {
		return "", err
	}
	ps := c.presigner()
	if ps == nil {
		return "", ErrNoPresign
	}
	u, err := ps.PresignGet(ctx, c.FullKey(key), ttl)
	if err != nil {
		return "", fmt.Errorf("presign get %s: %w", key, err)
	}
	return u, nil
}

// Cached reports whether key is currently held by the local cache of the
// class, in which case serving it through the server is cheaper than a
// redirect to the primary.
func (c *Class) Cached(key string) bool {
	if c.cache == nil {
		return false
	}
	_, ok := c.cache.lookup(c.FullKey(key), c.t.now())
	return ok
}

// Adopt records an object that a client uploaded directly to the primary
// with a URL from PresignPut. The size is checked on the primary; the
// content digest was enforced by the store at upload time (the presigned
// request carries the checksum). Adopting an object already recorded with
// the same digest is a no-op.
func (c *Class) Adopt(ctx context.Context, key string, size int64, sha256 []byte) (Object, error) {
	if err := validKey(key); err != nil {
		return Object{}, err
	}
	if len(sha256) != 32 {
		return Object{}, fmt.Errorf("adopt %s: sha256 must be 32 bytes", key)
	}
	if c.presigner() == nil {
		return Object{}, ErrNoPresign
	}
	full := c.FullKey(key)
	if row, err := c.row(ctx, full); err == nil {
		if row.Size == size && string(row.Sha256) == string(sha256) {
			return objectFromRow(row), nil
		}
		return Object{}, fmt.Errorf("%w: %s is recorded with another content", ErrCorrupt, full)
	} else if !errors.Is(err, blob.ErrNotFound) {
		return Object{}, err
	}
	info, err := c.primary.Store.Stat(ctx, full)
	if err != nil {
		return Object{}, fmt.Errorf("adopt %s: %w", full, err)
	}
	if info.Size != size {
		return Object{}, fmt.Errorf("adopt %s: %w: have %d bytes, want %d", full, blob.ErrSizeMismatch, info.Size, size)
	}
	row, err := c.t.q.UpsertStorageObject(context.WithoutCancel(ctx), dbgen.UpsertStorageObjectParams{
		Target: c.primary.Name, Key: full, Policy: c.name, Size: size, Sha256: sha256,
		ReplicasOk: len(c.replicas) == 0,
	})
	if err != nil {
		return Object{}, fmt.Errorf("record %s: %w", full, err)
	}
	if !row.ReplicasOk && c.t.onStored != nil {
		if err := c.t.onStored(ctx, ObjectRef{Target: row.Target, Key: row.Key}); err != nil {
			c.t.log.WarnContext(ctx, "enqueue replication", "key", full, "error", err)
		}
	}
	return objectFromRow(row), nil
}
