// Package gitjournal is the write path of the git journal (PLAN §4.3): it
// turns every push into a self-contained pack segment and a ref snapshot in
// the remote store, committed by a CAS write of the manifest, and keeps the
// journal compact (compaction, git maintenance, garbage collection).
//
// The invariant is that the manifest never references a missing object:
// packs and snapshots are uploaded first, the manifest last.
package gitjournal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/quota"
)

// Durability policies (storage.policy.git.durability).
const (
	DurabilitySync  = "sync"
	DurabilityAsync = "async"
)

// Defaults.
const (
	DefaultMaxFileSize      = 100 << 20
	DefaultMaintenanceEvery = 20
	// CompactSegments and CompactRatio trigger a compaction: more segments
	// than CompactSegments, or segments larger than CompactRatio of the base.
	CompactSegments = 64
	CompactRatio    = 0.30
	// compactNoBase triggers the first compaction of a repository without a
	// base pack.
	compactNoBase = 8

	packTimeout  = 2 * time.Hour
	casAttempts  = 3
	maxLargeList = 10
)

// Enqueuer inserts background jobs (*jobs.Runner).
type Enqueuer interface {
	Enqueue(ctx context.Context, args river.JobArgs) error
}

// Options configures a Journal.
type Options struct {
	Pool *pgxpool.Pool
	Tier *gitstore.Tier
	// Durability is "sync" (the push succeeds once the journal holds it)
	// or "async" (a job replicates it within a minute).
	Durability string
	// MaxPushSize bounds the pack of one push; 0 = no limit.
	MaxPushSize int64
	// MaxFileSize is the largest blob accepted outside Git LFS.
	MaxFileSize int64
	// MaintenanceEvery runs git maintenance after that many pushes.
	MaintenanceEvery int
	Log              *slog.Logger
	Now              func() time.Time
}

// Journal implements the push hooks and the journal jobs.
type Journal struct {
	o     Options
	q     *dbgen.Queries
	store *gitstore.Store
	log   *slog.Logger
	enq   atomic.Pointer[enqueuerBox]
}

type enqueuerBox struct{ e Enqueuer }

// New builds a Journal. The tier must have a remote store.
func New(o Options) (*Journal, error) {
	if o.Pool == nil || o.Tier == nil {
		return nil, errors.New("gitjournal: pool and tier are required")
	}
	if o.Tier.Store() == nil {
		return nil, errors.New("gitjournal: the git tier has no remote store")
	}
	switch o.Durability {
	case "":
		o.Durability = DurabilitySync
	case DurabilitySync, DurabilityAsync:
	default:
		return nil, fmt.Errorf("gitjournal: unknown durability %q", o.Durability)
	}
	if o.MaxFileSize <= 0 {
		o.MaxFileSize = DefaultMaxFileSize
	}
	if o.MaintenanceEvery <= 0 {
		o.MaintenanceEvery = DefaultMaintenanceEvery
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Journal{o: o, q: dbgen.New(o.Pool), store: o.Tier.Store(), log: o.Log}, nil
}

// SetEnqueuer sets the job queue (built after the Journal, whose workers it
// runs).
func (j *Journal) SetEnqueuer(e Enqueuer) { j.enq.Store(&enqueuerBox{e: e}) }

func (j *Journal) enqueue(ctx context.Context, args river.JobArgs) {
	b := j.enq.Load()
	if b == nil || b.e == nil {
		j.log.WarnContext(ctx, "git journal job not enqueued: no queue", "kind", args.Kind())
		return
	}
	if err := b.e.Enqueue(ctx, args); err != nil {
		j.log.ErrorContext(ctx, "enqueue git journal job", "kind", args.Kind(), "error", err)
	}
}

// head is the journal state read before a change.
type head struct {
	m    *gitstore.Manifest
	refs *gitstore.RefsSnapshot // nil: no snapshot yet
}

func (j *Journal) loadHead(ctx context.Context, repo uuid.UUID) (head, error) {
	m, err := j.store.LoadManifest(ctx, repo)
	if errors.Is(err, gitstore.ErrNoManifest) {
		return head{m: gitstore.NewManifest(repo)}, nil
	}
	if err != nil {
		return head{}, err
	}
	h := head{m: m}
	if m.RefsSeq > 0 {
		if h.refs, err = j.store.LoadRefs(ctx, repo, m.RefsSeq); err != nil {
			return head{}, err
		}
	}
	return h, nil
}

func (h head) tips() []string {
	if h.refs == nil {
		return nil
	}
	return tips(h.refs.Refs)
}

// hasPack reports whether the manifest already references the pack sha.
func hasPack(m *gitstore.Manifest, sha string) bool {
	for _, p := range m.Packs() {
		if p.SHA == sha {
			return true
		}
	}
	return false
}

// pending is a segment uploaded by pre-receive for the push that follows
// journal state baseSeq.
type pending struct {
	baseSeq int64
	seg     *gitstore.Segment // nil: the push adds no object
}

// flush brings the remote journal up to the refs of the hot repository at
// path, which the caller holds with a Write (or Exclusive) lease. pre, if
// it still applies, is reused instead of packing again.
func (j *Journal) flush(ctx context.Context, repo uuid.UUID, path string, pre *pending) error {
	for attempt := 0; ; attempt++ {
		h, err := j.loadHead(ctx, repo)
		if err != nil {
			return err
		}
		cur, err := j.readRefs(ctx, path)
		if err != nil {
			return err
		}
		if cur.equal(h.refs) {
			return j.recordClean(ctx, repo)
		}
		seq := h.m.RefsSeq + 1
		var seg *gitstore.Segment
		if pre != nil && pre.baseSeq == h.m.RefsSeq && (pre.seg == nil || pre.seg.Seq == seq) {
			seg = pre.seg
		} else {
			exclude, err := existing(ctx, path, nil, h.tips())
			if err != nil {
				return err
			}
			if seg, err = j.uploadSegment(ctx, repo, path, nil, tips(cur.Refs), exclude, seq, nil); err != nil {
				return err
			}
		}
		if seg != nil && hasPack(h.m, seg.SHA) {
			seg = nil // same objects as an earlier push
		}
		snap := &gitstore.RefsSnapshot{Version: gitstore.ManifestVersion, Seq: seq, Head: cur.Head, Refs: cur.Refs}
		if err := j.store.PutRefs(ctx, repo, snap); err != nil {
			return err
		}
		m := h.m.Clone()
		if seg != nil {
			m.Segments = append(m.Segments, *seg)
		}
		m.RefsSeq = seq
		err = j.store.SaveManifest(ctx, m)
		if errors.Is(err, gitstore.ErrConflict) && attempt+1 < casAttempts {
			j.log.WarnContext(ctx, "git manifest changed concurrently, retrying", "repo", repo)
			pre = nil
			continue
		}
		if err != nil {
			return err
		}
		return j.recordPush(ctx, repo, m)
	}
}

// size is the remote size of a manifest: every pack it references.
func size(m *gitstore.Manifest) int64 {
	var n int64
	for _, p := range m.Packs() {
		n += p.Size
	}
	return n
}

// recordSize stores the repository size and adjusts the owner's usage.
func (j *Journal) recordSize(ctx context.Context, repo uuid.UUID, m *gitstore.Manifest) error {
	tx, err := j.o.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("record size: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := j.q.WithTx(tx)
	row, err := q.GetRepoJournal(ctx, repo)
	if err != nil {
		return fmt.Errorf("record size: %w", err)
	}
	if err := q.SetRepoManifestSeq(ctx, dbgen.SetRepoManifestSeqParams{ID: repo, Seq: m.RefsSeq}); err != nil {
		return fmt.Errorf("record manifest seq: %w", err)
	}
	prev, err := q.SetRepoSize(ctx, dbgen.SetRepoSizeParams{ID: repo, SizeBytes: size(m)})
	if err != nil {
		return fmt.Errorf("record size: %w", err)
	}
	if err := quota.Adjust(ctx, q, row.OwnerID, quota.Delta{Bytes: size(m) - prev}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("record size: %w", err)
	}
	return nil
}

// recordPush updates the database after a manifest commit and schedules
// maintenance and compaction when due.
func (j *Journal) recordPush(ctx context.Context, repo uuid.UUID, m *gitstore.Manifest) error {
	if err := j.recordSize(ctx, repo, m); err != nil {
		return err
	}
	if err := j.recordClean(ctx, repo); err != nil {
		return err
	}
	n, err := j.q.IncRepoPushes(ctx, repo)
	if err != nil {
		return fmt.Errorf("count pushes: %w", err)
	}
	if int(n) >= j.o.MaintenanceEvery {
		j.enqueue(ctx, MaintenanceArgs{RepoID: repo})
	}
	if needsCompaction(m) {
		j.enqueue(ctx, CompactArgs{RepoID: repo})
	}
	return nil
}

// recordClean clears the dirty marker: the journal matches the hot copy.
// The caller holds the write lease, under which every marker is set.
func (j *Journal) recordClean(ctx context.Context, repo uuid.UUID) error {
	if err := j.q.ClearRepoJournalDirty(ctx, repo); err != nil {
		return fmt.Errorf("clear journal marker: %w", err)
	}
	return nil
}

func (j *Journal) markDirty(ctx context.Context, repo uuid.UUID) error {
	if err := j.q.MarkRepoJournalDirty(ctx, repo); err != nil {
		return fmt.Errorf("mark journal dirty: %w", err)
	}
	return nil
}

// needsCompaction applies the compaction thresholds.
func needsCompaction(m *gitstore.Manifest) bool {
	n := len(m.Segments)
	if n > CompactSegments {
		return true
	}
	if m.Base == nil {
		return n > compactNoBase
	}
	var segBytes int64
	for _, s := range m.Segments {
		segBytes += s.Size
	}
	return float64(segBytes) > CompactRatio*float64(m.Base.Size)
}
