package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"sync"
)

// VerifyReader wraps the body of a Put. It honours ctx cancellation, and when
// the source is exhausted it checks the byte count against the declared size
// and the content against the expected SHA-256, returning ErrSizeMismatch or
// ErrChecksumMismatch instead of io.EOF. Drivers must therefore read it to
// io.EOF before making the object visible.
type VerifyReader struct {
	ctx  context.Context
	r    io.Reader
	size int64
	want []byte
	h    hash.Hash
	n    int64
	err  error // sticky terminal error (including io.EOF)
}

// NewVerifyReader returns a VerifyReader. size < 0 disables the size check;
// sha256Hex == "" disables the checksum check.
func NewVerifyReader(ctx context.Context, r io.Reader, size int64, sha256Hex string) (*VerifyReader, error) {
	v := &VerifyReader{ctx: ctx, r: r, size: size}
	if sha256Hex != "" {
		want, err := hex.DecodeString(sha256Hex)
		if err != nil || len(want) != sha256.Size {
			return nil, fmt.Errorf("%w: malformed sha256 %q", ErrChecksumMismatch, sha256Hex)
		}
		v.want = want
		v.h = sha256.New()
	}
	return v, nil
}

// N returns the number of bytes read so far.
func (v *VerifyReader) N() int64 { return v.n }

func (v *VerifyReader) Read(p []byte) (int, error) {
	if v.err != nil {
		return 0, v.err
	}
	if err := v.ctx.Err(); err != nil {
		v.err = err
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if v.size >= 0 && v.n == v.size {
		// Declared size reached: the source must be exhausted.
		var probe [1]byte
		n, err := io.ReadFull(v.r, probe[:])
		if n > 0 {
			v.err = fmt.Errorf("%w: more than %d bytes", ErrSizeMismatch, v.size)
			return 0, v.err
		}
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			v.err = err
			return 0, err
		}
		return 0, v.finish()
	}
	if v.size >= 0 && int64(len(p)) > v.size-v.n {
		p = p[:v.size-v.n]
	}
	n, err := v.r.Read(p)
	v.n += int64(n)
	if v.h != nil {
		v.h.Write(p[:n])
	}
	if err == io.EOF {
		if n > 0 {
			return n, nil // report EOF (or a mismatch) on the next call
		}
		return 0, v.finish()
	}
	if err != nil {
		v.err = err
	}
	return n, err
}

func (v *VerifyReader) finish() error {
	switch {
	case v.size >= 0 && v.n != v.size:
		v.err = fmt.Errorf("%w: got %d bytes, want %d", ErrSizeMismatch, v.n, v.size)
	case v.h != nil && !hashEqual(v.h.Sum(nil), v.want):
		v.err = fmt.Errorf("%w: sha256 %x, want %x", ErrChecksumMismatch, v.h.Sum(nil), v.want)
	default:
		v.err = io.EOF
	}
	return v.err
}

func hashEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// KeyedMutex serialises operations per string key. The zero value is ready.
type KeyedMutex struct {
	mu    sync.Mutex
	locks map[string]*keyLock
}

type keyLock struct {
	mu   sync.Mutex
	refs int
}

// Lock locks key and returns the matching unlock function.
func (k *KeyedMutex) Lock(key string) (unlock func()) {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = make(map[string]*keyLock)
	}
	l := k.locks[key]
	if l == nil {
		l = &keyLock{}
		k.locks[key] = l
	}
	l.refs++
	k.mu.Unlock()

	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		k.mu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(k.locks, key)
		}
		k.mu.Unlock()
	}
}
