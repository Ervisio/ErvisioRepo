// Package retry wraps a blob.Store and retries transient failures (see
// blob.IsTransient) with exponential backoff and full jitter.
//
// Only operations that can be repeated safely are retried:
//   - Put, when the body is an io.Seeker (it is rewound before each attempt);
//   - Get, both opening and reading: a read that fails mid-stream resumes
//     with a Range request when the store supports ranges and the object's
//     ETag is unchanged;
//   - Stat and Delete;
//   - List, until the callback has seen the first entry.
//
// Conditional puts are never retried: after an ambiguous failure the write
// may have happened, and a retry would report a false conflict.
package retry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

// Options configures the backoff.
type Options struct {
	MaxAttempts int           // total attempts, default 5
	BaseDelay   time.Duration // default 100ms
	MaxDelay    time.Duration // default 10s
	// Sleep waits d or until ctx ends (tests replace it).
	Sleep func(ctx context.Context, d time.Duration) error
}

// Store is the retrying wrapper.
type Store struct {
	inner blob.Store
	o     Options
}

var _ blob.Store = (*Store)(nil)

// Wrap returns inner with retries. The result also implements
// blob.ConditionalPutter and blob.Presigner when inner does (both passed
// through without retries).
func Wrap(inner blob.Store, o Options) blob.Store {
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 5
	}
	if o.BaseDelay <= 0 {
		o.BaseDelay = 100 * time.Millisecond
	}
	if o.MaxDelay <= 0 {
		o.MaxDelay = 10 * time.Second
	}
	if o.Sleep == nil {
		o.Sleep = sleep
	}
	s := &Store{inner: inner, o: o}
	cp, isCP := inner.(blob.ConditionalPutter)
	ps, isPS := inner.(blob.Presigner)
	switch {
	case isCP && isPS:
		return &struct {
			*Store
			blob.ConditionalPutter
			blob.Presigner
		}{s, cp, ps}
	case isCP:
		return &struct {
			*Store
			blob.ConditionalPutter
		}{s, cp}
	case isPS:
		return &struct {
			*Store
			blob.Presigner
		}{s, ps}
	}
	return s
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// delay returns the full-jitter backoff before attempt n (n >= 1).
func (s *Store) delay(n int) time.Duration {
	d := s.o.BaseDelay << min(n-1, 30)
	if d <= 0 || d > s.o.MaxDelay {
		d = s.o.MaxDelay
	}
	return time.Duration(rand.Int64N(int64(d)) + 1) //nolint:gosec // jitter, not security
}

// do runs fn until it succeeds, fails permanently or attempts run out.
func (s *Store) do(ctx context.Context, fn func() error) error {
	var err error
	for n := 1; ; n++ {
		if err = fn(); err == nil || !blob.IsTransient(err) || n >= s.o.MaxAttempts {
			return err
		}
		if serr := s.o.Sleep(ctx, s.delay(n)); serr != nil {
			return fmt.Errorf("%w (last error: %w)", serr, err)
		}
	}
}

// Name implements blob.Store.
func (s *Store) Name() string { return s.inner.Name() }

// Unwrap implements blob.Unwrapper.
func (s *Store) Unwrap() blob.Store { return s.inner }

// Caps implements blob.Store.
func (s *Store) Caps() blob.Caps { return s.inner.Caps() }

// Put implements blob.Store.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, o blob.PutOpts) (blob.Info, error) {
	seeker, ok := r.(io.Seeker)
	if !ok {
		return s.inner.Put(ctx, key, r, size, o)
	}
	start, err := seeker.Seek(0, io.SeekCurrent)
	if err != nil {
		return s.inner.Put(ctx, key, r, size, o)
	}
	var info blob.Info
	first := true
	err = s.do(ctx, func() error {
		if !first {
			if _, err := seeker.Seek(start, io.SeekStart); err != nil {
				return fmt.Errorf("retry: rewind body: %w", err)
			}
		}
		first = false
		var err error
		info, err = s.inner.Put(ctx, key, r, size, o)
		return err
	})
	return info, err
}

// Get implements blob.Store.
func (s *Store) Get(ctx context.Context, key string, rng *blob.Range) (io.ReadCloser, blob.Info, error) {
	var rc io.ReadCloser
	var info blob.Info
	err := s.do(ctx, func() error {
		var err error
		rc, info, err = s.inner.Get(ctx, key, rng)
		return err
	})
	if err != nil {
		return nil, blob.Info{}, err
	}
	if !s.inner.Caps().Range {
		return rc, info, nil
	}
	r := &resumer{s: s, ctx: ctx, key: key, rc: rc, etag: info.ETag, remain: -1}
	if rng != nil {
		r.offset = rng.Offset
		if rng.Length >= 0 {
			r.remain = rng.Length
		}
	}
	return r, info, nil
}

// resumer reopens the object at the current offset after a transient read
// error.
type resumer struct {
	s      *Store
	ctx    context.Context
	key    string
	rc     io.ReadCloser
	etag   string
	offset int64
	remain int64 // -1: to the end
	fails  int
}

func (r *resumer) Read(p []byte) (int, error) {
	for {
		if r.remain == 0 {
			return 0, io.EOF
		}
		if r.remain > 0 && int64(len(p)) > r.remain {
			p = p[:r.remain]
		}
		n, err := r.rc.Read(p)
		r.offset += int64(n)
		if r.remain > 0 {
			r.remain -= int64(n)
		}
		if err == nil || err == io.EOF || n > 0 {
			if n > 0 {
				r.fails = 0
				if err != nil && err != io.EOF {
					err = nil // report the failure on the next call
				}
			}
			return n, err
		}
		if !blob.IsTransient(err) {
			return 0, err
		}
		r.fails++
		if r.fails >= r.s.o.MaxAttempts {
			return 0, err
		}
		if serr := r.s.o.Sleep(r.ctx, r.s.delay(r.fails)); serr != nil {
			return 0, fmt.Errorf("%w (last error: %w)", serr, err)
		}
		_ = r.rc.Close()
		rc, info, gerr := r.s.inner.Get(r.ctx, r.key, &blob.Range{Offset: r.offset, Length: r.remain})
		if gerr != nil {
			r.rc = io.NopCloser(errReader{gerr})
			if !blob.IsTransient(gerr) {
				return 0, gerr
			}
			continue
		}
		if info.ETag != r.etag {
			_ = rc.Close()
			err := fmt.Errorf("retry: get %s: object changed while reading: %w", r.key, blob.ErrPreconditionFailed)
			r.rc = io.NopCloser(errReader{err})
			return 0, err
		}
		r.rc = rc
	}
}

func (r *resumer) Close() error { return r.rc.Close() }

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// Stat implements blob.Store.
func (s *Store) Stat(ctx context.Context, key string) (blob.Info, error) {
	var info blob.Info
	err := s.do(ctx, func() error {
		var err error
		info, err = s.inner.Stat(ctx, key)
		return err
	})
	return info, err
}

// Delete implements blob.Store.
func (s *Store) Delete(ctx context.Context, key string) error {
	return s.do(ctx, func() error { return s.inner.Delete(ctx, key) })
}

// List implements blob.Store.
func (s *Store) List(ctx context.Context, prefix string, fn func(blob.Info) error) error {
	called := false
	var fnErr error
	err := s.do(ctx, func() error {
		err := s.inner.List(ctx, prefix, func(i blob.Info) error {
			called = true
			if err := fn(i); err != nil {
				fnErr = err
				return err
			}
			return nil
		})
		if err != nil && (called || fnErr != nil) {
			return permanent{err}
		}
		return err
	})
	var p permanent
	if errors.As(err, &p) {
		return p.err
	}
	return err
}

// permanent stops retries for an error that is otherwise transient.
type permanent struct{ err error }

func (p permanent) Error() string   { return p.err.Error() }
func (p permanent) Unwrap() error   { return p.err }
func (p permanent) Transient() bool { return false }
