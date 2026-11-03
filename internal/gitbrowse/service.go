// Package gitbrowse reads repositories for the web UI and the REST API
// (PLAN §5.4, phase 4): refs, trees with the last commit of each entry,
// blobs, blame, history, diffs, branches and tags, comparisons, archives
// and in-repository search. It also commits web edits without a working
// tree.
//
// Every read runs under a gitstore Read lease; a web edit holds the Write
// lease and updates the ref through a githooks session, so the journal
// records it like a push.
package gitbrowse

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/githooks"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/sys"
)

// Errors. Tier errors (gitstore.ErrBusy, ErrCold, ...) are returned wrapped.
var (
	// ErrNotFound: unknown ref, path or commit.
	ErrNotFound = errors.New("not found")
	// ErrInvalid: a malformed argument (it also wraps git.ErrInvalid).
	ErrInvalid = errors.New("invalid argument")
	// ErrEmpty: the repository has no commit yet.
	ErrEmpty = errors.New("repository is empty")
	// ErrConflict: a web edit raced with another change, or the target
	// already exists.
	ErrConflict = errors.New("conflict")
	// ErrTooLarge: the object exceeds a limit of the operation.
	ErrTooLarge = errors.New("too large")
	// ErrUnavailable: the operation needs a collaborator that is not
	// configured.
	ErrUnavailable = errors.New("unavailable")
	// ErrRejected is a ref update refused by a hook (branch protection).
	ErrRejected = errors.New("refused")
)

func invalidf(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

func notFoundf(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrNotFound, fmt.Sprintf(format, a...))
}

// Defaults of Options.
const (
	DefaultMaxBlobSize    = 1 << 20
	DefaultMaxEditSize    = 10 << 20
	DefaultMaxDiffLines   = 10000
	DefaultMaxDiffFiles   = 300
	DefaultMaxDiffBytes   = 32 << 20
	DefaultMaxTreeEntries = 1000
	DefaultMaxGrepResults = 200
	DefaultGrepTimeout    = 10 * time.Second
	DefaultArchiveTimeout = time.Hour
	// lastCommitBudget bounds the history walk of a tree listing.
	lastCommitBudget  = 5 * time.Second
	lastCommitCommits = 5000
	lastCommitEntries = 300
	readTimeout       = 30 * time.Second
)

// Options configures a Service.
type Options struct {
	Tier *gitstore.Tier
	// Hooks notifies the journal of web edits; nil leaves the edit to the
	// hot copy only (no remote store).
	Hooks *githooks.Server
	// DB is used for the author identity and the quota of web edits.
	DB *pgxpool.Pool
	// CatFile is a shared cat-file pool; nil creates one.
	CatFile *git.Pool
	// NoReplyDomain builds the e-mail of an author without a verified
	// address: <username>@<NoReplyDomain>.
	NoReplyDomain string

	MaxBlobSize    int64 // inline blob content
	MaxEditSize    int64 // content of a web edit
	MaxDiffLines   int
	MaxDiffFiles   int
	MaxDiffBytes   int64
	MaxTreeEntries int
	MaxGrepResults int
	GrepTimeout    time.Duration
	ArchiveTimeout time.Duration
	Log            *slog.Logger
}

// Service implements the code browsing operations.
type Service struct {
	o     Options
	q     *dbgen.Queries
	cat   *git.Pool
	owned bool
	cache *lastCommitCache
	log   *slog.Logger
}

// New builds a Service.
func New(o Options) (*Service, error) {
	if o.Tier == nil {
		return nil, errors.New("gitbrowse: tier is required")
	}
	if o.MaxBlobSize <= 0 {
		o.MaxBlobSize = DefaultMaxBlobSize
	}
	if o.MaxEditSize <= 0 {
		o.MaxEditSize = DefaultMaxEditSize
	}
	if o.MaxDiffLines <= 0 {
		o.MaxDiffLines = DefaultMaxDiffLines
	}
	if o.MaxDiffFiles <= 0 {
		o.MaxDiffFiles = DefaultMaxDiffFiles
	}
	if o.MaxDiffBytes <= 0 {
		o.MaxDiffBytes = DefaultMaxDiffBytes
	}
	if o.MaxTreeEntries <= 0 {
		o.MaxTreeEntries = DefaultMaxTreeEntries
	}
	if o.MaxGrepResults <= 0 {
		o.MaxGrepResults = DefaultMaxGrepResults
	}
	if o.GrepTimeout <= 0 {
		o.GrepTimeout = DefaultGrepTimeout
	}
	if o.ArchiveTimeout <= 0 {
		o.ArchiveTimeout = DefaultArchiveTimeout
	}
	if o.NoReplyDomain == "" {
		o.NoReplyDomain = "users.noreply.localhost"
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	s := &Service{o: o, cat: o.CatFile, cache: newLastCommitCache(512), log: o.Log}
	if o.DB != nil {
		s.q = dbgen.New(o.DB)
	}
	if s.cat == nil {
		s.cat = git.NewPool(git.PoolOptions{})
		s.owned = true
	}
	return s, nil
}

// Close stops the cat-file processes the Service owns.
func (s *Service) Close() {
	if s.owned {
		s.cat.Close()
	}
}

// Repo identifies the repository an operation works on; the caller has
// already checked the permission.
type Repo struct {
	ID            uuid.UUID
	OwnerID       uuid.UUID
	Name          string
	DefaultBranch string
}

// read runs fn with the hot path of repo held by a Read lease.
func (s *Service) read(ctx context.Context, repo Repo, fn func(path string) error) error {
	l, err := s.o.Tier.Acquire(ctx, repo.ID, gitstore.Read)
	if err != nil {
		return fmt.Errorf("acquire repository: %w", err)
	}
	defer l.Release()
	return fn(l.Path)
}

// Person is an author or committer.
type Person struct {
	Name  string
	Email string
	Date  time.Time
}

// Commit is the metadata of a commit.
type Commit struct {
	SHA       string
	Parents   []string
	Author    Person
	Committer Person
	Summary   string
	Message   string
}

// run runs git in the repository at path with a timeout and returns stdout.
func run(ctx context.Context, path string, timeout time.Duration, args ...string) ([]byte, error) {
	var out bytes.Buffer
	err := git.Exec(ctx, git.Cmd{RepoPath: path, Args: args, Stdout: &out, Timeout: timeout})
	return out.Bytes(), err
}

// stream is a git process whose stdout is read incrementally.
type stream struct {
	io.Reader
	pr   *io.PipeReader
	proc *sys.Process
	done chan struct{}
	err  error
}

// startStream starts git; the caller reads it and must call Close.
func startStream(ctx context.Context, rp string, timeout time.Duration, args ...string) (*stream, error) {
	pr, pw := io.Pipe()
	proc, err := git.Start(ctx, git.Cmd{RepoPath: rp, Args: args, Stdout: pw, Timeout: timeout})
	if err != nil {
		_ = pw.Close()
		return nil, err
	}
	st := &stream{Reader: pr, pr: pr, proc: proc, done: make(chan struct{})}
	go func() {
		st.err = proc.Wait()
		_ = pw.CloseWithError(st.err)
		close(st.done)
	}()
	return st, nil
}

// Close stops git if it is still running and returns its exit error.
func (st *stream) Close() error {
	select {
	case <-st.done:
	default:
		st.proc.Kill()
	}
	_, _ = io.Copy(io.Discard, st.pr)
	<-st.done
	return st.err
}
