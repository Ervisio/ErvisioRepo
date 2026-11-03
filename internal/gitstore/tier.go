package gitstore

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/git"
)

// Repository storage states (repos.storage_state, PLAN §4.3).
const (
	StateHot       = "hot"
	StateCold      = "cold"
	StateHydrating = "hydrating"
	StateEvicting  = "evicting"
	StateDegraded  = "degraded"
)

// Errors returned by Acquire.
var (
	ErrNotFound = errors.New("gitstore: repository not found")
	// ErrCold: the repository is only on the remote tier and no Hydrator is
	// configured (or hydration did not make it hot).
	ErrCold = errors.New("gitstore: repository is cold")
	// ErrBusy: the repository is being hydrated or evicted and no Hydrator
	// can be waited on. Callers may retry later (HTTP 503 + Retry-After).
	ErrBusy = errors.New("gitstore: repository is busy")
	// ErrDegraded: the manifest is unreadable; an operator must intervene.
	ErrDegraded = errors.New("gitstore: repository is degraded")
	// ErrHotMissing: the database says hot but the directory is gone (lost
	// disk). Hydration code may treat it as cold after checking the remote.
	ErrHotMissing = errors.New("gitstore: hot repository missing on disk")
)

// Mode selects what a Lease allows.
type Mode int

const (
	// Read allows any read-only git command. Reads run concurrently with
	// each other and with one writer.
	Read Mode = iota
	// Write allows ref and object updates (receive-pack, merges from the UI,
	// mirror sync). At most one writer per repository across the whole
	// cluster: it holds the Postgres advisory lock of the repository.
	Write
	// Exclusive excludes every other lease: eviction, compaction swaps and
	// hydration use it. It also holds the advisory lock. Exclusive leases
	// are taken with Tier.Lock and do not check the storage state.
	Exclusive
)

func (m Mode) String() string {
	switch m {
	case Read:
		return "read"
	case Write:
		return "write"
	case Exclusive:
		return "exclusive"
	}
	return "mode(" + strconv.Itoa(int(m)) + ")"
}

// Hydrator brings a cold repository back to the hot state (PLAN §4.3). It
// must deduplicate concurrent calls for the same repository (singleflight),
// take Tier.Lock itself and leave repos.storage_state = 'hot' on success.
// Acquire calls it without holding any lock.
type Hydrator interface {
	Hydrate(ctx context.Context, repoID uuid.UUID) error
}

// Options configure NewTier.
type Options struct {
	Pool *pgxpool.Pool
	// HotPath is config.Storage.HotPath.
	HotPath string
	// Store is the remote journal; nil disables the initial manifest on Init
	// and DeleteRemote (tests, or a server without a git policy).
	Store *Store
	// Hydrator is nil until hydration is implemented: Acquire then fails
	// with ErrCold on cold repositories.
	Hydrator Hydrator
	// CatFile, when set, is told to drop the processes of a repository that
	// leaves the hot directory.
	CatFile *git.Pool
	Log     *slog.Logger
	// TouchInterval throttles the last_access_at updates (default 1 minute).
	TouchInterval time.Duration
	Now           func() time.Time
}

// Tier hands out hot repositories. It is safe for concurrent use.
type Tier struct {
	o       Options
	q       *dbgen.Queries
	mu      sync.Mutex
	entries map[uuid.UUID]*entry
}

type entry struct {
	rw   rwlock
	wmu  mutex // serializes Write and Exclusive leases inside the process
	refs int
}

// NewTier checks the options and returns the tier.
func NewTier(o Options) (*Tier, error) {
	if o.Pool == nil {
		return nil, errors.New("gitstore: pool is required")
	}
	if !filepath.IsAbs(o.HotPath) {
		return nil, fmt.Errorf("gitstore: hot path %q must be absolute", o.HotPath)
	}
	o.HotPath = filepath.Clean(o.HotPath)
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.TouchInterval <= 0 {
		o.TouchInterval = time.Minute
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Tier{o: o, q: dbgen.New(o.Pool), entries: map[uuid.UUID]*entry{}}, nil
}

// Store returns the remote journal (nil when not configured).
func (t *Tier) Store() *Store { return t.o.Store }

// HotRoot is the directory of the hot repositories.
func (t *Tier) HotRoot() string { return t.o.HotPath }

// HotPath is where the bare repository lives while hot:
// <hot_path>/git/<aa>/<uuid>.git, <aa> being the hex of the last UUID byte
// (the first bytes of a UUIDv7 are a timestamp and would not spread).
// Only internal/git may touch it, and only under a Lease.
func (t *Tier) HotPath(repoID uuid.UUID) string {
	return filepath.Join(t.o.HotPath, "git", hex.EncodeToString(repoID[15:]), repoID.String()+".git")
}

// TrashDir holds evicted and deleted repositories until PurgeTrash.
func (t *Tier) TrashDir() string { return filepath.Join(t.o.HotPath, ".trash") }

// Lease is a hot repository held in a Mode. Path is valid until Release.
type Lease struct {
	RepoID uuid.UUID
	Path   string
	Mode   Mode
	once   sync.Once
	free   func()
}

// Release frees the lease. It is idempotent and safe on a nil lease.
func (l *Lease) Release() {
	if l == nil {
		return
	}
	l.once.Do(l.free)
}

// maxHydrations bounds how many times Acquire hydrates before giving up
// (the repository can be evicted again between hydration and locking).
const maxHydrations = 3

// Acquire returns the hot path of a repository locked in mode (Read or
// Write). It waits for the lock as long as ctx allows. On a cold repository
// it calls the Hydrator, if any, and retries; without one it fails with
// ErrCold. hydrating/evicting fail with ErrBusy (or wait for the Hydrator),
// degraded with ErrDegraded, an unknown ID with ErrNotFound.
//
// While a Read or Write lease is held the repository cannot be evicted.
// Do not call Acquire again for the same repository while holding a Write
// lease, and do not run a transaction that takes LockRepoWrite on it.
func (t *Tier) Acquire(ctx context.Context, repoID uuid.UUID, mode Mode) (*Lease, error) {
	if mode != Read && mode != Write {
		return nil, fmt.Errorf("gitstore: Acquire mode %s (use Lock for exclusive leases)", mode)
	}
	for attempt := 0; ; attempt++ {
		l, err := t.lock(ctx, repoID, mode)
		if err != nil {
			return nil, err
		}
		row, err := t.q.GetRepoStorage(ctx, repoID)
		if err != nil {
			l.Release()
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("%s: %w", repoID, ErrNotFound)
			}
			return nil, fmt.Errorf("load repository state %s: %w", repoID, err)
		}
		switch row.StorageState {
		case StateHot:
			if _, err := os.Stat(l.Path); err != nil {
				l.Release()
				if errors.Is(err, fs.ErrNotExist) {
					return nil, fmt.Errorf("%s: %w", repoID, ErrHotMissing)
				}
				return nil, fmt.Errorf("stat %s: %w", repoID, err)
			}
			t.touch(ctx, row)
			return l, nil
		case StateDegraded:
			l.Release()
			return nil, fmt.Errorf("%s: %w", repoID, ErrDegraded)
		case StateCold, StateHydrating:
			l.Release()
			if t.o.Hydrator == nil {
				if row.StorageState == StateCold {
					return nil, fmt.Errorf("%s: %w", repoID, ErrCold)
				}
				return nil, fmt.Errorf("%s: %w", repoID, ErrBusy)
			}
			if attempt >= maxHydrations {
				return nil, fmt.Errorf("%s: still %s after %d hydrations: %w", repoID, row.StorageState, attempt, ErrCold)
			}
			if err := t.o.Hydrator.Hydrate(ctx, repoID); err != nil {
				return nil, fmt.Errorf("hydrate %s: %w", repoID, err)
			}
		default: // evicting, or a state added later
			l.Release()
			return nil, fmt.Errorf("%s is %s: %w", repoID, row.StorageState, ErrBusy)
		}
	}
}

// Lock takes an Exclusive lease without looking at the storage state; the
// directory may not exist. It is for the hydration, eviction and
// compaction code, which update repos.storage_state themselves with
// SetRepoStorageState (a compare-and-swap) while holding it.
func (t *Tier) Lock(ctx context.Context, repoID uuid.UUID) (*Lease, error) {
	return t.lock(ctx, repoID, Exclusive)
}

func (t *Tier) lock(ctx context.Context, repoID uuid.UUID, mode Mode) (*Lease, error) {
	e := t.ref(repoID)
	var undo []func()
	fail := func(err error) (*Lease, error) {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
		t.unref(repoID)
		return nil, err
	}
	if mode != Read {
		if err := e.wmu.lock(ctx); err != nil {
			return fail(fmt.Errorf("lock %s: %w", repoID, err))
		}
		undo = append(undo, e.wmu.unlock)
		unlockPG, err := t.lockPG(ctx, repoID)
		if err != nil {
			return fail(err)
		}
		undo = append(undo, unlockPG)
	}
	exclusive := mode == Exclusive
	if err := e.rw.lock(ctx, exclusive); err != nil {
		return fail(fmt.Errorf("lock %s: %w", repoID, err))
	}
	undo = append(undo, func() { e.rw.unlock(exclusive) })
	return &Lease{RepoID: repoID, Path: t.HotPath(repoID), Mode: mode, free: func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
		t.unref(repoID)
	}}, nil
}

// lockPG takes the session advisory lock on a dedicated connection.
func (t *Tier) lockPG(ctx context.Context, repoID uuid.UUID) (func(), error) {
	conn, err := t.o.Pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("lock %s: acquire connection: %w", repoID, err)
	}
	discard := func() {
		// Closing the session is the only way to be sure the lock is gone.
		_ = conn.Conn().Close(context.Background())
		conn.Release()
	}
	if err := dbgen.New(conn).LockRepoSession(ctx, repoID); err != nil {
		discard() // a cancelled lock call may still have been granted
		return nil, fmt.Errorf("lock %s: advisory lock: %w", repoID, err)
	}
	return func() {
		uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ok, err := dbgen.New(conn).UnlockRepoSession(uctx, repoID)
		if err != nil || !ok {
			t.o.Log.Warn("release repository lock", "repo", repoID, "unlocked", ok, "error", err)
			discard()
			return
		}
		conn.Release()
	}, nil
}

func (t *Tier) ref(id uuid.UUID) *entry {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entries[id]
	if e == nil {
		e = &entry{wmu: newMutex()}
		t.entries[id] = e
	}
	e.refs++
	return e
}

func (t *Tier) unref(id uuid.UUID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if e := t.entries[id]; e != nil {
		if e.refs--; e.refs == 0 {
			delete(t.entries, id)
		}
	}
}

func (t *Tier) touch(ctx context.Context, row dbgen.GetRepoStorageRow) {
	now := t.o.Now()
	if row.LastAccessAt.Valid && now.Sub(row.LastAccessAt.Time) < t.o.TouchInterval {
		return
	}
	err := t.q.TouchRepoAccess(context.WithoutCancel(ctx), dbgen.TouchRepoAccessParams{
		At: pgtype.Timestamptz{Time: now, Valid: true}, ID: row.ID,
		NotBefore: pgtype.Timestamptz{Time: now.Add(-t.o.TouchInterval), Valid: true},
	})
	if err != nil {
		t.o.Log.WarnContext(ctx, "touch repository", "repo", row.ID, "error", err)
	}
}

// Init creates the hot bare repository of a new repository row (state hot)
// and, when a Store is configured, its empty remote manifest. It fails with
// os.ErrExist when the directory exists and with ErrConflict when a manifest
// already exists. On failure nothing is left behind.
func (t *Tier) Init(ctx context.Context, repoID uuid.UUID, defaultBranch string) error {
	l, err := t.Lock(ctx, repoID)
	if err != nil {
		return err
	}
	defer l.Release()
	if err := git.InitBare(ctx, l.Path, defaultBranch); err != nil {
		return fmt.Errorf("init %s: %w", repoID, err)
	}
	if t.o.Store != nil {
		if err := t.o.Store.SaveManifest(ctx, NewManifest(repoID)); err != nil {
			if rerr := os.RemoveAll(l.Path); rerr != nil {
				t.o.Log.ErrorContext(ctx, "remove repository after failed init", "repo", repoID, "error", rerr)
			}
			return fmt.Errorf("init %s: %w", repoID, err)
		}
	}
	return nil
}

// Trash moves the hot directory of a repository to TrashDir, where
// PurgeTrash deletes it later. The caller must hold an Exclusive lease (an
// evictor) or have deleted the repository row (no new leases can be
// state-checked). A missing directory is not an error.
func (t *Tier) Trash(ctx context.Context, repoID uuid.UUID) error {
	src := t.HotPath(repoID)
	if t.o.CatFile != nil {
		t.o.CatFile.Forget(src)
	}
	if err := os.MkdirAll(t.TrashDir(), 0o750); err != nil {
		return fmt.Errorf("trash %s: %w", repoID, err)
	}
	dst := filepath.Join(t.TrashDir(), repoID.String()+".git."+strconv.FormatInt(t.o.Now().UnixNano(), 10))
	if err := os.Rename(src, dst); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("trash %s: %w", repoID, err)
	}
	t.o.Log.InfoContext(ctx, "repository moved to trash", "repo", repoID, "path", dst)
	return nil
}

// PurgeTrash deletes trash entries moved there more than olderThan ago.
func (t *Tier) PurgeTrash(ctx context.Context, olderThan time.Duration) (int, error) {
	entries, err := os.ReadDir(t.TrashDir())
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read trash: %w", err)
	}
	cutoff := t.o.Now().Add(-olderThan).UnixNano()
	n := 0
	var errs []error
	for _, de := range entries {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		i := strings.LastIndexByte(de.Name(), '.')
		ts, perr := strconv.ParseInt(de.Name()[i+1:], 10, 64)
		if i < 0 || perr != nil || ts > cutoff {
			continue
		}
		if err := os.RemoveAll(filepath.Join(t.TrashDir(), de.Name())); err != nil {
			errs = append(errs, err)
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
}
