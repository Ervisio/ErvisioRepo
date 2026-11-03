package issues

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/gitbrowse"
)

// Issue template location and limits.
const (
	TemplateDir  = ".github/ISSUE_TEMPLATE"
	MaxTemplates = 50
)

// Template kinds.
const (
	TemplateMarkdown = "markdown"
	TemplateForm     = "form"
)

// Template is an issue template of the default branch: a Markdown file
// with front matter or a YAML issue form.
type Template struct {
	FileName  string
	Kind      string
	Name      string
	About     string
	Title     string
	Labels    []string
	Assignees []string
	Body      string           // Markdown templates
	Form      []map[string]any // issue forms, as written
}

// ContactLink is an external link offered on the new issue page.
type ContactLink struct {
	Name  string
	URL   string
	About string
}

// TemplateConfig is .github/ISSUE_TEMPLATE/config.yml.
type TemplateConfig struct {
	BlankIssuesEnabled bool
	ContactLinks       []ContactLink
}

// Templates is the issue template chooser of a repository.
type Templates struct {
	Config    TemplateConfig
	Templates []Template
}

// stringList accepts a YAML list or a comma-separated string.
type stringList []string

func (l *stringList) UnmarshalYAML(n *yaml.Node) error {
	var out []string
	switch n.Kind {
	case yaml.ScalarNode:
		for _, v := range strings.Split(n.Value, ",") {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
	case yaml.SequenceNode:
		var vs []string
		if err := n.Decode(&vs); err != nil {
			return err
		}
		for _, v := range vs {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
	}
	*l = out
	return nil
}

type templateHeader struct {
	Name        string     `yaml:"name"`
	About       string     `yaml:"about"`
	Description string     `yaml:"description"`
	Title       string     `yaml:"title"`
	Labels      stringList `yaml:"labels"`
	Assignees   stringList `yaml:"assignees"`
}

type formFile struct {
	templateHeader `yaml:",inline"`
	Body           []map[string]any `yaml:"body"`
}

type configFile struct {
	BlankIssuesEnabled *bool `yaml:"blank_issues_enabled"`
	ContactLinks       []struct {
		Name  string `yaml:"name"`
		URL   string `yaml:"url"`
		About string `yaml:"about"`
	} `yaml:"contact_links"`
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// parseMarkdownTemplate splits the YAML front matter of a Markdown
// template from its body.
func parseMarkdownTemplate(file string, content []byte) (Template, bool) {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	t := Template{FileName: file, Kind: TemplateMarkdown, Body: text}
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		if front, body, ok := strings.Cut(rest, "\n---"); ok {
			var h templateHeader
			if err := yaml.Unmarshal([]byte(front), &h); err != nil {
				return Template{}, false
			}
			t.Body = strings.TrimPrefix(strings.TrimLeft(body, "-"), "\n")
			t.Name, t.About, t.Title = h.Name, h.About, h.Title
			t.Labels, t.Assignees = h.Labels, h.Assignees
		}
	}
	if t.Name == "" {
		t.Name = strings.TrimSuffix(file, path.Ext(file))
	}
	t.Labels, t.Assignees = nonNil(t.Labels), nonNil(t.Assignees)
	return t, true
}

func parseFormTemplate(file string, content []byte) (Template, bool) {
	var f formFile
	if err := yaml.Unmarshal(content, &f); err != nil || f.Name == "" || len(f.Body) == 0 {
		return Template{}, false
	}
	about := f.Description
	if about == "" {
		about = f.About
	}
	return Template{FileName: file, Kind: TemplateForm, Name: f.Name, About: about, Title: f.Title,
		Labels: nonNil(f.Labels), Assignees: nonNil(f.Assignees), Form: f.Body}, true
}

func parseTemplateConfig(content []byte) TemplateConfig {
	c := TemplateConfig{BlankIssuesEnabled: true, ContactLinks: []ContactLink{}}
	var f configFile
	if err := yaml.Unmarshal(content, &f); err != nil {
		return c
	}
	if f.BlankIssuesEnabled != nil {
		c.BlankIssuesEnabled = *f.BlankIssuesEnabled
	}
	for _, l := range f.ContactLinks {
		if l.Name == "" || (!strings.HasPrefix(l.URL, "https://") && !strings.HasPrefix(l.URL, "http://")) {
			continue
		}
		c.ContactLinks = append(c.ContactLinks, ContactLink{Name: l.Name, URL: l.URL, About: l.About})
	}
	return c
}

// ListTemplates returns the issue templates of the default branch of
// owner/name (.github/ISSUE_TEMPLATE). Files that do not parse are skipped.
func (s *Service) ListTemplates(ctx context.Context, who auth.Principal, owner, name string) (Templates, error) {
	out := Templates{Config: TemplateConfig{BlankIssuesEnabled: true, ContactLinks: []ContactLink{}}, Templates: []Template{}}
	r, err := s.repo(ctx, who, owner, name)
	if err != nil {
		return Templates{}, err
	}
	if s.o.Browse == nil {
		return out, nil
	}
	gr := gitbrowse.Repo{ID: r.ID, OwnerID: r.OwnerID, Name: r.Name, DefaultBranch: r.DefaultBranch}
	tree, err := s.o.Browse.Tree(ctx, gr, "", TemplateDir, false)
	if errors.Is(err, gitbrowse.ErrNotFound) || errors.Is(err, gitbrowse.ErrEmpty) {
		return out, nil
	}
	if err != nil {
		return Templates{}, fmt.Errorf("list issue templates: %w", err)
	}
	for _, e := range tree.Entries {
		if e.Type != gitbrowse.TypeBlob {
			continue
		}
		ext := strings.ToLower(path.Ext(e.Name))
		if ext != ".md" && ext != ".yml" && ext != ".yaml" {
			continue
		}
		isConfig := strings.EqualFold(strings.TrimSuffix(e.Name, path.Ext(e.Name)), "config") && ext != ".md"
		if !isConfig && len(out.Templates) >= MaxTemplates {
			continue
		}
		b, err := s.o.Browse.Blob(ctx, gr, tree.Ref.Name, e.Path)
		if errors.Is(err, gitbrowse.ErrNotFound) || errors.Is(err, gitbrowse.ErrTooLarge) {
			continue
		}
		if err != nil {
			return Templates{}, fmt.Errorf("read issue template: %w", err)
		}
		if b.Content == nil || b.Binary || bytes.ContainsRune(b.Content, 0) {
			continue
		}
		var t Template
		ok := false
		switch {
		case isConfig:
			out.Config = parseTemplateConfig(b.Content)
			continue
		case ext == ".md":
			t, ok = parseMarkdownTemplate(e.Name, b.Content)
		default:
			t, ok = parseFormTemplate(e.Name, b.Content)
		}
		if ok {
			out.Templates = append(out.Templates, t)
		}
	}
	slices.SortStableFunc(out.Templates, func(a, b Template) int { return strings.Compare(a.FileName, b.FileName) })
	return out, nil
}
