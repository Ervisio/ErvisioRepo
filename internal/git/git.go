// Package git wraps the git binary. Every git invocation of the server goes
// through Run or Exec (CLAUDE.md rule 3): argv only, a clean environment
// without user or system configuration, and a timeout.
//
// repoPath is always the path of a bare repository returned by
// gitstore.Tier.Acquire (or, for InitBare, the path being created).
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/sys"
)

// Binary is the git executable, resolved on PATH.
const Binary = "git"

// DefaultTimeout bounds a git command that does not set its own.
const DefaultTimeout = 10 * time.Minute

// ErrNotRepoPath is returned for a relative or empty repository path.
var ErrNotRepoPath = errors.New("git: repository path must be absolute")

// Env is the environment of every git process: no system, global or XDG
// configuration, no prompts, a stable locale and an empty HOME. Extra holds
// additional KEY=VALUE pairs (for example GIT_PROTOCOL).
func Env(repoPath string, extra ...string) []string {
	env := []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"HOME=",
		"XDG_CONFIG_HOME=",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"GIT_NO_REPLACE_OBJECTS=1",
		"LC_ALL=C",
		"LANG=C",
	}
	if repoPath != "" {
		env = append(env, "GIT_DIR="+repoPath)
	}
	return append(env, extra...)
}

// Cmd is one git invocation.
type Cmd struct {
	RepoPath string   // absolute; empty only for commands that need no repository
	Args     []string // arguments after "git"
	Env      []string // extra KEY=VALUE pairs
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
	Timeout  time.Duration // 0 = DefaultTimeout
}

func (c Cmd) sys() (sys.Cmd, error) {
	if c.RepoPath != "" && !filepath.IsAbs(c.RepoPath) {
		return sys.Cmd{}, ErrNotRepoPath
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	argv := append([]string{Binary}, c.Args...)
	dir := c.RepoPath
	if dir == "" {
		dir = "/"
	}
	return sys.Cmd{
		Argv: argv, Dir: dir, Env: Env(c.RepoPath, c.Env...),
		Stdin: c.Stdin, Stdout: c.Stdout, Stderr: c.Stderr, Timeout: timeout,
	}, nil
}

// Exec runs c to completion. A failure is a *sys.ExitError carrying the
// stderr tail.
func Exec(ctx context.Context, c Cmd) error {
	sc, err := c.sys()
	if err != nil {
		return err
	}
	if err := sys.Exec(ctx, sc); err != nil {
		return fmt.Errorf("git %s: %w", firstArg(c.Args), err)
	}
	return nil
}

// Start starts c without waiting (streaming transports, long-lived
// processes). The caller must call Wait on the result.
func Start(ctx context.Context, c Cmd) (*sys.Process, error) {
	sc, err := c.sys()
	if err != nil {
		return nil, err
	}
	p, err := sys.Start(ctx, sc)
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", firstArg(c.Args), err)
	}
	return p, nil
}

// Run runs git with args in the repository at repoPath and returns stdout.
func Run(ctx context.Context, repoPath string, args ...string) ([]byte, error) {
	var out bytes.Buffer
	err := Exec(ctx, Cmd{RepoPath: repoPath, Args: args, Stdout: &out})
	return out.Bytes(), err
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
