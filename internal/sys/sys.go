// Package sys runs external programs. It is the only place that starts
// processes (CLAUDE.md rule 3): there is no shell, argv is passed verbatim
// and the environment is never inherited beyond what the caller lists.
package sys

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// DefaultTimeout bounds a command that does not set its own.
const DefaultTimeout = 10 * time.Minute

// maxStderr is how much of stderr an ExitError keeps.
const maxStderr = 4 << 10

// ErrInvalidArgv is returned for an empty argv or one containing NUL bytes.
var ErrInvalidArgv = errors.New("invalid argv")

// Cmd describes one process. Argv[0] is resolved with exec.LookPath on the
// PATH of the server process; nothing else of the server environment is
// passed unless listed in Env.
type Cmd struct {
	Argv    []string
	Dir     string
	Env     []string // KEY=VALUE pairs; PATH is added when missing
	Stdin   io.Reader
	Stdout  io.Writer // nil discards
	Stderr  io.Writer // nil: kept (tail) for the ExitError only
	Timeout time.Duration
}

// ExitError is a process that ran and failed.
type ExitError struct {
	Argv   []string
	Code   int // -1 when killed by a signal or the timeout
	Stderr string
	Err    error
}

func (e *ExitError) Error() string {
	msg := fmt.Sprintf("%s: exit %d", e.Argv[0], e.Code)
	if s := strings.TrimSpace(e.Stderr); s != "" {
		msg += ": " + s
	}
	return msg
}

func (e *ExitError) Unwrap() error { return e.Err }

// Validate checks argv: not empty, a non-empty program and no NUL bytes.
func Validate(argv []string) error {
	if len(argv) == 0 || argv[0] == "" {
		return fmt.Errorf("%w: empty", ErrInvalidArgv)
	}
	for _, a := range argv {
		if strings.IndexByte(a, 0) >= 0 {
			return fmt.Errorf("%w: NUL byte", ErrInvalidArgv)
		}
	}
	return nil
}

// Run runs argv with no stdin, a minimal environment and the default timeout,
// and returns its stdout.
func Run(ctx context.Context, argv ...string) ([]byte, error) {
	var out bytes.Buffer
	err := Exec(ctx, Cmd{Argv: argv, Stdout: &out})
	return out.Bytes(), err
}

// Exec runs c to completion.
func Exec(ctx context.Context, c Cmd) error {
	p, err := Start(ctx, c)
	if err != nil {
		return err
	}
	return p.Wait()
}

// Process is a started command.
type Process struct {
	cmd    *exec.Cmd
	argv   []string
	stderr *tailBuffer
	cancel context.CancelFunc
	ctx    context.Context
}

// Start starts c and returns without waiting. The process is killed when ctx
// is done or the timeout expires; Wait must always be called.
func Start(ctx context.Context, c Cmd) (*Process, error) {
	if err := Validate(c.Argv); err != nil {
		return nil, err
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	path, err := exec.LookPath(c.Argv[0])
	if err != nil {
		cancel()
		return nil, fmt.Errorf("look up %s: %w", c.Argv[0], err)
	}
	cmd := exec.CommandContext(ctx, path, c.Argv[1:]...) //nolint:gosec // argv only, validated by callers; no shell
	cmd.Args[0] = c.Argv[0]
	cmd.Dir = c.Dir
	cmd.Env = withPath(c.Env)
	cmd.Stdin = c.Stdin
	cmd.Stdout = c.Stdout
	tail := &tailBuffer{max: maxStderr}
	if c.Stderr != nil {
		cmd.Stderr = io.MultiWriter(c.Stderr, tail)
	} else {
		cmd.Stderr = tail
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// Kill the whole process group: git spawns helpers.
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start %s: %w", c.Argv[0], err)
	}
	return &Process{cmd: cmd, argv: c.Argv, stderr: tail, cancel: cancel, ctx: ctx}, nil
}

// Wait waits for the process and releases its resources.
func (p *Process) Wait() error {
	defer p.cancel()
	err := p.cmd.Wait()
	if err == nil {
		return nil
	}
	code := -1
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.Exited() {
		code = ee.ExitCode()
	}
	if ctxErr := p.ctx.Err(); ctxErr != nil {
		err = fmt.Errorf("%w: %w", ctxErr, err)
		code = -1
	}
	return &ExitError{Argv: p.argv, Code: code, Stderr: p.stderr.String(), Err: err}
}

// Kill kills the process group. Wait must still be called.
func (p *Process) Kill() {
	if p.cmd.Process != nil {
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	}
}

// Pid is the process id.
func (p *Process) Pid() int { return p.cmd.Process.Pid }

func withPath(env []string) []string {
	out := make([]string, 0, len(env)+1)
	hasPath := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			hasPath = true
		}
		out = append(out, kv)
	}
	if !hasPath {
		out = append(out, "PATH="+os.Getenv("PATH"))
	}
	return out
}

// tailBuffer keeps the last max bytes written.
type tailBuffer struct {
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.buf) }
