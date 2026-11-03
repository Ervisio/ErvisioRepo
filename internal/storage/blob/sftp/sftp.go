// Package sftp implements blob.Store over SFTP (Hetzner StorageBox and any
// OpenSSH server).
//
// Writes go to a temporary file next to the destination, are fsynced when the
// server supports fsync@openssh.com, then renamed into place with
// posix-rename@openssh.com, so an object is either fully present or absent.
// Conditional writes are emulated with a per-key lock file created with
// O_EXCL plus a process-wide mutex; they are safe as long as every writer goes
// through this driver.
package sftp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/Ervisio/ErvisioRepo/internal/storage/blob"
)

const (
	// StaleLockAge is how old a lock file must be before it is broken.
	StaleLockAge   = 2 * time.Minute
	lockPoll       = 25 * time.Millisecond
	defaultConns   = 4
	extPosixRename = "posix-rename@openssh.com"
	extFsync       = "fsync@openssh.com"
)

// casLocks serialises conditional writes per remote path inside the process.
var casLocks blob.KeyedMutex

// DialFunc opens one SFTP session. close releases everything behind it.
type DialFunc func(ctx context.Context) (c *sftp.Client, close func() error, err error)

// Options configures a Store.
type Options struct {
	Name string
	// Addr is host:port of the SSH server.
	Addr string
	User string
	// Auth methods (e.g. ssh.PublicKeys(signer), ssh.Password(pw)).
	Auth []ssh.AuthMethod
	// HostKeyCallback verifies the server key. Required unless Dial is set;
	// use knownhosts.New or ssh.FixedHostKey.
	HostKeyCallback ssh.HostKeyCallback
	// Root is the remote directory holding the objects (relative to the
	// login directory, or absolute). Default ".".
	Root string
	// MaxConns bounds concurrent SFTP sessions (StorageBox allows ~10).
	MaxConns int
	// Dial overrides how sessions are opened (tests).
	Dial DialFunc
	// DialTimeout bounds the TCP+SSH handshake (default 30s).
	DialTimeout time.Duration
}

type conn struct {
	c     *sftp.Client
	close func() error
}

// Store is a blob.Store on an SFTP server.
type Store struct {
	opts Options
	root string
	dial DialFunc
	sem  chan struct{}

	mu     sync.Mutex
	idle   []*conn
	closed bool
}

var (
	_ blob.Store             = (*Store)(nil)
	_ blob.ConditionalPutter = (*Store)(nil)
)

// New returns a Store. Sessions are opened lazily.
func New(o Options) (*Store, error) {
	if o.MaxConns <= 0 {
		o.MaxConns = defaultConns
	}
	if o.DialTimeout <= 0 {
		o.DialTimeout = 30 * time.Second
	}
	root := path.Clean(o.Root)
	if o.Root == "" {
		root = "."
	}
	s := &Store{opts: o, root: root, sem: make(chan struct{}, o.MaxConns), dial: o.Dial}
	if s.dial == nil {
		if o.Addr == "" || o.User == "" {
			return nil, fmt.Errorf("sftp %s: addr and user are required", o.Name)
		}
		if o.HostKeyCallback == nil {
			return nil, fmt.Errorf("sftp %s: a host key callback is required", o.Name)
		}
		s.dial = s.dialSSH
	}
	return s, nil
}

func (s *Store) dialSSH(ctx context.Context) (*sftp.Client, func() error, error) {
	cfg := &ssh.ClientConfig{
		User:            s.opts.User,
		Auth:            s.opts.Auth,
		HostKeyCallback: s.opts.HostKeyCallback,
		Timeout:         s.opts.DialTimeout,
	}
	d := net.Dialer{Timeout: s.opts.DialTimeout}
	nc, err := d.DialContext(ctx, "tcp", s.opts.Addr)
	if err != nil {
		return nil, nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = nc.SetDeadline(dl)
	}
	cc, chans, reqs, err := ssh.NewClientConn(nc, s.opts.Addr, cfg)
	if err != nil {
		_ = nc.Close()
		return nil, nil, err
	}
	_ = nc.SetDeadline(time.Time{})
	sc := ssh.NewClient(cc, chans, reqs)
	c, err := sftp.NewClient(sc, sftp.UseConcurrentWrites(true))
	if err != nil {
		_ = sc.Close()
		return nil, nil, err
	}
	return c, func() error { _ = c.Close(); return sc.Close() }, nil
}

// Close closes idle sessions; sessions in use are closed when released.
func (s *Store) Close() error {
	s.mu.Lock()
	idle := s.idle
	s.idle, s.closed = nil, true
	s.mu.Unlock()
	for _, c := range idle {
		_ = c.close()
	}
	return nil
}

// Name implements blob.Store.
func (s *Store) Name() string { return s.opts.Name }

// Caps implements blob.Store.
func (s *Store) Caps() blob.Caps {
	return blob.Caps{Range: true, AtomicRename: true, ConditionalPut: true, MaxConns: s.opts.MaxConns}
}

// acquire borrows a session, waiting for a free slot.
func (s *Store) acquire(ctx context.Context) (*conn, error) {
	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		<-s.sem
		return nil, fmt.Errorf("sftp %s: store closed", s.opts.Name)
	}
	if n := len(s.idle); n > 0 {
		c := s.idle[n-1]
		s.idle = s.idle[:n-1]
		s.mu.Unlock()
		return c, nil
	}
	s.mu.Unlock()
	dctx, cancel := context.WithTimeout(ctx, s.opts.DialTimeout)
	defer cancel()
	c, closeFn, err := s.dial(dctx)
	if err != nil {
		<-s.sem
		return nil, blob.MarkTransient(fmt.Errorf("sftp %s: connect: %w", s.opts.Name, err))
	}
	return &conn{c: c, close: closeFn}, nil
}

// release returns a session to the pool, or drops it when err shows the
// connection is broken.
func (s *Store) release(c *conn, err error) {
	defer func() { <-s.sem }()
	if broken(err) {
		_ = c.close()
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = c.close()
		return
	}
	s.idle = append(s.idle, c)
	s.mu.Unlock()
}

// broken reports errors that are not protocol status replies, i.e. the
// session itself failed (or was abandoned mid-transfer by a cancellation).
func broken(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, blob.ErrNotFound) || errors.Is(err, blob.ErrPreconditionFailed) ||
		errors.Is(err, blob.ErrInvalidKey) || errors.Is(err, blob.ErrInvalidRange) ||
		errors.Is(err, blob.ErrSizeMismatch) || errors.Is(err, blob.ErrChecksumMismatch) ||
		errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) || errors.Is(err, errStatus) {
		return false
	}
	var se *sftp.StatusError
	if errors.As(err, &se) {
		return se.FxCode() == sftp.ErrSSHFxConnectionLost || se.FxCode() == sftp.ErrSSHFxNoConnection
	}
	return true
}

// errStatus marks errors that came from a caller-side reader, not the session.
var errStatus = errors.New("sftp: request failed")

func (s *Store) with(ctx context.Context, fn func(c *sftp.Client) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	err = fn(c.c)
	s.release(c, err)
	return err
}

func (s *Store) remote(key string) string { return path.Join(s.root, key) }

// Put implements blob.Store.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, o blob.PutOpts) (blob.Info, error) {
	return s.put(ctx, key, r, size, nil, o)
}

// PutConditional implements blob.ConditionalPutter.
func (s *Store) PutConditional(ctx context.Context, key string, r io.Reader, size int64, c blob.Condition, o blob.PutOpts) (blob.Info, error) {
	return s.put(ctx, key, r, size, &c, o)
}

func (s *Store) put(ctx context.Context, key string, r io.Reader, size int64, cond *blob.Condition, o blob.PutOpts) (blob.Info, error) {
	if err := blob.ValidateKey(key); err != nil {
		return blob.Info{}, err
	}
	vr, err := blob.NewVerifyReader(ctx, r, size, o.SHA256)
	if err != nil {
		return blob.Info{}, err
	}
	var info blob.Info
	err = s.with(ctx, func(c *sftp.Client) error {
		var err error
		info, err = s.putOn(ctx, c, key, vr, cond)
		return err
	})
	return info, err
}

func (s *Store) putOn(ctx context.Context, c *sftp.Client, key string, vr *blob.VerifyReader, cond *blob.Condition) (info blob.Info, err error) {
	dst := s.remote(key)
	dir := path.Dir(dst)
	if err := c.MkdirAll(dir); err != nil {
		return blob.Info{}, s.wrap("put: mkdir", key, err)
	}
	tmp, f, err := createTemp(c, dir, "tmp-")
	if err != nil {
		return blob.Info{}, s.wrap("put", key, err)
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = c.Remove(tmp)
		}
	}()
	src := &trackReader{r: vr}
	if _, err = io.Copy(f, src); err != nil {
		if src.err != nil && errors.Is(err, src.err) {
			// The caller's reader failed (or verification did): the session
			// is healthy and can go back to the pool.
			return blob.Info{}, fmt.Errorf("sftp %s: put %s: %w: %w", s.opts.Name, key, errStatus, err)
		}
		return blob.Info{}, s.wrap("put: write", key, err)
	}
	if v, ok := c.HasExtension(extFsync); ok && v == "1" {
		if err = f.Sync(); err != nil {
			return blob.Info{}, s.wrap("put: fsync", key, err)
		}
	}
	if err = f.Close(); err != nil {
		return blob.Info{}, s.wrap("put: close", key, err)
	}
	if cond != nil {
		unlock := casLocks.Lock(s.opts.Addr + "\x00" + dst)
		defer unlock()
		lock, lerr := s.lockFile(ctx, c, dst)
		if lerr != nil {
			return blob.Info{}, s.wrap("put: lock", key, lerr)
		}
		defer func() { _ = c.Remove(lock) }()
		var cur *blob.Info
		if st, serr := c.Stat(dst); serr == nil && st.Mode().IsRegular() {
			ci := infoOf(key, st)
			cur = &ci
		} else if serr != nil && !errors.Is(serr, fs.ErrNotExist) {
			return blob.Info{}, s.wrap("put: stat", key, serr)
		}
		if err = blob.CheckCondition(*cond, cur); err != nil {
			return blob.Info{}, err
		}
	}
	if err = bumpMtime(c, tmp, dst); err != nil {
		return blob.Info{}, s.wrap("put: chtimes", key, err)
	}
	if err = ctx.Err(); err != nil {
		return blob.Info{}, err
	}
	if err = rename(c, tmp, dst); err != nil {
		return blob.Info{}, s.wrap("put: rename", key, err)
	}
	st, err := c.Stat(dst)
	if err != nil {
		return blob.Info{}, s.wrap("put: stat", key, err)
	}
	return infoOf(key, st), nil
}

// trackReader remembers the error of the source reader.
type trackReader struct {
	r   io.Reader
	err error
}

func (t *trackReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && err != io.EOF {
		t.err = err
	}
	return n, err
}

func createTemp(c *sftp.Client, dir, kind string) (string, *sftp.File, error) {
	for range 10 {
		var b [12]byte
		_, _ = rand.Read(b[:])
		name := path.Join(dir, blob.ReservedPrefix+kind+hex.EncodeToString(b[:]))
		f, err := c.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if err == nil {
			return name, f, nil
		}
		if _, serr := c.Lstat(name); serr == nil {
			continue // name taken
		}
		return "", nil, err
	}
	return "", nil, errors.New("cannot create a unique temporary file")
}

// lockFile takes the remote lock for dst, breaking locks older than
// StaleLockAge (left by a crashed writer).
func (s *Store) lockFile(ctx context.Context, c *sftp.Client, dst string) (string, error) {
	lock := path.Join(path.Dir(dst), blob.ReservedPrefix+"lock-"+path.Base(dst))
	for {
		f, err := c.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if err == nil {
			_ = f.Close()
			return lock, nil
		}
		st, serr := c.Lstat(lock)
		if serr != nil {
			if errors.Is(serr, fs.ErrNotExist) {
				continue // released meanwhile
			}
			return "", err
		}
		if time.Since(st.ModTime()) > StaleLockAge {
			_ = c.Remove(lock)
			continue
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(lockPoll):
		}
	}
}

// bumpMtime makes the new file's mtime (second resolution over SFTP) later
// than the one it replaces, so the ETag changes on every overwrite.
func bumpMtime(c *sftp.Client, tmp, dst string) error {
	old, err := c.Stat(dst)
	if err != nil {
		return nil //nolint:nilerr // nothing to replace
	}
	nw, err := c.Stat(tmp)
	if err != nil {
		return err
	}
	if nw.ModTime().Unix() > old.ModTime().Unix() {
		return nil
	}
	t := old.ModTime().Truncate(time.Second).Add(time.Second)
	return c.Chtimes(tmp, t, t)
}

func rename(c *sftp.Client, from, to string) error {
	if _, ok := c.HasExtension(extPosixRename); ok {
		return c.PosixRename(from, to)
	}
	// Plain SFTP rename fails when the target exists: not atomic, but the
	// window is a single round trip.
	if err := c.Rename(from, to); err == nil {
		return nil
	}
	if err := c.Remove(to); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return c.Rename(from, to)
}

// Get implements blob.Store. The session stays borrowed until the reader is
// closed.
func (s *Store) Get(ctx context.Context, key string, rng *blob.Range) (io.ReadCloser, blob.Info, error) {
	if err := blob.ValidateKey(key); err != nil {
		return nil, blob.Info{}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, blob.Info{}, err
	}
	cn, err := s.acquire(ctx)
	if err != nil {
		return nil, blob.Info{}, err
	}
	fail := func(err error) (io.ReadCloser, blob.Info, error) {
		s.release(cn, err)
		return nil, blob.Info{}, err
	}
	f, err := cn.c.Open(s.remote(key))
	if err != nil {
		return fail(s.wrap("get", key, err))
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fail(s.wrap("get", key, err))
	}
	if !st.Mode().IsRegular() {
		_ = f.Close()
		return fail(fmt.Errorf("sftp %s: get %s: %w", s.opts.Name, key, blob.ErrNotFound))
	}
	info := infoOf(key, st)
	start, end, err := blob.ResolveRange(rng, info.Size)
	if err != nil {
		_ = f.Close()
		return fail(err)
	}
	if start > 0 {
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			_ = f.Close()
			return fail(s.wrap("get: seek", key, err))
		}
	}
	return &reader{s: s, cn: cn, f: f, r: io.LimitReader(f, end-start), key: key, ctx: ctx}, info, nil
}

type reader struct {
	s    *Store
	cn   *conn
	f    *sftp.File
	r    io.Reader
	key  string
	ctx  context.Context
	err  error
	once sync.Once
}

func (r *reader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		r.err = err
		return 0, err
	}
	n, err := r.r.Read(p)
	if err != nil && err != io.EOF {
		r.err = err
		err = blob.MarkTransient(fmt.Errorf("sftp %s: read %s: %w", r.s.opts.Name, r.key, err))
	}
	return n, err
}

func (r *reader) Close() error {
	var err error
	r.once.Do(func() {
		err = r.f.Close()
		r.s.release(r.cn, errors.Join(r.err, err))
	})
	return err
}

// Stat implements blob.Store.
func (s *Store) Stat(ctx context.Context, key string) (blob.Info, error) {
	if err := blob.ValidateKey(key); err != nil {
		return blob.Info{}, err
	}
	var info blob.Info
	err := s.with(ctx, func(c *sftp.Client) error {
		st, err := c.Stat(s.remote(key))
		if err != nil {
			return s.wrap("stat", key, err)
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("sftp %s: stat %s: %w", s.opts.Name, key, blob.ErrNotFound)
		}
		info = infoOf(key, st)
		return nil
	})
	return info, err
}

// Delete implements blob.Store.
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := blob.ValidateKey(key); err != nil {
		return err
	}
	return s.with(ctx, func(c *sftp.Client) error {
		p := s.remote(key)
		st, err := c.Lstat(p)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || isNoSuchFile(err) {
				return nil
			}
			return s.wrap("delete", key, err)
		}
		if !st.Mode().IsRegular() {
			return nil
		}
		if err := c.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return s.wrap("delete", key, err)
		}
		return nil
	})
}

// List implements blob.Store.
func (s *Store) List(ctx context.Context, prefix string, fn func(blob.Info) error) error {
	if err := blob.ValidatePrefix(prefix); err != nil {
		return err
	}
	start := ""
	if i := strings.LastIndexByte(prefix, '/'); i >= 0 {
		start = prefix[:i]
	}
	var fnErr error
	err := s.with(ctx, func(c *sftp.Client) error {
		return s.walk(ctx, c, start, prefix, fn, &fnErr)
	})
	if fnErr != nil {
		return fnErr
	}
	return err
}

func (s *Store) walk(ctx context.Context, c *sftp.Client, rel, prefix string, fn func(blob.Info) error, fnErr *error) error {
	entries, err := c.ReadDirContext(ctx, s.remote(rel))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || isNoSuchFile(err) {
			return nil
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		return s.wrap("list", prefix, err)
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		p := name
		if rel != "" {
			p = rel + "/" + name
		}
		switch {
		case e.IsDir():
			if strings.HasPrefix(p+"/", prefix) || strings.HasPrefix(prefix, p+"/") {
				if err := s.walk(ctx, c, p, prefix, fn, fnErr); err != nil {
					return err
				}
			}
		case e.Mode().IsRegular() && strings.HasPrefix(p, prefix) && blob.ValidateKey(p) == nil:
			if err := fn(infoOf(p, e)); err != nil {
				*fnErr = err
				return errStatus
			}
		}
	}
	return nil
}

func isNoSuchFile(err error) bool {
	var se *sftp.StatusError
	return errors.As(err, &se) && se.FxCode() == sftp.ErrSSHFxNoSuchFile
}

func infoOf(key string, st fs.FileInfo) blob.Info {
	mt := st.ModTime()
	return blob.Info{Key: key, Size: st.Size(), ETag: fmt.Sprintf("%x-%x", st.Size(), mt.Unix()), ModTime: mt}
}

func (s *Store) wrap(op, key string, err error) error {
	if errors.Is(err, fs.ErrNotExist) || isNoSuchFile(err) {
		return fmt.Errorf("sftp %s: %s %s: %w", s.opts.Name, op, key, blob.ErrNotFound)
	}
	err = fmt.Errorf("sftp %s: %s %s: %w", s.opts.Name, op, key, err)
	if broken(err) {
		return blob.MarkTransient(err)
	}
	return err
}
