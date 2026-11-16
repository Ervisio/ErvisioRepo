package ghapi

import (
	"archive/zip"
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/actions"
	"github.com/Ervisio/ErvisioRepo/internal/actions/artifacts"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

// GitHub Actions REST endpoints over the Ervisio Actions service. Runs and
// jobs keep their UUIDs behind numeric ids; a workflow is identified by
// its path, with a numeric id derived from the repository and the path.
// Runs imported from GitHub (actions.Run.External) are listed read-only:
// their html_url is the run on GitHub and they carry "external": true.

func (c *call) actionsSvc() (Actions, error) {
	if c.s.d.Actions == nil {
		return nil, errf(http.StatusNotFound, "Actions are not enabled on this server")
	}
	return c.s.d.Actions, nil
}

// workflowUUID is the stable UUID behind the numeric id of a workflow.
func workflowUUID(repoID uuid.UUID, p string) uuid.UUID {
	return uuid.NewSHA1(repoID, []byte("workflow:"+p))
}

type ghWorkflow struct {
	ID        int64  `json:"id"`
	NodeID    string `json:"node_id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	State     string `json:"state"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	URL       string `json:"url"`
	HTMLURL   string `json:"html_url"`
	BadgeURL  string `json:"badge_url"`
}

func (c *call) workflowObj(r repos.Repo, w actions.Workflow) ghWorkflow {
	id := workflowUUID(r.ID, w.Path)
	at := r.CreatedAt
	if w.LastRun != nil {
		at = *w.LastRun
	}
	name := w.Name
	if name == "" {
		name = w.Path
	}
	return ghWorkflow{ID: c.ids.of(id), NodeID: nodeID("Workflow", id), Name: name, Path: w.Path, State: "active",
		CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(at), URL: c.repoAPI() + "/actions/workflows/" + strconv.FormatInt(c.ids.of(id), 10),
		HTMLURL:  c.repoHTML() + "/blob/" + escPath(r.DefaultBranch) + "/" + escPath(w.Path),
		BadgeURL: c.s.base + "/api/v1/repos/" + esc(r.Owner) + "/" + esc(r.Name) + "/actions/workflows/" + esc(path.Base(w.Path)) + "/badge.svg"}
}

// workflowParam resolves the {workflow} path parameter: a numeric id, a
// file name or a path.
func (c *call) workflowParam(svc Actions, r repos.Repo) (actions.Workflow, error) {
	p := chi.URLParam(c.r, "workflow")
	list, err := svc.ListWorkflows(c.ctx, c.who, c.owner, c.repo)
	if err != nil {
		return actions.Workflow{}, err
	}
	if n, err := strconv.ParseInt(p, 10, 64); err == nil && n > 0 {
		id, err := c.ids.parse(p)
		if err != nil {
			return actions.Workflow{}, errNotFound
		}
		for _, w := range list {
			if workflowUUID(r.ID, w.Path) == id {
				return w, nil
			}
		}
		return actions.Workflow{}, errNotFound
	}
	for _, w := range list {
		if w.Path == p || path.Base(w.Path) == p {
			return w, nil
		}
	}
	return actions.Workflow{}, errNotFound
}

func (c *call) listWorkflows() error {
	svc, err := c.actionsSvc()
	if err != nil {
		return err
	}
	r, err := c.loadRepo()
	if err != nil {
		return err
	}
	list, err := svc.ListWorkflows(c.ctx, c.who, c.owner, c.repo)
	if err != nil {
		return err
	}
	p := c.pager()
	page, more := slice(p, list)
	us := make([]uuid.UUID, len(page))
	for i, w := range page {
		us[i] = workflowUUID(r.ID, w.Path)
	}
	c.ids.prefetch(us...)
	out := make([]ghWorkflow, 0, len(page))
	for _, w := range page {
		out = append(out, c.workflowObj(r, w))
	}
	c.link(p, more)
	return c.ok(http.StatusOK, map[string]any{"total_count": len(list), "workflows": out})
}

func (c *call) getWorkflow() error {
	svc, err := c.actionsSvc()
	if err != nil {
		return err
	}
	r, err := c.loadRepo()
	if err != nil {
		return err
	}
	w, err := c.workflowParam(svc, r)
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.workflowObj(r, w))
}

func (c *call) dispatchWorkflow() error {
	svc, err := c.actionsSvc()
	if err != nil {
		return err
	}
	r, err := c.loadRepoWrite()
	if err != nil {
		return err
	}
	w, err := c.workflowParam(svc, r)
	if err != nil {
		return err
	}
	var b struct {
		Ref    string         `json:"ref"`
		Inputs map[string]any `json:"inputs"`
	}
	if err := c.decode(&b); err != nil {
		return err
	}
	if b.Ref == "" {
		b.Ref = r.DefaultBranch
	}
	if _, err := svc.Dispatch(c.ctx, c.who, c.owner, c.repo, w.Path, b.Ref, b.Inputs); err != nil {
		return err
	}
	return c.noContent()
}

func (c *call) workflowRuns() error {
	svc, err := c.actionsSvc()
	if err != nil {
		return err
	}
	r, err := c.loadRepo()
	if err != nil {
		return err
	}
	w, err := c.workflowParam(svc, r)
	if err != nil {
		return err
	}
	return c.listRunsOf(svc, r, w.Path)
}

// ---- runs ----

type ghRunCommit struct {
	ID      string `json:"id"`
	TreeID  string `json:"tree_id"`
	Message string `json:"message"`
}

type ghRun struct {
	ID                 int64        `json:"id"`
	Name               string       `json:"name"`
	NodeID             string       `json:"node_id"`
	HeadBranch         *string      `json:"head_branch"`
	HeadSHA            string       `json:"head_sha"`
	Path               string       `json:"path"`
	DisplayTitle       string       `json:"display_title"`
	RunNumber          int64        `json:"run_number"`
	Event              string       `json:"event"`
	Status             string       `json:"status"`
	Conclusion         *string      `json:"conclusion"`
	WorkflowID         int64        `json:"workflow_id"`
	CheckSuiteID       *int64       `json:"check_suite_id"`
	URL                string       `json:"url"`
	HTMLURL            string       `json:"html_url"`
	PullRequests       []any        `json:"pull_requests"`
	CreatedAt          string       `json:"created_at"`
	UpdatedAt          string       `json:"updated_at"`
	Actor              *ghUser      `json:"actor"`
	TriggeringActor    *ghUser      `json:"triggering_actor"`
	RunAttempt         int32        `json:"run_attempt"`
	RunStartedAt       *string      `json:"run_started_at"`
	JobsURL            string       `json:"jobs_url"`
	LogsURL            string       `json:"logs_url"`
	CheckSuiteURL      string       `json:"check_suite_url"`
	ArtifactsURL       string       `json:"artifacts_url"`
	CancelURL          string       `json:"cancel_url"`
	RerunURL           string       `json:"rerun_url"`
	WorkflowURL        string       `json:"workflow_url"`
	HeadCommit         *ghRunCommit `json:"head_commit"`
	Repository         *ghRepo      `json:"repository"`
	HeadRepository     *ghRepo      `json:"head_repository"`
	PreviousAttemptURL *string      `json:"previous_attempt_url"`
	// External marks a run imported from GitHub (not a GitHub field):
	// html_url is its page there and it has no jobs, logs or artifacts.
	External bool `json:"external,omitempty"`
}

func (c *call) runObj(r repos.Repo, run actions.Run) ghRun {
	self := c.repoAPI() + "/actions/runs/" + strconv.FormatInt(c.ids.of(run.ID), 10)
	wfID := workflowUUID(r.ID, run.WorkflowPath)
	name := run.WorkflowName
	if name == "" {
		name = run.WorkflowPath
	}
	var branch *string
	if b, ok := strings.CutPrefix(run.Ref, "refs/heads/"); ok {
		branch = &b
	} else if t, ok := strings.CutPrefix(run.Ref, "refs/tags/"); ok {
		branch = &t
	}
	repo := c.repoObj(r)
	start := run.StartedAt
	if start == nil {
		t := run.CreatedAt
		start = &t
	}
	out := ghRun{ID: c.ids.of(run.ID), Name: name, NodeID: nodeID("WorkflowRun", run.ID), HeadBranch: branch,
		HeadSHA: run.Sha, Path: run.WorkflowPath, DisplayTitle: run.Title, RunNumber: run.RunNumber, Event: run.Event,
		Status: run.Status, Conclusion: strp(run.Conclusion), WorkflowID: c.ids.of(wfID), URL: self,
		HTMLURL: run.URL, PullRequests: []any{}, CreatedAt: ts(run.CreatedAt), UpdatedAt: ts(run.UpdatedAt),
		Actor: c.userByName(run.Actor), TriggeringActor: c.userByName(run.TriggerActor), RunAttempt: max(run.Attempt, 1),
		RunStartedAt: tsp(start), JobsURL: self + "/jobs", LogsURL: self + "/logs", ArtifactsURL: self + "/artifacts",
		CancelURL: self + "/cancel", RerunURL: self + "/rerun",
		WorkflowURL: c.repoAPI() + "/actions/workflows/" + strconv.FormatInt(c.ids.of(wfID), 10),
		HeadCommit:  &ghRunCommit{ID: run.Sha, Message: run.Title}, Repository: repo, HeadRepository: repo,
		External: run.External}
	if run.External {
		out.HTMLURL = run.HTMLURL
	}
	if run.Attempt > 1 {
		prev := self + "/attempts/" + strconv.Itoa(int(run.Attempt-1))
		out.PreviousAttemptURL = &prev
	}
	return out
}

// runParam resolves the {run} path parameter.
func (c *call) runParam() (uuid.UUID, error) { return c.ids.parse(chi.URLParam(c.r, "run")) }

// parseCreated parses the created filter of a run listing: a date or time,
// optionally prefixed with >, >=, < or <=, or a range a..b (* open).
func parseCreated(s string) (from, to *time.Time, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil, nil
	}
	parse := func(v string, end bool) (*time.Time, error) {
		if v == "*" || v == "" {
			return nil, nil //nolint:nilnil // open bound
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			if end {
				t = t.Add(time.Second)
			}
			return &t, nil
		}
		t, err := time.Parse(time.DateOnly, v)
		if err != nil {
			return nil, errf(http.StatusUnprocessableEntity, "Validation Failed: invalid created filter %q", s)
		}
		if end {
			t = t.AddDate(0, 0, 1)
		}
		return &t, nil
	}
	if a, b, ok := strings.Cut(s, ".."); ok {
		if from, err = parse(a, false); err != nil {
			return nil, nil, err
		}
		to, err = parse(b, true)
		return from, to, err
	}
	switch {
	case strings.HasPrefix(s, ">="):
		from, err = parse(s[2:], false)
	case strings.HasPrefix(s, ">"):
		from, err = parse(s[1:], true)
	case strings.HasPrefix(s, "<="):
		to, err = parse(s[2:], true)
	case strings.HasPrefix(s, "<"):
		to, err = parse(s[1:], false)
	default:
		if from, err = parse(s, false); err == nil {
			to, err = parse(s, true)
		}
	}
	return from, to, err
}

func (c *call) listRuns() error {
	svc, err := c.actionsSvc()
	if err != nil {
		return err
	}
	r, err := c.loadRepo()
	if err != nil {
		return err
	}
	return c.listRunsOf(svc, r, "")
}

func (c *call) listRunsOf(svc Actions, r repos.Repo, workflow string) error {
	p := c.pager()
	from, to, err := parseCreated(c.query("created"))
	if err != nil {
		return err
	}
	f := actions.RunFilter{Workflow: workflow, Event: c.query("event"), Status: c.query("status"),
		Ref: c.query("branch"), Sha: c.query("head_sha"), Actor: c.query("actor"), CreatedFrom: from, CreatedTo: to,
		Page: p.page, PerPage: p.per, External: true}
	list, total, err := svc.ListRuns(c.ctx, c.who, c.owner, c.repo, f)
	if err != nil {
		return err
	}
	us := []uuid.UUID{r.ID, r.OwnerID}
	var names []string
	for _, run := range list {
		us = append(us, run.ID, workflowUUID(r.ID, run.WorkflowPath))
		names = append(names, run.Actor, run.TriggerActor)
	}
	c.ids.prefetch(us...)
	c.ids.prefetchUsers(names...)
	out := make([]ghRun, 0, len(list))
	for _, run := range list {
		out = append(out, c.runObj(r, run))
	}
	c.link(p, int64(p.offset()+len(list)) < total)
	return c.ok(http.StatusOK, map[string]any{"total_count": total, "workflow_runs": out})
}

func (c *call) getRun() error {
	svc, err := c.actionsSvc()
	if err != nil {
		return err
	}
	r, err := c.loadRepo()
	if err != nil {
		return err
	}
	id, err := c.runParam()
	if err != nil {
		return err
	}
	run, err := svc.GetRun(c.ctx, c.who, c.owner, c.repo, id)
	if err != nil {
		return err
	}
	if n := chi.URLParam(c.r, "attempt"); n != "" {
		a, err := strconv.Atoi(n)
		if err != nil || a < 1 || a > int(max(run.Attempt, 1)) {
			return errNotFound
		}
		run.Attempt = int32(a) //nolint:gosec // bounded by the run attempt
	}
	return c.ok(http.StatusOK, c.runObj(r, run))
}

// runControl runs a control action on the {run} of the path.
func (c *call) runControl(fn func(svc Actions, id uuid.UUID) error, status int) error {
	svc, err := c.actionsSvc()
	if err != nil {
		return err
	}
	if _, err := c.loadRepoWrite(); err != nil {
		return err
	}
	id, err := c.runParam()
	if err != nil {
		return err
	}
	if err := fn(svc, id); err != nil {
		return err
	}
	if status == http.StatusNoContent {
		return c.noContent()
	}
	return c.ok(status, map[string]any{})
}

func (c *call) cancelRun() error {
	return c.runControl(func(svc Actions, id uuid.UUID) error {
		return svc.CancelRun(c.ctx, c.who, c.owner, c.repo, id)
	}, http.StatusAccepted)
}

func (c *call) rerunRun() error {
	return c.runControl(func(svc Actions, id uuid.UUID) error {
		_, err := svc.RerunRun(c.ctx, c.who, c.owner, c.repo, id)
		return err
	}, http.StatusCreated)
}

func (c *call) rerunFailedJobs() error {
	return errf(http.StatusUnprocessableEntity,
		"Re-running only the failed jobs is not supported by this server; re-run the whole workflow run")
}

// ---- jobs ----

type ghStep struct {
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	Conclusion  *string `json:"conclusion"`
	Number      int32   `json:"number"`
	StartedAt   *string `json:"started_at"`
	CompletedAt *string `json:"completed_at"`
}

type ghJob struct {
	ID              int64    `json:"id"`
	RunID           int64    `json:"run_id"`
	RunURL          string   `json:"run_url"`
	RunAttempt      int32    `json:"run_attempt"`
	NodeID          string   `json:"node_id"`
	HeadSHA         string   `json:"head_sha"`
	HeadBranch      *string  `json:"head_branch"`
	URL             string   `json:"url"`
	HTMLURL         string   `json:"html_url"`
	Status          string   `json:"status"`
	Conclusion      *string  `json:"conclusion"`
	CreatedAt       string   `json:"created_at"`
	StartedAt       *string  `json:"started_at"`
	CompletedAt     *string  `json:"completed_at"`
	Name            string   `json:"name"`
	Steps           []ghStep `json:"steps"`
	CheckRunURL     string   `json:"check_run_url"`
	Labels          []string `json:"labels"`
	RunnerID        *int64   `json:"runner_id"`
	RunnerName      *string  `json:"runner_name"`
	RunnerGroupID   *int64   `json:"runner_group_id"`
	RunnerGroupName *string  `json:"runner_group_name"`
	WorkflowName    string   `json:"workflow_name"`
}

// jobStatus maps a job status to GitHub's: blocked jobs wait for their
// needs, which GitHub reports as pending.
func jobStatus(s string) string {
	if s == "blocked" {
		return "pending"
	}
	return s
}

func (c *call) jobObj(run actions.Run, j actions.Job, steps []actions.Step) ghJob {
	runSelf := c.repoAPI() + "/actions/runs/" + strconv.FormatInt(c.ids.of(run.ID), 10)
	self := c.repoAPI() + "/actions/jobs/" + strconv.FormatInt(c.ids.of(j.ID), 10)
	var branch *string
	if b, ok := strings.CutPrefix(run.Ref, "refs/heads/"); ok {
		branch = &b
	}
	created := run.CreatedAt
	out := ghJob{ID: c.ids.of(j.ID), RunID: c.ids.of(run.ID), RunURL: runSelf, RunAttempt: max(run.Attempt, 1),
		NodeID: nodeID("WorkflowJob", j.ID), HeadSHA: run.Sha, HeadBranch: branch, URL: self, HTMLURL: j.URL,
		Status: jobStatus(j.Status), Conclusion: strp(j.Conclusion), CreatedAt: ts(created), StartedAt: tsp(j.StartedAt),
		CompletedAt: tsp(j.CompletedAt), Name: j.Name, Steps: make([]ghStep, 0, len(steps)), Labels: j.RunsOn,
		RunnerName: strp(j.Runner), WorkflowName: run.WorkflowName}
	if out.Labels == nil {
		out.Labels = []string{}
	}
	for _, st := range steps {
		out.Steps = append(out.Steps, ghStep{Name: st.Name, Status: st.Status, Conclusion: strp(st.Conclusion),
			Number: st.Index + 1, StartedAt: tsp(st.StartedAt), CompletedAt: tsp(st.CompletedAt)})
	}
	return out
}

func (c *call) listRunJobs() error {
	svc, err := c.actionsSvc()
	if err != nil {
		return err
	}
	if err := c.read(); err != nil {
		return err
	}
	id, err := c.runParam()
	if err != nil {
		return err
	}
	run, err := svc.GetRun(c.ctx, c.who, c.owner, c.repo, id)
	if err != nil {
		return err
	}
	if run.External {
		return c.ok(http.StatusOK, map[string]any{"total_count": 0, "jobs": []ghJob{}})
	}
	var attempt int32
	if n := chi.URLParam(c.r, "attempt"); n != "" {
		a, err := strconv.Atoi(n)
		if err != nil || a < 1 || a > int(run.Attempt) {
			return errNotFound
		}
		attempt = int32(a) //nolint:gosec // bounded by the run attempt
		run.Attempt = attempt
	}
	jobs, err := svc.ListJobs(c.ctx, c.who, c.owner, c.repo, id, attempt)
	if err != nil {
		return err
	}
	p := c.pager()
	page, more := slice(p, jobs)
	us := []uuid.UUID{run.ID}
	for _, j := range page {
		us = append(us, j.ID)
	}
	c.ids.prefetch(us...)
	out := make([]ghJob, 0, len(page))
	for _, j := range page {
		_, steps, err := svc.GetJob(c.ctx, c.who, c.owner, c.repo, j.ID)
		if err != nil {
			return err
		}
		out = append(out, c.jobObj(run, j, steps))
	}
	c.link(p, more)
	return c.ok(http.StatusOK, map[string]any{"total_count": len(jobs), "jobs": out})
}

// jobParam loads the {job} of the path with its run and steps.
func (c *call) jobParam(svc Actions) (actions.Job, []actions.Step, actions.Run, error) {
	id, err := c.ids.parse(chi.URLParam(c.r, "job"))
	if err != nil {
		return actions.Job{}, nil, actions.Run{}, err
	}
	j, steps, err := svc.GetJob(c.ctx, c.who, c.owner, c.repo, id)
	if err != nil {
		return actions.Job{}, nil, actions.Run{}, err
	}
	run, err := svc.GetRun(c.ctx, c.who, c.owner, c.repo, j.RunID)
	if err != nil {
		return actions.Job{}, nil, actions.Run{}, err
	}
	return j, steps, run, nil
}

func (c *call) getJob() error {
	svc, err := c.actionsSvc()
	if err != nil {
		return err
	}
	if err := c.read(); err != nil {
		return err
	}
	j, steps, run, err := c.jobParam(svc)
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.jobObj(run, j, steps))
}

// jobLogs answers the log of a job as plain text (GitHub redirects to a
// download URL; clients follow either).
func (c *call) jobLogs() error {
	svc, err := c.actionsSvc()
	if err != nil {
		return err
	}
	if err := c.read(); err != nil {
		return err
	}
	j, _, _, err := c.jobParam(svc)
	if err != nil {
		return err
	}
	rc, err := svc.RawLog(c.ctx, c.who, c.owner, c.repo, j.ID)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	c.w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	c.w.Header().Set("Content-Disposition", `attachment; filename="job-logs.txt"`)
	if _, err := io.Copy(c.w, rc); err != nil {
		c.s.d.Log.WarnContext(c.ctx, "ghapi: job log interrupted", "job", j.ID, "error", err)
	}
	return nil
}

// zipName makes a job or step name safe as a zip path element, as GitHub
// does.
func zipName(s string) string {
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_", "\x00", "").Replace(strings.TrimSpace(s))
}

// runLogs answers a zip of the logs of the jobs of a run attempt in
// GitHub's layout: "<n>_<job>.txt" with the whole log of each job and
// "<job>/<step number>_<step>.txt" with the lines of each step.
func (c *call) runLogs() error {
	svc, err := c.actionsSvc()
	if err != nil {
		return err
	}
	if err := c.read(); err != nil {
		return err
	}
	id, err := c.runParam()
	if err != nil {
		return err
	}
	run, err := svc.GetRun(c.ctx, c.who, c.owner, c.repo, id)
	if err != nil {
		return err
	}
	if run.External {
		return errf(http.StatusNotFound, "The logs of this run are on GitHub: %s", run.HTMLURL)
	}
	jobs, err := svc.ListJobs(c.ctx, c.who, c.owner, c.repo, id, 0)
	if err != nil {
		return err
	}
	c.w.Header().Set("Content-Type", "application/zip")
	c.w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="logs_%d.zip"`, c.ids.of(run.ID)))
	if c.ids.err != nil {
		return c.ids.err
	}
	zw := zip.NewWriter(c.w)
	for i, j := range jobs {
		if err := c.zipJob(zw, svc, i, j); err != nil {
			// The answer has started: log and end the archive early.
			c.s.d.Log.WarnContext(c.ctx, "ghapi: run log archive interrupted", "run", run.ID, "job", j.ID, "error", err)
			break
		}
	}
	if err := zw.Close(); err != nil {
		c.s.d.Log.WarnContext(c.ctx, "ghapi: close run log archive", "run", run.ID, "error", err)
	}
	return nil
}

func (c *call) zipJob(zw *zip.Writer, svc Actions, i int, j actions.Job) error {
	name := zipName(j.Name)
	f, err := zw.Create(strconv.Itoa(i) + "_" + name + ".txt")
	if err != nil {
		return fmt.Errorf("zip entry: %w", err)
	}
	rc, err := svc.RawLog(c.ctx, c.who, c.owner, c.repo, j.ID)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, rc)
	_ = rc.Close()
	if err != nil {
		return fmt.Errorf("copy job log: %w", err)
	}
	_, steps, err := svc.GetJob(c.ctx, c.who, c.owner, c.repo, j.ID)
	if err != nil || len(steps) == 0 {
		return err
	}
	// One more pass over the log, cut at the row ranges of the steps.
	rc, err = svc.RawLog(c.ctx, c.who, c.owner, c.repo, j.ID)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var row int64
	for _, st := range steps {
		w, err := zw.Create(name + "/" + strconv.Itoa(int(st.Index)+1) + "_" + zipName(st.Name) + ".txt")
		if err != nil {
			return fmt.Errorf("zip entry: %w", err)
		}
		end := st.LogIndex + st.LogLength
		for row < end && sc.Scan() {
			if row >= st.LogIndex {
				if _, err := w.Write(append(sc.Bytes(), '\n')); err != nil {
					return fmt.Errorf("write step log: %w", err)
				}
			}
			row++
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read job log: %w", err)
	}
	return nil
}

// ---- artifacts ----

type ghArtifactRun struct {
	ID               int64   `json:"id"`
	RepositoryID     int64   `json:"repository_id"`
	HeadRepositoryID int64   `json:"head_repository_id"`
	HeadBranch       *string `json:"head_branch"`
	HeadSHA          string  `json:"head_sha"`
}

type ghArtifact struct {
	ID                 int64          `json:"id"`
	NodeID             string         `json:"node_id"`
	Name               string         `json:"name"`
	SizeInBytes        int64          `json:"size_in_bytes"`
	URL                string         `json:"url"`
	ArchiveDownloadURL string         `json:"archive_download_url"`
	Expired            bool           `json:"expired"`
	Digest             *string        `json:"digest"`
	CreatedAt          string         `json:"created_at"`
	UpdatedAt          string         `json:"updated_at"`
	ExpiresAt          string         `json:"expires_at"`
	WorkflowRun        *ghArtifactRun `json:"workflow_run"`
}

func (c *call) artifactObj(r repos.Repo, a artifacts.Artifact) ghArtifact {
	self := c.repoAPI() + "/actions/artifacts/" + strconv.FormatInt(a.ID, 10)
	rid := c.ids.of(r.ID)
	return ghArtifact{ID: a.ID, NodeID: nodeIDString("Artifact", strconv.FormatInt(a.ID, 10)), Name: a.Name,
		SizeInBytes: a.Size, URL: self, ArchiveDownloadURL: self + "/zip", Expired: a.Expired, Digest: strp(a.Digest),
		CreatedAt: ts(a.CreatedAt), UpdatedAt: ts(a.CreatedAt), ExpiresAt: ts(a.ExpiresAt),
		WorkflowRun: &ghArtifactRun{ID: c.ids.of(a.RunID), RepositoryID: rid, HeadRepositoryID: rid}}
}

func (c *call) artifactsSvc() (Artifacts, error) {
	if c.s.d.Artifacts == nil {
		return nil, errf(http.StatusNotFound, "Artifacts are not available on this server")
	}
	return c.s.d.Artifacts, nil
}

// listArtifacts lists the artifacts of a repository or, under
// /runs/{run}, of one run.
func (c *call) listArtifacts() error {
	r, err := c.loadRepo()
	if err != nil {
		return err
	}
	f := artifacts.ListFilter{Name: c.query("name")}
	if chi.URLParam(c.r, "run") != "" {
		if f.RunID, err = c.runParam(); err != nil {
			return err
		}
	}
	empty := map[string]any{"total_count": 0, "artifacts": []ghArtifact{}}
	svc, err := c.artifactsSvc()
	if err != nil {
		return c.ok(http.StatusOK, empty)
	}
	p := c.pager()
	f.Offset, f.Max = p.offset(), p.per
	list, total, err := svc.List(c.ctx, r.ID, f)
	if err != nil {
		return err
	}
	us := []uuid.UUID{r.ID}
	for _, a := range list {
		us = append(us, a.RunID)
	}
	c.ids.prefetch(us...)
	out := make([]ghArtifact, 0, len(list))
	for _, a := range list {
		out = append(out, c.artifactObj(r, a))
	}
	c.link(p, int64(p.offset()+len(list)) < total)
	return c.ok(http.StatusOK, map[string]any{"total_count": total, "artifacts": out})
}

// artifactParam parses the {artifact} id and loads the repository.
func (c *call) artifactParam(write bool) (Artifacts, repos.Repo, int64, error) {
	load := c.loadRepo
	if write {
		load = c.loadRepoWrite
	}
	r, err := load()
	if err != nil {
		return nil, repos.Repo{}, 0, err
	}
	act := authz.ActRead
	if write {
		act = authz.ActPush
	}
	if !authz.Allowed(r.Permission, act) {
		return nil, repos.Repo{}, 0, errf(http.StatusForbidden, "Resource not accessible: you do not have permission to do this")
	}
	svc, err := c.artifactsSvc()
	if err != nil {
		return nil, repos.Repo{}, 0, err
	}
	id, err := strconv.ParseInt(chi.URLParam(c.r, "artifact"), 10, 64)
	if err != nil || id <= 0 {
		return nil, repos.Repo{}, 0, errNotFound
	}
	return svc, r, id, nil
}

func (c *call) getArtifact() error {
	svc, r, id, err := c.artifactParam(false)
	if err != nil {
		return err
	}
	a, err := svc.Get(c.ctx, r.ID, id)
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, c.artifactObj(r, a))
}

// downloadArtifact redirects to a short-lived signed URL, as GitHub does.
func (c *call) downloadArtifact() error {
	svc, r, id, err := c.artifactParam(false)
	if err != nil {
		return err
	}
	u, err := svc.DownloadURL(c.ctx, r.ID, id)
	if err != nil {
		return err
	}
	http.Redirect(c.w, c.r, u, http.StatusFound)
	return nil
}

func (c *call) deleteArtifact() error {
	svc, r, id, err := c.artifactParam(true)
	if err != nil {
		return err
	}
	if err := svc.Delete(c.ctx, r.ID, id); err != nil {
		return err
	}
	return c.noContent()
}

// ---- secrets and variables ----

func (c *call) repoScope() (Actions, actions.Scope, error) {
	svc, err := c.actionsSvc()
	if err != nil {
		return nil, actions.Scope{}, err
	}
	sc, err := svc.RepoScope(c.ctx, c.who, c.owner, c.repo, "")
	return svc, sc, err
}

type ghSecret struct {
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func (c *call) listSecrets() error {
	if err := c.read(); err != nil {
		return err
	}
	svc, sc, err := c.repoScope()
	if err != nil {
		return err
	}
	list, err := svc.ListSecrets(c.ctx, sc)
	if err != nil {
		return err
	}
	p := c.pager()
	page, more := slice(p, list)
	out := make([]ghSecret, 0, len(page))
	for _, s := range page {
		out = append(out, ghSecret{Name: s.Name, CreatedAt: ts(s.CreatedAt), UpdatedAt: ts(s.UpdatedAt)})
	}
	c.link(p, more)
	return c.ok(http.StatusOK, map[string]any{"total_count": len(list), "secrets": out})
}

func (c *call) findSecret(svc Actions, sc actions.Scope, name string) (*actions.Secret, error) {
	list, err := svc.ListSecrets(c.ctx, sc)
	if err != nil {
		return nil, err
	}
	for _, s := range list {
		if strings.EqualFold(s.Name, name) {
			return &s, nil
		}
	}
	return nil, nil //nolint:nilnil // not found is no secret
}

func (c *call) getSecret() error {
	if err := c.read(); err != nil {
		return err
	}
	svc, sc, err := c.repoScope()
	if err != nil {
		return err
	}
	s, err := c.findSecret(svc, sc, chi.URLParam(c.r, "name"))
	if err != nil {
		return err
	}
	if s == nil {
		return errNotFound
	}
	return c.ok(http.StatusOK, ghSecret{Name: s.Name, CreatedAt: ts(s.CreatedAt), UpdatedAt: ts(s.UpdatedAt)})
}

func (c *call) secretsPublicKey() error {
	if err := c.read(); err != nil {
		return err
	}
	svc, _, err := c.repoScope()
	if err != nil {
		return err
	}
	id, key, err := svc.SecretsPublicKey()
	if err != nil {
		return err
	}
	return c.ok(http.StatusOK, map[string]string{"key_id": id, "key": base64.StdEncoding.EncodeToString(key)})
}

// putSecret stores a secret sealed for the public key (encrypted_value
// with key_id, as GitHub clients send it).
func (c *call) putSecret() error {
	if err := c.write(); err != nil {
		return err
	}
	svc, sc, err := c.repoScope()
	if err != nil {
		return err
	}
	var b struct {
		EncryptedValue string `json:"encrypted_value"`
		KeyID          string `json:"key_id"`
	}
	if err := c.decode(&b); err != nil {
		return err
	}
	sealed, err := base64.StdEncoding.DecodeString(b.EncryptedValue)
	if err != nil || b.EncryptedValue == "" {
		return errf(http.StatusUnprocessableEntity, "Validation Failed: encrypted_value must be a base64 sealed box")
	}
	value, err := svc.OpenSealedSecret(b.KeyID, sealed)
	if err != nil {
		return err
	}
	name := chi.URLParam(c.r, "name")
	old, err := c.findSecret(svc, sc, name)
	if err != nil {
		return err
	}
	if _, err := svc.SetSecret(c.ctx, c.who, sc, name, value); err != nil {
		return err
	}
	if old != nil {
		return c.noContent()
	}
	return c.ok(http.StatusCreated, map[string]any{})
}

func (c *call) deleteSecret() error {
	if err := c.write(); err != nil {
		return err
	}
	svc, sc, err := c.repoScope()
	if err != nil {
		return err
	}
	if err := svc.DeleteSecret(c.ctx, sc, chi.URLParam(c.r, "name")); err != nil {
		return err
	}
	return c.noContent()
}

type ghVariable struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func varObj(v actions.Variable) ghVariable {
	return ghVariable{Name: v.Name, Value: v.Value, CreatedAt: ts(v.CreatedAt), UpdatedAt: ts(v.UpdatedAt)}
}

func (c *call) findVariable(svc Actions, sc actions.Scope, name string) (*actions.Variable, error) {
	list, err := svc.ListVariables(c.ctx, sc)
	if err != nil {
		return nil, err
	}
	for _, v := range list {
		if strings.EqualFold(v.Name, name) {
			return &v, nil
		}
	}
	return nil, nil //nolint:nilnil // not found is no variable
}

func (c *call) listVariables() error {
	if err := c.read(); err != nil {
		return err
	}
	svc, sc, err := c.repoScope()
	if err != nil {
		return err
	}
	list, err := svc.ListVariables(c.ctx, sc)
	if err != nil {
		return err
	}
	p := c.pager()
	page, more := slice(p, list)
	out := make([]ghVariable, 0, len(page))
	for _, v := range page {
		out = append(out, varObj(v))
	}
	c.link(p, more)
	return c.ok(http.StatusOK, map[string]any{"total_count": len(list), "variables": out})
}

func (c *call) getVariable() error {
	if err := c.read(); err != nil {
		return err
	}
	svc, sc, err := c.repoScope()
	if err != nil {
		return err
	}
	v, err := c.findVariable(svc, sc, chi.URLParam(c.r, "name"))
	if err != nil {
		return err
	}
	if v == nil {
		return errNotFound
	}
	return c.ok(http.StatusOK, varObj(*v))
}

type variableBody struct {
	Name  *string `json:"name"`
	Value *string `json:"value"`
}

func (c *call) createVariable() error {
	if err := c.write(); err != nil {
		return err
	}
	svc, sc, err := c.repoScope()
	if err != nil {
		return err
	}
	var b variableBody
	if err := c.decode(&b); err != nil {
		return err
	}
	if deref(b.Name) == "" || b.Value == nil {
		return errf(http.StatusUnprocessableEntity, "Validation Failed: name and value are required")
	}
	old, err := c.findVariable(svc, sc, *b.Name)
	if err != nil {
		return err
	}
	if old != nil {
		return errf(http.StatusConflict, "Variable already exists")
	}
	if _, err := svc.SetVariable(c.ctx, c.who, sc, *b.Name, *b.Value); err != nil {
		return err
	}
	return c.ok(http.StatusCreated, map[string]any{})
}

func (c *call) updateVariable() error {
	if err := c.write(); err != nil {
		return err
	}
	svc, sc, err := c.repoScope()
	if err != nil {
		return err
	}
	var b variableBody
	if err := c.decode(&b); err != nil {
		return err
	}
	name := chi.URLParam(c.r, "name")
	old, err := c.findVariable(svc, sc, name)
	if err != nil {
		return err
	}
	if old == nil {
		return errNotFound
	}
	value, newName := old.Value, old.Name
	if b.Value != nil {
		value = *b.Value
	}
	if deref(b.Name) != "" {
		newName = *b.Name
	}
	if _, err := svc.SetVariable(c.ctx, c.who, sc, newName, value); err != nil {
		return err
	}
	if !strings.EqualFold(newName, old.Name) {
		if err := svc.DeleteVariable(c.ctx, sc, old.Name); err != nil {
			return err
		}
	}
	return c.noContent()
}

func (c *call) deleteVariable() error {
	if err := c.write(); err != nil {
		return err
	}
	svc, sc, err := c.repoScope()
	if err != nil {
		return err
	}
	if err := svc.DeleteVariable(c.ctx, sc, chi.URLParam(c.r, "name")); err != nil {
		return err
	}
	return c.noContent()
}
