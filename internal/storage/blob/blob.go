// Package blob defines the object store abstraction implemented by every
// storage driver (local, s3, sftp) and by the wrappers composed on top of them
// (crypt, retry, metered). See PLAN §4.1.
//
// Contract shared by all implementations, enforced by the blobtest suite:
//
//   - Keys are validated with ValidateKey; invalid keys fail with ErrInvalidKey.
//   - Put is atomic: readers see either the previous object or the new one in
//     full, never a partial write. A failed Put (reader error, size or checksum
//     mismatch, cancelled context) leaves no visible object behind.
//   - Delete of a missing key succeeds.
//   - Get and Stat of a missing key fail with ErrNotFound.
//   - ETags are opaque tokens that change whenever the object is rewritten.
package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Store is the interface implemented by every driver and wrapper.
type Store interface {
	// Name is the configured target name, used in logs and metrics.
	Name() string
	// Caps reports what the store supports.
	Caps() Caps
	// Put writes size bytes read from r under key. size < 0 means unknown.
	// When size >= 0 and r yields a different number of bytes, Put fails with
	// ErrSizeMismatch.
	Put(ctx context.Context, key string, r io.Reader, size int64, o PutOpts) (Info, error)
	// Get opens the object, or the part selected by rng when not nil.
	// The returned Info always describes the whole object.
	Get(ctx context.Context, key string, rng *Range) (io.ReadCloser, Info, error)
	// Stat returns the object metadata.
	Stat(ctx context.Context, key string) (Info, error)
	// Delete removes the object; deleting a missing key is not an error.
	Delete(ctx context.Context, key string) error
	// List calls fn for every object whose key starts with prefix (a plain
	// string prefix, not only whole path segments). Order is unspecified.
	// An error returned by fn stops the listing and is returned unchanged.
	List(ctx context.Context, prefix string, fn func(Info) error) error
}

// Unwrapper is implemented by wrappers (crypt, retry, metered) to expose the
// store they decorate.
type Unwrapper interface {
	Unwrap() Store
}

// Close releases the resources (connections, handles) held by s: it closes
// the outermost io.Closer found by walking the wrapper chain. Stores without
// resources are left alone.
func Close(s Store) error {
	for s != nil {
		if c, ok := s.(io.Closer); ok {
			return c.Close()
		}
		u, ok := s.(Unwrapper)
		if !ok {
			return nil
		}
		s = u.Unwrap()
	}
	return nil
}

// Caps describes optional features of a Store.
type Caps struct {
	Range          bool // Get honours Range without reading the whole object
	Presign        bool // implements Presigner
	Multipart      bool // large objects are uploaded in parts
	AtomicRename   bool // overwrites are atomic (always true for s3)
	ConditionalPut bool // implements ConditionalPutter
	MaxConns       int  // connection limit, 0 = unlimited
}

// Info is the metadata of a stored object.
type Info struct {
	Key     string
	Size    int64
	ETag    string // opaque; changes on every rewrite
	ModTime time.Time
}

// Range selects Length bytes starting at Offset. Length < 0 reads to the end.
// Offset may equal the object size (empty result); beyond it Get fails with
// ErrInvalidRange.
type Range struct {
	Offset int64
	Length int64
}

// PutOpts are optional parameters for Put.
type PutOpts struct {
	// SHA256 is the expected lowercase hex digest of the content. When set,
	// a mismatch fails the Put with ErrChecksumMismatch and nothing is stored.
	SHA256 string
	// ContentType is stored where the backend supports it.
	ContentType string
}

// Condition guards a conditional write.
type Condition struct {
	// IfMatch requires the current object to exist with this ETag.
	IfMatch string
	// IfNoneMatch requires the key not to exist.
	IfNoneMatch bool
}

// ConditionalPutter is implemented by stores that can write only when a
// precondition holds (compare-and-swap). The pack journal manifest (§4.3)
// requires it. A failed precondition returns ErrPreconditionFailed.
type ConditionalPutter interface {
	PutConditional(ctx context.Context, key string, r io.Reader, size int64, c Condition, o PutOpts) (Info, error)
}

// Presigner is implemented by stores that can hand out direct URLs.
type Presigner interface {
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	// PresignPut returns the URL and the headers the client must send.
	// sha256 (hex, optional) makes the backend verify the uploaded content.
	PresignPut(ctx context.Context, key string, size int64, sha256 string, ttl time.Duration) (string, http.Header, error)
}

// Sentinel errors. Implementations wrap them with fmt.Errorf("...: %w").
var (
	ErrNotFound           = errors.New("blob: object not found")
	ErrPreconditionFailed = errors.New("blob: precondition failed")
	ErrInvalidKey         = errors.New("blob: invalid key")
	ErrInvalidRange       = errors.New("blob: invalid range")
	ErrSizeMismatch       = errors.New("blob: size mismatch")
	ErrChecksumMismatch   = errors.New("blob: checksum mismatch")
	ErrNotSupported       = errors.New("blob: operation not supported")
)

// CheckCondition evaluates c against the current object (nil when missing).
// Drivers that emulate conditional writes use it under their lock.
func CheckCondition(c Condition, cur *Info) error {
	if c.IfNoneMatch && cur != nil {
		return fmt.Errorf("%w: object exists", ErrPreconditionFailed)
	}
	if c.IfMatch != "" {
		if cur == nil {
			return fmt.Errorf("%w: object missing", ErrPreconditionFailed)
		}
		if cur.ETag != c.IfMatch {
			return fmt.Errorf("%w: etag changed", ErrPreconditionFailed)
		}
	}
	return nil
}

// ResolveRange validates rng against an object of the given size and returns
// the absolute [start, end) byte interval. A nil rng selects everything.
func ResolveRange(rng *Range, size int64) (start, end int64, err error) {
	if rng == nil {
		return 0, size, nil
	}
	if rng.Offset < 0 || rng.Offset > size {
		return 0, 0, fmt.Errorf("%w: offset %d, size %d", ErrInvalidRange, rng.Offset, size)
	}
	end = size
	if rng.Length >= 0 && rng.Offset+rng.Length < size {
		end = rng.Offset + rng.Length
	}
	return rng.Offset, end, nil
}
