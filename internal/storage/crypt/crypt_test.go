package crypt

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob/blobtest"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob/local"
)

func testKey(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func newLocal(t *testing.T) *local.Store {
	t.Helper()
	l, err := local.New("hot", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func wrap(t *testing.T, inner blob.Store, o Options) blob.Store {
	t.Helper()
	s, err := Wrap(inner, o)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestConformance(t *testing.T) {
	blobtest.Run(t, func(t *testing.T) blob.Store {
		return wrap(t, newLocal(t), Options{Master: testKey(1)})
	}, blobtest.Options{})
}

func TestConformanceSmallChunks(t *testing.T) {
	blobtest.Run(t, func(t *testing.T) blob.Store {
		return wrap(t, newLocal(t), Options{Master: testKey(1), ChunkSize: 1024})
	}, blobtest.Options{LargeSize: 3<<20 + 7, FailSize: 1 << 20})
}

func TestSizes(t *testing.T) {
	for _, cs := range []int{1024, DefaultChunkSize} {
		for _, n := range []int64{0, 1, int64(cs) - 1, int64(cs), int64(cs) + 1, 5 * int64(cs), 1 << 30} {
			got, err := plaintextSize(CiphertextSize(n, cs), cs)
			if err != nil || got != n {
				t.Fatalf("cs=%d n=%d: got %d %v", cs, n, got, err)
			}
		}
	}
}

func TestRangesAcrossChunks(t *testing.T) {
	s := wrap(t, newLocal(t), Options{Master: testKey(1), ChunkSize: 1024})
	ctx := context.Background()
	for _, n := range []int{0, 1, 1023, 1024, 1025, 4096, 5000} {
		data := blobtest.Data(uint64(n), n)
		if _, err := s.Put(ctx, "obj", bytes.NewReader(data), int64(n), blob.PutOpts{}); err != nil {
			t.Fatal(err)
		}
		for _, off := range []int{0, 1, 1023, 1024, 1025, 2048, n} {
			if off > n {
				continue
			}
			for _, l := range []int{-1, 0, 1, 1024, 2049, n} {
				rc, info, err := s.Get(ctx, "obj", &blob.Range{Offset: int64(off), Length: int64(l)})
				if err != nil {
					t.Fatalf("n=%d off=%d len=%d: %v", n, off, l, err)
				}
				got, err := io.ReadAll(rc)
				_ = rc.Close()
				if err != nil {
					t.Fatalf("n=%d off=%d len=%d: read: %v", n, off, l, err)
				}
				end := n
				if l >= 0 && off+l < n {
					end = off + l
				}
				if !bytes.Equal(got, data[off:end]) || info.Size != int64(n) {
					t.Fatalf("n=%d off=%d len=%d: got %d bytes, size %d", n, off, l, len(got), info.Size)
				}
			}
		}
	}
}

func TestRotation(t *testing.T) {
	inner := newLocal(t)
	ctx := context.Background()
	old := wrap(t, inner, Options{Master: testKey(1)})
	if _, err := old.Put(ctx, "a", bytes.NewReader([]byte("hello")), 5, blob.PutOpts{}); err != nil {
		t.Fatal(err)
	}
	rotated := wrap(t, inner, Options{Master: testKey(2), Previous: [][]byte{testKey(1)}})
	rc, _, err := rotated.Get(ctx, "a", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil || string(got) != "hello" {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestParseMasterKey(t *testing.T) {
	k, err := ParseMasterKey([]byte(" " + string(bytes.Repeat([]byte("ab"), 32)) + "\n"))
	if err != nil || len(k) != 32 || k[0] != 0xab {
		t.Fatalf("got %x %v", k, err)
	}
	for _, bad := range []string{"", "abcd", string(bytes.Repeat([]byte("zz"), 32))} {
		if _, err := ParseMasterKey([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := New(newLocal(t), Options{Master: []byte("short")}); err == nil {
		t.Fatal("short master key accepted")
	}
}

func TestCapsHidePresign(t *testing.T) {
	s := wrap(t, newLocal(t), Options{Master: testKey(1)})
	if s.Caps().Presign {
		t.Fatal("encrypted store must not presign")
	}
	if _, ok := s.(blob.ConditionalPutter); !ok || !s.Caps().ConditionalPut {
		t.Fatal("conditional put not forwarded")
	}
}
