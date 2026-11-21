package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// Run is a workflow run as shown to users.
type Run struct {
	ID           uuid.UUID
	RunNumber    int64
	Attempt      int32
	WorkflowPath string
	WorkflowName string
	Event        string
	EventAction  string
	Ref          string
	Sha          string
	Title        string
	// Status is waiting (approval), pending (concurrency), queued,
	// in_progress or completed.
	Status        string
	Conclusion    string
	NeedsApproval bool
	Fork          bool
	Actor         string
	TriggerActor  string
	URL           string
	// External marks a read-only run imported from GitHub: no jobs or
	// logs here, HTMLURL is its page on GitHub.
	External    bool
	HTMLURL     string
	StartedAt   *time.Time
	CompletedAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Job is one job of a run attempt.
type Job struct {
	ID     uuid.UUID
	RunID  uuid.UUID
	Key    string
	Name   string
	Needs  []string
	Status string // blocked, waiting, queued, in_progress or completed
	// Conclusion is success, failure, cancelled, skipped or timed_out.
	Conclusion      string
	Environment     string
	CancelRequested bool
	Approved        bool
	RunsOn          []string
	Runner          string
	// Reusable is true for a job that calls a reusable workflow; its jobs
	// follow it with Caller set.
	Reusable    bool
	Caller      string
	LogLength   int64
	// LogExpired: the log was deleted because the run is not tied to a
	// release (actions.unreleased_retention).
	LogExpired  bool
	URL         string
	StartedAt   *time.Time
	CompletedAt *time.Time
}

// Step is one step of a job.
type Step struct {
	Index       int32
	Name        string
	Status      string
	Conclusion  string
	LogIndex    int64
	LogLength   int64
	StartedAt   *time.Time
	CompletedAt *time.Time
}

// Workflow is a workflow file of the default branch or of past runs.
type Workflow struct {
	Path string
	Name string
	// LastRun is nil when the workflow never ran.
	LastRun *time.Time
	// OnDefaultBranch reports whether the file exists on the default
	// branch.
	OnDefaultBranch bool
}

// RunFilter filters ListRuns.
type RunFilter struct {
	Workflow string // path or file name
	Event    string
	Status   string // a status or a conclusion
	Ref      string // full ref or branch name
	Sha      string
	// Actor is the user name (or bot name) that caused the run.
	Actor string
	// CreatedFrom and CreatedTo bound the creation time [from, to).
	CreatedFrom, CreatedTo *time.Time
	Page                   int // from 1
	PerPage                int // default 30, at most 100
	// External includes the runs imported from GitHub.
	External bool
}

func timePtr(t time.Time, ok bool) *time.Time {
	if !ok {
		return nil
	}
	return &t
}

func (s *Service) runFrom(ctx context.Context, repo repos.Repo, r dbgen.ActionsRun) Run {
	return Run{ID: r.ID, RunNumber: r.RunNumber, Attempt: r.Attempt, WorkflowPath: r.WorkflowPath,
		WorkflowName: r.WorkflowName, Event: r.Event, EventAction: r.EventAction, Ref: r.Ref, Sha: r.Sha, Title: r.Title,
		Status: r.Status, Conclusion: r.Conclusion, NeedsApproval: r.NeedsApproval, Fork: r.Fork,
		Actor: s.actorName(ctx, r.ActorID, r.SystemActor), TriggerActor: s.actorName(ctx, r.TriggerActorID, r.SystemActor), URL: s.RunURL(repo, r),
		StartedAt: timePtr(r.StartedAt.Time, r.StartedAt.Valid), CompletedAt: timePtr(r.CompletedAt.Time, r.CompletedAt.Valid),
		CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
}

func (s *Service) jobFrom(ctx context.Context, repo repos.Repo, run dbgen.ActionsRun, j dbgen.ActionsJob) Job {
	extra := decodeExtra(j.Inputs)
	out := Job{ID: j.ID, RunID: j.RunID, Key: j.JobKey, Name: j.Name, Needs: j.Needs, Status: j.Status,
		Conclusion: j.Conclusion, Environment: j.Environment, CancelRequested: j.CancelRequested,
		Approved: j.ApprovedBy.Valid, RunsOn: j.RunsOn, Reusable: extra.Expanded, Caller: extra.Caller,
		LogLength: j.LogLength, LogExpired: j.LogExpiredAt.Valid, URL: s.JobURL(repo, run, j.ID),
		StartedAt: timePtr(j.StartedAt.Time, j.StartedAt.Valid), CompletedAt: timePtr(j.CompletedAt.Time, j.CompletedAt.Valid)}
	if j.RunnerID.Valid {
		if rn, err := s.q.GetActionsRunner(ctx, j.RunnerID.UUID); err == nil {
			out.Runner = rn.Name
		}
	}
	return out
}

// repoFor resolves a repository the caller may read (act: the action the
// caller must be allowed).
func (s *Service) repoFor(ctx context.Context, who auth.Principal, owner, name string, act authz.Action) (*Deps, repos.Repo, error) {
	d, err := s.d()
	if err != nil {
		return nil, repos.Repo{}, err
	}
	repo, err := d.Repos.Get(ctx, who, owner, name)
	if err != nil {
		return nil, repos.Repo{}, err
	}
	if who.Grant != nil && who.Grant.RepoID != repo.ID {
		return nil, repos.Repo{}, ErrForbidden
	}
	if who.Installation != nil && act != authz.ActRead {
		// /api/v3 checked the actions permission of the endpoint; the
		// installation must cover the repository.
		if !who.WritesTo(repo.ID, repo.OwnerID) {
			return nil, repos.Repo{}, ErrForbidden
		}
		return d, repo, nil
	}
	if !authz.Allowed(repo.Permission, act) {
		return nil, repos.Repo{}, ErrForbidden
	}
	return d, repo, nil
}

// runByID loads a run of the repository by id.
func (s *Service) runByID(ctx context.Context, repo repos.Repo, id uuid.UUID) (dbgen.ActionsRun, error) {
	run, err := s.q.GetActionsRun(ctx, id)
	if isNoRows(err) || (err == nil && run.RepoID != repo.ID) {
		return dbgen.ActionsRun{}, ErrNotFound
	}
	if err != nil {
		return dbgen.ActionsRun{}, fmt.Errorf("get run: %w", err)
	}
	return run, nil
}

func (s *Service) jobOf(ctx context.Context, repo repos.Repo, id uuid.UUID) (dbgen.ActionsJob, dbgen.ActionsRun, error) {
	j, err := s.q.GetActionsJob(ctx, id)
	if isNoRows(err) || (err == nil && j.RepoID != repo.ID) {
		return dbgen.ActionsJob{}, dbgen.ActionsRun{}, ErrNotFound
	}
	if err != nil {
		return dbgen.ActionsJob{}, dbgen.ActionsRun{}, fmt.Errorf("get job: %w", err)
	}
	run, err := s.q.GetActionsRun(ctx, j.RunID)
	if err != nil {
		return dbgen.ActionsJob{}, dbgen.ActionsRun{}, fmt.Errorf("get run: %w", err)
	}
	return j, run, nil
}

// workflowPath resolves a workflow given by path or file name among the
// workflows that ran in a repository.
func (s *Service) workflowPath(ctx context.Context, repoID uuid.UUID, wf string) (string, error) {
	if wf == "" || strings.Contains(wf, "/") {
		return wf, nil
	}
	rows, err := s.q.ListActionsWorkflows(ctx, repoID)
	if err != nil {
		return "", fmt.Errorf("list workflows: %w", err)
	}
	found := ""
	for _, r := range rows {
		if path.Base(r.WorkflowPath) != wf {
			continue
		}
		// .ervisio/workflows wins over .github/workflows.
		if found == "" || strings.HasPrefix(r.WorkflowPath, ".ervisio/") {
			found = r.WorkflowPath
		}
	}
	if found == "" {
		return ".github/workflows/" + wf, nil
	}
	return found, nil
}

// ListWorkflows lists the workflow files of the default branch and the
// ones that ran in a repository (here or, imported, on GitHub), by path.
func (s *Service) ListWorkflows(ctx context.Context, who auth.Principal, owner, name string) ([]Workflow, error) {
	d, repo, err := s.repoFor(ctx, who, owner, name, authz.ActRead)
	if err != nil {
		return nil, err
	}
	byPath := map[string]*Workflow{}
	for _, f := range s.defaultWorkflows(ctx, d, repo) {
		w := &Workflow{Path: f.Path, OnDefaultBranch: true}
		if pw, err := parseWorkflow(f.Path, f.Content); err == nil {
			w.Name = pw.workflowName()
		}
		byPath[f.Path] = w
	}
	ran := func(p, n string, at time.Time) {
		w, ok := byPath[p]
		if !ok {
			w = &Workflow{Path: p, Name: n}
			byPath[p] = w
		}
		if w.Name == "" {
			w.Name = n
		}
		if w.LastRun == nil || at.After(*w.LastRun) {
			t := at
			w.LastRun = &t
		}
	}
	rows, err := s.q.ListActionsWorkflows(ctx, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	for _, r := range rows {
		ran(r.WorkflowPath, r.WorkflowName, r.CreatedAt.Time)
	}
	ext, err := s.q.ListActionsExternalWorkflows(ctx, repo.ID)
	if err != nil {
		return nil, fmt.Errorf("list external workflows: %w", err)
	}
	for _, r := range ext {
		ran(r.WorkflowPath, r.WorkflowName, r.CreatedAt.Time)
	}
	out := make([]Workflow, 0, len(byPath))
	for _, w := range byPath {
		out = append(out, *w)
	}
	slices.SortFunc(out, func(a, b Workflow) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

// defaultWorkflows reads the workflow files of the head of the default
// branch; none when the repository is empty or unreadable.
func (s *Service) defaultWorkflows(ctx context.Context, d *Deps, repo repos.Repo) []workflowFile {
	if repo.DefaultBranch == "" {
		return nil
	}
	head, err := d.Browse.Resolve(ctx, browseRepo(repo), "refs/heads/"+repo.DefaultBranch)
	if err != nil {
		return nil
	}
	files, err := s.discover(ctx, d.Browse, browseRepo(repo), head.Commit)
	if err != nil {
		s.log.Info("default branch workflows unknown", "repo_id", repo.ID, "err", err)
		return nil
	}
	return files
}

// ListRuns lists the runs of a repository, newest first, with the total
// count of the filter.
func (s *Service) ListRuns(ctx context.Context, who auth.Principal, owner, name string, f RunFilter) ([]Run, int64, error) {
	_, repo, err := s.repoFor(ctx, who, owner, name, authz.ActRead)
	if err != nil {
		return nil, 0, err
	}
	per := f.PerPage
	if per <= 0 {
		per = 30
	}
	per = min(per, 100)
	page := min(max(f.Page, 1), 1<<20)
	wf, err := s.workflowPath(ctx, repo.ID, f.Workflow)
	if err != nil {
		return nil, 0, err
	}
	ref := f.Ref
	if ref != "" && !strings.HasPrefix(ref, "refs/") {
		ref = "refs/heads/" + ref
	}
	from, to := tsArg(f.CreatedFrom), tsArg(f.CreatedTo)
	keys, err := s.q.ListActionsRunKeys(ctx, dbgen.ListActionsRunKeysParams{RepoID: repo.ID, WorkflowPath: wf, Event: f.Event,
		Status: f.Status, Ref: ref, Sha: f.Sha, Actor: f.Actor, CreatedFrom: from, CreatedTo: to, WithExternal: f.External,
		Lim: int32(per), Off: int32((page - 1) * per)}) // #nosec G115 -- per <= 100, page <= 2^20
	if err != nil {
		return nil, 0, fmt.Errorf("list runs: %w", err)
	}
	total, err := s.q.CountActionsRuns(ctx, dbgen.CountActionsRunsParams{RepoID: repo.ID, WorkflowPath: wf, Event: f.Event,
		Status: f.Status, Ref: ref, Sha: f.Sha, Actor: f.Actor, CreatedFrom: from, CreatedTo: to})
	if err != nil {
		return nil, 0, fmt.Errorf("count runs: %w", err)
	}
	if f.External {
		n, err := s.q.CountActionsExternalRuns(ctx, dbgen.CountActionsExternalRunsParams{RepoID: repo.ID, WorkflowPath: wf,
			Event: f.Event, Status: f.Status, Ref: ref, Sha: f.Sha, Actor: f.Actor, CreatedFrom: from, CreatedTo: to})
		if err != nil {
			return nil, 0, fmt.Errorf("count external runs: %w", err)
		}
		total += n
	}
	var local, external []uuid.UUID
	for _, k := range keys {
		if k.External {
			external = append(external, k.ID)
		} else {
			local = append(local, k.ID)
		}
	}
	byID := make(map[uuid.UUID]Run, len(keys))
	if len(local) > 0 {
		rows, err := s.q.ListActionsRunsByIDs(ctx, local)
		if err != nil {
			return nil, 0, fmt.Errorf("list runs: %w", err)
		}
		for _, r := range rows {
			byID[r.ID] = s.runFrom(ctx, repo, r)
		}
	}
	if len(external) > 0 {
		rows, err := s.q.ListActionsExternalRunsByIDs(ctx, external)
		if err != nil {
			return nil, 0, fmt.Errorf("list external runs: %w", err)
		}
		for _, r := range rows {
			byID[r.ID] = externalRunFrom(r)
		}
	}
	out := make([]Run, 0, len(keys))
	for _, k := range keys {
		if r, ok := byID[k.ID]; ok {
			out = append(out, r)
		}
	}
	return out, total, nil
}

// GetRun returns a run by its id, also a run imported from GitHub
// (External, read-only).
func (s *Service) GetRun(ctx context.Context, who auth.Principal, owner, name string, runID uuid.UUID) (Run, error) {
	_, repo, err := s.repoFor(ctx, who, owner, name, authz.ActRead)
	if err != nil {
		return Run{}, err
	}
	run, err := s.runByID(ctx, repo, runID)
	if errors.Is(err, ErrNotFound) {
		ext, xerr := s.q.ListActionsExternalRunsByIDs(ctx, []uuid.UUID{runID})
		if xerr != nil {
			return Run{}, fmt.Errorf("get external run: %w", xerr)
		}
		if len(ext) == 1 && ext[0].RepoID == repo.ID {
			return externalRunFrom(ext[0]), nil
		}
	}
	if err != nil {
		return Run{}, err
	}
	return s.runFrom(ctx, repo, run), nil
}

// tsArg is an optional time as a query argument.
func tsArg(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// RunWorkflow returns the workflow file a run was planned from.
func (s *Service) RunWorkflow(ctx context.Context, who auth.Principal, owner, name string, runID uuid.UUID) ([]byte, error) {
	_, repo, err := s.repoFor(ctx, who, owner, name, authz.ActRead)
	if err != nil {
		return nil, err
	}
	run, err := s.runByID(ctx, repo, runID)
	if err != nil {
		return nil, err
	}
	return run.Workflow, nil
}

// ListJobs lists the jobs of a run attempt (0: the latest).
func (s *Service) ListJobs(ctx context.Context, who auth.Principal, owner, name string, runID uuid.UUID, attempt int32) ([]Job, error) {
	_, repo, err := s.repoFor(ctx, who, owner, name, authz.ActRead)
	if err != nil {
		return nil, err
	}
	run, err := s.runByID(ctx, repo, runID)
	if err != nil {
		return nil, err
	}
	if attempt <= 0 {
		attempt = run.Attempt
	}
	if attempt > run.Attempt {
		return nil, ErrNotFound
	}
	rows, err := s.q.ListActionsJobs(ctx, dbgen.ListActionsJobsParams{RunID: run.ID, Attempt: attempt})
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	out := make([]Job, 0, len(rows))
	for _, j := range rows {
		out = append(out, s.jobFrom(ctx, repo, run, j))
	}
	return out, nil
}

// GetJob returns one job with its steps.
func (s *Service) GetJob(ctx context.Context, who auth.Principal, owner, name string, jobID uuid.UUID) (Job, []Step, error) {
	_, repo, err := s.repoFor(ctx, who, owner, name, authz.ActRead)
	if err != nil {
		return Job{}, nil, err
	}
	j, run, err := s.jobOf(ctx, repo, jobID)
	if err != nil {
		return Job{}, nil, err
	}
	rows, err := s.q.ListActionsSteps(ctx, j.ID)
	if err != nil {
		return Job{}, nil, fmt.Errorf("list steps: %w", err)
	}
	steps := make([]Step, 0, len(rows))
	for _, st := range rows {
		steps = append(steps, Step{Index: st.Idx, Name: st.Name, Status: st.Status, Conclusion: st.Conclusion,
			LogIndex: st.LogIndex, LogLength: st.LogLength, StartedAt: timePtr(st.StartedAt.Time, st.StartedAt.Valid),
			CompletedAt: timePtr(st.CompletedAt.Time, st.CompletedAt.Valid)})
	}
	return s.jobFrom(ctx, repo, run, j), steps, nil
}

// ReadLog returns up to limit log rows of a job from index from, and
// whether the log is complete (the job ended and every row was read).
func (s *Service) ReadLog(ctx context.Context, who auth.Principal, owner, name string, jobID uuid.UUID, from int64, limit int) ([]LogLine, bool, error) {
	_, repo, err := s.repoFor(ctx, who, owner, name, authz.ActRead)
	if err != nil {
		return nil, false, err
	}
	j, _, err := s.jobOf(ctx, repo, jobID)
	if err != nil {
		return nil, false, err
	}
	lines, err := s.readLog(ctx, j, from, limit)
	if err != nil {
		return nil, false, err
	}
	next := from
	if n := len(lines); n > 0 {
		next = lines[n-1].Index + 1
	}
	return lines, j.Status == "completed" && next >= j.LogLength, nil
}

// WaitLog blocks until the log of a job grows past from, the job ends or
// d elapses (log streaming: call ReadLog again after it returns).
func (s *Service) WaitLog(ctx context.Context, who auth.Principal, owner, name string, jobID uuid.UUID, from int64, d time.Duration) error {
	_, repo, err := s.repoFor(ctx, who, owner, name, authz.ActRead)
	if err != nil {
		return err
	}
	deadline := s.now().Add(d)
	for {
		ch := s.hub.logChanged(jobID)
		j, _, err := s.jobOf(ctx, repo, jobID)
		if err != nil {
			return err
		}
		if j.LogLength > from || j.Status == "completed" {
			return nil
		}
		left := deadline.Sub(s.now())
		if left <= 0 || ctx.Err() != nil {
			return ctx.Err()
		}
		s.wait(ctx, ch, left)
	}
}

// RawLog streams the whole log of a job as text; the caller closes it.
func (s *Service) RawLog(ctx context.Context, who auth.Principal, owner, name string, jobID uuid.UUID) (io.ReadCloser, error) {
	_, repo, err := s.repoFor(ctx, who, owner, name, authz.ActRead)
	if err != nil {
		return nil, err
	}
	j, _, err := s.jobOf(ctx, repo, jobID)
	if err != nil {
		return nil, err
	}
	return s.rawLog(ctx, j)
}

// CancelRun cancels a run that is not completed.
func (s *Service) CancelRun(ctx context.Context, who auth.Principal, owner, name string, runID uuid.UUID) error {
	_, repo, err := s.repoFor(ctx, who, owner, name, authz.ActPush)
	if err != nil {
		return err
	}
	run, err := s.runByID(ctx, repo, runID)
	if err != nil {
		return err
	}
	if run.Status == "completed" {
		return ErrConflict
	}
	return s.cancelRun(ctx, run.ID, who.Actor())
}

// CancelJob cancels one job of a run.
func (s *Service) CancelJob(ctx context.Context, who auth.Principal, owner, name string, jobID uuid.UUID) error {
	_, repo, err := s.repoFor(ctx, who, owner, name, authz.ActPush)
	if err != nil {
		return err
	}
	j, _, err := s.jobOf(ctx, repo, jobID)
	if err != nil {
		return err
	}
	if j.Status == "completed" {
		return ErrConflict
	}
	return s.cancelJob(ctx, j.ID)
}

// RerunRun starts a new attempt of a completed run with every job, from
// the workflow file the run was planned from.
func (s *Service) RerunRun(ctx context.Context, who auth.Principal, owner, name string, runID uuid.UUID) (Run, error) {
	if who.UserID == uuid.Nil && who.Installation == nil {
		return Run{}, ErrForbidden
	}
	_, repo, err := s.repoFor(ctx, who, owner, name, authz.ActPush)
	if err != nil {
		return Run{}, err
	}
	run, err := s.runByID(ctx, repo, runID)
	if err != nil {
		return Run{}, err
	}
	if run.Status != "completed" {
		return Run{}, ErrConflict
	}
	pw, err := parseWorkflow(run.WorkflowPath, run.Workflow)
	if err != nil {
		return Run{}, invalid("%s: %v", run.WorkflowPath, err)
	}
	var ev struct {
		Inputs map[string]any `json:"inputs"`
	}
	_ = json.Unmarshal(run.EventPayload, &ev)
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		locked, err := q.GetActionsRunForUpdate(ctx, run.ID)
		if err != nil {
			return fmt.Errorf("lock run: %w", err)
		}
		if locked.Status != "completed" {
			return ErrConflict
		}
		locked.Attempt++
		if err := q.StartActionsRunAttempt(ctx, dbgen.StartActionsRunAttemptParams{Attempt: locked.Attempt,
			TriggerActorID: nullID(who.Actor()), ID: locked.ID}); err != nil {
			return fmt.Errorf("start attempt: %w", err)
		}
		run = locked
		return s.insertTemplates(ctx, q, locked, pw, jobExtra{Inputs: ev.Inputs, Path: run.WorkflowPath}, defaultPermissions())
	})
	if err != nil {
		return Run{}, err
	}
	s.log.Info("workflow run re-run", "run_id", run.ID, "attempt", run.Attempt, "by", who.Username)
	if err := s.advance(ctx, run.ID); err != nil {
		s.log.Warn("advance re-run", "run_id", run.ID, "err", err)
	}
	s.enqueue(ctx, ReportArgs{RunID: run.ID})
	if run, err = s.q.GetActionsRun(ctx, run.ID); err != nil {
		return Run{}, fmt.Errorf("get run: %w", err)
	}
	return s.runFrom(ctx, repo, run), nil
}

// ApproveRun lets a run that waits for approval (a pull request from a
// fork) start. Writers of the repository may approve.
func (s *Service) ApproveRun(ctx context.Context, who auth.Principal, owner, name string, runID uuid.UUID) error {
	if who.UserID == uuid.Nil || who.Grant != nil {
		return ErrForbidden
	}
	_, repo, err := s.repoFor(ctx, who, owner, name, authz.ActPush)
	if err != nil {
		return err
	}
	run, err := s.runByID(ctx, repo, runID)
	if err != nil {
		return err
	}
	if !run.NeedsApproval || run.Status != "waiting" {
		return ErrConflict
	}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		locked, err := q.GetActionsRunForUpdate(ctx, run.ID)
		if err != nil {
			return fmt.Errorf("lock run: %w", err)
		}
		if !locked.NeedsApproval || locked.Status != "waiting" {
			return ErrConflict
		}
		if err := q.ApproveActionsRun(ctx, dbgen.ApproveActionsRunParams{ApprovedBy: nullID(who.UserID), ID: run.ID}); err != nil {
			return fmt.Errorf("approve run: %w", err)
		}
		// The concurrency group is checked again by advance.
		status := "queued"
		if locked.ConcurrencyGroup != "" {
			status = "pending"
		}
		if err := q.SetActionsRunStatus(ctx, dbgen.SetActionsRunStatusParams{Status: status, ID: run.ID}); err != nil {
			return fmt.Errorf("start run: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.log.Info("workflow run approved", "run_id", run.ID, "by", who.Username)
	if err := s.advance(ctx, run.ID); err != nil {
		s.log.Warn("advance approved run", "run_id", run.ID, "err", err)
	}
	s.enqueue(ctx, ReportArgs{RunID: run.ID})
	return nil
}

// ApproveDeployment approves a job waiting for a protected environment,
// or rejects it (the job is cancelled).
func (s *Service) ApproveDeployment(ctx context.Context, who auth.Principal, owner, name string, jobID uuid.UUID, approve bool) error {
	_, repo, err := s.repoFor(ctx, who, owner, name, authz.ActRead)
	if err != nil {
		return err
	}
	j, _, err := s.jobOf(ctx, repo, jobID)
	if err != nil {
		return err
	}
	if j.Status != "waiting" || j.Environment == "" || j.ApprovedBy.Valid {
		return ErrConflict
	}
	env, err := s.q.GetActionsEnvironment(ctx, dbgen.GetActionsEnvironmentParams{RepoID: repo.ID, Name: j.Environment})
	if err != nil && !isNoRows(err) {
		return fmt.Errorf("get environment: %w", err)
	}
	if !canApprove(who, repo, env) {
		return ErrForbidden
	}
	if !approve {
		s.log.Info("deployment rejected", "job_id", j.ID, "environment", j.Environment, "by", who.Username)
		return s.cancelJob(ctx, j.ID)
	}
	if err := s.q.ApproveActionsJob(ctx, dbgen.ApproveActionsJobParams{ApprovedBy: nullID(who.UserID), ID: j.ID}); err != nil {
		return fmt.Errorf("approve job: %w", err)
	}
	s.log.Info("deployment approved", "job_id", j.ID, "environment", j.Environment, "by", who.Username)
	if err := s.advance(ctx, j.RunID); err != nil {
		s.log.Warn("advance approved job", "run_id", j.RunID, "err", err)
	}
	s.enqueue(ctx, ReportArgs{RunID: j.RunID})
	return nil
}

// Badge is the SVG status badge of the latest completed run of a workflow
// on a branch (empty: the default branch).
func (s *Service) Badge(ctx context.Context, who auth.Principal, owner, name, workflow, branch string) ([]byte, error) {
	_, repo, err := s.repoFor(ctx, who, owner, name, authz.ActRead)
	if err != nil {
		return nil, err
	}
	wf, err := s.workflowPath(ctx, repo.ID, workflow)
	if err != nil {
		return nil, err
	}
	if branch == "" {
		branch = repo.DefaultBranch
	}
	ref := branch
	if !strings.HasPrefix(ref, "refs/") {
		ref = "refs/heads/" + ref
	}
	label, status, color := strings.TrimSuffix(path.Base(wf), path.Ext(wf)), "no status", "#9f9f9f"
	run, err := s.q.LatestActionsRun(ctx, dbgen.LatestActionsRunParams{RepoID: repo.ID, WorkflowPath: wf, Ref: ref})
	switch {
	case err == nil:
		label = run.WorkflowName
		switch run.Conclusion {
		case "success":
			status, color = "passing", "#4c1"
		case "failure", "timed_out":
			status, color = "failing", "#e05d44"
		case "cancelled":
			status, color = "cancelled", "#9f9f9f"
		default:
			status = run.Conclusion
		}
	case !isNoRows(err):
		return nil, fmt.Errorf("latest run: %w", err)
	}
	return badgeSVG(label, status, color), nil
}

// badgeSVG draws a flat two-part badge; widths are estimated at 6.5px per
// character of Verdana 11px.
func badgeSVG(label, status, color string) []byte {
	label = truncate(label, 64)
	w := func(s string) int { return utf8.RuneCountInString(s)*13/2 + 12 }
	lw, sw := w(label), w(status)
	l, st := html.EscapeString(label), html.EscapeString(status)
	return fmt.Appendf(nil, `<svg xmlns="http://www.w3.org/2000/svg" width="%[1]d" height="20" role="img" aria-label="%[4]s: %[5]s">`+
		`<title>%[4]s: %[5]s</title><linearGradient id="s" x2="0" y2="100%%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/>`+
		`<stop offset="1" stop-opacity=".1"/></linearGradient><clipPath id="r"><rect width="%[1]d" height="20" rx="3" fill="#fff"/></clipPath>`+
		`<g clip-path="url(#r)"><rect width="%[2]d" height="20" fill="#555"/><rect x="%[2]d" width="%[3]d" height="20" fill="%[6]s"/>`+
		`<rect width="%[1]d" height="20" fill="url(#s)"/></g><g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11">`+
		`<text x="%[7]d" y="14">%[4]s</text><text x="%[8]d" y="14">%[5]s</text></g></svg>`,
		lw+sw, lw, sw, l, st, color, lw/2, lw+sw/2)
}

// IsNotFound reports whether err means "not found" for the HTTP layer.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) || errors.Is(err, errNoWorkflow) }
