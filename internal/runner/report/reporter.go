// Package report streams the log and the state of a running job to the
// server.
//
// Ported from gitea.com/gitea/runner (internal/pkg/report), MIT License,
// Copyright 2022 The Gitea Authors.
package report

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"gitea.com/gitea/runner/act/runner"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1"
	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1/runnerv1connect"
)

// Output limits enforced by the server.
const (
	maxOutputKey   = 255
	maxOutputValue = 1 << 20
)

// Options are the batching knobs of a Reporter.
type Options struct {
	LogReportInterval   time.Duration
	LogReportMaxLatency time.Duration
	LogBatchSize        int
	StateReportInterval time.Duration
	CloseTimeout        time.Duration
}

// Reporter is a logrus hook that turns act log entries into log rows and
// step states, and uploads them in the background.
type Reporter struct {
	ctx    context.Context
	cancel context.CancelFunc
	logger *slog.Logger

	client  runnerv1connect.RunnerServiceClient
	clientM sync.Mutex

	closed      bool
	logOffset   int
	logRows     []*runnerv1.LogRow
	logReplacer *strings.Replacer
	oldnew      []string

	state        *runnerv1.TaskState
	stateChanged bool
	stateMu      sync.RWMutex
	outputs      sync.Map // key -> string (pending) or struct{} (sent)
	daemon       chan struct{}
	daemonOnce   sync.Once
	cancelled    atomic.Bool

	lastReportedAtNanos atomic.Int64

	opts        Options
	logNotify   chan struct{}
	stateNotify chan struct{}

	debugOutputEnabled  bool
	stopCommandEndToken string
}

// New builds a Reporter for task. cancel aborts the job: it is called
// when the server reports the job as cancelled. secrets are masked in every
// row.
func New(ctx context.Context, cancel context.CancelFunc, client runnerv1connect.RunnerServiceClient, task *runnerv1.Task, secrets []string, opts Options, logger *slog.Logger) *Reporter {
	var oldnew []string
	for _, v := range secrets {
		if v != "" {
			oldnew = runner.AppendSecretMasker(oldnew, v)
		}
	}
	if opts.CloseTimeout <= 0 {
		opts.CloseTimeout = 10 * time.Second
	}
	if opts.LogBatchSize <= 0 {
		opts.LogBatchSize = 100
	}
	r := &Reporter{
		ctx:         ctx,
		cancel:      cancel,
		logger:      logger,
		client:      client,
		oldnew:      oldnew,
		logReplacer: strings.NewReplacer(oldnew...),
		opts:        opts,
		logNotify:   make(chan struct{}, 1),
		stateNotify: make(chan struct{}, 1),
		state:       &runnerv1.TaskState{Id: task.GetId()},
		daemon:      make(chan struct{}),
	}
	if task.GetSecrets()["ACTIONS_STEP_DEBUG"] == "true" || task.GetVars()["ACTIONS_STEP_DEBUG"] == "true" {
		r.debugOutputEnabled = true
	}
	return r
}

// Result returns the final job result. Safe to call after Close returns.
func (r *Reporter) Result() runnerv1.Result {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.state.GetResult()
}

// Cancelled reports whether the server asked to cancel the job.
func (r *Reporter) Cancelled() bool { return r.cancelled.Load() }

// SetSteps declares the steps of the job, in order.
func (r *Reporter) SetSteps(names []string) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.state.Steps = make([]*runnerv1.StepState, len(names))
	for i, n := range names {
		r.state.Steps[i] = &runnerv1.StepState{Index: int32(i), Name: n} //nolint:gosec // step count is small
	}
}

// Levels implements logrus.Hook.
func (r *Reporter) Levels() []log.Level { return log.AllLevels }

func appendIfNotNil[T any](s []*T, v *T) []*T {
	if v != nil {
		return append(s, v)
	}
	return s
}

// isJobStepEntry reports whether the entry belongs to a job step and not to
// a step of a composite action, whose results must not be reported as step
// results.
func isJobStepEntry(entry *log.Entry) bool {
	if v, ok := entry.Data["stepID"]; ok {
		if v, ok := v.([]string); ok && len(v) > 1 {
			return false
		}
	}
	return true
}

func (r *Reporter) notifyLog() {
	select {
	case r.logNotify <- struct{}{}:
	default:
	}
}

func (r *Reporter) notifyState() {
	select {
	case r.stateNotify <- struct{}{}:
	default:
	}
}

// unlockAndNotify releases stateMu (held by the caller) and wakes the
// daemon.
func (r *Reporter) unlockAndNotify(urgent bool) {
	r.stateMu.Unlock()
	r.notifyLog()
	if urgent {
		r.notifyState()
	}
}

// Fire implements logrus.Hook.
func (r *Reporter) Fire(entry *log.Entry) error {
	urgent := false
	r.stateMu.Lock()
	r.stateChanged = true

	ts := entry.Time
	if r.state.StartedAt == nil {
		r.state.StartedAt = timestamppb.New(ts)
	}

	if entry.Data["stage"] != "Main" {
		if v, ok := entry.Data["jobResult"]; ok {
			if res, ok := parseResult(v); ok {
				r.state.Result = res
				r.state.StoppedAt = timestamppb.New(ts)
				for _, s := range r.state.Steps {
					if s.Result == runnerv1.Result_RESULT_UNSPECIFIED {
						s.Result = runnerv1.Result_RESULT_CANCELLED
						if res == runnerv1.Result_RESULT_SKIPPED {
							s.Result = runnerv1.Result_RESULT_SKIPPED
						}
					}
				}
				urgent = true
			}
		}
		if r.shouldAppendLogRow(entry) {
			r.logRows = appendIfNotNil(r.logRows, r.parseLogRow(entry))
		}
		r.unlockAndNotify(urgent)
		return nil
	}

	var step *runnerv1.StepState
	if v, ok := entry.Data["stepNumber"]; ok {
		if v, ok := v.(int); ok && v >= 0 && len(r.state.Steps) > v {
			step = r.state.Steps[v]
		}
	}
	if step == nil {
		if r.shouldAppendLogRow(entry) {
			r.logRows = appendIfNotNil(r.logRows, r.parseLogRow(entry))
		}
		r.unlockAndNotify(false)
		return nil
	}

	if step.StartedAt == nil {
		step.StartedAt = timestamppb.New(ts)
		urgent = true
	}
	// Errors always reach the job log, never silently dropped.
	raw := entry.Level == log.ErrorLevel
	if v, ok := entry.Data["raw_output"].(bool); ok && v {
		raw = true
	}
	if raw {
		if row := r.parseLogRow(entry); row != nil {
			if step.LogLength == 0 {
				step.LogIndex = int64(r.logOffset + len(r.logRows))
			}
			step.LogLength++
			r.logRows = append(r.logRows, row)
		}
	} else if r.shouldAppendLogRow(entry) {
		r.logRows = appendIfNotNil(r.logRows, r.parseLogRow(entry))
	}
	if v, ok := entry.Data["stepResult"]; ok && isJobStepEntry(entry) {
		if res, ok := parseResult(v); ok {
			if step.LogLength == 0 {
				step.LogIndex = int64(r.logOffset + len(r.logRows))
			}
			step.Result = res
			step.StoppedAt = timestamppb.New(ts)
			urgent = true
		}
	}
	r.unlockAndNotify(urgent)
	return nil
}

// RunDaemon starts the background uploader.
func (r *Reporter) RunDaemon() { go r.runDaemonLoop() }

func (r *Reporter) closeDaemon() { r.daemonOnce.Do(func() { close(r.daemon) }) }

func stopTimer(active *bool, t *time.Timer) {
	if *active {
		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
		*active = false
	}
}

func (r *Reporter) runDaemonLoop() {
	defer r.closeDaemon()
	logTicker := time.NewTicker(r.opts.LogReportInterval)
	stateTicker := time.NewTicker(r.opts.StateReportInterval)
	// Armed when the first row lands in an empty buffer.
	latency := time.NewTimer(time.Hour)
	latency.Stop()
	latencyActive := false
	defer logTicker.Stop()
	defer stateTicker.Stop()
	defer latency.Stop()

	for {
		select {
		case <-logTicker.C:
			r.logErr("report log", r.ReportLog(false))
			stopTimer(&latencyActive, latency)
		case <-stateTicker.C:
			r.logErr("report state", r.ReportState(false))
		case <-r.logNotify:
			r.stateMu.RLock()
			n := len(r.logRows)
			r.stateMu.RUnlock()
			if n >= r.opts.LogBatchSize {
				r.logErr("report log", r.ReportLog(false))
				stopTimer(&latencyActive, latency)
			} else if !latencyActive && n > 0 {
				latency.Reset(r.opts.LogReportMaxLatency)
				latencyActive = true
			}
		case <-r.stateNotify:
			r.logErr("report log", r.ReportLog(false))
			r.logErr("report state", r.ReportState(false))
			stopTimer(&latencyActive, latency)
		case <-latency.C:
			latencyActive = false
			r.logErr("report log", r.ReportLog(false))
		case <-r.ctx.Done():
			// Close delivers the final flush on a detached context.
			return
		}
		r.stateMu.RLock()
		closed := r.closed
		r.stateMu.RUnlock()
		if closed {
			return
		}
	}
}

func (r *Reporter) logErr(what string, err error) {
	if err != nil {
		r.logger.Warn("job report failed", "op", what, "task_id", r.state.GetId(), "err", err)
	}
}

// Logf appends a runner message to the job log (outside of steps only).
func (r *Reporter) Logf(format string, a ...any) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.logf(format, a...)
}

func (r *Reporter) logf(format string, a ...any) {
	if !r.duringSteps() {
		r.logRows = append(r.logRows, &runnerv1.LogRow{
			Time:    timestamppb.Now(),
			Content: r.logReplacer.Replace(fmt.Sprintf(format, a...)),
		})
	}
}

// SetOutputs records the job outputs; the first value of each key wins.
func (r *Reporter) SetOutputs(outputs map[string]string) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	for k, v := range outputs {
		if len(k) > maxOutputKey {
			r.logf("ignore output because the key is too long: %q", k)
			continue
		}
		if l := len(v); l > maxOutputValue {
			r.logf("ignore output because the value %q is too long: %d", k, l)
			continue
		}
		if _, ok := r.outputs.Load(k); ok {
			continue
		}
		r.outputs.Store(k, v)
	}
}

// Close finalises the job. If no result was recorded the job fails (or is
// cancelled, when the server asked for it) and lastWords is appended to the
// log. It then flushes the remaining rows with no_more and the final state.
func (r *Reporter) Close(lastWords string) error {
	r.stateMu.Lock()
	r.closed = true
	if r.state.Result == runnerv1.Result_RESULT_UNSPECIFIED {
		if lastWords == "" {
			lastWords = "Early termination"
		}
		for _, s := range r.state.Steps {
			if s.Result == runnerv1.Result_RESULT_UNSPECIFIED {
				s.Result = runnerv1.Result_RESULT_CANCELLED
			}
		}
		r.state.Result = runnerv1.Result_RESULT_FAILURE
		if r.cancelled.Load() || r.ctx.Err() != nil {
			r.state.Result = runnerv1.Result_RESULT_CANCELLED
		}
		r.state.StoppedAt = timestamppb.Now()
	}
	if lastWords != "" {
		r.logRows = append(r.logRows, &runnerv1.LogRow{
			Time:    timestamppb.Now(),
			Content: r.logReplacer.Replace(lastWords),
		})
	}
	r.stateMu.Unlock()

	r.notifyLog()
	select {
	case <-r.daemon:
	case <-time.After(60 * time.Second):
		r.logger.Error("job reporter did not stop within 60s, flushing anyway", "task_id", r.state.GetId())
	}

	// Separate budgets so a slow log upload cannot starve the final state.
	return errors.Join(
		r.flushFinal(func() error { return r.ReportLog(true) }),
		r.flushFinal(func() error { return r.ReportState(true) }),
	)
}

// flushFinal retries fn with backoff on a detached, bounded context, so a
// cancelled job still delivers its final rows and result.
func (r *Reporter) flushFinal(fn func() error) error {
	deadline := time.Now().Add(3 * r.opts.CloseTimeout)
	wait := 200 * time.Millisecond
	for {
		err := fn()
		if err == nil {
			return nil
		}
		if time.Now().Add(wait).After(deadline) {
			return err
		}
		time.Sleep(wait)
		wait = min(wait*2, 5*time.Second)
	}
}

// rpcCtx returns r.ctx while the job runs and a fresh bounded context once
// it has been cancelled, so the final flush still lands.
func (r *Reporter) rpcCtx() (context.Context, context.CancelFunc) {
	if r.ctx.Err() != nil || r.isClosed() {
		return context.WithTimeout(context.Background(), r.opts.CloseTimeout)
	}
	return context.WithTimeout(r.ctx, r.opts.CloseTimeout)
}

func (r *Reporter) isClosed() bool {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.closed
}

// ReportLog uploads the buffered rows; noMore marks the log as complete.
func (r *Reporter) ReportLog(noMore bool) error {
	r.clientM.Lock()
	defer r.clientM.Unlock()

	r.stateMu.RLock()
	rows := r.logRows
	offset := r.logOffset
	r.stateMu.RUnlock()
	if !noMore && len(rows) == 0 {
		return nil
	}

	ctx, cancel := r.rpcCtx()
	defer cancel()
	resp, err := r.client.UpdateLog(ctx, connect.NewRequest(&runnerv1.UpdateLogRequest{
		TaskId: r.state.GetId(),
		Index:  int64(offset),
		Rows:   rows,
		NoMore: noMore,
	}))
	if err != nil {
		return fmt.Errorf("update log: %w", err)
	}
	ack := int(resp.Msg.GetAckIndex())
	if ack < offset {
		return errors.New("update log: submitted rows were lost by the server")
	}
	submitted := offset + len(rows)
	if ack > submitted {
		ack = submitted
	}

	r.stateMu.Lock()
	r.logRows = r.logRows[ack-offset:]
	r.logOffset = ack
	r.stateMu.Unlock()

	if noMore && ack < submitted {
		return errors.New("update log: not all rows were acknowledged")
	}
	return nil
}

// ReportState uploads the task state; the result is sent only when
// reportResult is true (the daemon never sends it).
func (r *Reporter) ReportState(reportResult bool) error {
	r.clientM.Lock()
	defer r.clientM.Unlock()

	outputs := map[string]string{}
	r.outputs.Range(func(k, v any) bool {
		if val, ok := v.(string); ok {
			outputs[k.(string)] = val
		}
		return true
	})

	// Heartbeat at state_report_interval even without changes, so the
	// server does not consider a silent job orphaned.
	last := r.lastReportedAtNanos.Load()
	recent := last != 0 && time.Since(time.Unix(0, last)) < r.opts.StateReportInterval
	r.stateMu.Lock()
	if !reportResult && !r.stateChanged && len(outputs) == 0 && recent {
		r.stateMu.Unlock()
		return nil
	}
	state := proto.Clone(r.state).(*runnerv1.TaskState)
	r.stateChanged = false
	r.stateMu.Unlock()

	if !reportResult {
		state.Result = runnerv1.Result_RESULT_UNSPECIFIED
		state.StoppedAt = nil
	}

	ctx, cancel := r.rpcCtx()
	defer cancel()
	resp, err := r.client.UpdateTask(ctx, connect.NewRequest(&runnerv1.UpdateTaskRequest{
		State:   state,
		Outputs: outputs,
	}))
	if err != nil {
		r.stateMu.Lock()
		r.stateChanged = true
		r.stateMu.Unlock()
		return fmt.Errorf("update task: %w", err)
	}
	r.lastReportedAtNanos.Store(time.Now().UnixNano())

	for _, k := range resp.Msg.GetSentOutputs() {
		r.outputs.Store(k, struct{}{})
	}
	if resp.Msg.GetCancelled() && !r.cancelled.Swap(true) {
		r.logger.Info("job cancelled by the server", "task_id", r.state.GetId())
		r.cancel()
	}

	var pending []string
	r.outputs.Range(func(k, v any) bool {
		if _, ok := v.(string); ok {
			pending = append(pending, k.(string))
		}
		return true
	})
	if len(pending) > 0 {
		return fmt.Errorf("update task: outputs not stored yet: %v", pending)
	}
	return nil
}

// duringSteps reports whether a step is running; runner messages are then
// kept out of the log so step ranges stay contiguous.
func (r *Reporter) duringSteps() bool {
	steps := r.state.Steps
	if len(steps) == 0 {
		return false
	}
	if first := steps[0]; first.Result == runnerv1.Result_RESULT_UNSPECIFIED && first.LogLength == 0 {
		return false
	}
	if last := steps[len(steps)-1]; last.Result != runnerv1.Result_RESULT_UNSPECIFIED {
		return false
	}
	return true
}

// shouldAppendLogRow reports whether a non-raw entry goes to the job log:
// only between steps and within the configured log level.
func (r *Reporter) shouldAppendLogRow(entry *log.Entry) bool {
	return !r.duringSteps() && entry.Level <= log.GetLevel()
}

var stringToResult = map[string]runnerv1.Result{
	"success":   runnerv1.Result_RESULT_SUCCESS,
	"failure":   runnerv1.Result_RESULT_FAILURE,
	"skipped":   runnerv1.Result_RESULT_SKIPPED,
	"cancelled": runnerv1.Result_RESULT_CANCELLED,
}

func parseResult(v any) (runnerv1.Result, bool) {
	var s string
	switch t := v.(type) {
	case string: // jobResult
		s = t
	case fmt.Stringer: // stepResult
		s = t.String()
	}
	res, ok := stringToResult[s]
	return res, ok
}

var cmdRegex = regexp.MustCompile(`^::([^ :]+)( .*)?::(.*)$`)

// handleCommand processes a workflow command; nil drops the row.
func (r *Reporter) handleCommand(original, command, value string) *string {
	if r.stopCommandEndToken != "" && command != r.stopCommandEndToken {
		return &original
	}
	switch command {
	case "add-mask":
		r.addMask(value)
		return nil
	case "debug":
		if r.debugOutputEnabled {
			return &value
		}
		return nil
	case "stop-commands":
		r.stopCommandEndToken = value
		return nil
	case r.stopCommandEndToken:
		r.stopCommandEndToken = ""
		return nil
	}
	// notice, warning, error, group, endgroup and unknown commands are
	// kept verbatim for the log viewer.
	return &original
}

func (r *Reporter) parseLogRow(entry *log.Entry) *runnerv1.LogRow {
	content := strings.TrimRight(entry.Message, "\r\n")
	if m := cmdRegex.FindStringSubmatch(content); m != nil {
		out := r.handleCommand(content, m[1], runner.UnescapeCommandData(m[3]))
		if out == nil {
			return nil
		}
		content = *out
	}
	content = r.logReplacer.Replace(content)
	return &runnerv1.LogRow{
		Time:    timestamppb.New(entry.Time),
		Content: strings.ToValidUTF8(content, "?"),
	}
}

func (r *Reporter) addMask(secret string) {
	if secret == "" {
		return
	}
	r.oldnew = runner.AppendSecretMasker(r.oldnew, secret)
	r.logReplacer = strings.NewReplacer(r.oldnew...)
}
