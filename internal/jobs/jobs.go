// Package jobs runs the background workers on the River queue.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
)

// HeartbeatInterval is how often the heartbeat job runs.
const HeartbeatInterval = 5 * time.Minute

// SettingHeartbeatAt is the settings key the heartbeat job updates.
const SettingHeartbeatAt = "jobs.heartbeat_at"

// Runner owns the River client.
type Runner struct {
	client  *river.Client[pgx.Tx]
	running atomic.Bool
}

// New configures the River client and registers every worker.
// The River schema must already be migrated (see db.Migrate).
func New(pool *pgxpool.Pool, log *slog.Logger) (*Runner, error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, &HeartbeatWorker{Settings: dbgen.New(pool)})

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Logger:  log,
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 10}},
		Workers: workers,
		PeriodicJobs: []*river.PeriodicJob{
			river.NewPeriodicJob(
				river.PeriodicInterval(HeartbeatInterval),
				func() (river.JobArgs, *river.InsertOpts) { return HeartbeatArgs{}, nil },
				&river.PeriodicJobOpts{RunOnStart: true},
			),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("river client: %w", err)
	}
	return &Runner{client: client}, nil
}

// Start starts fetching and working jobs. It returns once River is running.
func (r *Runner) Start(ctx context.Context) error {
	if err := r.client.Start(ctx); err != nil {
		return fmt.Errorf("start river: %w", err)
	}
	r.running.Store(true)
	return nil
}

// Stop waits for running jobs to finish, or cancels them once ctx is done.
func (r *Runner) Stop(ctx context.Context) error {
	r.running.Store(false)
	if err := r.client.Stop(ctx); err != nil {
		if ctx.Err() != nil {
			return r.client.StopAndCancel(context.WithoutCancel(ctx))
		}
		return fmt.Errorf("stop river: %w", err)
	}
	return nil
}

// Running reports whether the client has started and has not been stopped.
func (r *Runner) Running() bool { return r.running.Load() }

// Client exposes the River client to insert jobs.
func (r *Runner) Client() *river.Client[pgx.Tx] { return r.client }

// HeartbeatArgs are the (empty) arguments of the heartbeat job.
type HeartbeatArgs struct{}

func (HeartbeatArgs) Kind() string { return "heartbeat" }

// SettingsWriter is the subset of dbgen.Querier the heartbeat needs.
type SettingsWriter interface {
	UpsertSetting(ctx context.Context, arg dbgen.UpsertSettingParams) error
}

// HeartbeatWorker records the time of its last run, which proves that the
// queue is being worked.
type HeartbeatWorker struct {
	river.WorkerDefaults[HeartbeatArgs]
	Settings SettingsWriter
	Now      func() time.Time // defaults to time.Now
}

func (w *HeartbeatWorker) Work(ctx context.Context, _ *river.Job[HeartbeatArgs]) error {
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	value, err := json.Marshal(now().UTC())
	if err != nil {
		return fmt.Errorf("encode heartbeat: %w", err)
	}
	if err := w.Settings.UpsertSetting(ctx, dbgen.UpsertSettingParams{Key: SettingHeartbeatAt, Value: value}); err != nil {
		return fmt.Errorf("store heartbeat: %w", err)
	}
	return nil
}
