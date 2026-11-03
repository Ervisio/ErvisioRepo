package gittier

import (
	"context"
	"log/slog"

	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/jobs"
)

// EvictArgs is the periodic eviction pass.
type EvictArgs struct{}

// Kind implements river.JobArgs.
func (EvictArgs) Kind() string { return "git_evict" }

// InsertOpts does not retry: the next pass comes in 15 minutes.
func (EvictArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 1, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// JobsOption registers the eviction pass, every jobs.EvictInterval.
func JobsOption(ev *Evictor, log *slog.Logger) jobs.Option {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return func(r *jobs.Registry) {
		river.AddWorker(r.Workers, &evictWorker{ev: ev, log: log})
		r.Periodic = append(r.Periodic, river.NewPeriodicJob(river.PeriodicInterval(jobs.EvictInterval),
			func() (river.JobArgs, *river.InsertOpts) { return EvictArgs{}, nil }, &river.PeriodicJobOpts{RunOnStart: true}))
	}
}

type evictWorker struct {
	river.WorkerDefaults[EvictArgs]
	ev  *Evictor
	log *slog.Logger
}

func (w *evictWorker) Work(ctx context.Context, _ *river.Job[EvictArgs]) error {
	n, err := w.ev.Run(ctx)
	if n > 0 {
		w.log.InfoContext(ctx, "eviction pass", "evicted", n)
	}
	return err
}
