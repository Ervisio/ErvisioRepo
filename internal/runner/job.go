package runner

// Job execution follows gitea.com/gitea/runner (internal/app/run/runner.go),
// MIT License, Copyright 2022 The Gitea Authors.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gitea.com/gitea/runner/act/common"
	"gitea.com/gitea/runner/act/model"
	actrunner "gitea.com/gitea/runner/act/runner"
	"github.com/moby/moby/api/types/container"
	"github.com/sirupsen/logrus"

	"github.com/Ervisio/ErvisioRepo/internal/runner/config"
	"github.com/Ervisio/ErvisioRepo/internal/runner/report"
	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1"
	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1/runnerv1connect"
)

// executor runs tasks with act.
type executor struct {
	cfg     *config.Config
	labels  config.Labels
	client  runnerv1connect.RunnerServiceClient
	name    string
	version string
	server  string // server URL, used when the task context has none
	logger  *slog.Logger
}

// taskIDRe guards the task id before it becomes part of paths and
// container names.
var taskIDRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// repoRe guards owner/name before it becomes part of the workspace path.
var repoRe = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

// run executes task and always reports a final result. ctx cancellation
// (server cancel, forced shutdown) cancels the job.
func (e *executor) run(ctx context.Context, task *runnerv1.Task) {
	timeout := e.cfg.Runner.JobTimeout.D()
	if m := task.GetTimeoutMinutes(); m > 0 {
		timeout = time.Duration(m) * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	masks := []string{task.GetToken(), task.GetRuntimeToken(), task.GetContext().GetFields()["token"].GetStringValue()}
	for _, v := range task.GetSecrets() {
		masks = append(masks, v)
	}
	rep := report.New(ctx, cancel, e.client, task, masks, report.Options{
		LogReportInterval:   e.cfg.Runner.LogReportInterval.D(),
		LogReportMaxLatency: e.cfg.Runner.LogReportMaxLatency.D(),
		LogBatchSize:        e.cfg.Runner.LogReportBatchSize,
		StateReportInterval: e.cfg.Runner.StateReportInterval.D(),
		CloseTimeout:        e.cfg.Runner.ReportCloseTimeout.D(),
	}, e.logger)
	rep.RunDaemon()

	start := time.Now()
	err := e.execute(ctx, task, rep)
	lastWords := ""
	if err != nil {
		lastWords = err.Error()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			lastWords = fmt.Sprintf("the job exceeded its timeout of %s", timeout)
		}
	}
	if cerr := rep.Close(lastWords); cerr != nil {
		e.logger.Error("final job report failed", "task_id", task.GetId(), "err", cerr)
	}
	e.logger.Info("job finished", "task_id", task.GetId(), "result", rep.Result().String(), "duration", time.Since(start).Round(time.Millisecond).String())
}

func (e *executor) execute(ctx context.Context, task *runnerv1.Task, rep *report.Reporter) (err error) {
	defer func() {
		if p := recover(); p != nil {
			e.logger.Error("job panicked", "task_id", task.GetId(), "panic", fmt.Sprint(p))
			err = fmt.Errorf("runner panic: %v", p)
		}
	}()
	if !taskIDRe.MatchString(task.GetId()) {
		return fmt.Errorf("invalid task id %q", task.GetId())
	}
	tc := task.GetContext().GetFields()
	rep.Logf("%s (ervisio-runner %s) received job %s of %s, triggered by %s",
		e.name, e.version, tc["job"].GetStringValue(), tc["repository"].GetStringValue(), tc["event_name"].GetStringValue())

	wf, jobID, err := generateWorkflow(task)
	if err != nil {
		return err
	}
	plan, err := model.CombineWorkflowPlanner(wf).PlanJob(jobID)
	if err != nil {
		return fmt.Errorf("plan job: %w", err)
	}
	job := wf.GetJob(jobID)
	names := make([]string, len(job.Steps))
	for i, s := range job.Steps {
		names[i] = s.String()
	}
	rep.SetSteps(names)

	platform := e.labels.PickPlatform(task.GetLabel(), job.RunsOn())
	if platform == "" {
		return fmt.Errorf("no backend for runs-on %v", job.RunsOn())
	}
	hostMode := platform == "-self-hosted"
	if hostMode && !e.cfg.Host.Enabled {
		return fmt.Errorf("label %q needs the host backend, which is disabled", task.GetLabel())
	}

	repo := tc["repository"].GetStringValue()
	if !repoRe.MatchString(repo) || strings.Contains(repo, "..") {
		return fmt.Errorf("invalid repository %q in the task context", repo)
	}
	preset := &model.GithubContext{
		Event:           tc["event"].GetStructValue().AsMap(),
		RunID:           tc["run_id"].GetStringValue(),
		RunNumber:       tc["run_number"].GetStringValue(),
		RunAttempt:      tc["run_attempt"].GetStringValue(),
		Actor:           tc["actor"].GetStringValue(),
		Repository:      repo,
		EventName:       tc["event_name"].GetStringValue(),
		Sha:             tc["sha"].GetStringValue(),
		Ref:             tc["ref"].GetStringValue(),
		RefName:         tc["ref_name"].GetStringValue(),
		RefType:         tc["ref_type"].GetStringValue(),
		HeadRef:         tc["head_ref"].GetStringValue(),
		BaseRef:         tc["base_ref"].GetStringValue(),
		Token:           task.GetToken(),
		RepositoryOwner: tc["repository_owner"].GetStringValue(),
		RetentionDays:   tc["retention_days"].GetStringValue(),
	}
	if preset.RunAttempt == "" && task.GetAttempt() > 0 {
		preset.RunAttempt = fmt.Sprint(task.GetAttempt())
	}
	if preset.Event == nil {
		preset.Event = map[string]any{}
	}
	eventJSON, err := json.Marshal(preset.Event)
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}

	secrets := maps.Clone(task.GetSecrets())
	if secrets == nil {
		secrets = map[string]string{}
	}
	for _, k := range []string{"GITHUB_TOKEN", "ERVISIO_TOKEN"} {
		if secrets[k] == "" && task.GetToken() != "" {
			secrets[k] = task.GetToken()
		}
	}
	if t := task.GetRuntimeToken(); t != "" {
		secrets["ACTIONS_RUNTIME_TOKEN"] = t
	}

	env := maps.Clone(e.cfg.Runner.Env)
	if env == nil {
		env = map[string]string{}
	}
	env["ERVISIO_ACTIONS"] = "true"
	env["ERVISIO_ACTIONS_RUNNER_VERSION"] = e.version
	env["ACTIONS_CACHE_SERVICE_V2"] = "True"
	if v := task.GetRuntimeUrl(); v != "" {
		env["ACTIONS_RUNTIME_URL"] = withSlash(v)
	}
	if v := task.GetResultsUrl(); v != "" {
		env["ACTIONS_RESULTS_URL"] = v
	}
	if v := task.GetCacheUrl(); v != "" {
		env["ACTIONS_CACHE_URL"] = withSlash(v)
	}
	if v := task.GetRuntimeToken(); v != "" {
		env["ACTIONS_RUNTIME_TOKEN"] = v
	}

	// act derives github.server_url (and, for now, api_url) from this.
	instance := tc["server_url"].GetStringValue()
	if instance == "" {
		instance = e.server
	}
	instance = strings.TrimSuffix(instance, "/")

	maxLifetime := e.cfg.Runner.JobTimeout.D()
	if dl, ok := ctx.Deadline(); ok {
		maxLifetime = time.Until(dl)
	}

	var workdir string
	if hostMode {
		workdir = filepath.Join(e.cfg.Host.WorkdirParent, task.GetId(), filepath.FromSlash(repo))
	} else {
		workdir = path.Join("/", strings.Trim(e.cfg.Container.WorkdirParent, "/"), repo)
	}

	inputs := map[string]string{}
	if in, ok := preset.Event["inputs"].(map[string]any); ok {
		for k, v := range in {
			inputs[k] = fmt.Sprint(v)
		}
	}

	rc := &actrunner.Config{
		Workdir:               workdir,
		BindWorkdir:           false,
		ActionCacheDir:        e.cfg.Runner.CacheDir,
		ReuseContainers:       false,
		ForcePull:             e.cfg.Container.ForcePull,
		ForceRebuild:          e.cfg.Container.ForceRebuild,
		LogOutput:             true,
		Env:                   env,
		Inputs:                inputs,
		Secrets:               secrets,
		Vars:                  task.GetVars(),
		Token:                 task.GetToken(),
		Privileged:            false,
		ContainerDaemonSocket: e.cfg.Container.DockerHost,
		ContainerOptions:      e.cfg.Container.DockerOptions(),
		ContainerNetworkMode:  container.NetworkMode(e.cfg.Container.Network),
		GitHubInstance:        instance,
		AutoRemove:            true,
		NoSkipCheckout:        true,
		PresetGitHubContext:   preset,
		EventJSON:             string(eventJSON),
		ContainerNamePrefix:   "ERVISIO-ACTIONS-TASK-" + task.GetId(),
		ContainerMaxLifetime:  maxLifetime,
		CleanWorkdir:          true,
		DefaultActionInstance: strings.TrimSuffix(task.GetActionsUrl(), "/"),
		PlatformPicker:        func([]string) string { return platform },
		ValidVolumes:          e.cfg.Container.ValidVolumes,
		InsecureSkipTLS:       e.cfg.Server.InsecureTLS,
	}
	if rc.DefaultActionInstance == "" {
		rc.DefaultActionInstance = "https://github.com"
	}
	r, err := actrunner.New(rc)
	if err != nil {
		return fmt.Errorf("init act: %w", err)
	}
	exec := r.NewPlanExecutor(plan)
	rep.Logf("workflow prepared, running on %s", platformName(platform))

	ctx = common.WithLoggerHook(ctx, rep)
	ctx = actrunner.WithJobLoggerFactory(ctx, nullLogger{})
	execErr := exec(ctx)
	rep.SetOutputs(job.Outputs)
	if execErr != nil {
		return fmt.Errorf("run job: %w", execErr)
	}
	return nil
}

func withSlash(s string) string {
	if strings.HasSuffix(s, "/") {
		return s
	}
	return s + "/"
}

func platformName(p string) string {
	if p == "-self-hosted" {
		return "the host"
	}
	return "docker image " + p
}

// nullLogger gives act a job logger that discards its output: entries
// reach the Reporter through the logger hook only.
type nullLogger struct{}

// WithJobLogger implements actrunner.JobLoggerFactory.
func (nullLogger) WithJobLogger() *logrus.Logger {
	l := logrus.New()
	l.SetOutput(io.Discard)
	l.SetLevel(logrus.TraceLevel)
	return l
}
