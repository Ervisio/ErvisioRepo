package gitjournal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/git"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
)

const reconcileSizeInterval = 6 * time.Hour

func (j *Journal) flushLocal(ctx context.Context, repo uuid.UUID, path string) error {
	n, err := git.ReachableObjectBytes(ctx, path)
	if err != nil {
		return err
	}
	if err := j.recordSize(ctx, repo, n, 0); err != nil {
		return err
	}
	return j.recordClean(ctx, repo)
}

type reconcileSizeArgs struct{}

func (reconcileSizeArgs) Kind() string { return "gitjournal_reconcile_size" }

type sizeWorker struct {
	river.WorkerDefaults[reconcileSizeArgs]
	j *Journal
}

func (*sizeWorker) Timeout(*river.Job[reconcileSizeArgs]) time.Duration { return 2 * time.Hour }

func (w *sizeWorker) Work(ctx context.Context, _ *river.Job[reconcileSizeArgs]) error {
	var after uuid.UUID
	var failures []error
	for {
		ids, err := w.j.q.ListRepoIDsAfter(ctx, dbgen.ListRepoIDsAfterParams{After: after, Lim: gcBatch})
		if err != nil {
			return fmt.Errorf("list repository sizes: %w", err)
		}
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := w.j.reconcileSize(ctx, id); err != nil {
				w.j.log.WarnContext(ctx, "reconcile repository size", "repo", id, "error", err)
				failures = append(failures, err)
			}
		}
		if len(ids) < gcBatch {
			return errors.Join(failures...)
		}
		after = ids[len(ids)-1]
	}
}

func (j *Journal) reconcileSize(ctx context.Context, repo uuid.UUID) error {
	if j.store == nil {
		if hot, err := j.hot(ctx, repo); err != nil || !hot {
			return err
		}
		l, err := j.acquire(ctx, repo)
		if err != nil || l == nil {
			return err
		}
		defer l.Release()
		return j.flushLocal(ctx, repo, l.Path)
	}

	// A cold repository's manifest can be reconciled without hydrating it.
	l, err := j.o.Tier.Lock(ctx, repo)
	if err != nil {
		return err
	}
	defer l.Release()
	row, err := j.q.GetRepoJournal(ctx, repo)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load repository for size reconciliation: %w", err)
	}
	if row.JournalDirtySince.Valid {
		return nil // the sync worker will reconcile the size after committing the refs
	}
	m, err := j.store.LoadManifest(ctx, repo)
	if errors.Is(err, gitstore.ErrNoManifest) {
		if row.ManifestSeq == 0 {
			return nil
		}
		return fmt.Errorf("manifest missing at recorded sequence %d: %w", row.ManifestSeq, err)
	}
	if err != nil {
		return fmt.Errorf("load manifest for size reconciliation: %w", err)
	}
	if m.RefsSeq < row.ManifestSeq {
		return fmt.Errorf("manifest sequence %d precedes recorded sequence %d", m.RefsSeq, row.ManifestSeq)
	}
	return j.recordManifestSize(ctx, repo, m)
}
