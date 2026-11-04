package runner

// The polling loop follows gitea.com/gitea/runner (internal/app/poll),
// MIT License, Copyright 2023 The Gitea Authors.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/sirupsen/logrus"

	"github.com/Ervisio/ErvisioRepo/internal/runner/config"
	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1"
	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1/runnerv1connect"
)

// ErrUnauthorized means the server refused the runner credential.
var ErrUnauthorized = errors.New("the server refused the runner credential (deleted or disabled runner?), register it again")

// Daemon polls the server for tasks and runs them.
type Daemon struct {
	cfg     *config.Config
	state   *State
	labels  config.Labels
	client  runnerv1connect.RunnerServiceClient
	exec    *executor
	logger  *slog.Logger
	version string

	mu      sync.Mutex
	running map[string]context.CancelFunc
	wg      sync.WaitGroup
}

// NewDaemon prepares a daemon from the configuration and the state saved
// by "register".
func NewDaemon(cfg *config.Config, st *State, version string, logger *slog.Logger) (*Daemon, error) {
	list := st.Labels
	if cfg.LabelsSet || len(list) == 0 {
		list = cfg.Runner.Labels
	}
	labels, err := cfg.CheckLabels(list)
	if err != nil {
		return nil, fmt.Errorf("labels: %w", err)
	}
	server := cfg.Server.URL
	if server == "" {
		server = st.URL
	}
	if err := config.CheckURL(server); err != nil {
		return nil, fmt.Errorf("server url: %w", err)
	}
	client := NewClient(server, st.Credential, version, cfg.Server.InsecureTLS)
	configureLogrus(cfg.Log.Level)
	return &Daemon{
		cfg:     cfg,
		state:   st,
		labels:  labels,
		client:  client,
		logger:  logger,
		version: version,
		running: map[string]context.CancelFunc{},
		exec: &executor{
			cfg:     cfg,
			labels:  labels,
			client:  client,
			name:    st.Name,
			version: version,
			server:  server,
			logger:  logger,
		},
	}, nil
}

// configureLogrus sets the global logrus logger used by act: its level
// also decides which act messages reach the job log.
func configureLogrus(level string) {
	logrus.SetFormatter(&logrus.TextFormatter{DisableColors: true, FullTimestamp: true})
	switch level {
	case "debug":
		logrus.SetLevel(logrus.DebugLevel)
	case "warn":
		logrus.SetLevel(logrus.WarnLevel)
	case "error":
		logrus.SetLevel(logrus.ErrorLevel)
	default:
		logrus.SetLevel(logrus.InfoLevel)
	}
}

// Run declares the runner, then polls until ctx is cancelled (or, for an
// ephemeral runner, until its single job ends). On cancellation running
// jobs get shutdown_timeout to finish before they are cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	if err := d.declare(ctx); err != nil {
		return err
	}
	d.logger.Info("runner started", "name", d.state.Name, "labels", d.labels.Names(),
		"capacity", d.cfg.Runner.Capacity, "ephemeral", d.state.Ephemeral)

	// Jobs outlive ctx: they are cancelled only after the shutdown grace.
	jobsCtx, cancelJobs := context.WithCancel(context.Background())
	defer cancelJobs()

	hbCtx, stopHeartbeat := context.WithCancel(context.Background())
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		d.heartbeatLoop(hbCtx)
	}()

	pollErr := d.pollLoop(ctx, jobsCtx)

	d.shutdown(cancelJobs)
	stopHeartbeat()
	<-hbDone
	if pollErr != nil {
		return pollErr
	}
	d.logger.Info("runner stopped")
	return nil
}

func (d *Daemon) declare(ctx context.Context) error {
	wait := d.cfg.Runner.FetchInterval.D()
	for {
		resp, err := d.client.Declare(ctx, connect.NewRequest(&runnerv1.DeclareRequest{
			Version: d.version,
			Labels:  d.labels.Wire(),
		}))
		if err == nil {
			if r := resp.Msg.GetRunner(); r != nil {
				d.logger.Info("runner declared", "id", r.GetId(), "scope", r.GetScope())
			}
			return nil
		}
		if isAuthError(err) {
			return ErrUnauthorized
		}
		if ctx.Err() != nil {
			return fmt.Errorf("declare: %w", ctx.Err())
		}
		d.logger.Warn("declare failed, retrying", "err", err, "retry_in", wait.String())
		select {
		case <-ctx.Done():
			return fmt.Errorf("declare: %w", ctx.Err())
		case <-time.After(wait):
		}
		wait = min(wait*2, d.cfg.Runner.FetchIntervalMax.D())
	}
}

// pollLoop fetches tasks while a slot is free. It returns nil when ctx is
// cancelled or the ephemeral job has finished.
func (d *Daemon) pollLoop(ctx, jobsCtx context.Context) error {
	slots := make(chan struct{}, d.cfg.Runner.Capacity)
	var tasksVersion int64
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case slots <- struct{}{}:
		}

		task, version, err := d.fetch(ctx, tasksVersion)
		switch {
		case err != nil && ctx.Err() != nil:
			<-slots
			return nil
		case err != nil:
			<-slots
			if isAuthError(err) {
				return ErrUnauthorized
			}
			failures++
			wait := backoff(d.cfg.Runner.FetchInterval.D(), d.cfg.Runner.FetchIntervalMax.D(), failures)
			d.logger.Warn("fetch task failed", "err", err, "retry_in", wait.String())
			if !sleep(ctx, wait) {
				return nil
			}
			continue
		}
		failures = 0
		if version > tasksVersion {
			tasksVersion = version
		}
		if task == nil {
			<-slots
			if !sleep(ctx, d.cfg.Runner.FetchInterval.D()) {
				return nil
			}
			continue
		}

		done := d.start(jobsCtx, task, func() { <-slots })
		if d.state.Ephemeral {
			d.logger.Info("ephemeral runner: no more tasks will be fetched", "task_id", task.GetId())
			select {
			case <-done:
			case <-ctx.Done():
			}
			return nil
		}
	}
}

// fetch long-polls for one task; a timed-out poll is an empty answer.
func (d *Daemon) fetch(ctx context.Context, version int64) (*runnerv1.Task, int64, error) {
	fctx, cancel := context.WithTimeout(ctx, d.cfg.Runner.FetchTimeout.D())
	defer cancel()
	free := d.cfg.Runner.Capacity - d.runningCount()
	resp, err := d.client.FetchTask(fctx, connect.NewRequest(&runnerv1.FetchTaskRequest{
		TasksVersion: version,
		Capacity:     int32(max(free, 1)), //nolint:gosec // capacity is at most 64
	}))
	if err != nil {
		if ctx.Err() == nil && (errors.Is(err, context.DeadlineExceeded) || connect.CodeOf(err) == connect.CodeDeadlineExceeded) {
			return nil, version, nil
		}
		return nil, version, fmt.Errorf("fetch task: %w", err)
	}
	return resp.Msg.GetTask(), resp.Msg.GetTasksVersion(), nil
}

// start runs task in the background and calls release when it ends. The
// returned channel is closed at the end of the job.
func (d *Daemon) start(jobsCtx context.Context, task *runnerv1.Task, release func()) <-chan struct{} {
	id := task.GetId()
	jctx, cancel := context.WithCancel(jobsCtx)
	d.mu.Lock()
	if _, dup := d.running[id]; dup {
		d.mu.Unlock()
		cancel()
		release()
		d.logger.Warn("task already running, ignored", "task_id", id)
		done := make(chan struct{})
		close(done)
		return done
	}
	d.running[id] = cancel
	d.mu.Unlock()

	d.logger.Info("job started", "task_id", id, "label", task.GetLabel())
	done := make(chan struct{})
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer close(done)
		defer release()
		defer func() {
			d.mu.Lock()
			delete(d.running, id)
			d.mu.Unlock()
			cancel()
		}()
		d.exec.run(jctx, task)
	}()
	return done
}

func (d *Daemon) runningCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.running)
}

func (d *Daemon) runningIDs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	ids := make([]string, 0, len(d.running))
	for id := range d.running {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// cancelTask cancels a running job; its reporter then sends the cancelled
// result.
func (d *Daemon) cancelTask(id string) {
	d.mu.Lock()
	cancel, ok := d.running[id]
	d.mu.Unlock()
	if ok {
		d.logger.Info("job cancelled by the server", "task_id", id)
		cancel()
	}
}

// heartbeatLoop reports the running jobs and applies the cancellations the
// server returns, until ctx is cancelled.
func (d *Daemon) heartbeatLoop(ctx context.Context) {
	t := time.NewTicker(d.cfg.Runner.HeartbeatInterval.D())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		hctx, cancel := context.WithTimeout(ctx, d.cfg.Runner.FetchTimeout.D())
		resp, err := d.client.Heartbeat(hctx, connect.NewRequest(&runnerv1.HeartbeatRequest{
			RunningTaskIds: d.runningIDs(),
		}))
		cancel()
		if err != nil {
			if ctx.Err() == nil {
				d.logger.Warn("heartbeat failed", "err", err)
			}
			continue
		}
		for _, id := range resp.Msg.GetCancelTaskIds() {
			d.cancelTask(id)
		}
	}
}

// shutdown waits shutdown_timeout for the running jobs, then cancels them
// and waits for their final reports.
func (d *Daemon) shutdown(cancelJobs context.CancelFunc) {
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	if n := d.runningCount(); n > 0 {
		d.logger.Info("waiting for running jobs", "count", n, "timeout", d.cfg.Runner.ShutdownTimeout.D().String())
	}
	select {
	case <-done:
		return
	case <-time.After(d.cfg.Runner.ShutdownTimeout.D()):
	}
	d.logger.Warn("shutdown timeout reached, cancelling running jobs", "task_ids", d.runningIDs())
	cancelJobs()
	// Cancelled jobs still flush their logs and result (bounded by
	// report_close_timeout); give them a last fixed budget.
	select {
	case <-done:
	case <-time.After(2*time.Minute + 6*d.cfg.Runner.ReportCloseTimeout.D()):
		d.logger.Error("jobs did not stop after cancellation", "task_ids", d.runningIDs())
	}
}

// backoff returns base*2^(n-1) capped at maxWait, with ±20% jitter.
func backoff(base, maxWait time.Duration, n int) time.Duration {
	shift := min(n-1, 5)
	w := min(base<<shift, maxWait)
	jitter := time.Duration(rand.Int64N(int64(w)/5+1)) - w/10 //nolint:gosec // jitter does not need a CSPRNG
	return max(w+jitter, base)
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
