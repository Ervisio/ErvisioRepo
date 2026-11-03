package gittransport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
	"github.com/Ervisio/ErvisioRepo/internal/sys"
)

// Errors of Run. The transports show their messages to the git client.
var (
	ErrPushTooLarge = errors.New("push exceeds the maximum allowed size")
	ErrIdleTimeout  = errors.New("transfer idle for too long")
	ErrTooLong      = errors.New("transfer took too long")
)

// pushSlack is the room left above MaxPushSize for the ref update commands
// and pkt-line framing before the stream is cut; git itself enforces
// MaxPushSize on the pack (receive.maxInputSize) with a clean message.
const pushSlack = 1 << 20

// Options configure a Server.
type Options struct {
	Tier *gitstore.Tier
	// Hooks, when set, supplies the hook environment of every receive-pack.
	Hooks Hooks
	// MaxPushSize bounds the bytes received by one receive-pack.
	MaxPushSize int64
	// IdleTimeout cuts a transfer that moves no byte for this long.
	IdleTimeout time.Duration
	// MaxDuration bounds a whole transfer.
	MaxDuration time.Duration
	// Progress, when set, returns a context that makes a hydration started
	// by Acquire report its progress to w (the SSH stderr channel).
	Progress func(ctx context.Context, w io.Writer) context.Context
	Log      *slog.Logger
}

// Server runs git server commands. It is safe for concurrent use.
type Server struct{ o Options }

// New returns a Server.
func New(o Options) (*Server, error) {
	if o.Tier == nil {
		return nil, errors.New("gittransport: tier is required")
	}
	if o.MaxPushSize <= 0 || o.IdleTimeout <= 0 || o.MaxDuration <= 0 {
		return nil, errors.New("gittransport: limits must be positive")
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return &Server{o: o}, nil
}

// Request is one git command for an authorized caller.
type Request struct {
	Service Service
	Repo    repos.Repo
	// Pusher is the authenticated caller (zero for anonymous reads).
	Pusher    auth.Principal
	Transport string
	RemoteIP  string
	// Protocol is the GIT_PROTOCOL asked by the client, sanitized by Run.
	Protocol string
	// StatelessRPC and AdvertiseRefs select the Smart HTTP modes.
	StatelessRPC  bool
	AdvertiseRefs bool

	Stdin  io.Reader // nil: no input
	Stdout io.Writer
	// Stderr receives the git stderr (SSH); nil keeps it for the logs only.
	Stderr io.Writer
	// Abort, when set, unblocks the transport I/O (deadlines in the past)
	// after an idle timeout or once git has exited with input pending.
	Abort func()
}

// Acquire takes the lease the request needs. A cold repository is
// hydrated first; with req.Stderr set, its progress goes there.
func (s *Server) Acquire(ctx context.Context, req *Request) (*gitstore.Lease, error) {
	if s.o.Progress != nil && req.Stderr != nil {
		ctx = s.o.Progress(ctx, req.Stderr)
	}
	l, err := s.o.Tier.Acquire(ctx, req.Repo.ID, leaseMode(req.Service, req.AdvertiseRefs))
	if err != nil {
		return nil, fmt.Errorf("acquire %s: %w", req.Repo.FullName(), err)
	}
	return l, nil
}

// Run runs the git command of req on the repository held by lease and
// returns its exit code. The caller releases the lease. An error before git
// starts leaves Stdout untouched.
func (s *Server) Run(ctx context.Context, lease *gitstore.Lease, req *Request) (int, error) {
	protocol := SanitizeProtocol(req.Protocol)
	args := s.args(req, lease.Path)
	var env []string
	if protocol != "" {
		env = append(env, "GIT_PROTOCOL="+protocol)
	}
	if req.Service.IsWrite() && !req.AdvertiseRefs && s.o.Hooks != nil {
		hookEnv, done, err := s.o.Hooks.HookEnv(ctx, Push{Repo: req.Repo, Pusher: req.Pusher,
			Transport: req.Transport, RemoteIP: req.RemoteIP, Protocol: protocol})
		if err != nil {
			return -1, fmt.Errorf("hook environment: %w", err)
		}
		if done != nil {
			defer done()
		}
		env = append(env, hookEnv...)
	}

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	ctx, stop := context.WithTimeoutCause(ctx, s.o.MaxDuration, ErrTooLong)
	defer stop()

	inR, inW, err := os.Pipe()
	if err != nil {
		return -1, fmt.Errorf("stdin pipe: %w", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		closeAll(inR, inW)
		return -1, fmt.Errorf("stdout pipe: %w", err)
	}
	proc, err := git.Start(ctx, git.Cmd{RepoPath: lease.Path, Args: args, Env: env,
		Stdin: inR, Stdout: outW, Stderr: req.Stderr, Timeout: s.o.MaxDuration})
	// The child holds its own copies of these ends.
	closeAll(inR, outW)
	if err != nil {
		closeAll(inW, outR)
		return -1, err
	}

	act := &activity{}
	act.touch()
	watchDone := make(chan struct{})
	go s.watch(ctx, act, cancel, req.Abort, watchDone)

	stdinDone := make(chan error, 1)
	go func() {
		err := s.copyIn(inW, req, act)
		if errors.Is(err, ErrPushTooLarge) {
			cancel(ErrPushTooLarge)
		}
		_ = inW.Close()
		stdinDone <- err
	}()
	stdoutDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(flushWriter{w: req.Stdout, act: act}, outR)
		if err != nil {
			// The client went away: let git fail on EPIPE.
			cancel(fmt.Errorf("write to client: %w", err))
		}
		_ = outR.Close()
		stdoutDone <- err
	}()

	waitErr := proc.Wait()
	outErr := <-stdoutDone
	var inErr error
	select {
	case inErr = <-stdinDone:
	default:
		// git exited with input pending (an error, or an SSH client keeping
		// the channel open). Close our end so the copier stops on its next
		// write; the transport unblocks or ends its read.
		_ = inW.Close()
		if req.Abort != nil {
			req.Abort()
			inErr = <-stdinDone
		}
	}
	close(watchDone)

	cause := context.Cause(ctx)
	switch {
	case errors.Is(inErr, ErrPushTooLarge), errors.Is(cause, ErrPushTooLarge):
		return -1, ErrPushTooLarge
	case errors.Is(cause, ErrIdleTimeout), errors.Is(cause, ErrTooLong):
		return -1, cause
	}
	if waitErr != nil {
		var ee *sys.ExitError
		if errors.As(waitErr, &ee) && ee.Code > 0 {
			s.o.Log.InfoContext(ctx, "git command failed", "service", string(req.Service),
				"repo", req.Repo.FullName(), "code", ee.Code, "stderr", ee.Stderr)
			return ee.Code, nil
		}
		return -1, fmt.Errorf("git %s: %w", req.Service.subcommand(), waitErr)
	}
	if outErr != nil {
		return -1, fmt.Errorf("stream to client: %w", outErr)
	}
	return 0, nil
}

func (s *Server) args(req *Request, path string) []string {
	var args []string
	switch req.Service {
	case UploadPack:
		args = []string{"-c", "uploadpack.allowFilter=true", "-c", "uploadpack.allowAnySHA1InWant=false"}
	case ReceivePack:
		args = []string{"-c", "receive.maxInputSize=" + strconv.FormatInt(s.o.MaxPushSize, 10)}
	case UploadArchive:
		args = []string{"-c", "uploadArchive.allowUnreachable=false"}
	}
	args = append(args, req.Service.subcommand())
	if req.Service != UploadArchive {
		args = append(args, "--strict")
		if req.StatelessRPC {
			args = append(args, "--stateless-rpc")
		}
		if req.AdvertiseRefs {
			args = append(args, "--advertise-refs")
		}
	}
	return append(args, path)
}

// copyIn feeds git's stdin, bounding a receive-pack to MaxPushSize.
func (s *Server) copyIn(dst io.Writer, req *Request, act *activity) error {
	if req.Stdin == nil {
		return nil
	}
	src := io.Reader(activityReader{r: req.Stdin, act: act})
	if req.Service.IsWrite() {
		limit := s.o.MaxPushSize + pushSlack
		n, err := io.Copy(dst, io.LimitReader(src, limit))
		if err != nil {
			return err
		}
		if n == limit {
			return ErrPushTooLarge
		}
		return nil
	}
	_, err := io.Copy(dst, src)
	return err
}

// watch cancels the command when nothing moved for IdleTimeout.
func (s *Server) watch(ctx context.Context, act *activity, cancel context.CancelCauseFunc, abort func(), done <-chan struct{}) {
	tick := min(s.o.IdleTimeout/4, 15*time.Second)
	t := time.NewTicker(max(tick, 10*time.Millisecond))
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			if abort != nil {
				abort()
			}
			return
		case <-t.C:
			if act.idle() >= s.o.IdleTimeout {
				cancel(ErrIdleTimeout)
				if abort != nil {
					abort()
				}
				return
			}
		}
	}
}

func closeAll(fs ...*os.File) {
	for _, f := range fs {
		_ = f.Close()
	}
}

// activity records the last time a byte moved in either direction.
type activity struct{ last atomic.Int64 }

func (a *activity) touch()              { a.last.Store(time.Now().UnixNano()) }
func (a *activity) idle() time.Duration { return time.Duration(time.Now().UnixNano() - a.last.Load()) }

type activityReader struct {
	r   io.Reader
	act *activity
}

func (r activityReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.act.touch()
	}
	return n, err
}

// Flusher is implemented by transport writers that buffer (HTTP).
type Flusher interface{ Flush() error }

// flushWriter forwards every chunk git writes and flushes it at once: git
// output is already framed and the client waits for it.
type flushWriter struct {
	w   io.Writer
	act *activity
}

func (f flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if n > 0 {
		f.act.touch()
	}
	if err != nil {
		return n, err
	}
	if fl, ok := f.w.(Flusher); ok {
		if err := fl.Flush(); err != nil {
			return n, err
		}
	}
	return n, nil
}

// OnceWriter writes Prefix before the first byte of output (the Smart HTTP
// service announcement), so that nothing is sent when git fails to start.
// Wrote reports whether anything was written.
type OnceWriter struct {
	W      io.Writer
	Prefix []byte
	// Before, when set, runs once before the first write (HTTP headers).
	Before func()
	once   sync.Once
	wrote  atomic.Bool
}

func (o *OnceWriter) Write(p []byte) (int, error) {
	var err error
	o.once.Do(func() {
		o.wrote.Store(true)
		if o.Before != nil {
			o.Before()
		}
		if len(o.Prefix) > 0 {
			_, err = o.W.Write(o.Prefix)
		}
	})
	if err != nil {
		return 0, err
	}
	return o.W.Write(p)
}

// Flush flushes the underlying writer when it can.
func (o *OnceWriter) Flush() error {
	if fl, ok := o.W.(Flusher); ok {
		return fl.Flush()
	}
	return nil
}

// Wrote reports whether the first write happened.
func (o *OnceWriter) Wrote() bool { return o.wrote.Load() }
