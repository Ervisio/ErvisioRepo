package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// InitBare creates an empty bare repository at path with defaultBranch as
// HEAD. It uses no templates (no sample hooks) and fails if path exists.
// The parent directory is created with mode 0o750.
func InitBare(ctx context.Context, path, defaultBranch string) error {
	if !filepath.IsAbs(path) {
		return ErrNotRepoPath
	}
	if err := ValidateBranchName(defaultBranch); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("init %s: %w", path, os.ErrExist)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("init: %w", err)
	}
	if err := Exec(ctx, Cmd{Args: []string{
		"init", "--bare", "--quiet", "--template=", "--initial-branch=" + defaultBranch, "--", path,
	}}); err != nil {
		return err
	}
	// Server-side defaults; later phases add hooks and transport settings.
	for _, kv := range [][2]string{
		{"core.logAllRefUpdates", "false"},
		{"receive.fsckObjects", "true"},
		{"transfer.fsckObjects", "true"},
		{"uploadpack.allowFilter", "true"},
		{"uploadpack.allowAnySHA1InWant", "false"},
		{"gc.auto", "0"},
	} {
		if _, err := Run(ctx, path, "config", "--local", kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}
