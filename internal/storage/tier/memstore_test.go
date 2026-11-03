package tier

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

// memStore is an in-memory blob.Store with failure injection, used by the
// unit tests. Put is atomic like the real drivers: the content becomes
// visible only after it has been read in full and checked.
type memStore struct {
	name string

	mu      sync.Mutex
	objects map[string]memObject
	gen     atomic.Int64

	// Failure injection; nil means no failure.
	putErr func(key string) error
	getErr func(key string) error
	// failAfter makes Put fail after reading that many bytes (< 0: never).
	failAfter atomic.Int64
	// lazyPut makes Put return success after reading only the first byte,
	// like a broken driver, to exercise the tier's own verification.
	lazyPut atomic.Bool

	puts atomic.Int64
	gets atomic.Int64
}

type memObject struct {
	data []byte
	etag string
	mod  time.Time
}

func newMemStore(name string) *memStore {
	s := &memStore{name: name, objects: map[string]memObject{}}
	s.failAfter.Store(-1)
	return s
}

func (s *memStore) Name() string { return s.name }
func (s *memStore) Caps() blob.Caps {
	return blob.Caps{Range: true, AtomicRename: true}
}

func (s *memStore) Put(ctx context.Context, key string, r io.Reader, size int64, o blob.PutOpts) (blob.Info, error) {
	if err := blob.ValidateKey(key); err != nil {
		return blob.Info{}, err
	}
	s.puts.Add(1)
	s.mu.Lock()
	putErr := s.putErr
	s.mu.Unlock()
	if putErr != nil {
		if err := putErr(key); err != nil {
			return blob.Info{}, err
		}
	}
	if s.lazyPut.Load() {
		var b [1]byte
		_, _ = r.Read(b[:])
		return s.commit(key, b[:])
	}
	var buf bytes.Buffer
	src := r
	if n := s.failAfter.Load(); n >= 0 {
		src = io.LimitReader(r, n)
	}
	if _, err := io.Copy(&buf, &ctxReader{ctx: ctx, r: src}); err != nil {
		return blob.Info{}, fmt.Errorf("mem put %s: %w", key, err)
	}
	if s.failAfter.Load() >= 0 {
		return blob.Info{}, blob.MarkTransient(fmt.Errorf("mem put %s: connection reset", key))
	}
	if size >= 0 && int64(buf.Len()) != size {
		return blob.Info{}, fmt.Errorf("mem put %s: %w", key, blob.ErrSizeMismatch)
	}
	if o.SHA256 != "" {
		sum := sha256.Sum256(buf.Bytes())
		if hex.EncodeToString(sum[:]) != o.SHA256 {
			return blob.Info{}, fmt.Errorf("mem put %s: %w", key, blob.ErrChecksumMismatch)
		}
	}
	return s.commit(key, buf.Bytes())
}

func (s *memStore) commit(key string, data []byte) (blob.Info, error) {
	obj := memObject{data: bytes.Clone(data), etag: strconv.FormatInt(s.gen.Add(1), 10), mod: time.Now()}
	s.mu.Lock()
	s.objects[key] = obj
	s.mu.Unlock()
	return blob.Info{Key: key, Size: int64(len(data)), ETag: obj.etag, ModTime: obj.mod}, nil
}

func (s *memStore) Get(_ context.Context, key string, rng *blob.Range) (io.ReadCloser, blob.Info, error) {
	if err := blob.ValidateKey(key); err != nil {
		return nil, blob.Info{}, err
	}
	s.gets.Add(1)
	s.mu.Lock()
	getErr := s.getErr
	obj, ok := s.objects[key]
	s.mu.Unlock()
	if getErr != nil {
		if err := getErr(key); err != nil {
			return nil, blob.Info{}, err
		}
	}
	if !ok {
		return nil, blob.Info{}, fmt.Errorf("mem get %s: %w", key, blob.ErrNotFound)
	}
	info := blob.Info{Key: key, Size: int64(len(obj.data)), ETag: obj.etag, ModTime: obj.mod}
	start, end, err := blob.ResolveRange(rng, info.Size)
	if err != nil {
		return nil, blob.Info{}, err
	}
	return io.NopCloser(bytes.NewReader(obj.data[start:end])), info, nil
}

func (s *memStore) Stat(_ context.Context, key string) (blob.Info, error) {
	if err := blob.ValidateKey(key); err != nil {
		return blob.Info{}, err
	}
	s.mu.Lock()
	obj, ok := s.objects[key]
	s.mu.Unlock()
	if !ok {
		return blob.Info{}, fmt.Errorf("mem stat %s: %w", key, blob.ErrNotFound)
	}
	return blob.Info{Key: key, Size: int64(len(obj.data)), ETag: obj.etag, ModTime: obj.mod}, nil
}

func (s *memStore) Delete(_ context.Context, key string) error {
	if err := blob.ValidateKey(key); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.objects, key)
	s.mu.Unlock()
	return nil
}

func (s *memStore) List(_ context.Context, prefix string, fn func(blob.Info) error) error {
	s.mu.Lock()
	var infos []blob.Info
	for k, o := range s.objects {
		if strings.HasPrefix(k, prefix) {
			infos = append(infos, blob.Info{Key: k, Size: int64(len(o.data)), ETag: o.etag, ModTime: o.mod})
		}
	}
	s.mu.Unlock()
	for _, i := range infos {
		if err := fn(i); err != nil {
			return err
		}
	}
	return nil
}

// corrupt flips one byte of a stored object.
func (s *memStore) corrupt(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.objects[key]
	o.data = bytes.Clone(o.data)
	o.data[0] ^= 0xff
	s.objects[key] = o
}

func (s *memStore) has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[key]
	return ok
}

func (s *memStore) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.objects)
}

func (s *memStore) setPutErr(fn func(string) error) {
	s.mu.Lock()
	s.putErr = fn
	s.mu.Unlock()
}

func (s *memStore) setGetErr(fn func(string) error) {
	s.mu.Lock()
	s.getErr = fn
	s.mu.Unlock()
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
