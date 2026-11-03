package ghsync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/jobs"
)

const (
	// DueScanInterval is how often the due links are looked up.
	DueScanInterval = time.Minute
	// NightlyInterval is the period of the full backup check.
	NightlyInterval = 24 * time.Hour
	runTimeout      = 2 * time.Hour
	dueBatch        = 100
	nightlyPage     = 500
	busySnooze      = time.Minute
)

// RunArgs runs the link of a repository once.
type RunArgs struct {
	RepoID uuid.UUID `json:"repo_id"`
	// Debounced runs PushDebounce after the push that scheduled it; the
	// pushes of one debounce window share the run.
	Debounced bool `json:"debounced,omitempty"`
	// Force skips the equal-SHA shortcut of a backup.
	Force bool `json:"force,omitempty"`
}

// Kind implements river.JobArgs.
func (RunArgs) Kind() string { return "ghsync_run" }

// InsertOpts implements river.JobArgsWithInsertOpts. Debounced runs are
// unique per PushDebounce window and scheduled at its end, so that every
// push of a window happened before the run starts; other runs are unique
// per minute.
func (a RunArgs) InsertOpts() river.InsertOpts {
	if a.Debounced {
		return river.InsertOpts{MaxAttempts: 5, ScheduledAt: time.Now().Add(PushDebounce),
			UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: PushDebounce}}
	}
	return river.InsertOpts{MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: time.Minute}}
}

// ImportArgs runs an import.
type ImportArgs struct {
	ImportID uuid.UUID `json:"import_id"`
}

// Kind implements river.JobArgs.
func (ImportArgs) Kind() string { return "ghsync_import" }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (ImportArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 10, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

// DueArgs schedules the links whose interval elapsed.
type DueArgs struct{}

// Kind implements river.JobArgs.
func (DueArgs) Kind() string { return "ghsync_due" }

// NightlyArgs schedules a full check of every backup and sync link.
type NightlyArgs struct{}

// Kind implements river.JobArgs.
func (NightlyArgs) Kind() string { return "ghsync_nightly" }

// JobsOption registers the workers and periodic jobs of the service.
func (s *Service) JobsOption() jobs.Option {
	return func(r *jobs.Registry) {
		river.AddWorker(r.Workers, &runWorker{s: s})
		river.AddWorker(r.Workers, &importWorker{s: s})
		river.AddWorker(r.Workers, &dueWorker{s: s})
		river.AddWorker(r.Workers, &nightlyWorker{s: s})
		r.Periodic = append(r.Periodic,
			river.NewPeriodicJob(river.PeriodicInterval(DueScanInterval),
				func() (river.JobArgs, *river.InsertOpts) { return DueArgs{}, nil },
				&river.PeriodicJobOpts{RunOnStart: true}),
			river.NewPeriodicJob(river.PeriodicInterval(NightlyInterval),
				func() (river.JobArgs, *river.InsertOpts) { return NightlyArgs{}, nil }, nil))
	}
}

// busy turns a busy repository into a snooze instead of a failed attempt.
func busy(err error) error {
	if errors.Is(err, gitstore.ErrBusy) {
		return river.JobSnooze(busySnooze)
	}
	return err
}

type runWorker struct {
	river.WorkerDefaults[RunArgs]
	s *Service
}

func (w *runWorker) Timeout(*river.Job[RunArgs]) time.Duration { return runTimeout }

func (w *runWorker) Work(ctx context.Context, job *river.Job[RunArgs]) error {
	return busy(w.s.runLink(ctx, job.Args.RepoID, job.Args.Force))
}

type importWorker struct {
	river.WorkerDefaults[ImportArgs]
	s *Service
}

func (w *importWorker) Timeout(*river.Job[ImportArgs]) time.Duration { return runTimeout }

func (w *importWorker) Work(ctx context.Context, job *river.Job[ImportArgs]) error {
	return busy(w.s.runImport(ctx, job.Args.ImportID))
}

type dueWorker struct {
	river.WorkerDefaults[DueArgs]
	s *Service
}

func (w *dueWorker) Work(ctx context.Context, _ *river.Job[DueArgs]) error {
	links, err := w.s.q.ListDueGitHubSyncLinks(ctx, dueBatch)
	if err != nil {
		return fmt.Errorf("list due sync links: %w", err)
	}
	for _, l := range links {
		if err := w.s.enqueue(ctx, RunArgs{RepoID: l.RepoID}); err != nil {
			return fmt.Errorf("enqueue github sync: %w", err)
		}
	}
	return nil
}

type nightlyWorker struct {
	river.WorkerDefaults[NightlyArgs]
	s *Service
}

func (w *nightlyWorker) Work(ctx context.Context, _ *river.Job[NightlyArgs]) error {
	for offset := int32(0); ; offset += nightlyPage {
		links, err := w.s.q.ListGitHubBackupLinks(ctx, dbgen.ListGitHubBackupLinksParams{Limit: nightlyPage, Offset: offset})
		if err != nil {
			return fmt.Errorf("list backup links: %w", err)
		}
		for _, l := range links {
			if err := w.s.enqueue(ctx, RunArgs{RepoID: l.RepoID, Force: true}); err != nil {
				return fmt.Errorf("enqueue github sync: %w", err)
			}
		}
		if len(links) < nightlyPage {
			return nil
		}
	}
}
