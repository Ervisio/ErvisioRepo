package actions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"gitea.com/gitea/runner/act/exprparser"
	"gitea.com/gitea/runner/act/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.yaml.in/yaml/v4"

	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
)

const (
	// maxMatrix bounds the combinations of one matrix.
	maxMatrix = 256
	// defaultTimeoutMinutes is the timeout of a job without timeout-minutes.
	defaultTimeoutMinutes = 360
)

// secretLevel is one reusable workflow call between the repository secrets
// and the secrets a called job sees.
type secretLevel struct {
	// Inherit passes the caller secrets unchanged (secrets: inherit).
	Inherit bool `json:"inherit,omitempty"`
	// Map names the callee secrets with expressions over the caller
	// secrets; secrets that are not named are not passed.
	Map map[string]string `json:"map,omitempty"`
}

// jobExtra is what the planner keeps about a job beside its row (stored in
// the inputs column).
type jobExtra struct {
	// Inputs is the inputs context of the job (dispatch or call inputs).
	Inputs map[string]any `json:"inputs,omitempty"`
	// Prefix is the key prefix of the jobs of the same workflow file.
	Prefix string `json:"prefix,omitempty"`
	// Path is the workflow file of the job.
	Path string `json:"path,omitempty"`
	// Depth is the reusable workflow nesting level.
	Depth int `json:"depth,omitempty"`
	// Caller is the display name of the calling job.
	Caller  string        `json:"caller,omitempty"`
	Secrets []secretLevel `json:"secrets,omitempty"`
	// Expanded marks a job that calls a reusable workflow: its own row is
	// an aggregate of the rows under Callee.
	Expanded      bool              `json:"expanded,omitempty"`
	Callee        string            `json:"callee,omitempty"`
	CalleeInputs  map[string]any    `json:"callee_inputs,omitempty"`
	CalleeOutputs map[string]string `json:"callee_outputs,omitempty"`
}

func decodeExtra(b []byte) jobExtra {
	var e jobExtra
	if len(b) > 0 {
		_ = json.Unmarshal(b, &e)
	}
	return e
}

func (e jobExtra) encode() []byte {
	b, _ := json.Marshal(e)
	return b
}

// runInput describes a run to create.
type runInput struct {
	Repo   repos.Repo
	WF     *parsedWorkflow
	Event  string
	Action string
	Ref    string
	Sha    string
	Title  string
	// ActorID is who caused the event, TriggerActorID who started this
	// attempt (a re-run).
	ActorID uuid.UUID
	Payload map[string]any
	// Inputs are the workflow_dispatch inputs.
	Inputs     map[string]any
	HeadRepoID uuid.UUID
	// Fork marks a pull request from another repository: no secrets and a
	// read-only token.
	Fork          bool
	NeedsApproval bool
}

// createRun stores a run with one blocked row per job and starts it.
func (s *Service) createRun(ctx context.Context, in runInput) (dbgen.ActionsRun, error) {
	if len(in.WF.Order) > s.maxJobs() {
		return dbgen.ActionsRun{}, invalid("%s has %d jobs, more than the limit of %d", in.WF.Path, len(in.WF.Order), s.maxJobs())
	}
	payload := in.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	if in.Inputs != nil {
		payload["inputs"] = in.Inputs
	}
	evJSON, err := json.Marshal(payload)
	if err != nil {
		return dbgen.ActionsRun{}, fmt.Errorf("encode event payload: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return dbgen.ActionsRun{}, fmt.Errorf("run id: %w", err)
	}
	title := in.Title
	if title == "" {
		title = in.WF.workflowName()
	}
	title = truncate(title, 1024)
	actor := s.userName(ctx, nullID(in.ActorID))
	var run dbgen.ActionsRun
	var cancel []uuid.UUID
	err = s.tx(ctx, func(q *dbgen.Queries) error {
		num, err := q.NextActionsRunNumber(ctx, dbgen.NextActionsRunNumberParams{RepoID: in.Repo.ID, WorkflowPath: in.WF.Path})
		if err != nil {
			return fmt.Errorf("run number: %w", err)
		}
		draft := dbgen.ActionsRun{ID: id, RepoID: in.Repo.ID, WorkflowPath: in.WF.Path, WorkflowName: in.WF.workflowName(),
			Event: in.Event, Ref: in.Ref, Sha: in.Sha, RunNumber: num, Attempt: 1, EventPayload: evJSON}
		ec := &exprCtx{github: s.githubCtx(draft, in.Repo, actor), inputs: in.Inputs}
		group, cancelInProgress, err := evalConcurrency(ec, mapGet(in.WF.Doc, "concurrency"))
		if err != nil {
			return invalid("%s: %v", in.WF.Path, err)
		}
		status := "queued"
		switch {
		case in.NeedsApproval:
			status = "waiting"
		case group != "":
			if err := q.LockActionsGroup(ctx, "run:"+in.Repo.ID.String()+":"+group); err != nil {
				return fmt.Errorf("lock group: %w", err)
			}
			others, err := q.ListActiveActionsRunsInGroup(ctx, dbgen.ListActiveActionsRunsInGroupParams{
				RepoID: in.Repo.ID, ConcurrencyGroup: group, ID: id})
			if err != nil {
				return fmt.Errorf("concurrency group: %w", err)
			}
			busy := false
			for _, o := range others {
				switch {
				case cancelInProgress:
					cancel = append(cancel, o.ID)
				case o.Status == "pending":
					// Only the newest run of a group waits.
					cancel = append(cancel, o.ID)
				case o.Status == "queued" || o.Status == "in_progress":
					busy = true
				}
			}
			if busy {
				status = "pending"
			}
		}
		run, err = q.InsertActionsRun(ctx, dbgen.InsertActionsRunParams{ID: id, RepoID: in.Repo.ID, WorkflowPath: in.WF.Path,
			WorkflowName: truncate(in.WF.workflowName(), 1024), Workflow: in.WF.Raw, Event: in.Event, EventAction: in.Action,
			Ref: in.Ref, Sha: in.Sha, Title: title, RunNumber: num, Status: status, ActorID: nullID(in.ActorID),
			TriggerActorID: nullID(in.ActorID), EventPayload: evJSON, ConcurrencyGroup: truncate(group, 1024),
			ConcurrencyCancel: cancelInProgress, HeadRepoID: nullID(in.HeadRepoID), Fork: in.Fork, NeedsApproval: in.NeedsApproval})
		if err != nil {
			return fmt.Errorf("insert run: %w", err)
		}
		extra := jobExtra{Inputs: in.Inputs, Path: in.WF.Path}
		return s.insertTemplates(ctx, q, run, in.WF, extra, defaultPermissions())
	})
	if err != nil {
		return dbgen.ActionsRun{}, err
	}
	for _, c := range cancel {
		if err := s.cancelRun(ctx, c, uuid.Nil); err != nil {
			s.log.Warn("cancel run of the concurrency group", "run_id", c, "err", err)
		}
	}
	s.log.Info("workflow run created", "run_id", run.ID, "repo_id", run.RepoID, "workflow", run.WorkflowPath,
		"event", run.Event, "status", run.Status)
	if err := s.advance(ctx, run.ID); err != nil {
		s.log.Warn("advance new run", "run_id", run.ID, "err", err)
	}
	return run, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut]
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

func defaultPermissions() map[string]string { return map[string]string{"contents": "read"} }

// parsePermissions reads a permissions: node.
func parsePermissions(n *yaml.Node) (map[string]string, bool) {
	if n == nil {
		return nil, false
	}
	switch n.Kind {
	case yaml.ScalarNode:
		switch n.Value {
		case "read-all":
			return map[string]string{"all": "read"}, true
		case "write-all":
			return map[string]string{"all": "write"}, true
		}
		return map[string]string{}, true
	case yaml.MappingNode:
		out := map[string]string{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			out[n.Content[i].Value] = n.Content[i+1].Value
		}
		return out, true
	}
	return map[string]string{}, true
}

// skeleton is the top-level mapping of a workflow without its jobs and
// its concurrency.
func skeleton(top *yaml.Node) *yaml.Node {
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i+1 < len(top.Content); i += 2 {
		switch top.Content[i].Value {
		case "jobs", "concurrency":
			continue
		}
		out.Content = append(out.Content, cloneNode(top.Content[i]), cloneNode(top.Content[i+1]))
	}
	return out
}

func cloneNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	cp := *n
	if n.Alias != nil {
		cp.Alias = cloneNode(n.Alias)
	}
	if len(n.Content) > 0 {
		cp.Content = make([]*yaml.Node, len(n.Content))
		for i, c := range n.Content {
			cp.Content[i] = cloneNode(c)
		}
	}
	return &cp
}

// jobPayload is a single-job workflow.
func jobPayload(skel *yaml.Node, id string, job *yaml.Node) ([]byte, error) {
	top := cloneNode(skel)
	mapSet(top, "jobs", &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{scalar(id), job}})
	b, err := yaml.Marshal(&yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{top}})
	if err != nil {
		return nil, fmt.Errorf("encode job %s: %w", id, err)
	}
	return b, nil
}

// insertTemplates inserts one blocked row per job of pw.
func (s *Service) insertTemplates(ctx context.Context, q *dbgen.Queries, run dbgen.ActionsRun, pw *parsedWorkflow,
	extra jobExtra, defPerms map[string]string) error {
	skel := skeleton(pw.Doc)
	wfPerms, ok := parsePermissions(mapGet(pw.Doc, "permissions"))
	if !ok {
		wfPerms = defPerms
	}
	for _, id := range pw.Order {
		node := pw.Jobs[id]
		payload, err := jobPayload(skel, id, cloneNode(node))
		if err != nil {
			return err
		}
		perms, ok := parsePermissions(mapGet(node, "permissions"))
		if !ok {
			perms = wfPerms
		}
		pj, _ := json.Marshal(perms)
		needs := make([]string, 0)
		for _, n := range pw.Model.Jobs[id].Needs() {
			needs = append(needs, extra.Prefix+n)
		}
		name := id
		if extra.Caller != "" {
			name = extra.Caller + " / " + id
		}
		cond := ""
		if c := mapGet(node, "if"); c != nil && c.Kind == yaml.ScalarNode {
			cond = c.Value
		}
		jid, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("job id: %w", err)
		}
		if _, err := q.InsertActionsJob(ctx, dbgen.InsertActionsJobParams{ID: jid, RunID: run.ID, RepoID: run.RepoID,
			Attempt: run.Attempt, JobKey: extra.Prefix + id, Name: truncate(name, 1024), Needs: needs, Payload: payload,
			IfExpr: cond, RunsOn: []string{}, TimeoutMinutes: s.maxTimeoutMinutes(), Permissions: pj,
			Inputs: extra.encode(), Status: "blocked", FailFast: true, Outputs: []byte("{}")}); err != nil {
			return fmt.Errorf("insert job %s: %w", id, err)
		}
	}
	return nil
}

// evalConcurrency reads a concurrency: node (a group string or a mapping
// with group and cancel-in-progress).
func evalConcurrency(ec *exprCtx, n *yaml.Node) (string, bool, error) {
	if n == nil {
		return "", false, nil
	}
	switch n.Kind {
	case yaml.ScalarNode:
		g, err := ec.interpolate(n.Value)
		return strings.TrimSpace(g), false, err
	case yaml.MappingNode:
		var group string
		if g := mapGet(n, "group"); g != nil {
			v, err := ec.interpolate(g.Value)
			if err != nil {
				return "", false, err
			}
			group = strings.TrimSpace(v)
		}
		cancel := false
		if c := mapGet(n, "cancel-in-progress"); c != nil {
			v, err := ec.value(c.Value)
			if err != nil {
				return "", false, err
			}
			cancel = exprparser.IsTruthy(v)
			if s, ok := v.(string); ok {
				cancel = s == "true"
			}
		}
		return group, cancel, nil
	}
	return "", false, errors.New("concurrency has an invalid shape")
}

// runCtx is what the planner reads while it advances one run.
type runCtx struct {
	run  dbgen.ActionsRun
	repo repos.Repo
	gh   *model.GithubContext
	vars map[string]string
	b    Browser
	rows int
}

// aggregate folds the rows of one job key into a needs result.
func aggregate(rows []dbgen.ActionsJob) (string, bool) {
	if len(rows) == 0 {
		return "skipped", true
	}
	failed, cancelled, skipped := false, false, 0
	for _, r := range rows {
		if r.Status != "completed" {
			return "", false
		}
		switch r.Conclusion {
		case "failure", "timed_out":
			failed = true
		case "cancelled":
			cancelled = true
		case "skipped":
			skipped++
		}
	}
	switch {
	case failed:
		return "failure", true
	case cancelled:
		return "cancelled", true
	case skipped == len(rows):
		return "skipped", true
	}
	return "success", true
}

func mergedOutputs(rows []dbgen.ActionsJob) map[string]string {
	out := map[string]string{}
	for _, r := range rows {
		var m map[string]string
		if json.Unmarshal(r.Outputs, &m) == nil {
			maps.Copy(out, m)
		}
	}
	return out
}

// readyJob makes a blocked job concrete: its condition, matrix, runs-on,
// environment, concurrency, timeout and single-job payload. It returns
// whether any row was queued or set waiting.
func (s *Service) readyJob(ctx context.Context, q *dbgen.Queries, rc *runCtx, row dbgen.ActionsJob,
	byKey map[string][]dbgen.ActionsJob) error {
	extra := decodeExtra(row.Inputs)
	id := strings.TrimPrefix(row.JobKey, extra.Prefix)
	var doc yaml.Node
	if err := yaml.Unmarshal(row.Payload, &doc); err != nil || len(doc.Content) == 0 {
		return s.failRow(ctx, q, row, "The job payload cannot be read.")
	}
	top := doc.Content[0]
	jobNode := mapGet(mapGet(top, "jobs"), id)
	wf, err := model.ReadWorkflow(bytes.NewReader(row.Payload))
	if err != nil || jobNode == nil || wf.GetJob(id) == nil {
		return s.failRow(ctx, q, row, "The job payload cannot be read.")
	}
	job := wf.GetJob(id)

	needs := map[string]exprparser.Needs{}
	for _, n := range row.Needs {
		res, _ := aggregate(byKey[n])
		needs[strings.TrimPrefix(n, extra.Prefix)] = exprparser.Needs{Result: res, Outputs: mergedOutputs(byKey[n])}
	}
	ec := &exprCtx{github: rc.gh, vars: rc.vars, inputs: extra.Inputs}
	ec.withStatusJobs(id, job, needs)
	ok, err := ec.evalIf(row.IfExpr)
	if err != nil {
		return s.failRow(ctx, q, row, fmt.Sprintf("Error in the job condition: %v", err))
	}
	if !ok {
		return q.CompleteActionsJob(ctx, dbgen.CompleteActionsJobParams{Conclusion: "skipped", ID: row.ID})
	}

	combos, failFast, maxParallel, err := matrixOf(ec, mapGet(jobNode, "strategy"))
	if err != nil {
		return s.failRow(ctx, q, row, fmt.Sprintf("Error in the job matrix: %v", err))
	}
	if rc.rows+len(combos)-1 > s.maxJobs() {
		return s.failRow(ctx, q, row, fmt.Sprintf("The run would have more than %d jobs.", s.maxJobs()))
	}
	rc.rows += len(combos) - 1

	skel := skeleton(top)
	for i, combo := range combos {
		ci := *ec
		ci.matrix = combo
		ci.strategy = map[string]any{"fail-fast": failFast, "max-parallel": float64(maxParallel),
			"job-index": float64(i), "job-total": float64(len(combos))}
		var mj []byte
		if len(combo) > 0 {
			mj, _ = json.Marshal(combo)
		}
		name := defaultName(id, combo)
		if n := mapGet(jobNode, "name"); n != nil && n.Kind == yaml.ScalarNode {
			if v, err := ci.interpolate(n.Value); err == nil && strings.TrimSpace(v) != "" {
				name = v
			}
		}
		if extra.Caller != "" {
			name = extra.Caller + " / " + name
		}
		name = truncate(name, 1024)
		p := readyParams{row: row, index: i, name: name, matrix: mj, failFast: failFast, maxParallel: maxParallel}
		if job.Uses != "" {
			err = s.expandCall(ctx, q, rc, &ci, p, extra, job, jobNode)
		} else {
			err = s.readyCombo(ctx, q, &ci, p, extra, skel, id, jobNode, combo)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// readyParams is one matrix combination of a job being readied.
type readyParams struct {
	row         dbgen.ActionsJob
	index       int
	name        string
	matrix      []byte
	failFast    bool
	maxParallel int32
}

// store writes the row of one combination: the first reuses the template
// row, the others are new rows with the same key.
func (s *Service) store(ctx context.Context, q *dbgen.Queries, p readyParams, status string, payload []byte,
	runsOn []string, env, group string, cancel bool, timeout int32, extra jobExtra) error {
	if p.index == 0 {
		_, err := q.ReadyActionsJob(ctx, dbgen.ReadyActionsJobParams{Status: status, Name: p.name, Matrix: p.matrix,
			Payload: payload, RunsOn: runsOn, Environment: env, ConcurrencyGroup: group, ConcurrencyCancel: cancel,
			TimeoutMinutes: timeout, FailFast: p.failFast, MaxParallel: p.maxParallel, Inputs: extra.encode(), ID: p.row.ID})
		if err != nil {
			return fmt.Errorf("ready job: %w", err)
		}
		return nil
	}
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("job id: %w", err)
	}
	r := p.row
	if _, err := q.InsertActionsJob(ctx, dbgen.InsertActionsJobParams{ID: id, RunID: r.RunID, RepoID: r.RepoID,
		Attempt: r.Attempt, JobKey: r.JobKey, Name: p.name, Needs: r.Needs, Matrix: p.matrix, Payload: payload,
		IfExpr: r.IfExpr, RunsOn: runsOn, TimeoutMinutes: timeout, Permissions: r.Permissions, Inputs: extra.encode(),
		Environment: env, Status: status, ConcurrencyGroup: group, ConcurrencyCancel: cancel, FailFast: p.failFast,
		MaxParallel: p.maxParallel, Outputs: []byte("{}")}); err != nil {
		return fmt.Errorf("insert matrix job: %w", err)
	}
	return nil
}

func (s *Service) readyCombo(ctx context.Context, q *dbgen.Queries, ec *exprCtx, p readyParams, extra jobExtra,
	skel *yaml.Node, id string, jobNode *yaml.Node, combo map[string]any) error {
	fail := func(msg string) error {
		if p.index == 0 {
			return s.failRow(ctx, q, p.row, msg)
		}
		return s.store(ctx, q, p, "blocked", p.row.Payload, []string{}, "", "", false, 1, extra)
	}
	ron, err := ec.node(mapGet(jobNode, "runs-on"))
	if err != nil {
		return fail(fmt.Sprintf("Error in runs-on: %v", err))
	}
	var raw []string
	if ron != nil {
		raw = model.RunsOnFromNode(*ron)
	}
	var labels []string
	for _, l := range raw {
		l = strings.ToLower(strings.TrimSpace(l))
		if l != "" && !slices.Contains(labels, l) {
			labels = append(labels, l)
		}
	}
	if len(labels) == 0 {
		return fail("The job has no runs-on labels.")
	}
	env := ""
	if en := mapGet(jobNode, "environment"); en != nil {
		v := en.Value
		if en.Kind == yaml.MappingNode {
			v = ""
			if nn := mapGet(en, "name"); nn != nil {
				v = nn.Value
			}
		}
		if env, err = ec.interpolate(v); err != nil {
			return fail(fmt.Sprintf("Error in environment: %v", err))
		}
		env = truncate(strings.TrimSpace(env), 255)
	}
	group, cancel, err := evalConcurrency(ec, mapGet(jobNode, "concurrency"))
	if err != nil {
		return fail(fmt.Sprintf("Error in concurrency: %v", err))
	}
	group = truncate(group, 1024)
	timeout := int32(defaultTimeoutMinutes)
	if tn := mapGet(jobNode, "timeout-minutes"); tn != nil {
		v, err := ec.value(tn.Value)
		if err != nil {
			return fail(fmt.Sprintf("Error in timeout-minutes: %v", err))
		}
		f, err := strconv.ParseFloat(exprString(v), 64)
		if err != nil || f <= 0 {
			return fail(fmt.Sprintf("Invalid timeout-minutes %q.", exprString(v)))
		}
		timeout = int32(min(f, 1<<20))
		if timeout < 1 {
			timeout = 1
		}
	}
	timeout = min(timeout, s.maxTimeoutMinutes())

	jn := cloneNode(jobNode)
	for _, k := range []string{"needs", "environment", "concurrency", "strategy"} {
		mapDel(jn, k)
	}
	mapSet(jn, "if", scalar("always()"))
	if len(combo) > 0 {
		m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		keys := slices.Sorted(maps.Keys(combo))
		for _, k := range keys {
			v, err := toNode(combo[k])
			if err != nil {
				return fail(fmt.Sprintf("Error in the job matrix: %v", err))
			}
			m.Content = append(m.Content, scalar(k), &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{v}})
		}
		mapSet(jn, "strategy", &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{scalar("matrix"), m}})
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, l := range raw {
		if strings.TrimSpace(l) != "" {
			seq.Content = append(seq.Content, scalar(strings.TrimSpace(l)))
		}
	}
	mapSet(jn, "runs-on", seq)
	mapSet(jn, "timeout-minutes", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(int(timeout))})
	mapSet(jn, "name", scalar(p.name))
	payload, err := jobPayload(skel, id, jn)
	if err != nil {
		return fail(err.Error())
	}
	status := "queued"
	if env != "" || group != "" {
		status = "waiting"
	}
	return s.store(ctx, q, p, status, payload, labels, env, group, cancel, timeout, extra)
}

// expandCall turns a job that calls a local reusable workflow into an
// aggregate row and inserts the jobs of the called workflow.
func (s *Service) expandCall(ctx context.Context, q *dbgen.Queries, rc *runCtx, ec *exprCtx, p readyParams,
	extra jobExtra, job *model.Job, jobNode *yaml.Node) error {
	fail := func(msg string) error {
		if p.index == 0 {
			return s.failRow(ctx, q, p.row, msg)
		}
		return nil
	}
	uses := strings.TrimSpace(job.Uses)
	rel, ok := strings.CutPrefix(uses, "./")
	if !ok || strings.Contains(uses, "@") {
		return fail(fmt.Sprintf("Reusable workflow %q is not supported: only workflows of the same repository (./.github/workflows/… or ./.ervisio/workflows/…) can be called.", uses))
	}
	file := path.Clean(rel)
	if !slices.Contains(workflowDirs, path.Dir(file)) || (path.Ext(file) != ".yml" && path.Ext(file) != ".yaml") {
		return fail(fmt.Sprintf("Reusable workflow %q must be a file of .github/workflows or .ervisio/workflows.", uses))
	}
	if extra.Depth+1 > maxCallDepth {
		return fail(fmt.Sprintf("Reusable workflows can be nested at most %d levels.", maxCallDepth))
	}
	blob, err := rc.b.Blob(ctx, browseRepo(rc.repo), rc.run.Sha, file)
	if err != nil || blob.Truncated || blob.Binary {
		return fail(fmt.Sprintf("Reusable workflow %s cannot be read at %s.", file, rc.run.Sha[:12]))
	}
	pw, err := parseWorkflow(file, blob.Content)
	if err != nil {
		return fail(err.Error())
	}
	if pw.On["workflow_call"] == nil {
		return fail(fmt.Sprintf("Workflow %s has no workflow_call trigger.", file))
	}
	call := pw.Model.WorkflowCallConfig()
	inputs := map[string]any{}
	for k, in := range call.Inputs {
		if in.Default != "" {
			inputs[k] = typedInput(in.Type, in.Default)
		}
	}
	if w := mapGet(jobNode, "with"); w != nil {
		ev, err := ec.node(w)
		if err != nil {
			return fail(fmt.Sprintf("Error in with: %v", err))
		}
		var m map[string]any
		if err := ev.Decode(&m); err != nil {
			return fail(fmt.Sprintf("Invalid with: %v", err))
		}
		for k, v := range m {
			if _, ok := call.Inputs[k]; !ok {
				return fail(fmt.Sprintf("Input %q is not defined by %s.", k, file))
			}
			inputs[k] = v
		}
	}
	for k, in := range call.Inputs {
		if _, ok := inputs[k]; !ok && in.Required {
			return fail(fmt.Sprintf("Input %q of %s is required.", k, file))
		}
	}
	level := secretLevel{Map: map[string]string{}}
	if job.InheritSecrets() {
		level = secretLevel{Inherit: true}
	} else if sn := mapGet(jobNode, "secrets"); sn != nil && sn.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(sn.Content); i += 2 {
			level.Map[sn.Content[i].Value] = sn.Content[i+1].Value
		}
	}
	prefix := p.row.JobKey + "."
	if len(p.matrix) > 0 {
		prefix = p.row.JobKey + "[" + strconv.Itoa(p.index) + "]."
	}
	if rc.rows+len(pw.Order) > s.maxJobs() {
		return fail(fmt.Sprintf("The run would have more than %d jobs.", s.maxJobs()))
	}
	rc.rows += len(pw.Order)
	callee := jobExtra{Inputs: inputs, Prefix: prefix, Path: file, Depth: extra.Depth + 1, Caller: p.name,
		Secrets: append(slices.Clone(extra.Secrets), level)}
	var perms map[string]string
	_ = json.Unmarshal(p.row.Permissions, &perms)
	if perms == nil {
		perms = defaultPermissions()
	}
	if err := s.insertTemplates(ctx, q, rc.run, pw, callee, perms); err != nil {
		return err
	}
	agg := extra
	agg.Expanded, agg.Callee, agg.CalleeInputs = true, prefix, inputs
	agg.CalleeOutputs = map[string]string{}
	for k, o := range call.Outputs {
		agg.CalleeOutputs[k] = o.Value
	}
	return s.store(ctx, q, p, "blocked", p.row.Payload, []string{}, "", "", false, p.row.TimeoutMinutes, agg)
}

// typedInput converts a string input to its declared type.
func typedInput(typ, v string) any {
	switch typ {
	case "boolean":
		return v == "true"
	case "number":
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return v
}

// matrixOf evaluates a strategy: node into its combinations.
func matrixOf(ec *exprCtx, n *yaml.Node) ([]map[string]any, bool, int32, error) {
	if n == nil {
		return []map[string]any{nil}, true, 0, nil
	}
	ev, err := ec.node(n)
	if err != nil {
		return nil, false, 0, err
	}
	var st model.Strategy
	if err := ev.Decode(&st); err != nil {
		return nil, false, 0, fmt.Errorf("invalid strategy: %w", err)
	}
	mp := int32(0)
	if st.MaxParallelString != "" {
		v, err := strconv.Atoi(strings.TrimSpace(st.MaxParallelString))
		if err != nil || v < 0 {
			return nil, false, 0, fmt.Errorf("invalid max-parallel %q", st.MaxParallelString)
		}
		mp = int32(min(v, maxMatrix))
	}
	if st.RawMatrix.Kind == 0 {
		return []map[string]any{nil}, st.GetFailFast(), mp, nil
	}
	if st.RawMatrix.Kind != yaml.MappingNode {
		return nil, false, 0, errors.New("matrix must be a mapping")
	}
	j := &model.Job{Strategy: &st}
	m := j.Matrix()
	if m == nil {
		return nil, false, 0, errors.New("matrix cannot be read")
	}
	product := 1
	for k, v := range m {
		if k == "include" || k == "exclude" {
			continue
		}
		product *= max(len(v), 1)
		if product > 16*maxMatrix {
			return nil, false, 0, fmt.Errorf("the matrix has more than %d combinations", maxMatrix)
		}
	}
	combos, err := j.GetMatrixes()
	if err != nil {
		return nil, false, 0, err
	}
	if len(combos) == 0 {
		return nil, false, 0, errors.New("the matrix has no combinations")
	}
	if len(combos) > maxMatrix {
		return nil, false, 0, fmt.Errorf("the matrix has %d combinations, more than %d", len(combos), maxMatrix)
	}
	for i, c := range combos {
		if len(c) == 0 {
			combos[i] = nil
		}
	}
	return combos, st.FailFast, mp, nil
}

func defaultName(id string, combo map[string]any) string {
	if len(combo) == 0 {
		return id
	}
	keys := make([]string, 0, len(combo))
	for k := range combo {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	vals := make([]string, 0, len(keys))
	for _, k := range keys {
		vals = append(vals, exprString(combo[k]))
	}
	return id + " (" + strings.Join(vals, ", ") + ")"
}

// failRow ends a job that cannot run with a one-line log.
func (s *Service) failRow(ctx context.Context, q *dbgen.Queries, row dbgen.ActionsJob, msg string) error {
	at := pgtype.Timestamptz{Time: s.now(), Valid: true}
	if err := q.InsertActionsLogRows(ctx, dbgen.InsertActionsLogRowsParams{JobID: row.ID, Idxs: []int64{row.LogLength},
		Ats: []pgtype.Timestamptz{at}, Contents: []string{"##[error]" + msg}}); err != nil {
		return fmt.Errorf("job log: %w", err)
	}
	if err := q.SetActionsJobLogLength(ctx, dbgen.SetActionsJobLogLengthParams{LogLength: row.LogLength + 1, ID: row.ID}); err != nil {
		return fmt.Errorf("job log: %w", err)
	}
	if err := q.CompleteActionsJob(ctx, dbgen.CompleteActionsJobParams{Conclusion: "failure", ID: row.ID}); err != nil {
		return fmt.Errorf("fail job: %w", err)
	}
	return nil
}

// githubCtx is the github context of a run.
func (s *Service) githubCtx(run dbgen.ActionsRun, repo repos.Repo, actor string) *model.GithubContext {
	var ev map[string]any
	_ = json.Unmarshal(run.EventPayload, &ev)
	if ev == nil {
		ev = map[string]any{}
	}
	refName, refType := run.Ref, ""
	switch {
	case strings.HasPrefix(run.Ref, "refs/heads/"):
		refName, refType = strings.TrimPrefix(run.Ref, "refs/heads/"), "branch"
	case strings.HasPrefix(run.Ref, "refs/tags/"):
		refName, refType = strings.TrimPrefix(run.Ref, "refs/tags/"), "tag"
	case strings.HasPrefix(run.Ref, "refs/pull/"):
		refName = strings.TrimPrefix(run.Ref, "refs/pull/")
	}
	var headRef, baseRef string
	if pr, ok := ev["pull_request"].(map[string]any); ok {
		if h, ok := pr["head"].(map[string]any); ok {
			headRef, _ = h["ref"].(string)
		}
		if b, ok := pr["base"].(map[string]any); ok {
			baseRef, _ = b["ref"].(string)
		}
	}
	return &model.GithubContext{Event: ev, Workflow: run.WorkflowName, RunID: run.ID.String(),
		RunNumber: strconv.FormatInt(run.RunNumber, 10), RunAttempt: strconv.Itoa(int(max(run.Attempt, 1))),
		Actor: actor, Repository: repo.FullName(), EventName: run.Event, Sha: run.Sha, Ref: run.Ref,
		RefName: refName, RefType: refType, HeadRef: headRef, BaseRef: baseRef, RepositoryOwner: repo.Owner,
		RetentionDays: "90", ServerURL: s.public, APIURL: s.public + "/api/v3", GraphQLURL: s.public + "/api/graphql"}
}

// userName is the username of a user, or the Actions bot.
func (s *Service) userName(ctx context.Context, id uuid.NullUUID) string {
	if id.Valid {
		if u, err := s.q.GetUserByID(ctx, id.UUID); err == nil {
			return u.Username
		}
	}
	return "ervisio-actions[bot]"
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: !t.IsZero()} }
