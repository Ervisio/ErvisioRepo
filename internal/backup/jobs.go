package backup

import (
	"context"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/jobs"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// DumpArgs is the periodic PostgreSQL dump.
type DumpArgs struct{}

// Kind implements river.JobArgs.
func (DumpArgs) Kind() string { return "backup_pg_dump" }

// InsertOpts retries a failed dump a few times, one at a time.
func (DumpArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// DumpInterval is how often PostgreSQL is dumped; KeepDumps how many dumps
// survive the retention anyway.
const (
	DumpInterval = 24 * time.Hour
	KeepDumps    = 3
)

// JobOptions configure the backup jobs.
type JobOptions struct {
	Class       *tier.Class // the "backup" class
	DatabaseURL string
	Retention   time.Duration // 0 keeps every dump
	Log         *slog.Logger
}

// JobsOption registers the daily dump and its retention.
func JobsOption(o JobOptions) jobs.Option {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return func(r *jobs.Registry) {
		river.AddWorker(r.Workers, &dumpWorker{o: o})
		r.Periodic = append(r.Periodic, river.NewPeriodicJob(river.PeriodicInterval(DumpInterval),
			func() (river.JobArgs, *river.InsertOpts) { return DumpArgs{}, nil }, nil))
	}
}

type dumpWorker struct {
	river.WorkerDefaults[DumpArgs]
	o JobOptions
}

func (w *dumpWorker) Timeout(*river.Job[DumpArgs]) time.Duration { return DumpTimeout + time.Hour }

func (w *dumpWorker) Work(ctx context.Context, _ *river.Job[DumpArgs]) error {
	now := time.Now()
	d, err := DumpDatabase(ctx, w.o.Class, w.o.DatabaseURL, now)
	if err != nil {
		return err
	}
	w.o.Log.InfoContext(ctx, "database dumped", "key", d.Key, "size", d.Size)
	n, err := Prune(ctx, w.o.Class, w.o.Retention, KeepDumps, now)
	if n > 0 {
		w.o.Log.InfoContext(ctx, "old database dumps deleted", "count", n)
	}
	return err
}
