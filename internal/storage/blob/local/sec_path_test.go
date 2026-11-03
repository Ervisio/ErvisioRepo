package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

// TestSecPathTraversal checks that hostile keys are rejected before any
// filesystem access and that nothing is written outside the base directory.
func TestSecPathTraversal(t *testing.T) {
	parent := t.TempDir()
	base := filepath.Join(parent, "base")
	if err := os.Mkdir(base, 0o750); err != nil {
		t.Fatal(err)
	}
	s, err := New("hot", base)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	keys := []string{"../escape", "a/../../escape", "/etc/passwd", `..\escape`, "a/./b",
		".erepo-tmp-x", "a/.erepo-lock-b", "a\x00b", "a//b", filepath.Join(parent, "escape")}
	for _, k := range keys {
		if _, err := s.Put(ctx, k, strings.NewReader("x"), 1, blob.PutOpts{}); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("Put(%q) = %v", k, err)
		}
		if _, _, err := s.Get(ctx, k, nil); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("Get(%q) = %v", k, err)
		}
		if _, err := s.Stat(ctx, k); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("Stat(%q) = %v", k, err)
		}
		if err := s.Delete(ctx, k); !errors.Is(err, blob.ErrInvalidKey) {
			t.Errorf("Delete(%q) = %v", k, err)
		}
	}
	if err := s.List(ctx, "../", func(blob.Info) error { return nil }); !errors.Is(err, blob.ErrInvalidKey) {
		t.Errorf("List(../) = %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("files created outside the base directory: %v", entries)
	}
}
