// Package metered wraps a blob.Store with Prometheus metrics.
package metered

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

// Metrics holds the collectors shared by every metered store.
type Metrics struct {
	ops      *prometheus.CounterVec
	duration *prometheus.HistogramVec
	bytes    *prometheus.CounterVec
}

// NewMetrics registers the storage collectors on reg:
//
//	erepo_storage_ops_total{target,op,result}
//	erepo_storage_op_duration_seconds{target,op}
//	erepo_storage_bytes_total{target,direction}
//
// result is ok, not_found, precondition_failed, canceled or error; direction
// is in (written to the store) or out (read from it).
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		ops: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "erepo_storage_ops_total",
			Help: "Storage operations by target, operation and result.",
		}, []string{"target", "op", "result"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "erepo_storage_op_duration_seconds",
			Help:    "Storage operation latency (for get, until the body is closed).",
			Buckets: []float64{.001, .005, .01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 300},
		}, []string{"target", "op"}),
		bytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "erepo_storage_bytes_total",
			Help: "Bytes transferred to (in) and from (out) storage targets.",
		}, []string{"target", "direction"}),
	}
	m.ops = register(reg, m.ops)
	m.duration = register(reg, m.duration)
	m.bytes = register(reg, m.bytes)
	return m
}

// register adds c to reg, reusing the collector already registered under the
// same name so that every target can call NewMetrics on a shared registry.
func register[C prometheus.Collector](reg prometheus.Registerer, c C) C {
	if err := reg.Register(c); err != nil {
		var are prometheus.AlreadyRegisteredError
		if errors.As(err, &are) {
			if existing, ok := are.ExistingCollector.(C); ok {
				return existing
			}
		}
		panic(err)
	}
	return c
}

// Store is the metered wrapper.
type Store struct {
	inner blob.Store
	m     *Metrics
}

var _ blob.Store = (*Store)(nil)

// Wrap returns inner with metrics. The result implements
// blob.ConditionalPutter and blob.Presigner when inner does.
func Wrap(inner blob.Store, m *Metrics) blob.Store {
	s := &Store{inner: inner, m: m}
	cp, isCP := inner.(blob.ConditionalPutter)
	ps, isPS := inner.(blob.Presigner)
	switch {
	case isCP && isPS:
		return &struct {
			*Store
			*condPut
			*presign
		}{s, &condPut{s, cp}, &presign{s, ps}}
	case isCP:
		return &struct {
			*Store
			*condPut
		}{s, &condPut{s, cp}}
	case isPS:
		return &struct {
			*Store
			*presign
		}{s, &presign{s, ps}}
	}
	return s
}

func result(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, blob.ErrNotFound):
		return "not_found"
	case errors.Is(err, blob.ErrPreconditionFailed):
		return "precondition_failed"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "canceled"
	}
	return "error"
}

func (s *Store) observe(op string, start time.Time, err error) {
	t := s.inner.Name()
	s.m.ops.WithLabelValues(t, op, result(err)).Inc()
	s.m.duration.WithLabelValues(t, op).Observe(time.Since(start).Seconds())
}

// Name implements blob.Store.
func (s *Store) Name() string { return s.inner.Name() }

// Unwrap implements blob.Unwrapper.
func (s *Store) Unwrap() blob.Store { return s.inner }

// Caps implements blob.Store.
func (s *Store) Caps() blob.Caps { return s.inner.Caps() }

// Put implements blob.Store. Bytes are counted only for successful writes.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, o blob.PutOpts) (blob.Info, error) {
	start := time.Now()
	info, err := s.inner.Put(ctx, key, r, size, o)
	if err == nil {
		s.m.bytes.WithLabelValues(s.inner.Name(), "in").Add(float64(info.Size))
	}
	s.observe("put", start, err)
	return info, err
}

// Get implements blob.Store. The duration covers reading the body.
func (s *Store) Get(ctx context.Context, key string, rng *blob.Range) (io.ReadCloser, blob.Info, error) {
	start := time.Now()
	rc, info, err := s.inner.Get(ctx, key, rng)
	if err != nil {
		s.observe("get", start, err)
		return nil, info, err
	}
	return &body{rc: rc, s: s, start: start}, info, nil
}

type body struct {
	rc    io.ReadCloser
	s     *Store
	start time.Time
	n     int64
	err   error
	done  bool
}

func (b *body) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	b.n += int64(n)
	if err != nil && err != io.EOF {
		b.err = err
	}
	return n, err
}

func (b *body) Close() error {
	err := b.rc.Close()
	if !b.done {
		b.done = true
		b.s.m.bytes.WithLabelValues(b.s.inner.Name(), "out").Add(float64(b.n))
		b.s.observe("get", b.start, b.err)
	}
	return err
}

// Stat implements blob.Store.
func (s *Store) Stat(ctx context.Context, key string) (blob.Info, error) {
	start := time.Now()
	info, err := s.inner.Stat(ctx, key)
	s.observe("stat", start, err)
	return info, err
}

// Delete implements blob.Store.
func (s *Store) Delete(ctx context.Context, key string) error {
	start := time.Now()
	err := s.inner.Delete(ctx, key)
	s.observe("delete", start, err)
	return err
}

// List implements blob.Store.
func (s *Store) List(ctx context.Context, prefix string, fn func(blob.Info) error) error {
	start := time.Now()
	err := s.inner.List(ctx, prefix, fn)
	s.observe("list", start, err)
	return err
}

type condPut struct {
	s  *Store
	cp blob.ConditionalPutter
}

// PutConditional implements blob.ConditionalPutter.
func (c *condPut) PutConditional(ctx context.Context, key string, r io.Reader, size int64, cond blob.Condition, o blob.PutOpts) (blob.Info, error) {
	start := time.Now()
	info, err := c.cp.PutConditional(ctx, key, r, size, cond, o)
	if err == nil {
		c.s.m.bytes.WithLabelValues(c.s.inner.Name(), "in").Add(float64(info.Size))
	}
	c.s.observe("put_conditional", start, err)
	return info, err
}

type presign struct {
	s  *Store
	ps blob.Presigner
}

// PresignGet implements blob.Presigner.
func (p *presign) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	start := time.Now()
	u, err := p.ps.PresignGet(ctx, key, ttl)
	p.s.observe("presign_get", start, err)
	return u, err
}

// PresignPut implements blob.Presigner.
func (p *presign) PresignPut(ctx context.Context, key string, size int64, sha256Hex string, ttl time.Duration) (string, http.Header, error) {
	start := time.Now()
	u, h, err := p.ps.PresignPut(ctx, key, size, sha256Hex, ttl)
	p.s.observe("presign_put", start, err)
	return u, h, err
}
