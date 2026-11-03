package issues

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/jobs"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// CleanupArgs deletes attachment objects that no attachment row references
// (left by an interrupted upload or by a deleted repository).
type CleanupArgs struct{}

func (CleanupArgs) Kind() string { return "issue_attachments_cleanup" }

// Cleanup tuning: only objects older than CleanupGrace are considered, so an
// upload in progress is never touched.
const (
	CleanupInterval = 6 * time.Hour
	CleanupGrace    = 2 * time.Hour
	cleanupBatch    = 200
)

// JobsOption registers the attachment cleanup worker and its schedule.
func JobsOption(q *dbgen.Queries, st Storage, log *slog.Logger) jobs.Option {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return func(r *jobs.Registry) {
		river.AddWorker(r.Workers, &cleanupWorker{q: q, st: st, log: log})
		r.Periodic = append(r.Periodic, river.NewPeriodicJob(river.PeriodicInterval(CleanupInterval),
			func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{}, nil }, nil))
	}
}

type cleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	q   *dbgen.Queries
	st  Storage
	log *slog.Logger
}

func (w *cleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	if w.st == nil {
		return nil
	}
	before := pgtype.Timestamptz{Time: time.Now().Add(-CleanupGrace), Valid: true}
	rows, err := w.q.ListOrphanIssueAttachmentObjects(ctx, dbgen.ListOrphanIssueAttachmentObjectsParams{
		Policies: []string{PolicyAttachments, PolicyLFS}, Before: before, Lim: cleanupBatch})
	if err != nil {
		return fmt.Errorf("list orphan attachment objects: %w", err)
	}
	removed := 0
	for _, o := range rows {
		cl, err := w.st.Class(o.Policy)
		if err != nil {
			continue
		}
		if err := cl.Delete(ctx, o.Key[len(o.Policy)+1:]); err != nil {
			w.log.WarnContext(ctx, "delete orphan attachment object", "key", o.Key, "error", err)
			continue
		}
		removed++
	}
	if removed > 0 {
		w.log.InfoContext(ctx, "removed orphan attachment objects", "count", removed)
	}
	return nil
}

var _ Storage = (*tier.Tier)(nil)
