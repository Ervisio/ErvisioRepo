package git

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/sys"
)

// ErrObjectNotFound is returned by the cat-file pool for a missing object.
var ErrObjectNotFound = errors.New("git: object not found")

// ErrObjectTooLarge is returned by Batch.Object for an object above maxSize.
var ErrObjectTooLarge = errors.New("git: object too large")

// ErrPoolClosed is returned after Pool.Close.
var ErrPoolClosed = errors.New("git: cat-file pool closed")

// ObjectInfo describes one object.
type ObjectInfo struct {
	OID  string
	Type string // blob | tree | commit | tag
	Size int64
}

// PoolOptions configures a Pool. Zero values take the defaults.
type PoolOptions struct {
	MaxIdlePerRepo int           // default 4
	IdleTimeout    time.Duration // default 1 minute
	MaxLifetime    time.Duration // default 1 hour
}

// Pool keeps long-lived `git cat-file --batch` and `--batch-check`
// processes per repository for the reads of the UI and the API. It is safe
// for concurrent use; each Batch is used by one goroutine at a time.
//
// Two processes are used instead of --batch-command, which needs git 2.36.
type Pool struct {
	opts   PoolOptions
	mu     sync.Mutex
	idle   map[string][]*Batch
	closed bool
	stop   chan struct{}
	done   chan struct{}
}

// NewPool starts a pool and its idle reaper. Close stops it.
func NewPool(o PoolOptions) *Pool {
	if o.MaxIdlePerRepo <= 0 {
		o.MaxIdlePerRepo = 4
	}
	if o.IdleTimeout <= 0 {
		o.IdleTimeout = time.Minute
	}
	if o.MaxLifetime <= 0 {
		o.MaxLifetime = time.Hour
	}
	p := &Pool{opts: o, idle: map[string][]*Batch{}, stop: make(chan struct{}), done: make(chan struct{})}
	go p.reap()
	return p
}

// Get returns a Batch for repoPath, reusing an idle one when possible. The
// caller must call Put (or Close on the Batch) when done.
func (p *Pool) Get(repoPath string) (*Batch, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrPoolClosed
	}
	if l := p.idle[repoPath]; len(l) > 0 {
		b := l[len(l)-1]
		p.idle[repoPath] = l[:len(l)-1]
		if len(p.idle[repoPath]) == 0 {
			delete(p.idle, repoPath)
		}
		p.mu.Unlock()
		return b, nil
	}
	p.mu.Unlock()
	return &Batch{repo: repoPath, born: time.Now(), lifetime: p.opts.MaxLifetime}, nil
}

// Put returns b to the pool. A broken or expired Batch is closed instead.
func (p *Pool) Put(b *Batch) {
	if b == nil {
		return
	}
	if b.broken || time.Since(b.born) > p.opts.MaxLifetime {
		b.Close()
		return
	}
	b.lastUse = time.Now()
	p.mu.Lock()
	if p.closed || len(p.idle[b.repo]) >= p.opts.MaxIdlePerRepo {
		p.mu.Unlock()
		b.Close()
		return
	}
	p.idle[b.repo] = append(p.idle[b.repo], b)
	p.mu.Unlock()
}

// Forget closes the idle processes of repoPath. Eviction calls it before
// removing a repository from the hot tier.
func (p *Pool) Forget(repoPath string) {
	p.mu.Lock()
	l := p.idle[repoPath]
	delete(p.idle, repoPath)
	p.mu.Unlock()
	for _, b := range l {
		b.Close()
	}
}

// Info is a convenience wrapper: Get, Info, Put.
func (p *Pool) Info(ctx context.Context, repoPath, name string) (ObjectInfo, error) {
	b, err := p.Get(repoPath)
	if err != nil {
		return ObjectInfo{}, err
	}
	defer p.Put(b)
	return b.Info(ctx, name)
}

// Object is a convenience wrapper: Get, Object, Put.
func (p *Pool) Object(ctx context.Context, repoPath, name string, maxSize int64) (ObjectInfo, []byte, error) {
	b, err := p.Get(repoPath)
	if err != nil {
		return ObjectInfo{}, nil, err
	}
	defer p.Put(b)
	return b.Object(ctx, name, maxSize)
}

// Close stops the reaper and every idle process.
func (p *Pool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	all := p.idle
	p.idle = map[string][]*Batch{}
	p.mu.Unlock()
	close(p.stop)
	<-p.done
	for _, l := range all {
		for _, b := range l {
			b.Close()
		}
	}
}

func (p *Pool) reap() {
	defer close(p.done)
	t := time.NewTicker(p.opts.IdleTimeout / 2)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
		}
		var old []*Batch
		p.mu.Lock()
		for repo, l := range p.idle {
			keep := l[:0]
			for _, b := range l {
				if time.Since(b.lastUse) > p.opts.IdleTimeout || time.Since(b.born) > p.opts.MaxLifetime {
					old = append(old, b)
				} else {
					keep = append(keep, b)
				}
			}
			if len(keep) == 0 {
				delete(p.idle, repo)
			} else {
				p.idle[repo] = keep
			}
		}
		p.mu.Unlock()
		for _, b := range old {
			b.Close()
		}
	}
}

// Batch is a pair of cat-file processes on one repository, started lazily.
type Batch struct {
	repo     string
	born     time.Time
	lastUse  time.Time
	lifetime time.Duration
	broken   bool
	check    *catProc // --batch-check
	full     *catProc // --batch
}

type catProc struct {
	proc *sys.Process
	in   *os.File
	out  *bufio.Reader
	outF *os.File
}

func startCat(repo, mode string, lifetime time.Duration) (*catProc, error) {
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("cat-file pipe: %w", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		return nil, fmt.Errorf("cat-file pipe: %w", err)
	}
	// The process outlives the request that starts it: its lifetime is
	// bounded by MaxLifetime, and requests are bounded by killing it.
	proc, err := Start(context.Background(), Cmd{
		RepoPath: repo, Args: []string{"cat-file", mode}, Stdin: inR, Stdout: outW,
		Timeout: lifetime + time.Minute,
	})
	_ = inR.Close()
	_ = outW.Close()
	if err != nil {
		_ = inW.Close()
		_ = outR.Close()
		return nil, err
	}
	return &catProc{proc: proc, in: inW, out: bufio.NewReaderSize(outR, 64<<10), outF: outR}, nil
}

func (c *catProc) close() {
	_ = c.in.Close()
	c.proc.Kill()
	_ = c.proc.Wait()
	_ = c.outF.Close()
}

// Close kills the processes. It is safe to call more than once.
func (b *Batch) Close() {
	b.broken = true
	if b.check != nil {
		b.check.close()
		b.check = nil
	}
	if b.full != nil {
		b.full.close()
		b.full = nil
	}
}

func validObjectName(name string) error {
	if name == "" || strings.ContainsAny(name, "\n\r\x00") {
		return invalid("object name", "empty or contains a newline")
	}
	return nil
}

// guard kills the process if ctx ends during a request, marking b broken.
func (b *Batch) guard(ctx context.Context, c *catProc) func() error {
	stop := context.AfterFunc(ctx, c.proc.Kill)
	return func() error {
		if !stop() {
			b.broken = true
			return ctx.Err()
		}
		return nil
	}
}

func (b *Batch) proc(mode string) (*catProc, error) {
	if b.broken {
		return nil, errors.New("git: cat-file batch is closed")
	}
	ptr := &b.check
	if mode == "--batch" {
		ptr = &b.full
	}
	if *ptr == nil {
		c, err := startCat(b.repo, mode, b.lifetime)
		if err != nil {
			b.broken = true
			return nil, err
		}
		*ptr = c
	}
	return *ptr, nil
}

// Info returns type and size of the object named by name (an object id or
// an extended SHA-1 expression such as "HEAD:path"; validate client input
// first with ValidateRevision and ValidateTreePath).
func (b *Batch) Info(ctx context.Context, name string) (ObjectInfo, error) {
	if err := validObjectName(name); err != nil {
		return ObjectInfo{}, err
	}
	c, err := b.proc("--batch-check")
	if err != nil {
		return ObjectInfo{}, err
	}
	done := b.guard(ctx, c)
	info, err := b.request(c, name)
	if cerr := done(); cerr != nil {
		return ObjectInfo{}, cerr
	}
	return info, err
}

// Object returns info and content of an object of at most maxSize bytes
// (0 = no limit). A larger object returns ErrObjectTooLarge with its info.
func (b *Batch) Object(ctx context.Context, name string, maxSize int64) (ObjectInfo, []byte, error) {
	if maxSize > 0 {
		info, err := b.Info(ctx, name)
		if err != nil {
			return info, nil, err
		}
		if info.Size > maxSize {
			return info, nil, ErrObjectTooLarge
		}
		name = info.OID // the object cannot change between the two reads
	} else if err := validObjectName(name); err != nil {
		return ObjectInfo{}, nil, err
	}
	c, err := b.proc("--batch")
	if err != nil {
		return ObjectInfo{}, nil, err
	}
	done := b.guard(ctx, c)
	info, err := b.request(c, name)
	var data []byte
	if err == nil {
		data = make([]byte, info.Size+1) // content plus the trailing LF
		if _, rerr := io.ReadFull(c.out, data); rerr != nil {
			b.broken = true
			err = fmt.Errorf("git cat-file: read content: %w", rerr)
		} else if data[info.Size] != '\n' {
			b.broken = true
			err = errors.New("git cat-file: protocol error")
		}
		data = data[:info.Size]
	}
	if cerr := done(); cerr != nil {
		return ObjectInfo{}, nil, cerr
	}
	if err != nil {
		return info, nil, err
	}
	return info, data, nil
}

func (b *Batch) request(c *catProc, name string) (ObjectInfo, error) {
	if _, err := io.WriteString(c.in, name+"\n"); err != nil {
		b.broken = true
		return ObjectInfo{}, fmt.Errorf("git cat-file: write: %w", err)
	}
	line, err := c.out.ReadString('\n')
	if err != nil {
		b.broken = true
		return ObjectInfo{}, fmt.Errorf("git cat-file: read: %w", err)
	}
	line = strings.TrimSuffix(line, "\n")
	if strings.HasSuffix(line, " missing") || strings.HasSuffix(line, " ambiguous") {
		return ObjectInfo{}, ErrObjectNotFound
	}
	f := strings.Fields(line)
	if len(f) != 3 {
		b.broken = true
		return ObjectInfo{}, fmt.Errorf("git cat-file: unexpected header %q", line)
	}
	size, err := strconv.ParseInt(f[2], 10, 64)
	if err != nil || size < 0 {
		b.broken = true
		return ObjectInfo{}, fmt.Errorf("git cat-file: bad size in %q", line)
	}
	return ObjectInfo{OID: f[0], Type: f[1], Size: size}, nil
}
