package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1"
	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1/runnerv1connect"
)

const (
	// fetchWait is how long FetchTask holds a poll without work.
	fetchWait = 25 * time.Second
	// maxRunnerLabels bounds the labels of one runner.
	maxRunnerLabels = 64
	// runtimeTokenGrace is how long a runtime token outlives the job
	// timeout (artifact uploads at the end of a job).
	runtimeTokenGrace = time.Hour
	// defaultRegistrationTTL is the lifetime of registration tokens when
	// actions.registration_token_ttl is not set.
	defaultRegistrationTTL = time.Hour
)

// RunnerService implements the ConnectRPC runner protocol.
type RunnerService struct{ s *Service }

var _ runnerv1connect.RunnerServiceHandler = (*RunnerService)(nil)

// Runners is the runner protocol of the service.
func (s *Service) Runners() *RunnerService { return &RunnerService{s: s} }

var errUnauthenticated = connect.NewError(connect.CodeUnauthenticated, errors.New("invalid runner credential"))

// runner authenticates the runner credential of a call.
func (r *RunnerService) runner(ctx context.Context, h http.Header) (dbgen.ActionsRunner, error) {
	tok, ok := strings.CutPrefix(h.Get("Authorization"), "Bearer ")
	if !ok || !validToken(strings.TrimSpace(tok), RunnerCredentialPrefix) {
		return dbgen.ActionsRunner{}, errUnauthenticated
	}
	rn, err := r.s.q.GetActionsRunnerByCredential(ctx, auth.HashToken(strings.TrimSpace(tok)))
	if isNoRows(err) {
		return dbgen.ActionsRunner{}, errUnauthenticated
	}
	if err != nil {
		return dbgen.ActionsRunner{}, internalErr(r.s, "get runner", err)
	}
	if err := r.s.q.TouchActionsRunner(ctx, rn.ID); err != nil {
		r.s.log.Warn("touch runner", "runner_id", rn.ID, "err", err)
	}
	return rn, nil
}

func internalErr(s *Service, what string, err error) error {
	s.log.Error("runner service", "op", what, "err", err)
	return connect.NewError(connect.CodeInternal, errors.New(what+" failed"))
}

func badRequest(format string, a ...any) error {
	return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(format, a...))
}

// parseLabels splits runner labels into lower-case names and the raw
// declarations.
func parseLabels(in []string) (names, raw []string, err error) {
	if len(in) > maxRunnerLabels {
		return nil, nil, badRequest("at most %d labels", maxRunnerLabels)
	}
	for _, l := range in {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if len(l) > 1024 {
			return nil, nil, badRequest("label %.40q is too long", l)
		}
		name, _, _ := strings.Cut(l, ":")
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || len(name) > 255 {
			return nil, nil, badRequest("invalid label %.40q", l)
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
		raw = append(raw, l)
	}
	if names == nil {
		names, raw = []string{}, []string{}
	}
	return names, raw, nil
}

// runnerScope is the Runner.scope label of a runner row.
func (s *Service) runnerScope(ctx context.Context, scope string, owner, repo uuid.NullUUID) string {
	switch scope {
	case "org":
		if n, err := s.q.GetActionsNamespaceName(ctx, owner.UUID); err == nil {
			return "org:" + n
		}
	case "repo":
		if d := s.deps.Load(); d != nil && d.Repos != nil {
			if r, err := d.Repos.GetByID(ctx, system, repo.UUID); err == nil {
				return "repo:" + r.FullName()
			}
		}
	}
	return scope
}

func (s *Service) protoRunner(ctx context.Context, rn dbgen.ActionsRunner) *runnerv1.Runner {
	return &runnerv1.Runner{Id: rn.ID.String(), Name: rn.Name, Version: rn.Version, Labels: rn.RawLabels,
		Ephemeral: rn.Ephemeral, Scope: s.runnerScope(ctx, rn.Scope, rn.OwnerID, rn.RepoID)}
}

// Register exchanges a registration token for a runner credential.
func (r *RunnerService) Register(ctx context.Context, req *connect.Request[runnerv1.RegisterRequest]) (*connect.Response[runnerv1.RegisterResponse], error) {
	m := req.Msg
	tok := strings.TrimSpace(m.GetToken())
	if !validToken(tok, RegistrationTokenPrefix) {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid registration token"))
	}
	rt, err := r.s.q.GetActionsRegistrationToken(ctx, auth.HashToken(tok))
	if isNoRows(err) {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid or expired registration token"))
	}
	if err != nil {
		return nil, internalErr(r.s, "get registration token", err)
	}
	name := strings.TrimSpace(m.GetName())
	if name == "" || len(name) > 255 {
		return nil, badRequest("the runner name must be 1 to 255 characters")
	}
	if len(m.GetVersion()) > 255 {
		return nil, badRequest("the version is too long")
	}
	labels, raw, err := parseLabels(m.GetLabels())
	if err != nil {
		return nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, internalErr(r.s, "runner id", err)
	}
	cred, hash := newSecretToken(RunnerCredentialPrefix)
	rn, err := r.s.q.InsertActionsRunner(ctx, dbgen.InsertActionsRunnerParams{ID: id, Name: name, Scope: rt.Scope,
		OwnerID: rt.OwnerID, RepoID: rt.RepoID, Labels: labels, RawLabels: raw, Version: m.GetVersion(),
		Ephemeral: m.GetEphemeral(), CredentialHash: hash, CreatedBy: rt.CreatedBy})
	if err != nil {
		return nil, internalErr(r.s, "insert runner", err)
	}
	r.s.log.Info("actions runner registered", "runner_id", rn.ID, "name", rn.Name, "scope", rn.Scope,
		"ephemeral", rn.Ephemeral)
	return connect.NewResponse(&runnerv1.RegisterResponse{Runner: r.s.protoRunner(ctx, rn), Credential: cred}), nil
}

// Declare updates the version and labels of a runner.
func (r *RunnerService) Declare(ctx context.Context, req *connect.Request[runnerv1.DeclareRequest]) (*connect.Response[runnerv1.DeclareResponse], error) {
	rn, err := r.runner(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	if len(req.Msg.GetVersion()) > 255 {
		return nil, badRequest("the version is too long")
	}
	labels, raw, err := parseLabels(req.Msg.GetLabels())
	if err != nil {
		return nil, err
	}
	rn, err = r.s.q.DeclareActionsRunner(ctx, dbgen.DeclareActionsRunnerParams{Version: req.Msg.GetVersion(),
		Labels: labels, RawLabels: raw, ID: rn.ID})
	if err != nil {
		return nil, internalErr(r.s, "declare runner", err)
	}
	return connect.NewResponse(&runnerv1.DeclareResponse{Runner: r.s.protoRunner(ctx, rn)}), nil
}

// FetchTask hands the oldest matching queued job to the runner, waiting up
// to fetchWait for one.
func (r *RunnerService) FetchTask(ctx context.Context, req *connect.Request[runnerv1.FetchTaskRequest]) (*connect.Response[runnerv1.FetchTaskResponse], error) {
	rn, err := r.runner(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	s := r.s
	empty := func() (*connect.Response[runnerv1.FetchTaskResponse], error) {
		v, err := s.q.GetActionsTasksVersion(ctx)
		if err != nil && ctx.Err() == nil {
			return nil, internalErr(s, "tasks version", err)
		}
		return connect.NewResponse(&runnerv1.FetchTaskResponse{TasksVersion: v}), nil
	}
	if req.Msg.GetCapacity() <= 0 {
		return empty()
	}
	if rn.Ephemeral {
		used, err := s.q.ActionsRunnerHasJobs(ctx, rn.ID)
		if err != nil {
			return nil, internalErr(s, "runner jobs", err)
		}
		if used {
			return empty()
		}
	}
	d, err := s.d()
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	deadline := s.now().Add(fetchWait)
	for {
		changed := s.hub.tasksChanged()
		task, err := r.claim(ctx, d, rn)
		if err != nil {
			return nil, err
		}
		if task != nil {
			v, _ := s.q.GetActionsTasksVersion(ctx)
			return connect.NewResponse(&runnerv1.FetchTaskResponse{Task: task, TasksVersion: v}), nil
		}
		left := deadline.Sub(s.now())
		if left <= 0 || ctx.Err() != nil {
			return empty()
		}
		s.wait(ctx, changed, left)
		if err := s.q.TouchActionsRunner(ctx, rn.ID); err != nil && ctx.Err() == nil {
			s.log.Warn("touch runner", "runner_id", rn.ID, "err", err)
		}
	}
}

// claim takes one job for the runner and builds its task; nil when there
// is none.
func (r *RunnerService) claim(ctx context.Context, d *Deps, rn dbgen.ActionsRunner) (*runnerv1.Task, error) {
	s := r.s
	token, hash := newSecretToken(auth.JobTokenPrefix)
	job, err := s.q.ClaimActionsJob(ctx, dbgen.ClaimActionsJobParams{RunnerID: nullID(rn.ID), TokenHash: hash,
		Labels: rn.Labels, Scope: rn.Scope, OwnerID: rn.OwnerID.UUID, RepoID: rn.RepoID.UUID})
	if isNoRows(err) || (err != nil && ctx.Err() != nil) {
		return nil, nil
	}
	if err != nil {
		return nil, internalErr(s, "claim job", err)
	}
	s.log.Info("actions job assigned", "job_id", job.ID, "run_id", job.RunID, "runner_id", rn.ID)
	task, err := s.buildTask(ctx, d, job, token)
	if err != nil {
		s.log.Error("build actions task", "job_id", job.ID, "err", err)
		s.appendServerLog(ctx, job.ID, "The server could not prepare this job: "+err.Error())
		if err := s.completeJob(context.WithoutCancel(ctx), job.ID, "failure"); err != nil {
			s.log.Warn("fail job", "job_id", job.ID, "err", err)
		}
		return nil, nil
	}
	if err := s.advance(context.WithoutCancel(ctx), job.RunID); err != nil {
		s.log.Warn("advance run", "run_id", job.RunID, "err", err)
	}
	s.enqueue(ctx, ReportArgs{RunID: job.RunID})
	return task, nil
}

// buildTask is the task of a claimed job.
func (s *Service) buildTask(ctx context.Context, d *Deps, job dbgen.ActionsJob, token string) (*runnerv1.Task, error) {
	run, err := s.q.GetActionsRun(ctx, job.RunID)
	if err != nil {
		return nil, fmt.Errorf("get run: %w", err)
	}
	repo, err := d.Repos.GetByID(ctx, system, job.RepoID)
	if err != nil {
		return nil, fmt.Errorf("get repository: %w", err)
	}
	extra := decodeExtra(job.Inputs)
	actorID := run.TriggerActorID
	if !actorID.Valid {
		actorID = run.ActorID
	}
	gh := s.githubCtx(run, repo, s.userName(ctx, actorID))
	gh.Token = token
	gh.Job = strings.TrimPrefix(job.JobKey, extra.Prefix)
	gh.JobName = job.Name
	if len(extra.Inputs) > 0 {
		gh.Event["inputs"] = extra.Inputs
	}
	ghJSON, err := json.Marshal(gh)
	if err != nil {
		return nil, fmt.Errorf("github context: %w", err)
	}
	var ghMap map[string]any
	if err := json.Unmarshal(ghJSON, &ghMap); err != nil {
		return nil, fmt.Errorf("github context: %w", err)
	}
	ghStruct, err := structpb.NewStruct(ghMap)
	if err != nil {
		return nil, fmt.Errorf("github context: %w", err)
	}

	vars, err := s.variables(ctx, repo, job.Environment)
	if err != nil {
		return nil, err
	}
	secrets, err := s.jobSecrets(ctx, run, repo, job, extra, vars)
	if err != nil {
		return nil, err
	}
	secrets["GITHUB_TOKEN"] = token
	s.masks.Store(job.ID, slices.Collect(maps.Values(secrets)))

	rows, err := s.q.ListActionsJobs(ctx, dbgen.ListActionsJobsParams{RunID: run.ID, Attempt: job.Attempt})
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	byKey := map[string][]dbgen.ActionsJob{}
	for _, r := range rows {
		byKey[r.JobKey] = append(byKey[r.JobKey], r)
	}
	needs := map[string]*runnerv1.TaskNeed{}
	for _, n := range job.Needs {
		res, _ := aggregate(byKey[n])
		needs[strings.TrimPrefix(n, extra.Prefix)] = &runnerv1.TaskNeed{Outputs: mergedOutputs(byKey[n]), Result: protoResult(res)}
	}

	timeout := job.TimeoutMinutes
	if timeout <= 0 || timeout > s.maxTimeoutMinutes() {
		timeout = s.maxTimeoutMinutes()
	}
	rt, err := signRuntimeToken(s.jwtKey, repo.ID, run.ID, job.ID, s.now(),
		time.Duration(timeout)*time.Minute+runtimeTokenGrace)
	if err != nil {
		return nil, err
	}
	actionsURL := "https://github.com"
	if s.cfg.DefaultActionsURL == "mirror-only" {
		actionsURL = s.public
	}
	return &runnerv1.Task{Id: job.ID.String(), WorkflowPayload: job.Payload, Context: ghStruct, Secrets: secrets,
		Vars: vars, Needs: needs, Token: token,
		RuntimeUrl: s.public + "/api/actions/runtime/", ResultsUrl: s.public + "/api/actions/results/",
		CacheUrl: s.public + "/api/actions/cache/", TimeoutMinutes: timeout, Label: job.Label, Attempt: job.Attempt,
		RuntimeToken: rt, ActionsUrl: actionsURL}, nil
}

// jobSecrets resolves the stored secrets of a job and passes them through
// the secrets: of the reusable workflow calls above it.
func (s *Service) jobSecrets(ctx context.Context, run dbgen.ActionsRun, repo repos.Repo, job dbgen.ActionsJob,
	extra jobExtra, vars map[string]string) (map[string]string, error) {
	out, err := s.secrets(ctx, run, repo, job.Environment)
	if err != nil {
		return nil, err
	}
	gh := s.githubCtx(run, repo, "")
	for _, lvl := range extra.Secrets {
		if lvl.Inherit {
			continue
		}
		ec := &exprCtx{github: gh, vars: vars, secrets: out}
		next := map[string]string{}
		for k, expr := range lvl.Map {
			v, err := ec.interpolate(expr)
			if err != nil {
				s.log.Warn("reusable workflow secret", "job_id", job.ID, "secret", k, "err", err)
				continue
			}
			next[k] = v
		}
		out = next
	}
	return out, nil
}

func protoResult(c string) runnerv1.Result {
	switch c {
	case "success":
		return runnerv1.Result_RESULT_SUCCESS
	case "failure", "timed_out":
		return runnerv1.Result_RESULT_FAILURE
	case "cancelled":
		return runnerv1.Result_RESULT_CANCELLED
	case "skipped":
		return runnerv1.Result_RESULT_SKIPPED
	}
	return runnerv1.Result_RESULT_UNSPECIFIED
}

func conclusionOf(r runnerv1.Result) string {
	switch r {
	case runnerv1.Result_RESULT_SUCCESS:
		return "success"
	case runnerv1.Result_RESULT_FAILURE:
		return "failure"
	case runnerv1.Result_RESULT_CANCELLED:
		return "cancelled"
	case runnerv1.Result_RESULT_SKIPPED:
		return "skipped"
	}
	return ""
}

func pbTime(t *timestamppb.Timestamp) time.Time {
	if t == nil || (t.GetSeconds() == 0 && t.GetNanos() == 0) {
		return time.Time{}
	}
	return t.AsTime()
}

// ownJob loads a job the runner is (or was) running.
func (r *RunnerService) ownJob(ctx context.Context, rn dbgen.ActionsRunner, id string) (dbgen.ActionsJob, error) {
	jobID, err := uuid.Parse(id)
	if err != nil {
		return dbgen.ActionsJob{}, badRequest("invalid task id")
	}
	job, err := r.s.q.GetActionsJob(ctx, jobID)
	if isNoRows(err) || (err == nil && job.RunnerID.UUID != rn.ID) {
		return dbgen.ActionsJob{}, connect.NewError(connect.CodeNotFound, errors.New("unknown task"))
	}
	if err != nil {
		return dbgen.ActionsJob{}, internalErr(r.s, "get job", err)
	}
	return job, nil
}

const maxOutputs = 256

// UpdateTask stores the job and step state.
func (r *RunnerService) UpdateTask(ctx context.Context, req *connect.Request[runnerv1.UpdateTaskRequest]) (*connect.Response[runnerv1.UpdateTaskResponse], error) {
	rn, err := r.runner(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	s := r.s
	st := req.Msg.GetState()
	if st == nil {
		return nil, badRequest("state is required")
	}
	job, err := r.ownJob(ctx, rn, st.GetId())
	if err != nil {
		return nil, err
	}
	if len(st.GetSteps()) > 1000 || len(req.Msg.GetOutputs()) > maxOutputs {
		return nil, badRequest("too many steps or outputs")
	}
	for _, step := range st.GetSteps() {
		if step.GetIndex() < 0 {
			continue
		}
		p := dbgen.UpsertActionsStepParams{JobID: job.ID, Idx: step.GetIndex(), Name: truncate(step.GetName(), 1024),
			Status: "queued", LogIndex: max(step.GetLogIndex(), 0), LogLength: max(step.GetLogLength(), 0),
			StartedAt: ts(pbTime(step.GetStartedAt())), CompletedAt: ts(pbTime(step.GetStoppedAt()))}
		if c := conclusionOf(step.GetResult()); c != "" {
			p.Status, p.Conclusion = "completed", c
		} else if p.StartedAt.Valid {
			p.Status, p.CompletedAt.Valid = "in_progress", false
		}
		if err := s.q.UpsertActionsStep(ctx, p); err != nil {
			return nil, internalErr(s, "store step", err)
		}
	}
	stored := job.Outputs
	if outs := req.Msg.GetOutputs(); len(outs) > 0 && job.Status != "completed" {
		clean := map[string]string{}
		for k, v := range outs {
			if k == "" || len(k) > 255 {
				continue
			}
			clean[k] = truncate(v, 1<<20)
		}
		b, _ := json.Marshal(clean)
		if stored, err = s.q.MergeActionsJobOutputs(ctx, dbgen.MergeActionsJobOutputsParams{Outputs: b, ID: job.ID}); err != nil {
			return nil, internalErr(s, "store outputs", err)
		}
	} else if err := s.q.TouchActionsJob(ctx, job.ID); err != nil {
		return nil, internalErr(s, "touch job", err)
	}
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(stored, &keys)
	sent := slices.Sorted(maps.Keys(keys))
	if sent == nil {
		sent = []string{}
	}
	// Every requested key counts as sent: the runner retries until it
	// sees them all, and keys dropped as invalid will never be stored.
	for k := range req.Msg.GetOutputs() {
		if !slices.Contains(sent, k) {
			sent = append(sent, k)
		}
	}
	if c := conclusionOf(st.GetResult()); c != "" && job.Status != "completed" {
		if err := s.completeJob(context.WithoutCancel(ctx), job.ID, c); err != nil {
			return nil, internalErr(s, "complete job", err)
		}
		s.log.Info("actions job finished", "job_id", job.ID, "run_id", job.RunID, "conclusion", c)
	} else if len(st.GetSteps()) > 0 {
		s.enqueue(ctx, ReportArgs{RunID: job.RunID})
	}
	cancelled := job.CancelRequested || (job.Status == "completed" && conclusionOf(st.GetResult()) == "")
	return connect.NewResponse(&runnerv1.UpdateTaskResponse{Cancelled: cancelled, SentOutputs: sent}), nil
}

// UpdateLog appends log rows of a job.
func (r *RunnerService) UpdateLog(ctx context.Context, req *connect.Request[runnerv1.UpdateLogRequest]) (*connect.Response[runnerv1.UpdateLogResponse], error) {
	rn, err := r.runner(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	m := req.Msg
	job, err := r.ownJob(ctx, rn, m.GetTaskId())
	if err != nil {
		return nil, err
	}
	rows := make([]logRow, 0, len(m.GetRows()))
	for _, row := range m.GetRows() {
		rows = append(rows, logRow{at: pbTime(row.GetTime()), content: row.GetContent()})
	}
	length, err := r.s.appendLog(ctx, job.ID, m.GetIndex(), rows)
	var ve *ValidationError
	if errors.As(err, &ve) {
		return nil, badRequest("%s", ve.Msg)
	}
	if err != nil {
		return nil, internalErr(r.s, "append log", err)
	}
	if m.GetNoMore() {
		r.s.enqueue(ctx, ArchiveArgs{JobID: job.ID})
	}
	return connect.NewResponse(&runnerv1.UpdateLogResponse{AckIndex: length}), nil
}

// Heartbeat keeps the runner online and lists the jobs to stop.
func (r *RunnerService) Heartbeat(ctx context.Context, req *connect.Request[runnerv1.HeartbeatRequest]) (*connect.Response[runnerv1.HeartbeatResponse], error) {
	rn, err := r.runner(ctx, req.Header())
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(req.Msg.GetRunningTaskIds()))
	var bad []string
	for _, s := range req.Msg.GetRunningTaskIds() {
		id, err := uuid.Parse(s)
		if err != nil {
			bad = append(bad, s)
			continue
		}
		ids = append(ids, id)
	}
	out := bad
	if len(ids) > 0 {
		cancel, err := r.s.q.ListActionsJobsToCancel(ctx, dbgen.ListActionsJobsToCancelParams{Ids: ids, RunnerID: nullID(rn.ID)})
		if err != nil {
			return nil, internalErr(r.s, "jobs to cancel", err)
		}
		cancelled := map[uuid.UUID]bool{}
		for _, id := range cancel {
			cancelled[id] = true
			out = append(out, id.String())
		}
		// The jobs still running are alive.
		for _, id := range ids {
			if !cancelled[id] {
				if err := r.s.q.TouchActionsJob(ctx, id); err != nil {
					r.s.log.Warn("touch job", "job_id", id, "err", err)
				}
			}
		}
	}
	if out == nil {
		out = []string{}
	}
	return connect.NewResponse(&runnerv1.HeartbeatResponse{CancelTaskIds: out}), nil
}

// LookupJobToken authenticates a job token (auth.JobTokenLookup): the
// caller acts on the repository of the job only.
func (s *Service) LookupJobToken(ctx context.Context, token string) (auth.Principal, error) {
	row, err := s.q.GetActionsJobByToken(ctx, auth.HashToken(token))
	if isNoRows(err) {
		return auth.Principal{}, auth.ErrNoToken
	}
	if err != nil {
		return auth.Principal{}, fmt.Errorf("get job token: %w", err)
	}
	var perms map[string]string
	_ = json.Unmarshal(row.Permissions, &perms)
	if perms == nil {
		perms = defaultPermissions()
	}
	perm := func(k string) string {
		if v, ok := perms[k]; ok {
			return v
		}
		return perms["all"]
	}
	contents, packages := perm("contents"), perm("packages")
	if row.Fork {
		// Fork pull requests get a read-only token.
		if contents == "write" {
			contents = "read"
		}
		if packages == "write" {
			packages = "read"
		}
	}
	grant := &auth.RepoGrant{RepoID: row.RepoID, Permission: "read", JobID: row.ID}
	var scopes []string
	switch contents {
	case "write":
		grant.Permission = "write"
		scopes = append(scopes, auth.ScopeReadRepo, auth.ScopeWriteRepo)
	case "read":
		scopes = append(scopes, auth.ScopeReadRepo)
	default:
		grant.Permission = "none"
	}
	switch packages {
	case "write":
		scopes = append(scopes, auth.ScopeReadPackages, auth.ScopeWritePackages)
	case "read":
		scopes = append(scopes, auth.ScopeReadPackages)
	}
	name := "ervisio-actions[bot]"
	return auth.Principal{Username: name, Role: auth.RoleUser, Method: auth.MethodToken, Scopes: scopes,
		Grant: grant}, nil
}

// ---- runner administration ----

// Runner is a registered runner as shown to users.
type Runner struct {
	ID         uuid.UUID
	Name       string
	Scope      string
	Labels     []string
	Version    string
	Ephemeral  bool
	Online     bool
	Busy       bool
	LastSeenAt *time.Time
	CreatedAt  time.Time
}

func (s *Service) runnerFrom(ctx context.Context, rn dbgen.ActionsRunner, busy bool) Runner {
	out := Runner{ID: rn.ID, Name: rn.Name, Scope: s.runnerScope(ctx, rn.Scope, rn.OwnerID, rn.RepoID),
		Labels: rn.RawLabels, Version: rn.Version, Ephemeral: rn.Ephemeral, CreatedAt: rn.CreatedAt.Time, Busy: busy}
	if rn.LastSeenAt.Valid {
		t := rn.LastSeenAt.Time
		out.LastSeenAt = &t
		out.Online = rn.Online && s.now().Sub(t) < s.offlineAfter()
	}
	return out
}

func runnerScopeOK(sc Scope) error {
	switch sc.kind {
	case "instance", "org", "repo":
		return nil
	case "":
		return ErrForbidden
	}
	return invalid("runners cannot be registered at the %s scope", sc.kind)
}

// ListRunners lists the runners registered at a scope.
func (s *Service) ListRunners(ctx context.Context, sc Scope) ([]Runner, error) {
	if err := runnerScopeOK(sc); err != nil {
		return nil, err
	}
	owner, repo := sc.params()
	rows, err := s.q.ListActionsRunners(ctx, dbgen.ListActionsRunnersParams{Scope: sc.kind, OwnerID: owner, RepoID: repo})
	if err != nil {
		return nil, fmt.Errorf("list runners: %w", err)
	}
	return s.runnersFrom(ctx, rows)
}

// ListRepoRunners lists every runner that can take the jobs of a
// repository (its own, its owner's and the instance runners).
func (s *Service) ListRepoRunners(ctx context.Context, sc Scope) ([]Runner, error) {
	if sc.kind != "repo" {
		return nil, invalid("not a repository scope")
	}
	d, err := s.d()
	if err != nil {
		return nil, err
	}
	repo, err := d.Repos.GetByID(ctx, system, sc.repoID)
	if err != nil {
		return nil, fmt.Errorf("get repository: %w", err)
	}
	rows, err := s.q.ListActionsRunnersForRepo(ctx, dbgen.ListActionsRunnersForRepoParams{OwnerID: nullID(repo.OwnerID), RepoID: nullID(repo.ID)})
	if err != nil {
		return nil, fmt.Errorf("list runners: %w", err)
	}
	return s.runnersFrom(ctx, rows)
}

func (s *Service) runnersFrom(ctx context.Context, rows []dbgen.ActionsRunner) ([]Runner, error) {
	out := make([]Runner, 0, len(rows))
	for _, rn := range rows {
		running, err := s.q.ListRunningActionsJobsOfRunner(ctx, nullID(rn.ID))
		if err != nil {
			return nil, fmt.Errorf("runner jobs: %w", err)
		}
		out = append(out, s.runnerFrom(ctx, rn, len(running) > 0))
	}
	return out, nil
}

// DeleteRunner removes a runner registered at a scope; its running jobs
// fail at the next sweep.
func (s *Service) DeleteRunner(ctx context.Context, sc Scope, id uuid.UUID) error {
	if err := runnerScopeOK(sc); err != nil {
		return err
	}
	rn, err := s.q.GetActionsRunner(ctx, id)
	if isNoRows(err) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get runner: %w", err)
	}
	owner, repo := sc.params()
	if rn.Scope != sc.kind || rn.OwnerID != owner || rn.RepoID != repo {
		return ErrNotFound
	}
	n, err := s.q.DeleteActionsRunner(ctx, id)
	if err != nil {
		return fmt.Errorf("delete runner: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	s.log.Info("actions runner deleted", "runner_id", id, "scope", sc.label)
	return nil
}

// RegistrationToken is a new runner registration token.
type RegistrationToken struct {
	// Token is the errt_… secret, shown only once.
	Token     string
	Scope     string
	ExpiresAt time.Time
}

// CreateRegistrationToken issues a registration token for a scope.
func (s *Service) CreateRegistrationToken(ctx context.Context, who auth.Principal, sc Scope) (RegistrationToken, error) {
	if err := runnerScopeOK(sc); err != nil {
		return RegistrationToken{}, err
	}
	ttl := time.Duration(s.cfg.RegistrationTokenTTL)
	if ttl <= 0 {
		ttl = defaultRegistrationTTL
	}
	id, err := uuid.NewV7()
	if err != nil {
		return RegistrationToken{}, fmt.Errorf("token id: %w", err)
	}
	tok, hash := newSecretToken(RegistrationTokenPrefix)
	exp := s.now().Add(ttl)
	owner, repo := sc.params()
	if _, err := s.q.InsertActionsRegistrationToken(ctx, dbgen.InsertActionsRegistrationTokenParams{ID: id,
		TokenHash: hash, Scope: sc.kind, OwnerID: owner, RepoID: repo, CreatedBy: nullID(who.UserID),
		ExpiresAt: ts(exp)}); err != nil {
		return RegistrationToken{}, fmt.Errorf("insert registration token: %w", err)
	}
	s.log.Info("actions registration token created", "scope", sc.label, "by", who.Username)
	return RegistrationToken{Token: tok, Scope: sc.label, ExpiresAt: exp}, nil
}
