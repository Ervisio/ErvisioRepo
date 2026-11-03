package git

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// InitPool creates the bare object pool of a fork network at path, or
// leaves an existing one alone. A pool is append-only: automatic gc,
// pruning and maintenance are off, because the forks borrow its objects
// through objects/info/alternates and a pruned object would corrupt them.
func InitPool(ctx context.Context, path string) error {
	if !filepath.IsAbs(path) {
		return ErrNotRepoPath
	}
	if _, err := os.Stat(filepath.Join(path, "objects")); err == nil {
		return nil
	}
	if err := InitBare(ctx, path, "main"); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("init pool: %w", err)
	}
	for _, kv := range [][2]string{
		{"gc.pruneExpire", "never"},
		{"gc.reflogExpire", "never"},
		{"maintenance.auto", "false"},
		{"repack.writeBitmaps", "false"},
	} {
		if _, err := Run(ctx, path, "config", "--local", kv[0], kv[1]); err != nil {
			return fmt.Errorf("init pool: %w", err)
		}
	}
	return nil
}

// SetAlternates makes the bare repository at repoPath borrow the objects
// of the bare repository at poolPath (objects/info/alternates). The entry
// is relative, so the hot tier can be moved as a whole. Both paths must be
// held by the caller (a lease on the repository, the pool lock or a pool
// that is never pruned).
func SetAlternates(repoPath, poolPath string) error {
	if !filepath.IsAbs(repoPath) || !filepath.IsAbs(poolPath) {
		return ErrNotRepoPath
	}
	objects := filepath.Join(repoPath, "objects")
	rel, err := filepath.Rel(objects, filepath.Join(poolPath, "objects"))
	if err != nil {
		return fmt.Errorf("alternates: %w", err)
	}
	info := filepath.Join(objects, "info")
	if err := os.MkdirAll(info, 0o750); err != nil {
		return fmt.Errorf("alternates: %w", err)
	}
	tmp := filepath.Join(info, "alternates.tmp")
	if err := os.WriteFile(tmp, []byte(filepath.ToSlash(rel)+"\n"), 0o600); err != nil {
		return fmt.Errorf("alternates: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(info, "alternates")); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("alternates: %w", err)
	}
	return nil
}

// HasAlternates reports whether the bare repository at repoPath borrows
// objects from another object store.
func HasAlternates(repoPath string) (bool, error) {
	b, err := os.ReadFile(filepath.Join(repoPath, "objects", "info", "alternates")) //nolint:gosec // path under a leased repository
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("alternates: %w", err)
	}
	return strings.TrimSpace(string(b)) != "", nil
}
