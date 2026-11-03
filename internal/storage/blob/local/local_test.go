package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
	"github.com/Ervisio/ErvisioRepo/internal/storage/blob/blobtest"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := New("hot", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestConformance(t *testing.T) {
	blobtest.Run(t, func(t *testing.T) blob.Store { return newStore(t) }, blobtest.Options{})
}

func TestNoTempFilesLeft(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	r := &blobtest.FailingReader{R: blobtest.Reader(1, 1<<20), N: 1000, Err: blobtest.ErrSimulated}
	if _, err := s.Put(ctx, "a/b", r, 1<<20, blob.PutOpts{}); err == nil {
		t.Fatal("expected failure")
	}
	if _, err := s.Put(ctx, "a/c", strings.NewReader("ok"), 2, blob.PutOpts{}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(s.dir, "a"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "c" {
		t.Fatalf("unexpected files: %v", entries)
	}
}

func TestSymlinkEscape(t *testing.T) {
	s := newStore(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get(context.Background(), "link/secret", nil); err == nil {
		t.Fatal("read through a symlink escaping the root")
	}
	if _, err := s.Put(context.Background(), "link/new", strings.NewReader("x"), 1, blob.PutOpts{}); err == nil {
		t.Fatal("write through a symlink escaping the root")
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); err == nil {
		t.Fatal("file created outside the root")
	}
}
