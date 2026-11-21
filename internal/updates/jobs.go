package updates

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/jobs"
)

// tickEvery is how often the periodic job runs: it asks GitHub only every
// checkEvery, but an automatic update waits autoDelay after a release.
const tickEvery = time.Hour

// TickArgs runs the periodic update check.
type TickArgs struct{}

func (TickArgs) Kind() string { return "updates_check" }

type tickWorker struct {
	river.WorkerDefaults[TickArgs]
	s *Service
}

func (*tickWorker) Timeout(*river.Job[TickArgs]) time.Duration { return 2 * time.Minute }

func (w *tickWorker) Work(ctx context.Context, _ *river.Job[TickArgs]) error { return w.s.tick(ctx) }

// JobsOption registers the periodic check.
func (s *Service) JobsOption() jobs.Option {
	return func(r *jobs.Registry) {
		river.AddWorker(r.Workers, &tickWorker{s: s})
		r.Periodic = append(r.Periodic, river.NewPeriodicJob(river.PeriodicInterval(tickEvery),
			func() (river.JobArgs, *river.InsertOpts) { return TickArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true}))
	}
}
