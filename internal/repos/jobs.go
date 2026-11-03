package repos

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/jobs"
)

// PurgeArgs deletes the remote objects of a deleted repository.
type PurgeArgs struct {
	RepoID uuid.UUID `json:"repo_id"`
}

func (PurgeArgs) Kind() string { return "repos_purge" }

func (PurgeArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 10} }

// TrashArgs empties the hot-tier trash.
type TrashArgs struct{}

func (TrashArgs) Kind() string { return "repos_trash" }

// TrashInterval is how often the trash is emptied; TrashGrace how long a
// deleted repository stays there.
const (
	TrashInterval = time.Hour
	TrashGrace    = time.Hour
)

// JobsOption registers the purge worker and the periodic trash cleanup.
func JobsOption(t *gitstore.Tier, log *slog.Logger) jobs.Option {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return func(r *jobs.Registry) {
		river.AddWorker(r.Workers, &purgeWorker{t: t})
		river.AddWorker(r.Workers, &trashWorker{t: t, log: log})
		r.Periodic = append(r.Periodic, river.NewPeriodicJob(river.PeriodicInterval(TrashInterval),
			func() (river.JobArgs, *river.InsertOpts) { return TrashArgs{}, nil }, nil))
	}
}

type purgeWorker struct {
	river.WorkerDefaults[PurgeArgs]
	t *gitstore.Tier
}

func (w *purgeWorker) Work(ctx context.Context, j *river.Job[PurgeArgs]) error {
	if w.t.Store() == nil {
		return nil
	}
	if err := w.t.Store().DeleteRepo(ctx, j.Args.RepoID); err != nil {
		return fmt.Errorf("purge %s: %w", j.Args.RepoID, err)
	}
	return nil
}

type trashWorker struct {
	river.WorkerDefaults[TrashArgs]
	t   *gitstore.Tier
	log *slog.Logger
}

func (w *trashWorker) Work(ctx context.Context, _ *river.Job[TrashArgs]) error {
	n, err := w.t.PurgeTrash(ctx, TrashGrace)
	if n > 0 {
		w.log.InfoContext(ctx, "emptied repository trash", "removed", n)
	}
	return err
}
