package gittier

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
)

// ErrNotEvictable is returned by Evict when a safety check fails: the
// repository stays hot.
var ErrNotEvictable = errors.New("gittier: repository cannot be evicted")

// EvictorOptions configure an Evictor.
type EvictorOptions struct {
	Pool *pgxpool.Pool
	Tier *gitstore.Tier
	Log  *slog.Logger
	// TTL evicts the repositories not accessed for that long (0: never).
	TTL time.Duration
	// Budget is the space of the hot tier in bytes (0: no watermark
	// eviction). Above High×Budget the least valuable repositories are
	// evicted until the usage is at most Low×Budget.
	Budget    int64
	High, Low float64
	// LockTimeout bounds the wait for the exclusive lease (default 2s): a
	// repository in use is skipped, not waited for.
	LockTimeout time.Duration
	Now         func() time.Time
}

// Evictor moves idle hot repositories back to the remote journal only.
type Evictor struct {
	o EvictorOptions
	q *dbgen.Queries
}

// NewEvictor returns an Evictor.
func NewEvictor(o EvictorOptions) *Evictor {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.LockTimeout <= 0 {
		o.LockTimeout = 2 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.High <= 0 || o.High > 1 {
		o.High = 0.9
	}
	if o.Low <= 0 || o.Low >= o.High {
		o.Low = o.High * 0.8
	}
	return &Evictor{o: o, q: dbgen.New(o.Pool)}
}

// evictBatch bounds the candidates loaded per pass.
const evictBatch = 500

// Run makes one eviction pass: first the repositories idle for more than
// the TTL, then, above the high watermark, the ones with the largest
// age×size until the low watermark. Repositories failing a safety check are
// logged and skipped. It returns how many repositories were evicted.
func (e *Evictor) Run(ctx context.Context) (int, error) {
	if e.o.Tier.Store() == nil {
		return 0, nil // no remote journal: the hot tier is the only copy
	}
	evicted := 0
	done := map[uuid.UUID]bool{}
	if e.o.TTL > 0 {
		before := pgtype.Timestamptz{Time: e.o.Now().Add(-e.o.TTL), Valid: true}
		rows, err := e.q.ListEvictionCandidates(ctx, dbgen.ListEvictionCandidatesParams{Before: before, Lim: evictBatch})
		if err != nil {
			return 0, fmt.Errorf("list eviction candidates: %w", err)
		}
		for _, r := range rows {
			done[r.ID] = true
			if e.try(ctx, r.ID, "ttl") {
				evicted++
			}
		}
	}
	if e.o.Budget <= 0 {
		return evicted, ctx.Err()
	}
	used, err := e.q.HotRepoUsage(ctx)
	if err != nil {
		return evicted, fmt.Errorf("hot tier usage: %w", err)
	}
	if float64(used) <= e.o.High*float64(e.o.Budget) {
		return evicted, nil
	}
	rows, err := e.q.ListHotUnpinnedRepos(ctx, evictBatch)
	if err != nil {
		return evicted, fmt.Errorf("list hot repositories: %w", err)
	}
	now := e.o.Now()
	score := func(r dbgen.ListHotUnpinnedReposRow) float64 {
		age := now.Sub(r.LastAccessAt.Time).Seconds()
		return max(age, 1) * float64(max(r.SizeBytes, 1))
	}
	slices.SortFunc(rows, func(a, b dbgen.ListHotUnpinnedReposRow) int {
		sa, sb := score(a), score(b)
		switch {
		case sa > sb:
			return -1
		case sa < sb:
			return 1
		}
		return 0
	})
	target := int64(e.o.Low * float64(e.o.Budget))
	for _, r := range rows {
		if used <= target || ctx.Err() != nil {
			break
		}
		if done[r.ID] {
			continue
		}
		if e.try(ctx, r.ID, "watermark") {
			evicted++
			used -= r.SizeBytes
		}
	}
	return evicted, ctx.Err()
}

func (e *Evictor) try(ctx context.Context, id uuid.UUID, reason string) bool {
	err := e.Evict(ctx, id, false)
	switch {
	case err == nil:
		e.o.Log.InfoContext(ctx, "repository evicted", "repo", id, "reason", reason)
		return true
	case errors.Is(err, gitstore.ErrBusy), errors.Is(err, context.DeadlineExceeded):
		e.o.Log.DebugContext(ctx, "repository busy, not evicted", "repo", id)
	default:
		e.o.Log.WarnContext(ctx, "repository not evicted", "repo", id, "error", err)
	}
	return false
}

// Evict moves one hot repository to the cold state after checking that the
// remote journal holds all of it: the manifest is the one recorded in the
// database, every pack and index is committed with the expected size and
// digest, verified and replicated, and the local refs and HEAD equal the
// remote snapshot. The directory goes to the trash (purged after an hour).
// force also evicts a pinned repository.
func (e *Evictor) Evict(ctx context.Context, id uuid.UUID, force bool) error {
	t := e.o.Tier
	store := t.Store()
	if store == nil {
		return fmt.Errorf("%w: no remote journal configured", ErrNotEvictable)
	}
	lctx, cancel := context.WithTimeout(ctx, e.o.LockTimeout)
	l, err := t.Lock(lctx, id)
	cancel()
	if err != nil {
		if lctx.Err() != nil && ctx.Err() == nil {
			return fmt.Errorf("evict %s: %w", id, gitstore.ErrBusy)
		}
		return err
	}
	defer l.Release()

	row, err := e.q.GetRepoStorage(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", id, gitstore.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("load repository state %s: %w", id, err)
	}
	if row.StorageState != gitstore.StateHot {
		return fmt.Errorf("%w: %s is %s", ErrNotEvictable, id, row.StorageState)
	}
	if row.Pinned && !force {
		return fmt.Errorf("%w: %s is pinned", ErrNotEvictable, id)
	}
	dirty, err := e.q.RepoJournalDirty(ctx, id)
	if err != nil {
		return fmt.Errorf("load journal state %s: %w", id, err)
	}
	if dirty {
		return fmt.Errorf("%w: %s has pushes not yet in the remote journal", ErrNotEvictable, id)
	}
	if err := e.check(ctx, store, l.Path, row); err != nil {
		return err
	}
	if err := setState(ctx, e.q, id, gitstore.StateHot, gitstore.StateEvicting); err != nil {
		return err
	}
	if err := t.Trash(ctx, id); err != nil {
		if rerr := setState(context.WithoutCancel(ctx), e.q, id, gitstore.StateEvicting, gitstore.StateHot); rerr != nil {
			e.o.Log.ErrorContext(ctx, "restore state after failed eviction", "repo", id, "error", rerr)
		}
		return fmt.Errorf("evict %s: %w", id, err)
	}
	return setState(context.WithoutCancel(ctx), e.q, id, gitstore.StateEvicting, gitstore.StateCold)
}

func (e *Evictor) check(ctx context.Context, store *gitstore.Store, path string, row dbgen.GetRepoStorageRow) error {
	id := row.ID
	notEvictable := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s: %s", ErrNotEvictable, id, fmt.Sprintf(format, a...))
	}
	m, err := store.LoadManifest(ctx, id)
	if err != nil {
		return notEvictable("manifest: %v", err)
	}
	if m.Seq() != row.ManifestSeq {
		return notEvictable("remote manifest at seq %d, database at %d", m.Seq(), row.ManifestSeq)
	}
	class := store.Class()
	committed := func(key, sum string, size int64) error {
		o, err := class.Stat(ctx, key)
		if err != nil {
			return notEvictable("%s: %v", key, err)
		}
		switch {
		case sum != "" && !strings.EqualFold(o.SHA256Hex(), sum):
			return notEvictable("%s: digest mismatch", key)
		case size >= 0 && o.Size != size:
			return notEvictable("%s: size %d, manifest says %d", key, o.Size, size)
		case o.VerifiedAt.IsZero():
			return notEvictable("%s: never verified", key)
		case !o.ReplicasOK:
			return notEvictable("%s: replicas not up to date", key)
		}
		return nil
	}
	for _, p := range m.Packs() {
		if err := committed(p.Pack, p.SHA256, p.Size); err != nil {
			return err
		}
		if err := committed(p.Idx, p.IdxSHA256, -1); err != nil {
			return err
		}
	}
	local, head, err := localRefs(ctx, path)
	if err != nil {
		return notEvictable("%v", err)
	}
	if m.RefsSeq == 0 {
		if len(local) > 0 {
			return notEvictable("%d local refs, no remote snapshot", len(local))
		}
		return nil
	}
	if err := committed(gitstore.RefsKey(id, m.RefsSeq), "", -1); err != nil {
		return err
	}
	snap, err := store.LoadRefs(ctx, id, m.RefsSeq)
	if err != nil {
		return notEvictable("refs snapshot: %v", err)
	}
	if len(local) != len(snap.Refs) {
		return notEvictable("%d local refs, %d in the snapshot", len(local), len(snap.Refs))
	}
	for name, oid := range local {
		if snap.Refs[name] != oid {
			return notEvictable("ref %s differs from the snapshot", name)
		}
	}
	if snap.Head != "" && head != snap.Head {
		return notEvictable("HEAD %q, snapshot says %q", head, snap.Head)
	}
	return nil
}

// localRefs returns the refs of a bare repository and the target of HEAD.
func localRefs(ctx context.Context, path string) (map[string]string, string, error) {
	var out bytes.Buffer
	if err := git.Exec(ctx, git.Cmd{
		RepoPath: path, Args: []string{"for-each-ref", "--format=%(refname) %(objectname)"}, Stdout: &out,
	}); err != nil {
		return nil, "", fmt.Errorf("list refs: %w", err)
	}
	refs := map[string]string{}
	for line := range strings.Lines(out.String()) {
		name, oid, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		if _, err := hex.DecodeString(oid); err != nil {
			return nil, "", fmt.Errorf("list refs: bad object id %q", oid)
		}
		refs[name] = oid
	}
	h, err := git.Run(ctx, path, "symbolic-ref", "-q", "HEAD")
	if err != nil {
		return refs, "", nil //nolint:nilerr // detached or unset HEAD: compared as empty
	}
	return refs, strings.TrimSpace(string(h)), nil
}

func setState(ctx context.Context, q *dbgen.Queries, id uuid.UUID, from, to string) error {
	n, err := q.SetRepoStorageState(ctx, dbgen.SetRepoStorageStateParams{ID: id, FromState: from, ToState: to})
	if err != nil {
		return fmt.Errorf("set state %s %s→%s: %w", id, from, to, err)
	}
	if n == 0 {
		return fmt.Errorf("set state %s %s→%s: %w", id, from, to, gitstore.ErrBusy)
	}
	return nil
}
