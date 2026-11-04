package actions

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
	"github.com/Ervisio/ErvisioRepo/internal/statuses"
)

// RunURL is the web page of a run.
func (s *Service) RunURL(repo repos.Repo, run dbgen.ActionsRun) string {
	return s.public + "/" + repo.FullName() + "/actions/runs/" + run.ID.String()
}

// JobURL is the web page of a job.
func (s *Service) JobURL(repo repos.Repo, run dbgen.ActionsRun, jobID uuid.UUID) string {
	return s.RunURL(repo, run) + "/jobs/" + jobID.String()
}

// report mirrors a run to one check run per job and one commit status per
// workflow. It is idempotent: River retries it and every state change
// enqueues it again.
func (s *Service) report(ctx context.Context, runID uuid.UUID) error {
	d, err := s.d()
	if err != nil {
		return err
	}
	if d.Statuses == nil {
		return nil
	}
	run, err := s.q.GetActionsRun(ctx, runID)
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get run: %w", err)
	}
	repo, err := d.Repos.GetByID(ctx, system, run.RepoID)
	if err != nil {
		return fmt.Errorf("get repo: %w", err)
	}
	rows, err := s.q.ListActionsJobs(ctx, dbgen.ListActionsJobsParams{RunID: run.ID, Attempt: run.Attempt})
	if err != nil {
		return fmt.Errorf("list jobs: %w", err)
	}
	for _, j := range rows {
		if decodeExtra(j.Inputs).Expanded {
			// A reusable workflow call: its jobs are reported instead.
			continue
		}
		if err := s.reportJob(ctx, d, repo, run, j); err != nil {
			return err
		}
	}
	state, desc := statusOf(run)
	if _, err := d.Statuses.CreateStatus(ctx, system, repo.Owner, repo.Name, run.Sha, statuses.StatusInput{
		State: state, Context: truncate(run.WorkflowName+" / "+run.Event, 255), Description: desc,
		TargetURL: s.RunURL(repo, run)}); err != nil {
		return fmt.Errorf("commit status: %w", err)
	}
	return nil
}

func (s *Service) reportJob(ctx context.Context, d *Deps, repo repos.Repo, run dbgen.ActionsRun, j dbgen.ActionsJob) error {
	status, conclusion := "queued", ""
	switch j.Status {
	case "in_progress":
		status = "in_progress"
	case "completed":
		status, conclusion = "completed", j.Conclusion
	}
	var started, completed *time.Time
	if j.StartedAt.Valid {
		started = &j.StartedAt.Time
	}
	if j.CompletedAt.Valid && status == "completed" {
		completed = &j.CompletedAt.Time
	}
	name := truncate(run.WorkflowName+" / "+j.Name, 255)
	out := &statuses.Output{Title: j.Name, Summary: jobSummary(j)}
	url := s.JobURL(repo, run, j.ID)
	if j.CheckRunID.Valid {
		p := statuses.CheckRunPatch{Status: &status, DetailsURL: &url, Output: out}
		if conclusion != "" {
			p.Conclusion = &conclusion
		}
		p.StartedAt, p.CompletedAt = started, completed
		if _, err := d.Statuses.UpdateCheckRun(ctx, system, repo.Owner, repo.Name, j.CheckRunID.UUID, p); err != nil {
			return fmt.Errorf("update check run: %w", err)
		}
		return nil
	}
	in := statuses.CheckRunInput{Name: name, HeadSHA: run.Sha, Status: status, Conclusion: conclusion,
		ExternalID: j.ID.String(), DetailsURL: url, Output: out, App: AppName, StartedAt: started, CompletedAt: completed}
	cr, err := d.Statuses.CreateCheckRun(ctx, system, repo.Owner, repo.Name, in)
	if err != nil {
		return fmt.Errorf("create check run: %w", err)
	}
	if err := s.q.SetActionsJobCheckRun(ctx, dbgen.SetActionsJobCheckRunParams{CheckRunID: nullID(cr.ID), ID: j.ID}); err != nil {
		return fmt.Errorf("set check run: %w", err)
	}
	return nil
}

func jobSummary(j dbgen.ActionsJob) string {
	switch j.Status {
	case "blocked":
		return "Waiting for the jobs it needs."
	case "waiting":
		if j.Environment != "" {
			return "Waiting for the environment " + j.Environment + "."
		}
		return "Waiting for its concurrency group."
	case "queued":
		return "Waiting for a runner."
	case "in_progress":
		return "Running."
	}
	return "Job " + j.Conclusion + "."
}

// statusOf maps a run to a commit status state.
func statusOf(run dbgen.ActionsRun) (string, string) {
	if run.Status != "completed" {
		if run.NeedsApproval {
			return "pending", "Waiting for approval"
		}
		return "pending", "Run #" + strconv.FormatInt(run.RunNumber, 10) + " is " + run.Status
	}
	switch run.Conclusion {
	case "success", "skipped", "neutral":
		return "success", "Run #" + strconv.FormatInt(run.RunNumber, 10) + " succeeded"
	case "cancelled":
		return "error", "Run #" + strconv.FormatInt(run.RunNumber, 10) + " was cancelled"
	case "action_required":
		return "pending", "Run #" + strconv.FormatInt(run.RunNumber, 10) + " needs approval"
	}
	return "failure", "Run #" + strconv.FormatInt(run.RunNumber, 10) + " failed"
}
