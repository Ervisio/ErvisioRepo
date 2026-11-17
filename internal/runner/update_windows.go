//go:build windows

package runner

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// canExec: Windows cannot replace a running process; the daemon exits and
// the scheduled task starts the replaced executable.
func canExec() bool { return false }

func execBinary(string) error { return errors.New("exec is not supported on Windows") }

// installStaged replaces the running executable with the staged binary: the
// running file is renamed to .old (allowed while it runs) and the new one
// copied in its place. It fails when the executable directory is not
// writable.
func installStaged(staged string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate the executable: %w", err)
	}
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return "", fmt.Errorf("the executable directory is not writable, update with install.ps1: %w", err)
	}
	if err := copyFile(staged, exe); err != nil {
		_ = os.Remove(exe)
		if rerr := os.Rename(old, exe); rerr != nil {
			return "", fmt.Errorf("replace the executable: %w (restore failed: %v)", err, rerr)
		}
		return "", fmt.Errorf("replace the executable: %w", err)
	}
	_ = os.Remove(staged)
	return exe, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // the staged binary
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755) //nolint:gosec // an executable
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	return nil
}

// cleanupReplaced removes the executable left by the previous update.
func cleanupReplaced() {
	if exe, err := os.Executable(); err == nil {
		_ = os.Remove(exe + ".old")
	}
}
