package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/authz"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/events"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
	"github.com/Ervisio/ErvisioRepo/internal/webhooks"
)

// minScheduleInterval is the shortest interval between two runs of one
// schedule entry.
const minScheduleInterval = 5 * time.Minute

// zeroSha is the old or new id of a created or deleted ref.
const zeroSha = "0000000000000000000000000000000000000000"

// EventKinds are the bus event kinds that trigger workflows.
var EventKinds = []string{
	events.KindPush, events.KindPull, events.KindPullReview, events.KindPullReviewComment,
	events.KindIssue, events.KindIssueComment, events.KindLabel, events.KindMilestone, events.KindRelease,
}

// EventHandler is the bus handler that starts the workflows of events.
func (s *Service) EventHandler() events.Handler {
	return events.HandlerFunc(s.handleEvent)
}

func (s *Service) handleEvent(ctx context.Context, ev events.Event) error {
	if ev.RepoID == uuid.Nil || ev.ActorID == uuid.Nil {
		// Events of Actions itself (job tokens, check runs) and of the
		// system never start workflows: no loops.
		return nil
	}
	d, err := s.d()
	if err != nil {
		return err
	}
	if d.Payloads == nil {
		return nil
	}
	items, err := d.Payloads.GitHubEvents(ctx, ev)
	if errors.Is(err, webhooks.ErrNoRepo) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("event payloads: %w", err)
	}
	if len(items) == 0 {
		return nil
	}
	repo, err := d.Repos.GetByID(ctx, system, ev.RepoID)
	if errors.Is(err, repos.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get repo: %w", err)
	}
	for _, it := range items {
		var payload map[string]any
		if err := json.Unmarshal(it.Payload, &payload); err != nil {
			return fmt.Errorf("decode payload: %w", err)
		}
		var err error
		switch it.Event {
		case events.KindPush:
			err = s.onPush(ctx, d, repo, ev, payload)
		case events.KindPull, events.KindPullReview, events.KindPullReviewComment:
			err = s.onPull(ctx, d, repo, ev, it, payload)
		case events.KindRelease:
			err = s.onRelease(ctx, d, repo, ev, it, payload)
		default:
			err = s.onDefaultBranch(ctx, d, repo, ev, it, payload)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// event is one occurrence offered to the workflows of a commit.
type event struct {
	trigger
	Sha     string
	RunRef  string
	Title   string
	ActorID uuid.UUID
	Payload map[string]any
	// HeadRepoID and Fork describe pull requests.
	HeadRepoID    uuid.UUID
	Fork          bool
	NeedsApproval bool
}

// start creates a run for every workflow of the commit that accepts e.
func (s *Service) start(ctx context.Context, d *Deps, repo repos.Repo, e event) error {
	files, err := s.discover(ctx, d.Browse, browseRepo(repo), e.Sha)
	if err != nil {
		return err
	}
	for _, f := range files {
		pw, err := parseWorkflow(f.Path, f.Content)
		if err != nil {
			s.log.Info("invalid workflow file", "repo_id", repo.ID, "path", f.Path, "err", err)
			continue
		}
		filter, ok := pw.On[e.Event]
		if !ok || !filter.matches(e.trigger) {
			continue
		}
		payload := clonePayload(e.Payload)
		if _, err := s.createRun(ctx, runInput{Repo: repo, WF: pw, Event: e.Event, Action: e.Action, Ref: e.RunRef,
			Sha: e.Sha, Title: e.Title, ActorID: e.ActorID, Payload: payload, HeadRepoID: e.HeadRepoID, Fork: e.Fork,
			NeedsApproval: e.NeedsApproval}); err != nil {
			var ve *ValidationError
			if errors.As(err, &ve) {
				s.log.Info("workflow not started", "repo_id", repo.ID, "path", f.Path, "err", err)
				continue
			}
			return fmt.Errorf("start %s: %w", f.Path, err)
		}
	}
	return nil
}

func clonePayload(p map[string]any) map[string]any {
	b, _ := json.Marshal(p)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func (s *Service) onPush(ctx context.Context, d *Deps, repo repos.Repo, ev events.Event, payload map[string]any) error {
	ref, _ := payload["ref"].(string)
	before, _ := payload["before"].(string)
	after, _ := payload["after"].(string)
	if deleted, _ := payload["deleted"].(bool); deleted || after == "" || after == zeroSha {
		return nil
	}
	if ref == "refs/heads/"+repo.DefaultBranch {
		if err := s.syncSchedules(ctx, d, repo, after); err != nil {
			s.log.Warn("sync schedules", "repo_id", repo.ID, "err", err)
		}
	}
	if !strings.HasPrefix(ref, "refs/heads/") && !strings.HasPrefix(ref, "refs/tags/") {
		return nil
	}
	var files []string
	if before != "" && before != zeroSha {
		files = s.changedFiles(ctx, d, repo, before, after)
	}
	title := ""
	if hc, ok := payload["head_commit"].(map[string]any); ok {
		title, _ = hc["message"].(string)
		title, _, _ = strings.Cut(title, "\n")
	}
	return s.start(ctx, d, repo, event{trigger: trigger{Event: "push", Ref: ref, Files: files}, Sha: after, RunRef: ref,
		Title: title, ActorID: ev.ActorID, Payload: payload})
}

// changedFiles lists the paths changed between two commits; nil when
// unknown, so that path filters match.
func (s *Service) changedFiles(ctx context.Context, d *Deps, repo repos.Repo, from, to string) []string {
	changes, err := d.Browse.ChangedFiles(ctx, browseRepo(repo), from, to)
	if err != nil {
		s.log.Info("changed files unknown", "repo_id", repo.ID, "err", err)
		return nil
	}
	out := make([]string, 0, len(changes))
	for _, c := range changes {
		if c.NewPath != "" {
			out = append(out, c.NewPath)
		}
		if c.OldPath != "" && c.OldPath != c.NewPath {
			out = append(out, c.OldPath)
		}
	}
	return out
}

func (s *Service) onPull(ctx context.Context, d *Deps, repo repos.Repo, ev events.Event, it webhooks.GitHubEvent,
	payload map[string]any) error {
	num, _ := payload["number"].(float64)
	pr, err := s.q.GetPullRequest(ctx, dbgen.GetPullRequestParams{RepoID: repo.ID, Number: int64(num)})
	if isNoRows(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get pull request: %w", err)
	}
	baseRef := "refs/heads/" + strings.TrimPrefix(pr.BaseRef, "refs/heads/")
	title := ""
	if p, ok := payload["pull_request"].(map[string]any); ok {
		title, _ = p["title"].(string)
	}
	fork := !pr.HeadRepoID.Valid || pr.HeadRepoID.UUID != repo.ID
	var files []string
	if pr.MergeBaseSha != "" && pr.HeadSha != "" {
		files = s.changedFiles(ctx, d, repo, pr.MergeBaseSha, pr.HeadSha)
	}
	if pr.HeadSha != "" {
		// pull_request runs the head of the pull request; a fork gets no
		// secrets, a read-only token and needs approval for outside
		// contributors.
		runRef := "refs/pull/" + strconv.FormatInt(pr.Number, 10) + "/merge"
		if it.Action == "closed" {
			runRef = "refs/pull/" + strconv.FormatInt(pr.Number, 10) + "/head"
		}
		e := event{trigger: trigger{Event: it.Event, Action: it.Action, Ref: baseRef, Files: files}, Sha: pr.HeadSha,
			RunRef: runRef, Title: title, ActorID: ev.ActorID, Payload: payload, HeadRepoID: pr.HeadRepoID.UUID, Fork: fork}
		if fork {
			e.NeedsApproval = !s.canWrite(ctx, d, repo.ID, ev.ActorID)
		}
		if err := s.start(ctx, d, repo, e); err != nil {
			return err
		}
	}
	if it.Event != events.KindPull {
		return nil
	}
	// pull_request_target runs the base branch with the repository
	// secrets: the workflow comes from the base, never from the fork.
	base, err := d.Browse.Resolve(ctx, browseRepo(repo), baseRef)
	if err != nil {
		s.log.Info("pull_request_target base unknown", "repo_id", repo.ID, "ref", baseRef, "err", err)
		return nil
	}
	return s.start(ctx, d, repo, event{trigger: trigger{Event: "pull_request_target", Action: it.Action, Ref: baseRef,
		Files: files}, Sha: base.Commit, RunRef: baseRef, Title: title, ActorID: ev.ActorID, Payload: payload,
		HeadRepoID: pr.HeadRepoID.UUID})
}

// canWrite reports whether a user may push to the repository.
func (s *Service) canWrite(ctx context.Context, d *Deps, repoID, userID uuid.UUID) bool {
	u, err := s.q.GetUserByID(ctx, userID)
	if err != nil {
		return false
	}
	who := auth.Principal{UserID: u.ID, Username: u.Username, Role: u.Role}
	r, err := d.Repos.GetByID(ctx, who, repoID)
	return err == nil && r.Permission >= authz.PermWrite
}

func (s *Service) onRelease(ctx context.Context, d *Deps, repo repos.Repo, ev events.Event, it webhooks.GitHubEvent,
	payload map[string]any) error {
	rel, _ := payload["release"].(map[string]any)
	tag, _ := rel["tag_name"].(string)
	if tag == "" {
		return nil
	}
	ref := "refs/tags/" + tag
	res, err := d.Browse.Resolve(ctx, browseRepo(repo), ref)
	if err != nil {
		// A draft release may have no tag yet.
		return s.onDefaultBranch(ctx, d, repo, ev, it, payload)
	}
	name, _ := rel["name"].(string)
	return s.start(ctx, d, repo, event{trigger: trigger{Event: it.Event, Action: it.Action, Ref: ref}, Sha: res.Commit,
		RunRef: ref, Title: name, ActorID: ev.ActorID, Payload: payload})
}

// onDefaultBranch runs the workflows of the default branch (issues,
// comments, labels...).
func (s *Service) onDefaultBranch(ctx context.Context, d *Deps, repo repos.Repo, ev events.Event, it webhooks.GitHubEvent,
	payload map[string]any) error {
	ref := "refs/heads/" + repo.DefaultBranch
	res, err := d.Browse.Resolve(ctx, browseRepo(repo), ref)
	if err != nil {
		return nil // empty repository
	}
	title := ""
	for _, k := range []string{"issue", "pull_request", "label", "milestone"} {
		if o, ok := payload[k].(map[string]any); ok {
			if t, ok := o["title"].(string); ok {
				title = t
			} else if t, ok := o["name"].(string); ok {
				title = t
			}
			break
		}
	}
	return s.start(ctx, d, repo, event{trigger: trigger{Event: it.Event, Action: it.Action, Ref: ref}, Sha: res.Commit,
		RunRef: ref, Title: title, ActorID: ev.ActorID, Payload: payload})
}

// writable resolves a repository the caller may push to.
func (s *Service) writable(ctx context.Context, d *Deps, who auth.Principal, owner, name string) (repos.Repo, error) {
	repo, err := d.Repos.Get(ctx, who, owner, name)
	if errors.Is(err, repos.ErrNotFound) {
		return repos.Repo{}, ErrNotFound
	}
	if err != nil {
		return repos.Repo{}, fmt.Errorf("get repo: %w", err)
	}
	if repo.Permission < authz.PermWrite || who.UserID == uuid.Nil {
		return repos.Repo{}, ErrForbidden
	}
	return repo, nil
}

// Dispatch starts a workflow_dispatch run of workflow (its path or file
// name) at ref (empty: the default branch).
func (s *Service) Dispatch(ctx context.Context, who auth.Principal, owner, name, workflow, ref string,
	inputs map[string]any) (dbgen.ActionsRun, error) {
	d, err := s.d()
	if err != nil {
		return dbgen.ActionsRun{}, err
	}
	repo, err := s.writable(ctx, d, who, owner, name)
	if err != nil {
		return dbgen.ActionsRun{}, err
	}
	res, err := d.Browse.Resolve(ctx, browseRepo(repo), ref)
	if err != nil {
		return dbgen.ActionsRun{}, invalid("unknown ref %q", ref)
	}
	runRef := res.Name
	if res.Kind == gitbrowse.KindCommit || runRef == "" {
		return dbgen.ActionsRun{}, invalid("ref must be a branch or a tag")
	}
	pw, err := s.findWorkflow(ctx, d, repo, res.Commit, workflow)
	if err != nil {
		return dbgen.ActionsRun{}, err
	}
	if _, ok := pw.On["workflow_dispatch"]; !ok {
		return dbgen.ActionsRun{}, invalid("%s has no workflow_dispatch trigger", pw.Path)
	}
	in, err := dispatchInputs(pw, inputs)
	if err != nil {
		return dbgen.ActionsRun{}, err
	}
	payload := map[string]any{"ref": runRef, "workflow": pw.Path, "inputs": in}
	return s.createRun(ctx, runInput{Repo: repo, WF: pw, Event: "workflow_dispatch", Ref: runRef, Sha: res.Commit,
		ActorID: who.UserID, Payload: payload, Inputs: in})
}

// findWorkflow returns a workflow of a commit by path or file name.
func (s *Service) findWorkflow(ctx context.Context, d *Deps, repo repos.Repo, sha, workflow string) (*parsedWorkflow, error) {
	files, err := s.discover(ctx, d.Browse, browseRepo(repo), sha)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f.Path != workflow && path.Base(f.Path) != workflow {
			continue
		}
		pw, err := parseWorkflow(f.Path, f.Content)
		if err != nil {
			return nil, invalid("%v", err)
		}
		return pw, nil
	}
	return nil, ErrNotFound
}

// dispatchInputs checks the inputs of a dispatch against the workflow and
// fills the defaults.
func dispatchInputs(pw *parsedWorkflow, given map[string]any) (map[string]any, error) {
	cfg := pw.Model.WorkflowDispatchConfig()
	out := map[string]any{}
	if cfg == nil {
		return out, nil
	}
	for k := range given {
		if _, ok := cfg.Inputs[k]; !ok {
			return nil, invalid("unexpected input %q", k)
		}
	}
	for k, def := range cfg.Inputs {
		v, ok := given[k]
		if !ok || v == nil || v == "" {
			if def.Required && def.Default == "" {
				return nil, invalid("input %q is required", k)
			}
			out[k] = typedInput(def.Type, def.Default)
			continue
		}
		str, isStr := v.(string)
		if !isStr {
			str = fmt.Sprint(v)
		}
		switch def.Type {
		case "choice":
			if !slices.Contains(def.Options, str) {
				return nil, invalid("input %q must be one of %s", k, strings.Join(def.Options, ", "))
			}
		case "boolean":
			if str != "true" && str != "false" {
				return nil, invalid("input %q must be true or false", k)
			}
		case "number":
			if _, err := strconv.ParseFloat(str, 64); err != nil {
				return nil, invalid("input %q must be a number", k)
			}
		}
		out[k] = typedInput(def.Type, str)
	}
	return out, nil
}

// RepositoryDispatch starts the repository_dispatch workflows of the
// default branch that accept eventType.
func (s *Service) RepositoryDispatch(ctx context.Context, who auth.Principal, owner, name, eventType string,
	clientPayload map[string]any) error {
	d, err := s.d()
	if err != nil {
		return err
	}
	if eventType == "" || len(eventType) > 100 {
		return invalid("event_type must have 1 to 100 characters")
	}
	repo, err := s.writable(ctx, d, who, owner, name)
	if err != nil {
		return err
	}
	ref := "refs/heads/" + repo.DefaultBranch
	res, err := d.Browse.Resolve(ctx, browseRepo(repo), ref)
	if err != nil {
		return invalid("the repository is empty")
	}
	if clientPayload == nil {
		clientPayload = map[string]any{}
	}
	payload := map[string]any{"action": eventType, "branch": repo.DefaultBranch, "client_payload": clientPayload}
	return s.start(ctx, d, repo, event{trigger: trigger{Event: "repository_dispatch", Action: eventType, Ref: ref},
		Sha: res.Commit, RunRef: ref, Title: eventType, ActorID: who.UserID, Payload: payload})
}

// parseCron parses a schedule entry and refuses intervals below
// minScheduleInterval.
func parseCron(spec string, now time.Time) (cron.Schedule, error) {
	sched, err := cron.ParseStandard(spec)
	if err != nil {
		return nil, fmt.Errorf("invalid cron %q: %w", spec, err)
	}
	if strings.HasPrefix(strings.TrimSpace(spec), "@every") {
		return nil, fmt.Errorf("invalid cron %q: @every is not supported", spec)
	}
	t := sched.Next(now.UTC())
	// Two days of minute-level occurrences cover every hourly and daily
	// pattern.
	for range 600 {
		n := sched.Next(t)
		if n.Sub(t) < minScheduleInterval {
			return nil, fmt.Errorf("cron %q runs more often than every %s", spec, minScheduleInterval)
		}
		t = n
	}
	return sched, nil
}

// syncSchedules replaces the schedules of a repository with those of the
// default branch at sha.
func (s *Service) syncSchedules(ctx context.Context, d *Deps, repo repos.Repo, sha string) error {
	files, err := s.discover(ctx, d.Browse, browseRepo(repo), sha)
	if err != nil {
		return err
	}
	now := s.now()
	return s.tx(ctx, func(q *dbgen.Queries) error {
		if err := q.DeleteActionsSchedules(ctx, repo.ID); err != nil {
			return fmt.Errorf("delete schedules: %w", err)
		}
		for _, f := range files {
			pw, err := parseWorkflow(f.Path, f.Content)
			if err != nil {
				continue
			}
			filter, ok := pw.On["schedule"]
			if !ok {
				continue
			}
			for _, spec := range filter.Crons {
				sched, err := parseCron(spec, now)
				if err != nil {
					s.log.Info("schedule skipped", "repo_id", repo.ID, "path", f.Path, "err", err)
					continue
				}
				id, err := uuid.NewV7()
				if err != nil {
					return fmt.Errorf("schedule id: %w", err)
				}
				if err := q.InsertActionsSchedule(ctx, dbgen.InsertActionsScheduleParams{ID: id, RepoID: repo.ID,
					WorkflowPath: f.Path, Cron: spec, NextAt: ts(sched.Next(now.UTC()))}); err != nil {
					return fmt.Errorf("insert schedule: %w", err)
				}
			}
		}
		return nil
	})
}

// runSchedules starts the runs of the due schedules.
func (s *Service) runSchedules(ctx context.Context) error {
	d, err := s.d()
	if err != nil {
		return nil // not wired yet: the next tick retries
	}
	now := s.now()
	var due []dbgen.ActionsSchedule
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		rows, err := q.ClaimDueActionsSchedules(ctx)
		if err != nil {
			return fmt.Errorf("claim schedules: %w", err)
		}
		for _, r := range rows {
			next := now.Add(24 * time.Hour)
			if sched, err := parseCron(r.Cron, now); err == nil {
				next = sched.Next(now.UTC())
			}
			if err := q.SetActionsScheduleNext(ctx, dbgen.SetActionsScheduleNextParams{NextAt: ts(next), ID: r.ID}); err != nil {
				return fmt.Errorf("next schedule: %w", err)
			}
		}
		due = rows
		return nil
	})
	if err != nil {
		return err
	}
	for _, r := range due {
		if err := s.runSchedule(ctx, d, r); err != nil {
			s.log.Warn("scheduled run", "repo_id", r.RepoID, "path", r.WorkflowPath, "err", err)
		}
	}
	return nil
}

func (s *Service) runSchedule(ctx context.Context, d *Deps, r dbgen.ActionsSchedule) error {
	repo, err := d.Repos.GetByID(ctx, system, r.RepoID)
	if err != nil {
		return fmt.Errorf("get repo: %w", err)
	}
	ref := "refs/heads/" + repo.DefaultBranch
	res, err := d.Browse.Resolve(ctx, browseRepo(repo), ref)
	if err != nil {
		return fmt.Errorf("resolve default branch: %w", err)
	}
	pw, err := s.findWorkflow(ctx, d, repo, res.Commit, r.WorkflowPath)
	if errors.Is(err, ErrNotFound) {
		return nil // removed since the last sync
	}
	if err != nil {
		return err
	}
	if f, ok := pw.On["schedule"]; !ok || !slices.Contains(f.Crons, r.Cron) {
		return nil
	}
	_, err = s.createRun(ctx, runInput{Repo: repo, WF: pw, Event: "schedule", Ref: ref, Sha: res.Commit,
		Payload: map[string]any{"schedule": r.Cron}})
	return err
}
