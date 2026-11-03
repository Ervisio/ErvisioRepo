// Package gitssh serves git over SSH (PLAN §5.1). Every caller logs in as
// user "git" with a public key registered on its account; the requested
// command (git-upload-pack, git-receive-pack, git-upload-archive and, from
// Phase 4, the git-lfs commands) is parsed without a shell and runs through
// gittransport. The stderr channel carries git's progress and the progress
// of a hydration started by the request.
package gitssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/credentials"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/gittransport"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// SSHUser is the only login name accepted.
const SSHUser = "git"

// Resolver finds a repository for a caller (satisfied by *repos.Service).
type Resolver interface {
	Get(ctx context.Context, who auth.Principal, owner, name string) (repos.Repo, error)
}

// KeyAuthenticator maps a public key to its owner (satisfied by
// *credentials.Service).
type KeyAuthenticator interface {
	AuthenticateSSHKey(ctx context.Context, pub ssh.PublicKey) (credentials.SSHCaller, error)
}

// LFSRequest is a git-lfs-authenticate or git-lfs-transfer command of an
// authorized caller.
type LFSRequest struct {
	Command   string // "git-lfs-authenticate" or "git-lfs-transfer"
	Operation string // "upload" or "download"
	Repo      repos.Repo
	Caller    auth.Principal
	RemoteIP  string
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
}

// LFS serves the git-lfs SSH commands (Phase 4). It returns the exit status.
type LFS interface {
	ServeSSH(ctx context.Context, req LFSRequest) uint32
}

// Options configure a Server.
type Options struct {
	Transport *gittransport.Server
	Repos     Resolver
	Keys      KeyAuthenticator
	HostKeys  []ssh.Signer
	// LFS, when set, serves git-lfs-authenticate and git-lfs-transfer.
	LFS LFS
	// Failures slows down clients that fail to authenticate, per IP.
	Failures *auth.Limiter
	// Requests is the hourly git request budget per account; nil disables it.
	Requests *auth.Limiter
	// MaxConnections bounds concurrent connections.
	MaxConnections int
	// LoginGraceTime bounds the handshake and authentication.
	LoginGraceTime time.Duration
	// IdleTimeout closes a connection with no open session for this long.
	IdleTimeout time.Duration
	Log         *slog.Logger
}

// Server is the SSH server.
type Server struct {
	o      Options
	cfg    *ssh.ServerConfig
	sem    chan struct{}
	base   context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	ln     net.Listener
	conns  map[net.Conn]struct{}
	closed bool
	wg     sync.WaitGroup
}

// Permission extensions carrying the authenticated caller.
const (
	extUserID   = "ervisio-user-id"
	extUsername = "ervisio-username"
	extRole     = "ervisio-role"
	extKeyID    = "ervisio-key-id"
)

// New returns a Server.
func New(o Options) (*Server, error) {
	if o.Transport == nil || o.Repos == nil || o.Keys == nil || len(o.HostKeys) == 0 {
		return nil, errors.New("gitssh: transport, repos, keys and host keys are required")
	}
	if o.MaxConnections <= 0 || o.LoginGraceTime <= 0 || o.IdleTimeout <= 0 {
		return nil, errors.New("gitssh: limits must be positive")
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	s := &Server{o: o, sem: make(chan struct{}, o.MaxConnections), conns: map[net.Conn]struct{}{}}
	s.base, s.cancel = context.WithCancel(context.Background())
	s.cfg = &ssh.ServerConfig{
		PublicKeyCallback: s.publicKey,
		MaxAuthTries:      6,
		ServerVersion:     "SSH-2.0-ErvisioRepo",
	}
	for _, k := range o.HostKeys {
		s.cfg.AddHostKey(k)
	}
	return s, nil
}

func (s *Server) publicKey(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	if meta.User() != SSHUser {
		return nil, fmt.Errorf("unknown user %q", meta.User())
	}
	ctx, cancel := context.WithTimeout(s.base, 10*time.Second)
	defer cancel()
	c, err := s.o.Keys.AuthenticateSSHKey(ctx, key)
	if err != nil {
		if !errors.Is(err, credentials.ErrUnknownKey) {
			s.o.Log.ErrorContext(ctx, "git ssh: authenticate key", "error", err)
		}
		return nil, errors.New("public key not accepted")
	}
	return &ssh.Permissions{Extensions: map[string]string{
		extUserID:   c.Principal.UserID.String(),
		extUsername: c.Principal.Username,
		extRole:     c.Principal.Role,
		extKeyID:    c.KeyID.String(),
	}}, nil
}

func principalOf(p *ssh.Permissions) (auth.Principal, uuid.UUID, bool) {
	if p == nil {
		return auth.Principal{}, uuid.Nil, false
	}
	id, err := uuid.Parse(p.Extensions[extUserID])
	if err != nil {
		return auth.Principal{}, uuid.Nil, false
	}
	keyID, _ := uuid.Parse(p.Extensions[extKeyID])
	return auth.Principal{UserID: id, Username: p.Extensions[extUsername], Role: p.Extensions[extRole],
		Method: credentials.MethodSSHKey}, keyID, true
}

// Serve accepts connections on ln until Shutdown.
func (s *Server) Serve(ln net.Listener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return net.ErrClosed
	}
	s.ln = ln
	s.mu.Unlock()
	var backoff time.Duration
	for {
		c, err := ln.Accept()
		if err != nil {
			if s.isClosed() {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				backoff = min(max(2*backoff, 5*time.Millisecond), time.Second)
				time.Sleep(backoff)
				continue
			}
			return fmt.Errorf("ssh accept: %w", err)
		}
		backoff = 0
		select {
		case s.sem <- struct{}{}:
		default:
			s.o.Log.Warn("git ssh: too many connections", "remote", c.RemoteAddr().String())
			_ = c.Close()
			continue
		}
		if !s.track(c) {
			<-s.sem
			_ = c.Close()
			return nil
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { <-s.sem }()
			defer s.untrack(c)
			s.handleConn(c)
		}()
	}
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Server) track(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *Server) untrack(c net.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	_ = c.Close()
}

// Shutdown stops accepting connections and waits for the open ones until
// ctx is done, then closes them.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	var err error
	if s.ln != nil {
		err = s.ln.Close()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		s.cancel()
		s.mu.Lock()
		for c := range s.conns {
			_ = c.Close()
		}
		s.mu.Unlock()
		<-done
	}
	s.cancel()
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("close ssh listener: %w", err)
	}
	return nil
}

func remoteIP(c net.Conn) string {
	if ap, ok := c.RemoteAddr().(*net.TCPAddr); ok {
		return ap.AddrPort().Addr().Unmap().String()
	}
	host, _, err := net.SplitHostPort(c.RemoteAddr().String())
	if err != nil {
		return c.RemoteAddr().String()
	}
	return host
}

func (s *Server) handleConn(c net.Conn) {
	ip := remoteIP(c)
	if s.o.Failures != nil && s.o.Failures.LoginWait(auth.IPKey(ip)) > 0 {
		return
	}
	_ = c.SetDeadline(time.Now().Add(s.o.LoginGraceTime))
	sconn, chans, reqs, err := ssh.NewServerConn(c, s.cfg)
	if err != nil {
		var ae *ssh.ServerAuthError
		if errors.As(err, &ae) && s.o.Failures != nil {
			s.o.Failures.Fail(auth.IPKey(ip))
		}
		s.o.Log.Debug("git ssh: handshake failed", "remote", ip, "error", err)
		return
	}
	_ = c.SetDeadline(time.Time{})
	defer func() { _ = sconn.Close() }()
	who, keyID, ok := principalOf(sconn.Permissions)
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(s.base)
	defer cancel()
	go func() { _ = sconn.Wait(); cancel() }()
	go ssh.DiscardRequests(reqs)
	s.o.Log.Debug("git ssh: connection", "remote", ip, "user", who.Username, "key", keyID.String())

	// Close the connection once no session has been open for IdleTimeout.
	var active atomic.Int32
	idle := time.AfterFunc(s.o.IdleTimeout, func() {
		if active.Load() == 0 {
			_ = sconn.Close()
		}
	})
	defer idle.Stop()

	var sessions sync.WaitGroup
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "only session channels are supported")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			continue
		}
		active.Add(1)
		idle.Stop()
		sessions.Go(func() {
			defer func() {
				if active.Add(-1) == 0 {
					idle.Reset(s.o.IdleTimeout)
				}
			}()
			s.session(ctx, who, ip, ch, creqs)
		})
	}
	cancel()
	sessions.Wait()
}

// session serves one session channel: an optional GIT_PROTOCOL env, then
// one exec (or a shell request, answered with a greeting).
func (s *Server) session(ctx context.Context, who auth.Principal, ip string, ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer func() { _ = ch.Close() }()
	var protocol string
	for req := range reqs {
		switch req.Type {
		case "env":
			var kv struct{ Name, Value string }
			ok := ssh.Unmarshal(req.Payload, &kv) == nil && kv.Name == "GIT_PROTOCOL"
			if ok {
				protocol = kv.Value
			}
			_ = req.Reply(ok, nil)
		case "exec":
			var p struct{ Command string }
			if err := ssh.Unmarshal(req.Payload, &p); err != nil {
				_ = req.Reply(false, nil)
				continue
			}
			_ = req.Reply(true, nil)
			go ssh.DiscardRequests(reqs)
			code := s.exec(ctx, who, ip, protocol, p.Command, ch)
			exit(ch, code)
			return
		case "shell":
			_ = req.Reply(true, nil)
			go ssh.DiscardRequests(reqs)
			_, _ = fmt.Fprintf(ch.Stderr(), "Hi %s! You've successfully authenticated, but Ervisio Repo does not provide shell access.\n", who.Username)
			exit(ch, 1)
			return
		default:
			_ = req.Reply(false, nil)
		}
	}
}

func exit(ch ssh.Channel, code uint32) {
	_ = ch.CloseWrite()
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{code}))
}

func (s *Server) exec(ctx context.Context, who auth.Principal, ip, protocol, line string, ch ssh.Channel) uint32 {
	stderr := ch.Stderr()
	cmd, err := parseCommand(line)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "ERROR: %v\n", err)
		return 128
	}
	repo, err := s.resolve(ctx, who, cmd.owner, cmd.name, stderr)
	if err != nil {
		if errors.Is(err, repos.ErrNotFound) {
			_, _ = fmt.Fprintf(stderr, "ERROR: Repository %s/%s not found.\n", cmd.owner, cmd.name)
			return 1
		}
		s.o.Log.ErrorContext(ctx, "git ssh: resolve repository", "error", err)
		_, _ = fmt.Fprintln(stderr, "ERROR: internal error")
		return 1
	}
	if s.o.Requests != nil {
		if ok, wait := s.o.Requests.AllowAction("git:user:" + who.UserID.String()); !ok {
			_, _ = fmt.Fprintf(stderr, "ERROR: too many git requests; retry in %s\n", wait.Round(time.Second))
			return 1
		}
	}
	if !authz.Allowed(repo.Permission, cmd.action()) {
		_, _ = fmt.Fprintf(stderr, "ERROR: Permission to %s denied to %s.\n", repo.FullName(), who.Username)
		return 1
	}
	if cmd.lfs != "" {
		if s.o.LFS == nil {
			_, _ = fmt.Fprintln(stderr, "ERROR: git-lfs over SSH is not supported yet")
			return 1
		}
		return s.o.LFS.ServeSSH(ctx, LFSRequest{Command: cmd.lfs, Operation: cmd.operation, Repo: repo,
			Caller: who, RemoteIP: ip, Stdin: ch, Stdout: ch, Stderr: stderr})
	}
	return s.git(ctx, cmd.service, repo, who, ip, protocol, ch)
}

// resolve finds the repository, following one rename or transfer.
func (s *Server) resolve(ctx context.Context, who auth.Principal, owner, name string, stderr io.Writer) (repos.Repo, error) {
	repo, err := s.o.Repos.Get(ctx, who, owner, name)
	var moved *repos.MovedError
	if errors.As(err, &moved) {
		_, _ = fmt.Fprintf(stderr, "remote: This repository moved to %s/%s; please update your remote.\n", moved.Owner, moved.Name)
		repo, err = s.o.Repos.Get(ctx, who, moved.Owner, moved.Name)
		if errors.As(err, &moved) {
			return repos.Repo{}, repos.ErrNotFound
		}
	}
	if err != nil {
		return repos.Repo{}, fmt.Errorf("get repository: %w", err)
	}
	return repo, nil
}

func (s *Server) git(ctx context.Context, svc gittransport.Service, repo repos.Repo, who auth.Principal, ip, protocol string, ch ssh.Channel) uint32 {
	stderr := ch.Stderr()
	start := time.Now()
	req := &gittransport.Request{Service: svc, Repo: repo, Pusher: who, Transport: gittransport.TransportSSH,
		RemoteIP: ip, Protocol: protocol, Stdin: ch, Stdout: ch, Stderr: stderr}
	lease, err := s.o.Transport.Acquire(ctx, req)
	if err != nil {
		switch {
		case errors.Is(err, gitstore.ErrBusy), errors.Is(err, gitstore.ErrCold):
			_, _ = fmt.Fprintln(stderr, "ERROR: the repository is being moved between storage tiers; retry shortly")
		case errors.Is(err, gitstore.ErrDegraded), errors.Is(err, gitstore.ErrHotMissing):
			s.o.Log.ErrorContext(ctx, "git ssh: repository unavailable", "repo", repo.FullName(), "error", err)
			_, _ = fmt.Fprintln(stderr, "ERROR: the repository is temporarily unavailable")
		case ctx.Err() != nil:
		default:
			s.o.Log.ErrorContext(ctx, "git ssh: acquire repository", "repo", repo.FullName(), "error", err)
			_, _ = fmt.Fprintln(stderr, "ERROR: internal error")
		}
		return 1
	}
	defer lease.Release()
	code, err := s.o.Transport.Run(ctx, lease, req)
	log := s.o.Log.With("service", string(svc), "repo", repo.FullName(), "user", who.Username,
		"duration", time.Since(start).Round(time.Millisecond))
	switch {
	case err == nil:
		log.InfoContext(ctx, "git ssh request", "code", code)
		return uint32(max(code, 0)) //nolint:gosec // exit codes are small and non-negative
	case errors.Is(err, gittransport.ErrPushTooLarge), errors.Is(err, gittransport.ErrIdleTimeout),
		errors.Is(err, gittransport.ErrTooLong):
		log.WarnContext(ctx, "git ssh request aborted", "error", err)
		_, _ = fmt.Fprintf(stderr, "ERROR: %v\n", err)
	case ctx.Err() != nil:
		log.InfoContext(ctx, "git ssh request ended by the client", "error", err)
	default:
		log.ErrorContext(ctx, "git ssh request failed", "error", err)
		_, _ = fmt.Fprintln(stderr, "ERROR: internal error")
	}
	return 1
}
