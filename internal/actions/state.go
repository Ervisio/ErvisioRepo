package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"gitea.com/gitea/runner/act/model"
	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
)

// maxAdvancePasses bounds the planning passes of one advance.
const maxAdvancePasses = 64

// WorkflowRunPayload is the payload of KindWorkflowRun events.
type WorkflowRunPayload struct {
	RunID        uuid.UUID `json:"run_id"`
	RunNumber    int64     `json:"run_number"`
	Attempt      int32     `json:"attempt"`
	WorkflowPath string    `json:"workflow_path"`
	WorkflowName string    `json:"workflow_name"`
	Event        string    `json:"event"`
	Ref          string    `json:"ref"`
	Sha          string    `json:"sha"`
	Title        string    `json:"title"`
	Conclusion   string    `json:"conclusion"`
	ActorID      uuid.UUID `json:"actor_id,omitzero"`
}

// effects are what a transaction leaves to do once it committed.
type effects struct {
	// runs to advance (other runs of a concurrency group).
	runs []uuid.UUID
	// jobs of other runs to cancel (concurrency).
	cancel []uuid.UUID
	queued bool
	events bool
	report bool
}

func (e *effects) addRun(id uuid.UUID) {
	if !slices.Contains(e.runs, id) {
		e.runs = append(e.runs, id)
	}
}

// advance moves a run (and the runs it unblocks) forward: blocked jobs
// whose needs are met are planned, waiting jobs admitted and the run
// status recomputed.
func (s *Service) advance(ctx context.Context, runID uuid.UUID) error {
	queue := []uuid.UUID{runID}
	seen := map[uuid.UUID]bool{}
	var first error
	for len(queue) > 0 && len(seen) < 256 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		eff, err := s.advanceOne(ctx, id)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		s.apply(ctx, eff)
		queue = append(queue, eff.runs...)
		if eff.report {
			s.enqueue(ctx, ReportArgs{RunID: id})
		}
	}
	return first
}

// apply runs the effects of a committed transaction, except the runs to
// advance.
func (s *Service) apply(ctx context.Context, eff effects) {
	for _, j := range eff.cancel {
		if err := s.cancelJob(ctx, j); err != nil {
			s.log.Warn("cancel job of the concurrency group", "job_id", j, "err", err)
		}
	}
	if eff.queued {
		s.notifyTasks(ctx)
	}
	if eff.events {
		s.kick(ctx)
	}
}

// notifyTasks wakes the runners polling for tasks.
func (s *Service) notifyTasks(ctx context.Context) {
	v, err := s.q.BumpActionsTasksVersion(ctx)
	if err != nil {
		s.log.Warn("bump tasks version", "err", err)
		return
	}
	if err := s.q.NotifyActions(ctx, dbgen.NotifyActionsParams{Channel: tasksChannel, Payload: strconv.FormatInt(v, 10)}); err != nil {
		s.log.Warn("notify runners", "err", err)
	}
}

func (s *Service) advanceOne(ctx context.Context, runID uuid.UUID) (effects, error) {
	var eff effects
	d, err := s.d()
	if err != nil {
		return eff, err
	}
	run, err := s.q.GetActionsRun(ctx, runID)
	if err != nil {
		return eff, fmt.Errorf("get run: %w", err)
	}
	if run.Status == "completed" {
		return eff, nil
	}
	repo, err := d.Repos.GetByID(ctx, system, run.RepoID)
	if err != nil {
		return eff, fmt.Errorf("run repository: %w", err)
	}
	actor := s.userName(ctx, run.TriggerActorID)
	vars, err := s.variables(ctx, repo, "")
	if err != nil {
		return eff, err
	}
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		eff = effects{}
		run, err = q.GetActionsRunForUpdate(ctx, runID)
		if err != nil {
			return fmt.Errorf("lock run: %w", err)
		}
		if run.Status == "completed" || run.Status == "waiting" {
			return nil
		}
		if run.Status == "pending" {
			if err := q.LockActionsGroup(ctx, "run:"+run.RepoID.String()+":"+run.ConcurrencyGroup); err != nil {
				return fmt.Errorf("lock group: %w", err)
			}
			others, err := q.ListActiveActionsRunsInGroup(ctx, dbgen.ListActiveActionsRunsInGroupParams{
				RepoID: run.RepoID, ConcurrencyGroup: run.ConcurrencyGroup, ID: run.ID})
			if err != nil {
				return fmt.Errorf("concurrency group: %w", err)
			}
			for _, o := range others {
				if o.Status == "queued" || o.Status == "in_progress" {
					return nil
				}
			}
			if err := q.SetActionsRunStatus(ctx, dbgen.SetActionsRunStatusParams{Status: "queued", ID: run.ID}); err != nil {
				return fmt.Errorf("admit run: %w", err)
			}
			run.Status = "queued"
			eff.report = true
		}
		rc := &runCtx{run: run, repo: repo, gh: s.githubCtx(run, repo, actor), vars: vars, b: d.Browse}
		var rows []dbgen.ActionsJob
		for pass := 0; pass < maxAdvancePasses; pass++ {
			rows, err = q.ListActionsJobsForUpdate(ctx, dbgen.ListActionsJobsForUpdateParams{RunID: run.ID, Attempt: run.Attempt})
			if err != nil {
				return fmt.Errorf("list jobs: %w", err)
			}
			rc.rows = len(rows)
			changed, err := s.plan(ctx, q, rc, rows, &eff)
			if err != nil {
				return err
			}
			if !changed {
				break
			}
			eff.report = true
		}
		return s.settleRun(ctx, q, run, rows, &eff)
	})
	return eff, err
}

// plan makes one pass over the jobs of a run.
func (s *Service) plan(ctx context.Context, q *dbgen.Queries, rc *runCtx, rows []dbgen.ActionsJob, eff *effects) (bool, error) {
	byKey := map[string][]dbgen.ActionsJob{}
	for _, r := range rows {
		byKey[r.JobKey] = append(byKey[r.JobKey], r)
	}
	changed := false
	// Fail-fast: a failed matrix job cancels the other jobs of its matrix.
	for _, r := range rows {
		if r.Status != "completed" || (r.Conclusion != "failure" && r.Conclusion != "timed_out") || !r.FailFast ||
			len(r.Matrix) == 0 {
			continue
		}
		for _, o := range byKey[r.JobKey] {
			if o.Status != "completed" && !o.CancelRequested {
				if err := s.cancelRow(ctx, q, o, rows); err != nil {
					return false, err
				}
				changed = true
			}
		}
	}
	if changed {
		return true, nil
	}
	for _, r := range rows {
		switch r.Status {
		case "blocked":
			extra := decodeExtra(r.Inputs)
			if extra.Expanded {
				done, err := s.settleAggregate(ctx, q, rc, r, extra, rows)
				if err != nil {
					return false, err
				}
				changed = changed || done
				continue
			}
			ready := true
			for _, n := range r.Needs {
				if _, ok := aggregate(byKey[n]); !ok {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			if err := s.readyJob(ctx, q, rc, r, byKey); err != nil {
				return false, err
			}
			changed = true
		case "waiting":
			queued, err := s.admitJob(ctx, q, r, eff)
			if err != nil {
				return false, err
			}
			if queued {
				eff.queued = true
				changed = true
			}
		}
	}
	return changed, nil
}

// settleAggregate completes the row of a reusable workflow call once every
// job of the called workflow completed.
func (s *Service) settleAggregate(ctx context.Context, q *dbgen.Queries, rc *runCtx, row dbgen.ActionsJob,
	extra jobExtra, rows []dbgen.ActionsJob) (bool, error) {
	var callee []dbgen.ActionsJob
	direct := map[string][]dbgen.ActionsJob{}
	for _, r := range rows {
		rest, ok := strings.CutPrefix(r.JobKey, extra.Callee)
		if !ok {
			continue
		}
		callee = append(callee, r)
		if !strings.Contains(rest, ".") && !strings.Contains(rest, "[") {
			direct[rest] = append(direct[rest], r)
		}
	}
	conclusion, ok := aggregate(callee)
	if !ok {
		return false, nil
	}
	if len(extra.CalleeOutputs) > 0 {
		jobs := map[string]*model.WorkflowCallResult{}
		for id, rs := range direct {
			jobs[id] = &model.WorkflowCallResult{Outputs: mergedOutputs(rs)}
		}
		ec := &exprCtx{github: rc.gh, vars: rc.vars, inputs: extra.CalleeInputs, jobs: jobs}
		out := map[string]string{}
		for k, expr := range extra.CalleeOutputs {
			v, err := ec.interpolate(expr)
			if err != nil {
				s.log.Warn("reusable workflow output", "run_id", row.RunID, "job", row.JobKey, "output", k, "err", err)
				continue
			}
			out[k] = v
		}
		b, _ := json.Marshal(out)
		if _, err := q.MergeActionsJobOutputs(ctx, dbgen.MergeActionsJobOutputsParams{Outputs: b, ID: row.ID}); err != nil {
			return false, fmt.Errorf("call outputs: %w", err)
		}
	}
	if err := q.CompleteActionsJob(ctx, dbgen.CompleteActionsJobParams{Conclusion: conclusion, ID: row.ID}); err != nil {
		return false, fmt.Errorf("complete call: %w", err)
	}
	return true, nil
}

// admitJob queues a waiting job once its environment is approved and its
// concurrency group is free.
func (s *Service) admitJob(ctx context.Context, q *dbgen.Queries, row dbgen.ActionsJob, eff *effects) (bool, error) {
	if row.CancelRequested {
		return false, nil
	}
	if row.Environment != "" && !row.ApprovedBy.Valid {
		env, err := q.GetActionsEnvironment(ctx, dbgen.GetActionsEnvironmentParams{RepoID: row.RepoID, Name: row.Environment})
		if err != nil && !isNoRows(err) {
			return false, fmt.Errorf("environment: %w", err)
		}
		if err == nil && env.RequireApproval {
			return false, nil
		}
	}
	if row.ConcurrencyGroup != "" {
		if err := q.LockActionsGroup(ctx, "job:"+row.RepoID.String()+":"+row.ConcurrencyGroup); err != nil {
			return false, fmt.Errorf("lock group: %w", err)
		}
		waiting, err := q.ListWaitingActionsJobsInGroup(ctx, dbgen.ListWaitingActionsJobsInGroupParams{
			RepoID: row.RepoID, ConcurrencyGroup: row.ConcurrencyGroup})
		if err != nil {
			return false, fmt.Errorf("concurrency group: %w", err)
		}
		if n := len(waiting); n > 0 && waiting[n-1].ID != row.ID {
			// A newer job of the group waits: only the newest one does.
			return false, q.CompleteActionsJob(ctx, dbgen.CompleteActionsJobParams{Conclusion: "cancelled", ID: row.ID})
		}
		for _, w := range waiting {
			if w.ID != row.ID {
				if w.RunID == row.RunID {
					if err := q.CompleteActionsJob(ctx, dbgen.CompleteActionsJobParams{Conclusion: "cancelled", ID: w.ID}); err != nil {
						return false, fmt.Errorf("cancel older job: %w", err)
					}
				} else {
					eff.cancel = append(eff.cancel, w.ID)
				}
			}
		}
		active, err := q.ListActiveActionsJobsInGroup(ctx, dbgen.ListActiveActionsJobsInGroupParams{
			RepoID: row.RepoID, ConcurrencyGroup: row.ConcurrencyGroup, ID: row.ID})
		if err != nil {
			return false, fmt.Errorf("concurrency group: %w", err)
		}
		busy := false
		for _, a := range active {
			if a.Status != "queued" && a.Status != "in_progress" {
				continue
			}
			busy = true
			if row.ConcurrencyCancel {
				eff.cancel = append(eff.cancel, a.ID)
			}
		}
		if busy {
			return false, nil
		}
	}
	if err := q.SetActionsJobStatus(ctx, dbgen.SetActionsJobStatusParams{Status: "queued", ID: row.ID}); err != nil {
		return false, fmt.Errorf("queue job: %w", err)
	}
	return true, nil
}

// settleRun recomputes the run status from its jobs.
func (s *Service) settleRun(ctx context.Context, q *dbgen.Queries, run dbgen.ActionsRun, rows []dbgen.ActionsJob, eff *effects) error {
	if run.Status == "pending" || run.Status == "waiting" {
		return nil
	}
	started, done := false, true
	failed, cancelled := false, false
	for _, r := range rows {
		switch r.Status {
		case "in_progress":
			started = true
			done = false
		case "completed":
			started = true
			switch r.Conclusion {
			case "failure", "timed_out":
				failed = true
			case "cancelled":
				cancelled = true
			}
		default:
			done = false
		}
	}
	if done {
		conclusion := "success"
		switch {
		case failed:
			conclusion = "failure"
		case cancelled:
			conclusion = "cancelled"
		}
		return s.finishRun(ctx, q, run, conclusion, eff)
	}
	if started && run.Status == "queued" {
		if err := q.SetActionsRunStatus(ctx, dbgen.SetActionsRunStatusParams{Status: "in_progress", ID: run.ID}); err != nil {
			return fmt.Errorf("start run: %w", err)
		}
		eff.report = true
	}
	return nil
}

// finishRun completes a run, emits its workflow_run event and lets the
// next run of its concurrency group start.
func (s *Service) finishRun(ctx context.Context, q *dbgen.Queries, run dbgen.ActionsRun, conclusion string, eff *effects) error {
	if err := q.SetActionsRunStatus(ctx, dbgen.SetActionsRunStatusParams{Status: "completed", Conclusion: conclusion, ID: run.ID}); err != nil {
		return fmt.Errorf("complete run: %w", err)
	}
	eff.report = true
	actor := run.TriggerActorID.UUID
	if !run.TriggerActorID.Valid {
		actor = run.ActorID.UUID
	}
	p := WorkflowRunPayload{RunID: run.ID, RunNumber: run.RunNumber, Attempt: run.Attempt, WorkflowPath: run.WorkflowPath,
		WorkflowName: run.WorkflowName, Event: run.Event, Ref: run.Ref, Sha: run.Sha, Title: run.Title,
		Conclusion: conclusion, ActorID: actor}
	if err := events.Emit(ctx, q, events.Event{Kind: KindWorkflowRun, Action: "completed", RepoID: run.RepoID,
		SubjectID: run.ID}, p); err != nil {
		return fmt.Errorf("workflow_run event: %w", err)
	}
	eff.events = true
	if run.ConcurrencyGroup != "" {
		others, err := q.ListActiveActionsRunsInGroup(ctx, dbgen.ListActiveActionsRunsInGroupParams{
			RepoID: run.RepoID, ConcurrencyGroup: run.ConcurrencyGroup, ID: run.ID})
		if err != nil {
			return fmt.Errorf("concurrency group: %w", err)
		}
		for _, o := range others {
			if o.Status == "pending" {
				eff.addRun(o.ID)
			}
		}
	}
	s.log.Info("workflow run completed", "run_id", run.ID, "repo_id", run.RepoID, "conclusion", conclusion)
	return nil
}

// cancelRow cancels one job of a locked run: a running job is asked to
// stop, the others end at once; a reusable workflow call cancels its jobs.
func (s *Service) cancelRow(ctx context.Context, q *dbgen.Queries, row dbgen.ActionsJob, rows []dbgen.ActionsJob) error {
	if row.Status == "completed" {
		return nil
	}
	extra := decodeExtra(row.Inputs)
	if extra.Expanded {
		for _, r := range rows {
			if strings.HasPrefix(r.JobKey, extra.Callee) && r.Status != "completed" {
				if err := s.cancelRow(ctx, q, r, nil); err != nil {
					return err
				}
			}
		}
	}
	if row.Status == "in_progress" {
		if err := q.RequestActionsJobCancel(ctx, row.ID); err != nil {
			return fmt.Errorf("cancel job: %w", err)
		}
		return nil
	}
	if err := q.CompleteActionsJob(ctx, dbgen.CompleteActionsJobParams{Conclusion: "cancelled", ID: row.ID}); err != nil {
		return fmt.Errorf("cancel job: %w", err)
	}
	return nil
}

// cancelJob cancels one job in its own transaction and advances its run.
func (s *Service) cancelJob(ctx context.Context, jobID uuid.UUID) error {
	var runID uuid.UUID
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		j, err := q.GetActionsJob(ctx, jobID)
		if err != nil {
			return fmt.Errorf("get job: %w", err)
		}
		runID = j.RunID
		rows, err := q.ListActionsJobsForUpdate(ctx, dbgen.ListActionsJobsForUpdateParams{RunID: j.RunID, Attempt: j.Attempt})
		if err != nil {
			return fmt.Errorf("lock jobs: %w", err)
		}
		for _, r := range rows {
			if r.ID == jobID {
				return s.cancelRow(ctx, q, r, rows)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.advance(ctx, runID)
}

// cancelRun cancels every job of the current attempt of a run.
func (s *Service) cancelRun(ctx context.Context, runID uuid.UUID, by uuid.UUID) error {
	var eff effects
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		eff = effects{}
		run, err := q.GetActionsRunForUpdate(ctx, runID)
		if err != nil {
			return fmt.Errorf("lock run: %w", err)
		}
		if run.Status == "completed" {
			return nil
		}
		rows, err := q.ListActionsJobsForUpdate(ctx, dbgen.ListActionsJobsForUpdateParams{RunID: run.ID, Attempt: run.Attempt})
		if err != nil {
			return fmt.Errorf("lock jobs: %w", err)
		}
		running := false
		for _, r := range rows {
			if r.Status == "in_progress" {
				running = true
			}
			if err := s.cancelRow(ctx, q, r, rows); err != nil {
				return err
			}
		}
		eff.report = true
		if !running {
			return s.finishRun(ctx, q, run, "cancelled", &eff)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.log.Info("workflow run cancelled", "run_id", runID, "by", by)
	s.apply(ctx, eff)
	s.enqueue(ctx, ReportArgs{RunID: runID})
	for _, r := range eff.runs {
		if err := s.advance(ctx, r); err != nil {
			s.log.Warn("advance run of the concurrency group", "run_id", r, "err", err)
		}
	}
	return nil
}

// completeJob ends a job reported by its runner (or by the sweeper) and
// advances its run and the jobs waiting for its concurrency group.
func (s *Service) completeJob(ctx context.Context, jobID uuid.UUID, conclusion string) error {
	var job dbgen.ActionsJob
	err := s.tx(ctx, func(q *dbgen.Queries) error {
		var err error
		job, err = q.GetActionsJobForUpdate(ctx, jobID)
		if err != nil {
			return fmt.Errorf("lock job: %w", err)
		}
		if job.Status == "completed" {
			return nil
		}
		if job.CancelRequested && conclusion != "failure" && conclusion != "timed_out" {
			conclusion = "cancelled"
		}
		if err := q.CompleteActionsJob(ctx, dbgen.CompleteActionsJobParams{Conclusion: conclusion, ID: jobID}); err != nil {
			return fmt.Errorf("complete job: %w", err)
		}
		if err := q.CompleteOpenActionsSteps(ctx, dbgen.CompleteOpenActionsStepsParams{Conclusion: stepConclusion(conclusion), JobID: jobID}); err != nil {
			return fmt.Errorf("complete steps: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.masks.Delete(jobID)
	if err := s.advance(ctx, job.RunID); err != nil {
		s.log.Warn("advance run", "run_id", job.RunID, "err", err)
	}
	if job.ConcurrencyGroup != "" {
		waiting, err := s.q.ListWaitingActionsJobsInGroup(ctx, dbgen.ListWaitingActionsJobsInGroupParams{
			RepoID: job.RepoID, ConcurrencyGroup: job.ConcurrencyGroup})
		if err != nil {
			return fmt.Errorf("concurrency group: %w", err)
		}
		runs := map[uuid.UUID]bool{}
		for _, w := range waiting {
			runs[w.RunID] = true
		}
		for _, r := range slices.Collect(maps.Keys(runs)) {
			if err := s.advance(ctx, r); err != nil {
				s.log.Warn("advance run of the concurrency group", "run_id", r, "err", err)
			}
		}
	}
	s.enqueue(ctx, ArchiveArgs{JobID: jobID})
	return nil
}

func stepConclusion(c string) string {
	switch c {
	case "success", "failure", "cancelled", "skipped":
		return c
	}
	return "failure"
}
