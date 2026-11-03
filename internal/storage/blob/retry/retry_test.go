package retry

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob/blobtest"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob/local"
)

var errFlaky = blob.MarkTransient(errors.New("flaky: connection reset"))

// flaky fails the first fails[op] calls of each op with a transient error,
// and can break Get bodies after breakAfter bytes.
type flaky struct {
	blob.Store
	mu         sync.Mutex
	fails      map[string]int
	calls      map[string]int
	breakAfter int64 // >0: every opened body fails after this many bytes, breaks times
	breaks     int
}

func newFlaky(t *testing.T) *flaky {
	l, err := local.New("hot", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return &flaky{Store: l, fails: map[string]int{}, calls: map[string]int{}}
}

func (f *flaky) hit(op string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[op]++
	if f.fails[op] > 0 {
		f.fails[op]--
		return errFlaky
	}
	return nil
}

func (f *flaky) Put(ctx context.Context, key string, r io.Reader, size int64, o blob.PutOpts) (blob.Info, error) {
	if err := f.hit("put"); err != nil {
		_, _ = io.CopyN(io.Discard, r, 3) // consume part of the body
		return blob.Info{}, err
	}
	return f.Store.Put(ctx, key, r, size, o)
}

func (f *flaky) Get(ctx context.Context, key string, rng *blob.Range) (io.ReadCloser, blob.Info, error) {
	if err := f.hit("get"); err != nil {
		return nil, blob.Info{}, err
	}
	rc, info, err := f.Store.Get(ctx, key, rng)
	if err != nil {
		return nil, info, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.breakAfter > 0 && f.breaks > 0 {
		f.breaks--
		return &breaking{rc: rc, left: f.breakAfter}, info, nil
	}
	return rc, info, nil
}

func (f *flaky) Stat(ctx context.Context, key string) (blob.Info, error) {
	if err := f.hit("stat"); err != nil {
		return blob.Info{}, err
	}
	return f.Store.Stat(ctx, key)
}

func (f *flaky) List(ctx context.Context, prefix string, fn func(blob.Info) error) error {
	if err := f.hit("list"); err != nil {
		return err
	}
	return f.Store.List(ctx, prefix, fn)
}

type breaking struct {
	rc   io.ReadCloser
	left int64
}

func (b *breaking) Read(p []byte) (int, error) {
	if b.left <= 0 {
		return 0, errFlaky
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	return n, err
}

func (b *breaking) Close() error { return b.rc.Close() }

func noSleep(context.Context, time.Duration) error { return nil }

func TestConformance(t *testing.T) {
	blobtest.Run(t, func(t *testing.T) blob.Store {
		return Wrap(newFlaky(t), Options{Sleep: noSleep})
	}, blobtest.Options{})
}

func TestPutRetriesSeekable(t *testing.T) {
	f := newFlaky(t)
	s := Wrap(f, Options{Sleep: noSleep})
	f.fails["put"] = 2
	data := blobtest.Data(1, 1000)
	if _, err := s.Put(context.Background(), "k", bytes.NewReader(data), 1000, blob.PutOpts{}); err != nil {
		t.Fatal(err)
	}
	if f.calls["put"] != 3 {
		t.Fatalf("put calls = %d", f.calls["put"])
	}
	rc, _, err := s.Get(context.Background(), "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(got, data) {
		t.Fatal("body not rewound between attempts")
	}
}

func TestPutNoRetryUnseekable(t *testing.T) {
	f := newFlaky(t)
	s := Wrap(f, Options{Sleep: noSleep})
	f.fails["put"] = 1
	r := io.LimitReader(strings.NewReader("hello"), 5)
	if _, err := s.Put(context.Background(), "k", r, 5, blob.PutOpts{}); !errors.Is(err, errFlaky) {
		t.Fatalf("got %v", err)
	}
	if f.calls["put"] != 1 {
		t.Fatalf("unseekable body retried: %d calls", f.calls["put"])
	}
}

func TestGiveUp(t *testing.T) {
	f := newFlaky(t)
	s := Wrap(f, Options{MaxAttempts: 3, Sleep: noSleep})
	f.fails["stat"] = 10
	if _, err := s.Stat(context.Background(), "k"); !errors.Is(err, errFlaky) {
		t.Fatalf("got %v", err)
	}
	if f.calls["stat"] != 3 {
		t.Fatalf("stat calls = %d", f.calls["stat"])
	}
}

func TestPermanentNotRetried(t *testing.T) {
	f := newFlaky(t)
	s := Wrap(f, Options{Sleep: noSleep})
	if _, err := s.Stat(context.Background(), "missing"); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if f.calls["stat"] != 1 {
		t.Fatalf("not-found retried: %d calls", f.calls["stat"])
	}
}

func TestGetResumes(t *testing.T) {
	f := newFlaky(t)
	s := Wrap(f, Options{Sleep: noSleep})
	ctx := context.Background()
	data := blobtest.Data(2, 100_000)
	if _, err := f.Store.Put(ctx, "k", bytes.NewReader(data), int64(len(data)), blob.PutOpts{}); err != nil {
		t.Fatal(err)
	}
	f.breakAfter, f.breaks = 30_000, 3
	rc, _, err := s.Get(ctx, "k", &blob.Range{Offset: 10, Length: 90_000})
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data[10:90_010]) {
		t.Fatalf("resumed read mismatch: %d bytes", len(got))
	}
	if f.calls["get"] != 3 { // three bodies of 30k each
		t.Fatalf("get calls = %d", f.calls["get"])
	}
}

func TestGetDetectsChange(t *testing.T) {
	f := newFlaky(t)
	s := Wrap(f, Options{Sleep: noSleep})
	ctx := context.Background()
	if _, err := f.Store.Put(ctx, "k", bytes.NewReader(blobtest.Data(3, 1000)), 1000, blob.PutOpts{}); err != nil {
		t.Fatal(err)
	}
	f.breakAfter, f.breaks = 100, 1
	rc, _, err := s.Get(ctx, "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	buf := make([]byte, 100)
	if _, err := io.ReadFull(rc, buf); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Store.Put(ctx, "k", bytes.NewReader(blobtest.Data(4, 1000)), 1000, blob.PutOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(rc); !errors.Is(err, blob.ErrPreconditionFailed) {
		t.Fatalf("mixed content not detected: %v", err)
	}
}

func TestListRetriesOnlyBeforeFirstEntry(t *testing.T) {
	f := newFlaky(t)
	s := Wrap(f, Options{Sleep: noSleep})
	ctx := context.Background()
	f.fails["list"] = 2
	if err := s.List(ctx, "", func(blob.Info) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if f.calls["list"] != 3 {
		t.Fatalf("list calls = %d", f.calls["list"])
	}
	if _, err := f.Store.Put(ctx, "a", strings.NewReader("x"), 1, blob.PutOpts{}); err != nil {
		t.Fatal(err)
	}
	f.calls["list"] = 0
	err := s.List(ctx, "", func(blob.Info) error { return errFlaky })
	if !errors.Is(err, errFlaky) || f.calls["list"] != 1 {
		t.Fatalf("callback error retried: %v, %d calls", err, f.calls["list"])
	}
}

func TestBackoffBounds(t *testing.T) {
	s := &Store{o: Options{BaseDelay: 10 * time.Millisecond, MaxDelay: time.Second}}
	for n := 1; n < 70; n++ {
		if d := s.delay(n); d <= 0 || d > time.Second {
			t.Fatalf("delay(%d) = %v", n, d)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := newFlaky(t)
	f.fails["stat"] = 5
	if _, err := Wrap(f, Options{}).Stat(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled backoff: %v", err)
	}
}

func TestOptionalInterfaces(t *testing.T) {
	s := Wrap(newFlaky(t), Options{})
	if _, ok := s.(blob.ConditionalPutter); ok {
		t.Fatal("flaky does not implement ConditionalPutter, wrapper must not either")
	}
	l, _ := local.New("x", t.TempDir())
	defer l.Close()
	if _, ok := Wrap(l, Options{}).(blob.ConditionalPutter); !ok {
		t.Fatal("ConditionalPutter not forwarded")
	}
}
