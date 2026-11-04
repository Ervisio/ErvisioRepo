package actions

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"strconv"
	"strings"

	"gitea.com/gitea/runner/act/exprparser"
	"gitea.com/gitea/runner/act/model"
	"go.yaml.in/yaml/v4"
)

// exprCtx is the expression environment of the server-side evaluations:
// job conditions, matrices, runs-on, names, environments, concurrency
// groups and reusable workflow inputs and outputs.
type exprCtx struct {
	github   *model.GithubContext
	env      map[string]string
	vars     map[string]string
	secrets  map[string]string
	needs    map[string]exprparser.Needs
	inputs   map[string]any
	matrix   map[string]any
	strategy map[string]any
	jobs     map[string]*model.WorkflowCallResult
	// status is the job status the status functions read ("" = success).
	status string
	// workflow and jobID back the status functions: workflow holds the
	// job and one placeholder per need with its result.
	workflow *model.Workflow
	jobID    string
}

func (c *exprCtx) interpreter() exprparser.Interpreter {
	wf, id := c.workflow, c.jobID
	if wf == nil {
		id = "_"
		wf = &model.Workflow{Jobs: map[string]*model.Job{id: {}}}
	}
	status := c.status
	if status == "" {
		status = "success"
	}
	jobs := c.jobs
	if jobs == nil {
		jobs = map[string]*model.WorkflowCallResult{}
	}
	env := &exprparser.EvaluationEnvironment{
		Github:   c.github,
		Env:      nonNilS(c.env),
		Job:      &model.JobContext{Status: status},
		Jobs:     &jobs,
		Runner:   map[string]any{},
		Secrets:  nonNilS(c.secrets),
		Vars:     nonNilS(c.vars),
		Strategy: nonNilA(c.strategy),
		Matrix:   nonNilA(c.matrix),
		Needs:    c.needs,
		Inputs:   nonNilA(c.inputs),
	}
	if env.Github == nil {
		env.Github = &model.GithubContext{Event: map[string]any{}}
	}
	if env.Needs == nil {
		env.Needs = map[string]exprparser.Needs{}
	}
	return exprparser.NewInterpeter(env, exprparser.Config{Run: &model.Run{Workflow: wf, JobID: id}})
}

func nonNilS(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func nonNilA(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// stripExpr removes the ${{ }} wrapper of a whole expression.
func stripExpr(s string) string {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "${{") && strings.HasSuffix(t, "}}") && strings.Count(t, "${{") == 1 {
		return strings.TrimSpace(t[3 : len(t)-2])
	}
	return t
}

// isWholeExpr reports whether s is exactly one ${{ }} expression.
func isWholeExpr(s string) bool {
	t := strings.TrimSpace(s)
	return strings.HasPrefix(t, "${{") && strings.HasSuffix(t, "}}") && strings.Count(t, "${{") == 1 &&
		strings.Index(t, "}}") == len(t)-2
}

// eval evaluates an expression without the ${{ }} wrapper.
func (c *exprCtx) eval(expr string) (any, error) {
	v, err := c.interpreter().Evaluate(expr, exprparser.DefaultStatusCheckNone)
	if err != nil {
		return nil, fmt.Errorf("evaluate %q: %w", expr, err)
	}
	return v, nil
}

// evalIf evaluates a job condition: success() is implied unless the
// condition calls a status function.
func (c *exprCtx) evalIf(cond string) (bool, error) {
	expr := stripExpr(cond)
	if expr == "" {
		expr = "success()"
	}
	v, err := c.interpreter().Evaluate(expr, exprparser.DefaultStatusCheckSuccess)
	if err != nil {
		return false, fmt.Errorf("evaluate if %q: %w", cond, err)
	}
	return exprparser.IsTruthy(v), nil
}

// interpolate replaces every ${{ }} segment of s by its string form.
func (c *exprCtx) interpolate(s string) (string, error) {
	if !strings.Contains(s, "${{") {
		return s, nil
	}
	var b strings.Builder
	rest := s
	for {
		i := strings.Index(rest, "${{")
		if i < 0 {
			b.WriteString(rest)
			return b.String(), nil
		}
		j := strings.Index(rest[i:], "}}")
		if j < 0 {
			return "", fmt.Errorf("unterminated expression in %q", s)
		}
		b.WriteString(rest[:i])
		v, err := c.eval(strings.TrimSpace(rest[i+3 : i+j]))
		if err != nil {
			return "", err
		}
		b.WriteString(exprString(v))
		rest = rest[i+j+2:]
	}
}

// value evaluates a scalar: a whole expression keeps its type, anything
// else is interpolated into a string.
func (c *exprCtx) value(s string) (any, error) {
	if isWholeExpr(s) {
		return c.eval(stripExpr(s))
	}
	return c.interpolate(s)
}

// node returns a copy of n with every scalar evaluated.
func (c *exprCtx) node(n *yaml.Node) (*yaml.Node, error) {
	if n == nil {
		return nil, nil
	}
	switch n.Kind {
	case yaml.DocumentNode, yaml.AliasNode:
		if len(n.Content) == 0 {
			return n, nil
		}
		if n.Kind == yaml.AliasNode {
			return c.node(n.Alias)
		}
		return c.node(n.Content[0])
	case yaml.ScalarNode:
		if !strings.Contains(n.Value, "${{") {
			cp := *n
			return &cp, nil
		}
		v, err := c.value(n.Value)
		if err != nil {
			return nil, err
		}
		return toNode(v)
	}
	cp := *n
	cp.Content = make([]*yaml.Node, len(n.Content))
	for i, ch := range n.Content {
		if n.Kind == yaml.MappingNode && i%2 == 0 {
			k := *ch
			cp.Content[i] = &k
			continue
		}
		v, err := c.node(ch)
		if err != nil {
			return nil, err
		}
		cp.Content[i] = v
	}
	return &cp, nil
}

// toNode encodes v as a YAML node.
func toNode(v any) (*yaml.Node, error) {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return nil, fmt.Errorf("encode value: %w", err)
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		return n.Content[0], nil
	}
	return &n, nil
}

// exprString is the string form of an expression result.
func exprString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e15 {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// withStatusJobs sets the workflow the status functions read: the job id
// with its needs, each need a placeholder carrying its result.
func (c *exprCtx) withStatusJobs(id string, job *model.Job, needs map[string]exprparser.Needs) {
	wf := &model.Workflow{Jobs: map[string]*model.Job{}}
	cp := *job
	cp.RawNeeds = yaml.Node{Kind: yaml.SequenceNode}
	for n := range needs {
		cp.RawNeeds.Content = append(cp.RawNeeds.Content, scalar(n))
		wf.Jobs[n] = &model.Job{Name: n, Result: needs[n].Result}
		wf.Jobs[n].If.Value = "success()"
	}
	if cp.Name == "" {
		cp.Name = id
	}
	wf.Jobs[id] = &cp
	c.workflow, c.jobID = wf, id
	c.needs = maps.Clone(needs)
}
