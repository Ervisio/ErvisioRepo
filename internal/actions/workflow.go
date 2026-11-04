package actions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gitea.com/gitea/runner/act/model"
	"go.yaml.in/yaml/v4"

	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
)

// Workflow directories, in order of precedence: a file in .ervisio/workflows
// hides the file with the same name in .github/workflows.
var workflowDirs = []string{".ervisio/workflows", ".github/workflows"}

const (
	// maxWorkflowFiles bounds the files read from one commit.
	maxWorkflowFiles = 100
	// maxWorkflowSize bounds one workflow file.
	maxWorkflowSize = 512 << 10
	// maxCallDepth bounds nested reusable workflows (GitHub allows 4 levels).
	maxCallDepth = 4
)

func init() {
	// act aborts the process on a decode error by default.
	model.OnDecodeNodeError = func(yaml.Node, any, error) {}
}

// workflowFile is one workflow file read from a commit.
type workflowFile struct {
	Path    string
	Content []byte
}

// discover lists the workflow files of a commit.
func (s *Service) discover(ctx context.Context, b Browser, repo gitbrowse.Repo, sha string) ([]workflowFile, error) {
	seen := map[string]bool{}
	var out []workflowFile
	for _, dir := range workflowDirs {
		tree, err := b.Tree(ctx, repo, sha, dir, false)
		if errors.Is(err, gitbrowse.ErrNotFound) || errors.Is(err, gitbrowse.ErrEmpty) || errors.Is(err, gitbrowse.ErrInvalid) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", dir, err)
		}
		names := make([]gitbrowse.Entry, 0, len(tree.Entries))
		for _, e := range tree.Entries {
			ext := path.Ext(e.Name)
			if e.Type == gitbrowse.TypeBlob && (ext == ".yml" || ext == ".yaml") {
				names = append(names, e)
			}
		}
		sort.Slice(names, func(i, j int) bool { return names[i].Name < names[j].Name })
		for _, e := range names {
			if seen[e.Name] || len(out) >= maxWorkflowFiles {
				continue
			}
			seen[e.Name] = true
			if e.Size > maxWorkflowSize {
				s.log.Warn("workflow file too large, skipped", "repo_id", repo.ID, "path", e.Path)
				continue
			}
			blob, err := b.Blob(ctx, repo, sha, e.Path)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", e.Path, err)
			}
			if blob.Truncated || blob.Binary {
				continue
			}
			out = append(out, workflowFile{Path: e.Path, Content: blob.Content})
		}
	}
	return out, nil
}

// parsedWorkflow is a workflow file with what the server reads from it.
type parsedWorkflow struct {
	Path  string
	Raw   []byte
	Model *model.Workflow
	// Jobs holds the raw YAML node of every job, by id.
	Jobs map[string]*yaml.Node
	// Order lists the job ids in file order.
	Order []string
	// Doc is the top-level mapping of the file.
	Doc *yaml.Node
	On  map[string]*eventFilter
}

// parseWorkflow parses a workflow file.
func parseWorkflow(p string, raw []byte) (*parsedWorkflow, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("parse %s: the workflow is not a mapping", p)
	}
	top := doc.Content[0]
	wf, err := model.ReadWorkflow(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	wf.File = path.Base(p)
	pw := &parsedWorkflow{Path: p, Raw: raw, Model: wf, Jobs: map[string]*yaml.Node{}, Doc: top}
	if jobs := mapGet(top, "jobs"); jobs != nil && jobs.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(jobs.Content); i += 2 {
			id := jobs.Content[i].Value
			if !jobIDRe.MatchString(id) {
				return nil, fmt.Errorf("parse %s: invalid job id %q", p, id)
			}
			if jobs.Content[i+1].Kind != yaml.MappingNode {
				return nil, fmt.Errorf("parse %s: job %q is not a mapping", p, id)
			}
			pw.Jobs[id] = jobs.Content[i+1]
			pw.Order = append(pw.Order, id)
		}
	}
	if len(pw.Order) == 0 {
		return nil, fmt.Errorf("parse %s: the workflow has no jobs", p)
	}
	for _, id := range pw.Order {
		for _, n := range wf.Jobs[id].Needs() {
			if _, ok := pw.Jobs[n]; !ok {
				return nil, fmt.Errorf("parse %s: job %q needs unknown job %q", p, id, n)
			}
		}
	}
	if err := checkCycles(wf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	on := mapGet(top, "on")
	if on == nil {
		// YAML 1.1 reads a bare on: as true.
		on = mapGet(top, "true")
	}
	if pw.On, err = parseOn(on); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return pw, nil
}

var jobIDRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,99}$`)

func checkCycles(wf *model.Workflow) error {
	state := map[string]int{}
	var visit func(string) error
	visit = func(id string) error {
		switch state[id] {
		case 1:
			return fmt.Errorf("job %q is part of a needs cycle", id)
		case 2:
			return nil
		}
		state[id] = 1
		if j := wf.Jobs[id]; j != nil {
			for _, n := range j.Needs() {
				if err := visit(n); err != nil {
					return err
				}
			}
		}
		state[id] = 2
		return nil
	}
	for id := range wf.Jobs {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

// mapGet returns the value of key in a YAML mapping, or nil.
func mapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mapDel removes key from a YAML mapping.
func mapDel(m *yaml.Node, key string) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = slices.Delete(m.Content, i, i+2)
			return
		}
	}
}

// mapSet sets key in a YAML mapping.
func mapSet(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, scalar(key), v)
}

func scalar(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// nodeStrings reads a scalar or a sequence of scalars.
func nodeStrings(n *yaml.Node) []string {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case yaml.ScalarNode:
		return []string{n.Value}
	case yaml.SequenceNode:
		out := make([]string, 0, len(n.Content))
		for _, c := range n.Content {
			if c.Kind == yaml.ScalarNode {
				out = append(out, c.Value)
			}
		}
		return out
	}
	return nil
}

// eventFilter is the configuration of one event under on:.
type eventFilter struct {
	Types          []string
	Branches       []string
	BranchesIgnore []string
	Tags           []string
	TagsIgnore     []string
	Paths          []string
	PathsIgnore    []string
	hasBranches    bool
	hasTags        bool
	hasPaths       bool
	// Crons are the schedule entries.
	Crons []string
	// Workflows filters workflow_run by workflow name.
	Workflows []string
}

func parseOn(n *yaml.Node) (map[string]*eventFilter, error) {
	out := map[string]*eventFilter{}
	if n == nil {
		return nil, errors.New("on: is missing")
	}
	switch n.Kind {
	case yaml.ScalarNode:
		out[n.Value] = &eventFilter{}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			if c.Kind != yaml.ScalarNode {
				return nil, errors.New("on: list entries must be event names")
			}
			out[c.Value] = &eventFilter{}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			name, v := n.Content[i].Value, n.Content[i+1]
			f := &eventFilter{}
			out[name] = f
			if name == "schedule" {
				if v.Kind != yaml.SequenceNode {
					return nil, errors.New("on.schedule must be a list")
				}
				for _, c := range v.Content {
					if cr := mapGet(c, "cron"); cr != nil && cr.Kind == yaml.ScalarNode {
						f.Crons = append(f.Crons, cr.Value)
					}
				}
				continue
			}
			if v.Kind != yaml.MappingNode {
				continue
			}
			f.Types = nodeStrings(mapGet(v, "types"))
			f.Workflows = nodeStrings(mapGet(v, "workflows"))
			if b := mapGet(v, "branches"); b != nil {
				f.Branches, f.hasBranches = nodeStrings(b), true
			}
			if b := mapGet(v, "branches-ignore"); b != nil {
				f.BranchesIgnore, f.hasBranches = nodeStrings(b), true
			}
			if b := mapGet(v, "tags"); b != nil {
				f.Tags, f.hasTags = nodeStrings(b), true
			}
			if b := mapGet(v, "tags-ignore"); b != nil {
				f.TagsIgnore, f.hasTags = nodeStrings(b), true
			}
			if b := mapGet(v, "paths"); b != nil {
				f.Paths, f.hasPaths = nodeStrings(b), true
			}
			if b := mapGet(v, "paths-ignore"); b != nil {
				f.PathsIgnore, f.hasPaths = nodeStrings(b), true
			}
		}
	default:
		return nil, errors.New("on: has an invalid shape")
	}
	return out, nil
}

// defaultTypes are the activity types an event matches when its filter
// names none (GitHub semantics).
var defaultTypes = map[string][]string{
	"pull_request":        {"opened", "synchronize", "reopened"},
	"pull_request_target": {"opened", "synchronize", "reopened"},
}

// trigger is what an event offers to the filters of a workflow.
type trigger struct {
	Event  string
	Action string
	// Ref is the full ref (push) or the base branch ref (pull requests).
	Ref string
	// Files are the changed paths; nil when unknown (every path filter
	// matches).
	Files []string
}

// matches reports whether f accepts t.
func (f *eventFilter) matches(t trigger) bool {
	types := f.Types
	if len(types) == 0 {
		types = defaultTypes[t.Event]
	}
	if len(types) > 0 && t.Action != "" && !slices.Contains(types, t.Action) {
		return false
	}
	switch t.Event {
	case "push":
		branch, isBranch := strings.CutPrefix(t.Ref, "refs/heads/")
		tag, isTag := strings.CutPrefix(t.Ref, "refs/tags/")
		switch {
		case f.hasBranches || f.hasTags:
			if isBranch && (!f.hasBranches || !refFilter(branch, f.Branches, f.BranchesIgnore)) {
				return false
			}
			if isTag && (!f.hasTags || !refFilter(tag, f.Tags, f.TagsIgnore)) {
				return false
			}
			if !isBranch && !isTag {
				return false
			}
		}
		// Paths filters do not apply to tag pushes.
		if isTag {
			return true
		}
	case "pull_request", "pull_request_target":
		if f.hasBranches && !refFilter(strings.TrimPrefix(t.Ref, "refs/heads/"), f.Branches, f.BranchesIgnore) {
			return false
		}
	}
	if f.hasPaths && t.Files != nil {
		return pathFilter(t.Files, f.Paths, f.PathsIgnore)
	}
	return true
}

// refFilter applies a branches/tags (or -ignore) filter to one name.
func refFilter(name string, include, ignore []string) bool {
	if len(include) > 0 {
		return matchPatterns(name, include)
	}
	if len(ignore) > 0 {
		return !matchPatterns(name, ignore)
	}
	return true
}

// pathFilter applies a paths (or paths-ignore) filter: the workflow runs
// when one file is included (paths) or one file is not ignored.
func pathFilter(files, include, ignore []string) bool {
	for _, f := range files {
		if len(include) > 0 {
			if matchPatterns(f, include) {
				return true
			}
			continue
		}
		if !matchPatterns(f, ignore) {
			return true
		}
	}
	return false
}

// matchPatterns evaluates an ordered pattern list where a leading "!"
// negates: the last matching pattern decides.
func matchPatterns(name string, patterns []string) bool {
	matched := false
	for _, p := range patterns {
		if neg, ok := strings.CutPrefix(p, "!"); ok {
			if globMatch(neg, name) {
				matched = false
			}
			continue
		}
		if globMatch(p, name) {
			matched = true
		}
	}
	return matched
}

// globMatch implements the GitHub filter pattern syntax: * matches
// anything but /, ** anything, ? one character, + one or more of the
// preceding character and [] a character class.
func globMatch(pattern, name string) bool {
	re, err := globRegexp(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(name)
}

func globRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '+':
			b.WriteString("+")
		case '[':
			j := strings.IndexByte(pattern[i:], ']')
			if j < 0 {
				b.WriteString(`\[`)
				continue
			}
			b.WriteString(pattern[i : i+j+1])
			i += j
		case '\\':
			if i+1 < len(pattern) {
				i++
				b.WriteString(regexp.QuoteMeta(string(pattern[i])))
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// workflowName is the display name of a workflow.
func (pw *parsedWorkflow) workflowName() string {
	if pw.Model.Name != "" {
		return pw.Model.Name
	}
	return pw.Path
}
