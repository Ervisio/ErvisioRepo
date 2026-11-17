//go:build !windows

package runner

import (
	"fmt"
	"os"
	"syscall"
)

// canExec: the process can be replaced in place.
func canExec() bool { return true }

// execBinary replaces the process with path, keeping the arguments and the
// environment (same PID, so systemd and launchd keep tracking it).
func execBinary(path string) error {
	args := append([]string{path}, os.Args[1:]...)
	if err := syscall.Exec(path, args, os.Environ()); err != nil { //nolint:gosec // a binary this runner verified and staged
		return fmt.Errorf("exec: %w", err)
	}
	return nil
}

// installStaged: on unix the staged binary is started by exec, the
// installed one stays untouched (it may be read-only).
func installStaged(staged string) (string, error) { return staged, nil }

// cleanupReplaced has nothing to clean on unix.
func cleanupReplaced() {}
