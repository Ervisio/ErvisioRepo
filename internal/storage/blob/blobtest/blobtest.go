// Package blobtest is the conformance suite every blob.Store must pass
// (PLAN §11, Fase 1), plus testcontainers helpers for MinIO and SFTP.
//
// The large-object test uses 64 MiB + 1 byte by default; with
// EREPO_STORAGE_BIG=1 it uses 5 GiB.
package blobtest

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

// Options tunes the suite for a store.
type Options struct {
	// LargeSize overrides the size of the large object (default 64 MiB + 1,
	// or 5 GiB with EREPO_STORAGE_BIG=1).
	LargeSize int64
	// FailSize is the size of the object used by the "network failure in the
	// middle of a large upload" test (default 24 MiB). Drivers with multipart
	// uploads should set their threshold below it in tests.
	FailSize int64
	// ListCount is how many objects the list test creates (default 60).
	ListCount int
}

// Run runs the conformance suite against the store returned by newStore.
// Every subtest works under its own random prefix, so the store may be shared
// with other data (e.g. a bucket).
func Run(t *testing.T, newStore func(t *testing.T) blob.Store, opts Options) {
	t.Helper()
	if opts.LargeSize == 0 {
		opts.LargeSize = 64<<20 + 1
		if os.Getenv("EREPO_STORAGE_BIG") == "1" {
			opts.LargeSize = 5 << 30
		}
	}
	if opts.FailSize == 0 {
		opts.FailSize = 24 << 20
	}
	if opts.ListCount == 0 {
		opts.ListCount = 60
	}
	s := newStore(t)
	base := "conformance/" + randHex(6)
	tests := []struct {
		name string
		fn   func(t *testing.T, s blob.Store, p string, o Options)
	}{
		{"PutGet", testPutGet},
		{"ZeroBytes", testZeroBytes},
		{"Range", testRange},
		{"Large", testLarge},
		{"Overwrite", testOverwrite},
		{"NotFound", testNotFound},
		{"List", testList},
		{"InvalidKeys", testInvalidKeys},
		{"ConcurrentWrites", testConcurrent},
		{"FailingReader", testFailingReader},
		{"SizeMismatch", testSizeMismatch},
		{"Checksum", testChecksum},
		{"Cancel", testCancel},
		{"Conditional", testConditional},
		{"Presign", testPresign},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.fn(t, s, base+"/"+strings.ToLower(tc.name), opts)
		})
	}
}

// Data returns n deterministic pseudo-random bytes for seed.
func Data(seed uint64, n int) []byte {
	b := make([]byte, n)
	_, _ = newGen(seed).Read(b)
	return b
}

// Reader returns a reader of n deterministic pseudo-random bytes for seed,
// equal to Data(seed, n) without holding them in memory.
func Reader(seed uint64, n int64) io.Reader { return io.LimitReader(newGen(seed), n) }

func newGen(seed uint64) *mrand.ChaCha8 {
	var s [32]byte
	binary.LittleEndian.PutUint64(s[:8], seed)
	return mrand.NewChaCha8(s)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func put(t *testing.T, s blob.Store, key string, data []byte) blob.Info {
	t.Helper()
	info, err := s.Put(context.Background(), key, bytes.NewReader(data), int64(len(data)), blob.PutOpts{})
	if err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
	if info.Size != int64(len(data)) {
		t.Fatalf("put %s: info size %d, want %d", key, info.Size, len(data))
	}
	return info
}

func get(t *testing.T, s blob.Store, key string, rng *blob.Range) ([]byte, blob.Info) {
	t.Helper()
	rc, info, err := s.Get(context.Background(), key, rng)
	if err != nil {
		t.Fatalf("get %s %+v: %v", key, rng, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %s %+v: %v", key, rng, err)
	}
	return b, info
}

func mustNotExist(t *testing.T, s blob.Store, key string) {
	t.Helper()
	if _, err := s.Stat(context.Background(), key); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("stat %s: got %v, want ErrNotFound", key, err)
	}
}

func listKeys(t *testing.T, s blob.Store, prefix string) []string {
	t.Helper()
	var keys []string
	err := s.List(context.Background(), prefix, func(i blob.Info) error {
		keys = append(keys, i.Key)
		return nil
	})
	if err != nil {
		t.Fatalf("list %q: %v", prefix, err)
	}
	slices.Sort(keys)
	return keys
}

func testPutGet(t *testing.T, s blob.Store, p string, _ Options) {
	for i, n := range []int{1, 1000, 1<<20 + 3} {
		key := fmt.Sprintf("%s/obj-%d", p, i)
		data := Data(uint64(i)+1, n) //nolint:gosec // i is a small non-negative index
		put(t, s, key, data)
		got, info := get(t, s, key, nil)
		if !bytes.Equal(got, data) {
			t.Fatalf("%s: content mismatch (%d bytes, want %d)", key, len(got), n)
		}
		if info.Key != key || info.Size != int64(n) {
			t.Fatalf("%s: get info %+v", key, info)
		}
		st, err := s.Stat(context.Background(), key)
		if err != nil || st.Size != int64(n) || st.Key != key || st.ETag == "" {
			t.Fatalf("%s: stat %+v, %v", key, st, err)
		}
	}
}

func testZeroBytes(t *testing.T, s blob.Store, p string, _ Options) {
	key := p + "/empty"
	put(t, s, key, nil)
	got, info := get(t, s, key, nil)
	if len(got) != 0 || info.Size != 0 {
		t.Fatalf("got %d bytes, info %+v", len(got), info)
	}
	got, _ = get(t, s, key, &blob.Range{Offset: 0, Length: -1})
	if len(got) != 0 {
		t.Fatalf("range on empty: %d bytes", len(got))
	}
	if _, _, err := s.Get(context.Background(), key, &blob.Range{Offset: 1, Length: 1}); !errors.Is(err, blob.ErrInvalidRange) {
		t.Fatalf("range past end of empty object: %v", err)
	}
	// Unknown size.
	info, err := s.Put(context.Background(), p+"/empty-unknown", bytes.NewReader(nil), -1, blob.PutOpts{})
	if err != nil || info.Size != 0 {
		t.Fatalf("put unknown size: %+v %v", info, err)
	}
}

func testRange(t *testing.T, s blob.Store, p string, _ Options) {
	const n = 300_000
	key := p + "/obj"
	data := Data(7, n)
	put(t, s, key, data)
	for _, r := range []blob.Range{
		{Offset: 0, Length: 10}, {Offset: 5, Length: -1}, {Offset: 65530, Length: 20}, {Offset: 65536, Length: 65536}, {Offset: 131071, Length: 2}, {Offset: 299_990, Length: 100},
		{Offset: n - 1, Length: 1}, {Offset: n, Length: -1}, {Offset: n, Length: 5}, {Offset: 0, Length: 0}, {Offset: 100, Length: 0}, {Offset: 0, Length: n}, {Offset: 0, Length: -1},
	} {
		got, info := get(t, s, key, &r)
		end := n
		if r.Length >= 0 && r.Offset+r.Length < n {
			end = int(r.Offset + r.Length)
		}
		if want := data[r.Offset:end]; !bytes.Equal(got, want) {
			t.Fatalf("range %+v: got %d bytes, want %d", r, len(got), len(want))
		}
		if info.Size != n {
			t.Fatalf("range %+v: info size %d, want %d", r, info.Size, n)
		}
	}
	for _, r := range []blob.Range{{Offset: n + 1, Length: 1}, {Offset: -1, Length: 5}} {
		if _, _, err := s.Get(context.Background(), key, &r); !errors.Is(err, blob.ErrInvalidRange) {
			t.Fatalf("range %+v: got %v, want ErrInvalidRange", r, err)
		}
	}
}

func testLarge(t *testing.T, s blob.Store, p string, o Options) {
	if testing.Short() {
		t.Skip("short mode")
	}
	key := p + "/big"
	h := sha256.New()
	if _, err := io.Copy(h, Reader(42, o.LargeSize)); err != nil {
		t.Fatal(err)
	}
	digest := hex.EncodeToString(h.Sum(nil))
	ctx := context.Background()
	info, err := s.Put(ctx, key, Reader(42, o.LargeSize), o.LargeSize, blob.PutOpts{SHA256: digest})
	if err != nil {
		t.Fatalf("put large: %v", err)
	}
	if info.Size != o.LargeSize {
		t.Fatalf("put large: size %d, want %d", info.Size, o.LargeSize)
	}
	rc, _, err := s.Get(ctx, key, nil)
	if err != nil {
		t.Fatalf("get large: %v", err)
	}
	h.Reset()
	n, err := io.Copy(h, rc)
	_ = rc.Close()
	if err != nil || n != o.LargeSize || hex.EncodeToString(h.Sum(nil)) != digest {
		t.Fatalf("get large: %d bytes, err %v, digest match %v", n, err, hex.EncodeToString(h.Sum(nil)) == digest)
	}
	tail, _ := get(t, s, key, &blob.Range{Offset: o.LargeSize - 10, Length: -1})
	r := Reader(42, o.LargeSize)
	if _, err := io.CopyN(io.Discard, r, o.LargeSize-10); err != nil {
		t.Fatal(err)
	}
	want, _ := io.ReadAll(r)
	if !bytes.Equal(tail, want) {
		t.Fatalf("tail range mismatch")
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("delete large: %v", err)
	}
}

func testOverwrite(t *testing.T, s blob.Store, p string, _ Options) {
	key := p + "/obj"
	i1 := put(t, s, key, []byte("version one"))
	i2 := put(t, s, key, []byte("version two!!"))
	i3 := put(t, s, key, []byte("version 3 !!!")) // same size as v2
	got, _ := get(t, s, key, nil)
	if string(got) != "version 3 !!!" {
		t.Fatalf("got %q", got)
	}
	if i1.ETag == i2.ETag || i2.ETag == i3.ETag {
		t.Fatalf("etag did not change on overwrite: %q %q %q", i1.ETag, i2.ETag, i3.ETag)
	}
	st, err := s.Stat(context.Background(), key)
	if err != nil || st.ETag != i3.ETag {
		t.Fatalf("stat etag %q, put etag %q, err %v", st.ETag, i3.ETag, err)
	}
	if keys := listKeys(t, s, p+"/"); !slices.Equal(keys, []string{key}) {
		t.Fatalf("list after overwrites: %v", keys)
	}
}

func testNotFound(t *testing.T, s blob.Store, p string, _ Options) {
	ctx := context.Background()
	key := p + "/missing"
	if _, _, err := s.Get(ctx, key, nil); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("get missing: %v", err)
	}
	mustNotExist(t, s, key)
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	put(t, s, key, []byte("x"))
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	mustNotExist(t, s, key)
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("delete twice: %v", err)
	}
	// A "directory" that only exists as a prefix is not an object.
	put(t, s, p+"/dir/child", []byte("x"))
	mustNotExist(t, s, p+"/dir")
	if _, _, err := s.Get(ctx, p+"/dir", nil); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("get prefix-only key: %v", err)
	}
}

func testList(t *testing.T, s blob.Store, p string, o Options) {
	var want, wantA, wantASlash []string
	sizes := map[string]int64{}
	for i := range o.ListCount {
		var key string
		switch i % 3 {
		case 0:
			key = fmt.Sprintf("%s/l/a/%03d", p, i)
		case 1:
			key = fmt.Sprintf("%s/l/ab/%03d", p, i)
		default:
			key = fmt.Sprintf("%s/l/b/c/%03d", p, i)
		}
		put(t, s, key, Data(uint64(i), i))
		sizes[key] = int64(i)
		want = append(want, key)
		if i%3 != 2 {
			wantA = append(wantA, key)
		}
		if i%3 == 0 {
			wantASlash = append(wantASlash, key)
		}
	}
	put(t, s, p+"/other", []byte("x"))
	slices.Sort(want)
	slices.Sort(wantA)
	slices.Sort(wantASlash)

	check := func(prefix string, exp []string) {
		t.Helper()
		if got := listKeys(t, s, prefix); !slices.Equal(got, exp) {
			t.Fatalf("list %q: got %d keys %v, want %d", prefix, len(got), got, len(exp))
		}
	}
	check(p+"/l/", want)
	check(p+"/l", want)
	check(p+"/l/a", wantA)
	check(p+"/l/a/", wantASlash)
	check(p+"/nothing/", nil)
	check(p+"/l/b/c/00", []string{p + "/l/b/c/002", p + "/l/b/c/005", p + "/l/b/c/008"})

	err := s.List(context.Background(), p+"/l/", func(i blob.Info) error {
		if i.Size != sizes[i.Key] {
			return fmt.Errorf("size of %s: %d, want %d", i.Key, i.Size, sizes[i.Key])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("stop")
	calls := 0
	err = s.List(context.Background(), p+"/l/", func(blob.Info) error { calls++; return stop })
	if !errors.Is(err, stop) || calls != 1 {
		t.Fatalf("early stop: err %v, calls %d", err, calls)
	}
}

func testInvalidKeys(t *testing.T, s blob.Store, _ string, _ Options) {
	ctx := context.Background()
	bad := []string{
		"", "/abs", "trail/", "a//b", "../x", "a/../b", "a/./b", ".hidden", "a/.x",
		`a\b`, "a\x00b", "a b", "a\nb", "é", strings.Repeat("a", blob.MaxKeyLen+1),
	}
	for _, k := range bad {
		if _, err := s.Put(ctx, k, strings.NewReader("x"), 1, blob.PutOpts{}); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("put %q: %v", k, err)
		}
		if _, _, err := s.Get(ctx, k, nil); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("get %q: %v", k, err)
		}
		if _, err := s.Stat(ctx, k); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("stat %q: %v", k, err)
		}
		if err := s.Delete(ctx, k); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("delete %q: %v", k, err)
		}
	}
	for _, prefix := range []string{"../", "/x", "a//"} {
		if err := s.List(ctx, prefix, func(blob.Info) error { return nil }); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("list %q: %v", prefix, err)
		}
	}
}

func testConcurrent(t *testing.T, s blob.Store, p string, _ Options) {
	const workers = 8
	const size = 200_000
	ctx := context.Background()
	key := p + "/same"
	sums := map[string]bool{}
	var wg sync.WaitGroup
	errs := make(chan error, 2*workers)
	for i := range workers {
		data := Data(uint64(1000+i), size)
		sums[sum(data)] = true
		wg.Go(func() {
			if _, err := s.Put(ctx, key, bytes.NewReader(data), size, blob.PutOpts{}); err != nil {
				errs <- fmt.Errorf("put same key: %w", err)
			}
		})
		other := fmt.Sprintf("%s/k-%d", p, i)
		wg.Go(func() {
			if _, err := s.Put(ctx, other, bytes.NewReader(data), size, blob.PutOpts{}); err != nil {
				errs <- fmt.Errorf("put %s: %w", other, err)
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	got, _ := get(t, s, key, nil)
	if !sums[sum(got)] {
		t.Fatalf("concurrent writes produced a mixed object (%d bytes)", len(got))
	}
	for i := range workers {
		got, _ := get(t, s, fmt.Sprintf("%s/k-%d", p, i), nil)
		if !bytes.Equal(got, Data(uint64(1000+i), size)) {
			t.Fatalf("object %d corrupted", i)
		}
	}
	if keys := listKeys(t, s, p+"/"); len(keys) != workers+1 {
		t.Fatalf("list after concurrent writes: %v", keys)
	}
}

// FailingReader yields the first n bytes of r, then fails with err.
type FailingReader struct {
	R   io.Reader
	N   int64
	Err error
}

func (f *FailingReader) Read(p []byte) (int, error) {
	if f.N <= 0 {
		return 0, f.Err
	}
	if int64(len(p)) > f.N {
		p = p[:f.N]
	}
	n, err := f.R.Read(p)
	f.N -= int64(n)
	return n, err
}

// ErrSimulated is the error injected by the network failure tests.
var ErrSimulated = errors.New("blobtest: simulated network failure")

func testFailingReader(t *testing.T, s blob.Store, p string, o Options) {
	ctx := context.Background()
	cases := []struct {
		name      string
		size, cut int64
	}{
		{"small", 5000, 1000},
		{"large", o.FailSize, o.FailSize - 1<<20},
	}
	for _, c := range cases {
		key := p + "/new-" + c.name
		r := &FailingReader{R: Reader(1, c.size), N: c.cut, Err: ErrSimulated}
		if _, err := s.Put(ctx, key, r, c.size, blob.PutOpts{}); err == nil {
			t.Fatalf("%s: put with failing reader succeeded", c.name)
		}
		mustNotExist(t, s, key)

		key = p + "/old-" + c.name
		old := []byte("previous content")
		put(t, s, key, old)
		r = &FailingReader{R: Reader(1, c.size), N: c.cut, Err: ErrSimulated}
		if _, err := s.Put(ctx, key, r, c.size, blob.PutOpts{}); err == nil {
			t.Fatalf("%s: overwrite with failing reader succeeded", c.name)
		}
		if got, _ := get(t, s, key, nil); !bytes.Equal(got, old) {
			t.Fatalf("%s: previous content damaged: %d bytes", c.name, len(got))
		}
	}
	keys := listKeys(t, s, p+"/")
	if !slices.Equal(keys, []string{p + "/old-large", p + "/old-small"}) {
		t.Fatalf("leftovers after failed uploads: %v", keys)
	}
}

func testSizeMismatch(t *testing.T, s blob.Store, p string, _ Options) {
	ctx := context.Background()
	for _, c := range []struct {
		name     string
		declared int
		actual   int
	}{{"short", 100, 50}, {"long", 50, 100}, {"empty", 10, 0}} {
		key := p + "/" + c.name
		_, err := s.Put(ctx, key, bytes.NewReader(Data(3, c.actual)), int64(c.declared), blob.PutOpts{})
		if !errors.Is(err, blob.ErrSizeMismatch) {
			t.Fatalf("%s: got %v, want ErrSizeMismatch", c.name, err)
		}
		mustNotExist(t, s, key)
	}
	data := Data(4, 1<<20+7)
	info, err := s.Put(ctx, p+"/unknown", bytes.NewReader(data), -1, blob.PutOpts{})
	if err != nil || info.Size != int64(len(data)) {
		t.Fatalf("unknown size: %+v %v", info, err)
	}
	if got, _ := get(t, s, p+"/unknown", nil); !bytes.Equal(got, data) {
		t.Fatal("unknown size: content mismatch")
	}
}

func testChecksum(t *testing.T, s blob.Store, p string, _ Options) {
	ctx := context.Background()
	data := Data(5, 70_000)
	wrong := sum([]byte("something else"))
	if _, err := s.Put(ctx, p+"/bad", bytes.NewReader(data), int64(len(data)), blob.PutOpts{SHA256: wrong}); !errors.Is(err, blob.ErrChecksumMismatch) {
		t.Fatalf("wrong sha256: %v", err)
	}
	mustNotExist(t, s, p+"/bad")
	if _, err := s.Put(ctx, p+"/bad", bytes.NewReader(data), -1, blob.PutOpts{SHA256: wrong}); !errors.Is(err, blob.ErrChecksumMismatch) {
		t.Fatalf("wrong sha256, unknown size: %v", err)
	}
	mustNotExist(t, s, p+"/bad")
	if _, err := s.Put(ctx, p+"/good", bytes.NewReader(data), int64(len(data)), blob.PutOpts{SHA256: sum(data)}); err != nil {
		t.Fatalf("right sha256: %v", err)
	}
}

type cancelReader struct {
	r      io.Reader
	after  int64
	cancel context.CancelFunc
}

func (c *cancelReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.after -= int64(n)
	if c.after <= 0 {
		c.cancel()
	}
	return n, err
}

func testCancel(t *testing.T, s blob.Store, p string, _ Options) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const n = 4 << 20
	r := &cancelReader{r: Reader(9, n), after: n / 2, cancel: cancel}
	if _, err := s.Put(ctx, p+"/obj", r, n, blob.PutOpts{}); err == nil {
		t.Fatal("put with cancelled context succeeded")
	}
	mustNotExist(t, s, p+"/obj")
}

func testConditional(t *testing.T, s blob.Store, p string, _ Options) {
	cp, ok := s.(blob.ConditionalPutter)
	if !ok || !s.Caps().ConditionalPut {
		t.Skip("store does not support conditional writes")
	}
	ctx := context.Background()
	key := p + "/manifest.json"
	cput := func(body string, c blob.Condition) (blob.Info, error) {
		return cp.PutConditional(ctx, key, strings.NewReader(body), int64(len(body)), c, blob.PutOpts{})
	}
	if _, err := cput("x", blob.Condition{IfMatch: "nope"}); !errors.Is(err, blob.ErrPreconditionFailed) {
		t.Fatalf("if-match on missing key: %v", err)
	}
	i1, err := cput("gen 1", blob.Condition{IfNoneMatch: true})
	if err != nil {
		t.Fatalf("if-none-match on new key: %v", err)
	}
	if _, err := cput("gen X", blob.Condition{IfNoneMatch: true}); !errors.Is(err, blob.ErrPreconditionFailed) {
		t.Fatalf("if-none-match on existing key: %v", err)
	}
	i2, err := cput("gen 2", blob.Condition{IfMatch: i1.ETag})
	if err != nil {
		t.Fatalf("if-match with current etag: %v", err)
	}
	if i2.ETag == i1.ETag {
		t.Fatal("etag unchanged after conditional write")
	}
	if _, err := cput("gen Y", blob.Condition{IfMatch: i1.ETag}); !errors.Is(err, blob.ErrPreconditionFailed) {
		t.Fatalf("if-match with stale etag: %v", err)
	}
	if got, _ := get(t, s, key, nil); string(got) != "gen 2" {
		t.Fatalf("content after failed conditional writes: %q", got)
	}

	// Compare-and-swap race: exactly one writer wins.
	const workers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, fails := 0, 0
	var other []error
	for i := range workers {
		wg.Go(func() {
			_, err := cput(fmt.Sprintf("gen 3 by %d", i), blob.Condition{IfMatch: i2.ETag})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, blob.ErrPreconditionFailed):
				fails++
			default:
				other = append(other, err)
			}
		})
	}
	wg.Wait()
	if len(other) > 0 || wins != 1 || fails != workers-1 {
		t.Fatalf("cas race: %d wins, %d precondition failures, other errors %v", wins, fails, other)
	}
	if keys := listKeys(t, s, p+"/"); !slices.Equal(keys, []string{key}) {
		t.Fatalf("leftovers after conditional writes: %v", keys)
	}
}

func testPresign(t *testing.T, s blob.Store, p string, _ Options) {
	ps, ok := s.(blob.Presigner)
	if !ok || !s.Caps().Presign {
		t.Skip("store does not presign")
	}
	ctx := context.Background()
	key := p + "/obj"
	data := Data(11, 10_000)
	u, hdr, err := ps.PresignPut(ctx, key, int64(len(data)), sum(data), 5*time.Minute)
	if err != nil {
		t.Fatalf("presign put: %v", err)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(data))
	for k, v := range hdr {
		req.Header[k] = v
	}
	req.ContentLength = int64(len(data))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("presigned put: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("presigned put: %s: %s", resp.Status, body)
	}
	if got, _ := get(t, s, key, nil); !bytes.Equal(got, data) {
		t.Fatal("presigned put: content mismatch")
	}

	// A wrong body must be rejected thanks to the checksum.
	u, hdr, err = ps.PresignPut(ctx, p+"/bad", int64(len(data)), sum(data), 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(Data(12, len(data))))
	for k, v := range hdr {
		req.Header[k] = v
	}
	req.ContentLength = int64(len(data))
	if resp, err = http.DefaultClient.Do(req); err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode/100 == 2 {
			t.Fatal("presigned put with wrong content accepted")
		}
	}
	mustNotExist(t, s, p+"/bad")

	gu, err := ps.PresignGet(ctx, key, 5*time.Minute)
	if err != nil {
		t.Fatalf("presign get: %v", err)
	}
	gr, _ := http.NewRequestWithContext(ctx, http.MethodGet, gu, nil)
	resp, err = http.DefaultClient.Do(gr)
	if err != nil {
		t.Fatalf("presigned get: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, data) {
		t.Fatalf("presigned get: %s, %d bytes", resp.Status, len(body))
	}
}
