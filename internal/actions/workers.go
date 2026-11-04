package actions

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/jobs"
)

// tasksChannel is the NOTIFY channel that wakes runners polling for tasks;
// logChannel carries "<job id>:<length>" when log rows are added.
const (
	tasksChannel = "ervisio_actions_tasks"
	logChannel   = "ervisio_actions_logs"
)

const (
	sweepInterval    = time.Minute
	scheduleInterval = time.Minute
	// stuckRunAfter is when the sweeper advances a run nobody touched.
	stuckRunAfter = 10 * time.Minute
	// archiveAfter is when the sweeper archives logs a lost job left.
	archiveAfter = 15 * time.Minute
)

// ReportArgs reports a run to the check runs and commit statuses.
type ReportArgs struct {
	RunID uuid.UUID `json:"run_id"`
}

func (ReportArgs) Kind() string { return "actions_report" }

// InsertOpts retries reports for a while.
func (ReportArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 8} }

// ArchiveArgs moves the log of a completed job to storage.
type ArchiveArgs struct {
	JobID uuid.UUID `json:"job_id"`
}

func (ArchiveArgs) Kind() string { return "actions_archive" }

// InsertOpts deduplicates archive jobs of one job.
func (ArchiveArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 10, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// SweepArgs fails jobs of vanished runners, times out jobs, marks
// runners offline and advances stuck runs.
type SweepArgs struct{}

func (SweepArgs) Kind() string { return "actions_sweep" }

// ScheduleArgs starts the due on.schedule workflows.
type ScheduleArgs struct{}

func (ScheduleArgs) Kind() string { return "actions_schedule" }

// JobsOption registers the Actions workers and periodic jobs.
func (s *Service) JobsOption() jobs.Option {
	return func(r *jobs.Registry) {
		river.AddWorker(r.Workers, &reportWorker{s: s})
		river.AddWorker(r.Workers, &archiveWorker{s: s})
		river.AddWorker(r.Workers, &sweepWorker{s: s})
		river.AddWorker(r.Workers, &scheduleWorker{s: s})
		r.Periodic = append(r.Periodic,
			river.NewPeriodicJob(river.PeriodicInterval(sweepInterval),
				func() (river.JobArgs, *river.InsertOpts) { return SweepArgs{}, nil },
				&river.PeriodicJobOpts{RunOnStart: true}),
			river.NewPeriodicJob(river.PeriodicInterval(scheduleInterval),
				func() (river.JobArgs, *river.InsertOpts) { return ScheduleArgs{}, nil },
				&river.PeriodicJobOpts{RunOnStart: true}),
		)
	}
}

type reportWorker struct {
	river.WorkerDefaults[ReportArgs]
	s *Service
}

func (w *reportWorker) Work(ctx context.Context, job *river.Job[ReportArgs]) error {
	return w.s.report(ctx, job.Args.RunID)
}

type archiveWorker struct {
	river.WorkerDefaults[ArchiveArgs]
	s *Service
}

func (*archiveWorker) Timeout(*river.Job[ArchiveArgs]) time.Duration { return 30 * time.Minute }

func (w *archiveWorker) Work(ctx context.Context, job *river.Job[ArchiveArgs]) error {
	return w.s.archiveLog(ctx, job.Args.JobID)
}

type sweepWorker struct {
	river.WorkerDefaults[SweepArgs]
	s *Service
}

func (w *sweepWorker) Work(ctx context.Context, _ *river.Job[SweepArgs]) error {
	return w.s.sweep(ctx)
}

type scheduleWorker struct {
	river.WorkerDefaults[ScheduleArgs]
	s *Service
}

func (w *scheduleWorker) Work(ctx context.Context, _ *river.Job[ScheduleArgs]) error {
	return w.s.runSchedules(ctx)
}

// sweep is the periodic clean-up of the job queue.
func (s *Service) sweep(ctx context.Context) error {
	now := s.now()
	off := s.offlineAfter()
	if _, err := s.q.MarkStaleActionsRunnersOffline(ctx, ts(now.Add(-off))); err != nil {
		return fmt.Errorf("mark runners offline: %w", err)
	}
	vanished, err := s.q.ListVanishedActionsJobs(ctx, ts(now.Add(-2*off)))
	if err != nil {
		return fmt.Errorf("vanished jobs: %w", err)
	}
	for _, j := range vanished {
		s.log.Warn("actions job lost its runner", "job_id", j.ID, "run_id", j.RunID)
		s.appendServerLog(ctx, j.ID, "The runner of this job stopped responding.")
		if err := s.completeJob(ctx, j.ID, "failure"); err != nil {
			s.log.Warn("fail vanished job", "job_id", j.ID, "err", err)
		}
	}
	timedOut, err := s.q.ListTimedOutActionsJobs(ctx)
	if err != nil {
		return fmt.Errorf("timed out jobs: %w", err)
	}
	for _, j := range timedOut {
		s.appendServerLog(ctx, j.ID, "The job exceeded its maximum execution time.")
		if err := s.q.RequestActionsJobCancel(ctx, j.ID); err != nil {
			s.log.Warn("cancel timed out job", "job_id", j.ID, "err", err)
		}
		if err := s.completeJob(ctx, j.ID, "timed_out"); err != nil {
			s.log.Warn("time out job", "job_id", j.ID, "err", err)
		}
	}
	stuck, err := s.q.ListStuckActionsRuns(ctx, ts(now.Add(-stuckRunAfter)))
	if err != nil {
		return fmt.Errorf("stuck runs: %w", err)
	}
	for _, id := range stuck {
		_ = s.q.TouchActionsRun(ctx, id)
		if err := s.advance(ctx, id); err != nil {
			s.log.Warn("advance stuck run", "run_id", id, "err", err)
		}
	}
	unarchived, err := s.q.ListUnarchivedActionsJobs(ctx, ts(now.Add(-archiveAfter)))
	if err != nil {
		return fmt.Errorf("unarchived logs: %w", err)
	}
	for _, id := range unarchived {
		s.enqueue(ctx, ArchiveArgs{JobID: id})
	}
	if err := s.q.DeleteExpiredActionsRegistrationTokens(ctx); err != nil {
		return fmt.Errorf("expired registration tokens: %w", err)
	}
	if n, err := s.q.DeleteUsedEphemeralActionsRunners(ctx, ts(now.Add(-off))); err != nil {
		return fmt.Errorf("used ephemeral runners: %w", err)
	} else if n > 0 {
		s.log.Info("deleted used ephemeral runners", "count", n)
	}
	return nil
}
