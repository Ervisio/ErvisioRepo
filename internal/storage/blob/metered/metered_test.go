package metered

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob/blobtest"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob/local"
)

func newLocal(t *testing.T) blob.Store {
	t.Helper()
	l, err := local.New("hot", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func TestConformance(t *testing.T) {
	blobtest.Run(t, func(t *testing.T) blob.Store {
		return Wrap(newLocal(t), NewMetrics(prometheus.NewRegistry()))
	}, blobtest.Options{})
}

func TestCounters(t *testing.T) {
	m := NewMetrics(prometheus.NewRegistry())
	s := Wrap(newLocal(t), m)
	ctx := context.Background()
	if _, err := s.Put(ctx, "a/b", strings.NewReader("hello"), 5, blob.PutOpts{}); err != nil {
		t.Fatal(err)
	}
	rc, _, err := s.Get(ctx, "a/b", &blob.Range{Offset: 1, Length: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(rc); err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	_ = rc.Close() // a second close must not count twice
	if _, err := s.Stat(ctx, "missing"); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("stat: %v", err)
	}
	cp, ok := s.(blob.ConditionalPutter)
	if !ok {
		t.Fatal("ConditionalPutter not forwarded")
	}
	if _, err := cp.PutConditional(ctx, "a/b", strings.NewReader("x"), 1, blob.Condition{IfNoneMatch: true}, blob.PutOpts{}); !errors.Is(err, blob.ErrPreconditionFailed) {
		t.Fatalf("conditional put: %v", err)
	}
	if err := s.List(ctx, "", func(blob.Info) error { return nil }); err != nil {
		t.Fatal(err)
	}

	checks := []struct {
		c    prometheus.Collector
		want float64
	}{
		{m.ops.WithLabelValues("hot", "put", "ok"), 1},
		{m.ops.WithLabelValues("hot", "get", "ok"), 1},
		{m.ops.WithLabelValues("hot", "stat", "not_found"), 1},
		{m.ops.WithLabelValues("hot", "put_conditional", "precondition_failed"), 1},
		{m.ops.WithLabelValues("hot", "list", "ok"), 1},
		{m.bytes.WithLabelValues("hot", "in"), 5},
		{m.bytes.WithLabelValues("hot", "out"), 3},
	}
	for i, c := range checks {
		if got := value(t, c.c); got != c.want {
			t.Errorf("check %d: got %v, want %v", i, got, c.want)
		}
	}
	if n := count(m.duration); n != 5 {
		t.Errorf("duration series = %d", n)
	}
}

func TestResult(t *testing.T) {
	for err, want := range map[error]string{
		nil:                "ok",
		blob.ErrNotFound:   "not_found",
		context.Canceled:   "canceled",
		errors.New("boom"): "error",
	} {
		if got := result(err); got != want {
			t.Errorf("result(%v) = %q, want %q", err, got, want)
		}
	}
}

func value(t *testing.T, c prometheus.Collector) float64 {
	t.Helper()
	ch := make(chan prometheus.Metric, 1)
	c.Collect(ch)
	var pb dto.Metric
	if err := (<-ch).Write(&pb); err != nil {
		t.Fatal(err)
	}
	return pb.GetCounter().GetValue()
}

func count(c prometheus.Collector) int {
	ch := make(chan prometheus.Metric, 64)
	c.Collect(ch)
	close(ch)
	return len(ch)
}
