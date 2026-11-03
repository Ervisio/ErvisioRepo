package githooks

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
)

// Handler implements the hooks. Calls for one Session never overlap.
type Handler interface {
	// PreReceive checks a push whose objects are still in quarantine (q).
	// A *Reject refuses it with a message.
	PreReceive(ctx context.Context, s *Session, updates []RefUpdate, q Quarantine, out io.Writer) error
	// Committed runs after git updated refs. Its error is only reported.
	Committed(ctx context.Context, s *Session, updates []RefUpdate, out io.Writer) error
	// Finish runs when the session ends, after git exited.
	Finish(s *Session)
}

// Preparer is implemented by a Handler that also checks ref updates made
// without a pre-receive hook (git update-ref from web edits and merges):
// it runs in the "prepared" state of the reference transaction, and a
// *Reject aborts the transaction. It is not called for a session whose
// pre-receive hook already accepted the push.
type Preparer interface {
	Prepared(ctx context.Context, s *Session, updates []RefUpdate, out io.Writer) error
}

// Quarantine is the incoming object directory of a push.
type Quarantine struct {
	Dir     string // <repo>/objects/tmp_objdir-incoming-*
	Objects string // <repo>/objects
}

// Env returns the variables that make git see the quarantined objects.
func (q Quarantine) Env() []string {
	return []string{
		"GIT_QUARANTINE_PATH=" + q.Dir,
		"GIT_OBJECT_DIRECTORY=" + q.Dir,
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + q.Objects,
	}
}

// Options configures a Server.
type Options struct {
	// Dir is the hot root of the git tier: the hook scripts go to
	// Dir/.hooks and the socket to Dir/.run/hooks.sock.
	Dir string
	// Executable is the server binary the scripts exec; default
	// os.Executable.
	Executable string
	Handler    Handler
	Log        *slog.Logger
	// HookTimeout bounds one hook call (default 1h: a sync push uploads
	// its pack while the hook runs).
	HookTimeout time.Duration
}

// Server serves the hook socket.
type Server struct {
	o        Options
	hooksDir string
	sockPath string
	tmpDir   string // set when the socket had to move to a short path
	ln       net.Listener

	mu       sync.Mutex
	sessions map[[32]byte]*Session
	closed   bool
	wg       sync.WaitGroup
	cancel   context.CancelFunc
}

// Session is one git process allowed to run the hooks, typically one push.
type Session struct {
	RepoID   uuid.UUID
	RepoPath string
	PusherID uuid.UUID
	Protocol string

	srv  *Server
	key  [32]byte
	tok  string
	mu   sync.Mutex // serialises the hook calls
	once sync.Once

	// State is free for the Handler; it is only touched from hook calls
	// and Finish, which never overlap.
	State any

	received bool   // pre-receive accepted the push
	rejected string // last message a hook refused an update with
}

// Rejected returns the message of the last hook that refused an update of
// this session, or "" when none did. The caller of git uses it to tell a
// policy refusal from a failure.
func (sess *Session) Rejected() string {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.rejected
}

// sunPathMax is the portable bound of a unix socket path (macOS: 104).
const sunPathMax = 100

// Start installs the hook scripts and listens on the socket. Close stops it.
func Start(ctx context.Context, o Options) (*Server, error) {
	if o.Handler == nil {
		return nil, errors.New("githooks: no handler")
	}
	if !filepath.IsAbs(o.Dir) {
		return nil, fmt.Errorf("githooks: hot root %q must be absolute", o.Dir)
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.HookTimeout <= 0 {
		o.HookTimeout = time.Hour
	}
	if o.Executable == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("githooks: locate executable: %w", err)
		}
		if exe, err = filepath.EvalSymlinks(exe); err != nil {
			return nil, fmt.Errorf("githooks: locate executable: %w", err)
		}
		o.Executable = exe
	}
	s := &Server{o: o, hooksDir: filepath.Join(o.Dir, ".hooks"), sessions: map[[32]byte]*Session{}}
	if err := s.install(); err != nil {
		return nil, err
	}
	if err := s.listen(); err != nil {
		return nil, err
	}
	ctx, s.cancel = context.WithCancel(context.WithoutCancel(ctx))
	s.wg.Add(1)
	go s.accept(ctx)
	o.Log.Info("git hook socket listening", "socket", s.sockPath, "hooks", s.hooksDir)
	return s, nil
}

// HooksPath is the directory to set as core.hooksPath.
func (s *Server) HooksPath() string { return s.hooksDir }

// script is the body of every hook: the server binary does the work.
func script(exe, hook string) string {
	return "#!/bin/sh\n# Managed by ervisio-repo: do not edit.\nexec '" + exe + "' hook " + hook + " \"$@\"\n"
}

func (s *Server) install() error {
	if strings.ContainsAny(s.o.Executable, "'\n\x00") {
		return fmt.Errorf("githooks: unsupported executable path %q", s.o.Executable)
	}
	if err := os.MkdirAll(s.hooksDir, 0o750); err != nil {
		return fmt.Errorf("githooks: create %s: %w", s.hooksDir, err)
	}
	for _, hook := range []string{HookPreReceive, HookReferenceTransaction} {
		path := filepath.Join(s.hooksDir, hook)
		body := script(s.o.Executable, hook)
		if cur, err := os.ReadFile(path); err == nil && string(cur) == body { //nolint:gosec // fixed path under the hot root
			continue
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(body), 0o755); err != nil { //nolint:gosec // hook scripts must be executable
			return fmt.Errorf("githooks: write %s: %w", tmp, err)
		}
		if err := os.Rename(tmp, path); err != nil {
			return fmt.Errorf("githooks: install %s: %w", path, err)
		}
	}
	return nil
}

func (s *Server) listen() error {
	run := filepath.Join(s.o.Dir, ".run")
	sock := filepath.Join(run, "hooks.sock")
	if len(sock) > sunPathMax {
		dir, err := os.MkdirTemp("", "erepo-hooks-")
		if err != nil {
			return fmt.Errorf("githooks: socket directory: %w", err)
		}
		s.tmpDir, run, sock = dir, dir, filepath.Join(dir, "hooks.sock")
	}
	if err := os.MkdirAll(run, 0o700); err != nil {
		return fmt.Errorf("githooks: create %s: %w", run, err)
	}
	if err := os.Chmod(run, 0o700); err != nil { //nolint:gosec // directory: owner-only traversal
		return fmt.Errorf("githooks: chmod %s: %w", run, err)
	}
	if err := os.Remove(sock); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("githooks: remove stale socket: %w", err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return fmt.Errorf("githooks: listen %s: %w", sock, err)
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("githooks: chmod socket: %w", err)
	}
	s.ln, s.sockPath = ln, sock
	return nil
}

// Close stops accepting hook calls and waits for the running ones.
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	err := s.ln.Close()
	s.cancel()
	s.wg.Wait()
	if s.tmpDir != "" {
		_ = os.RemoveAll(s.tmpDir)
	}
	return err
}

// Begin opens a session for a git process working on the repository held
// by lease, which must be a Write lease kept until Session.Close. It makes
// sure the repository runs the managed hooks.
func (s *Server) Begin(ctx context.Context, lease *gitstore.Lease, pusherID uuid.UUID, protocol string) (*Session, error) {
	if lease == nil || lease.Mode != gitstore.Write {
		return nil, errors.New("githooks: a write lease is required")
	}
	return s.BeginPath(ctx, lease.RepoID, lease.Path, pusherID, protocol)
}

// BeginPath is Begin for callers that hold the Write lease of repoID
// elsewhere (the git transports): path is the leased hot path.
func (s *Server) BeginPath(ctx context.Context, repoID uuid.UUID, path string, pusherID uuid.UUID, protocol string) (*Session, error) {
	if repoID == uuid.Nil || path == "" {
		return nil, errors.New("githooks: repository and path are required")
	}
	if err := s.ensureHooksPath(ctx, path); err != nil {
		return nil, err
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, fmt.Errorf("githooks: token: %w", err)
	}
	tok := hex.EncodeToString(raw[:])
	sess := &Session{
		RepoID: repoID, RepoPath: path, PusherID: pusherID, Protocol: protocol,
		srv: s, key: sha256.Sum256([]byte(tok)), tok: tok,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("githooks: server closed")
	}
	s.sessions[sess.key] = sess
	return sess, nil
}

// Env is Begin for transports: it returns the variables to add to the git
// environment and the function to call once git has exited. A nil Server
// returns no variables (hooks disabled).
func (s *Server) Env(ctx context.Context, lease *gitstore.Lease, pusherID uuid.UUID, protocol string) ([]string, func(), error) {
	if s == nil {
		return nil, func() {}, nil
	}
	sess, err := s.Begin(ctx, lease, pusherID, protocol)
	if err != nil {
		return nil, nil, err
	}
	return sess.Env(), sess.Close, nil
}

// Env returns the variables to pass to git.
func (sess *Session) Env() []string {
	return []string{EnvSocket + "=" + sess.srv.sockPath, EnvToken + "=" + sess.tok}
}

// Close ends the session: later hook calls with its token are refused.
// Call it after git has exited.
func (sess *Session) Close() {
	sess.once.Do(func() {
		s := sess.srv
		s.mu.Lock()
		delete(s.sessions, sess.key)
		s.mu.Unlock()
		sess.mu.Lock()
		defer sess.mu.Unlock()
		s.o.Handler.Finish(sess)
	})
}

func (s *Server) ensureHooksPath(ctx context.Context, repo string) error {
	out, err := git.Run(ctx, repo, "config", "--local", "--get", "core.hooksPath")
	if err == nil && strings.TrimSpace(string(out)) == s.hooksDir {
		return nil
	}
	if _, err := git.Run(ctx, repo, "config", "--local", "core.hooksPath", s.hooksDir); err != nil {
		return fmt.Errorf("githooks: set core.hooksPath: %w", err)
	}
	return nil
}

func (s *Server) lookup(tok string) *Session {
	if tok == "" {
		return nil
	}
	key := sha256.Sum256([]byte(tok))
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[key]
}

func (s *Server) accept(ctx context.Context) {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				s.o.Log.Error("git hook socket accept", "error", err)
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(ctx, conn)
		}()
	}
}

// replyWriter turns handler output into progress replies.
type replyWriter struct {
	mu  sync.Mutex
	enc *json.Encoder
	w   *bufio.Writer
	err error
}

func (r *replyWriter) send(rep reply) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return
	}
	if r.err = r.enc.Encode(rep); r.err == nil {
		r.err = r.w.Flush()
	}
}

func (r *replyWriter) Write(p []byte) (int, error) {
	for line := range strings.Lines(string(p)) {
		if line = strings.TrimRight(line, "\r\n"); line != "" {
			r.send(reply{Msg: line})
		}
	}
	return len(p), nil
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(ctx, s.o.HookTimeout)
	defer cancel()
	_ = conn.SetReadDeadline(time.Now().Add(time.Minute))
	var req request
	if err := json.NewDecoder(io.LimitReader(conn, maxStdin+64<<10)).Decode(&req); err != nil {
		s.o.Log.Warn("git hook: bad request", "error", err)
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	bw := bufio.NewWriter(conn)
	out := &replyWriter{enc: json.NewEncoder(bw), w: bw}
	finish := func(err error) {
		if err == nil {
			out.send(reply{Done: true, OK: true})
			return
		}
		msg, ok := rejectMessage(err)
		if !ok {
			msg = "internal server error, the push was not accepted"
		}
		out.send(reply{Done: true, Msg: msg})
	}

	sess := s.lookup(req.Token)
	if sess == nil {
		finish(Rejectf("this push session is unknown or expired"))
		return
	}
	updates, err := ParseUpdates(req.Stdin)
	if err != nil {
		finish(Rejectf("%v", err))
		return
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	log := s.o.Log.With("repo", sess.RepoID, "hook", req.Hook)
	switch req.Hook {
	case HookPreReceive:
		q, err := s.quarantine(sess, req.Quarantine)
		if err != nil {
			log.Warn("git hook: bad quarantine", "error", err)
			finish(Rejectf("push quarantine missing"))
			return
		}
		err = s.o.Handler.PreReceive(ctx, sess, updates, q, out)
		if msg, ok := rejectMessage(err); err != nil && !ok {
			log.Error("git pre-receive failed", "error", err)
		} else if ok {
			sess.rejected = msg
		}
		sess.received = err == nil
		finish(err)
	case HookReferenceTransaction:
		if len(req.Args) == 1 && req.Args[0] == TxPrepared {
			p, ok := s.o.Handler.(Preparer)
			if !ok || sess.received {
				finish(nil)
				return
			}
			err := p.Prepared(ctx, sess, updates, out)
			if msg, ok := rejectMessage(err); err != nil && !ok {
				log.Error("git reference-transaction prepared failed", "error", err)
			} else if ok {
				sess.rejected = msg
			}
			finish(err)
			return
		}
		if len(req.Args) != 1 || req.Args[0] != TxCommitted {
			finish(nil)
			return
		}
		if err := s.o.Handler.Committed(ctx, sess, updates, out); err != nil {
			log.Error("git reference-transaction failed", "error", err)
		}
		finish(nil)
	default:
		finish(Rejectf("unknown hook %q", req.Hook))
	}
}

// quarantine validates the quarantine directory reported by git: it must
// be an incoming object directory of the session's repository.
func (s *Server) quarantine(sess *Session, dir string) (Quarantine, error) {
	if dir == "" || !filepath.IsAbs(dir) {
		return Quarantine{}, fmt.Errorf("quarantine path %q", dir)
	}
	objects := filepath.Join(sess.RepoPath, "objects")
	realObjects, err := filepath.EvalSymlinks(objects)
	if err != nil {
		return Quarantine{}, fmt.Errorf("resolve objects: %w", err)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return Quarantine{}, fmt.Errorf("resolve quarantine: %w", err)
	}
	if filepath.Dir(realDir) != realObjects || !strings.HasPrefix(filepath.Base(realDir), "tmp_objdir-incoming-") {
		return Quarantine{}, fmt.Errorf("quarantine %q outside %q", dir, objects)
	}
	return Quarantine{Dir: realDir, Objects: realObjects}, nil
}
