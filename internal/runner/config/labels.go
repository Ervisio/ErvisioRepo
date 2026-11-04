package config

import (
	"fmt"
	"regexp"
	"strings"
)

// Label schemes.
const (
	SchemeDocker = "docker"
	SchemeHost   = "host"
)

// Label maps a runs-on name to an execution backend.
type Label struct {
	Name   string
	Scheme string // SchemeDocker or SchemeHost
	// Image is the docker image for SchemeDocker.
	Image string
}

var labelNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// ParseLabel parses "name=docker://image", "name=host" or the wire form
// "name:docker://image" / "name:host".
func ParseLabel(s string) (Label, error) {
	s = strings.TrimSpace(s)
	name, backend, ok := strings.Cut(s, "=")
	if !ok {
		name, backend, ok = strings.Cut(s, ":")
	}
	if !ok {
		return Label{}, fmt.Errorf("label %q: expected name=docker://image or name=host", s)
	}
	name = strings.TrimSpace(name)
	if !labelNameRe.MatchString(name) {
		return Label{}, fmt.Errorf("label %q: invalid name", s)
	}
	backend = strings.TrimSpace(backend)
	switch {
	case backend == SchemeHost || backend == SchemeHost+"://":
		return Label{Name: name, Scheme: SchemeHost}, nil
	case strings.HasPrefix(backend, SchemeDocker+"://"):
		image := strings.TrimPrefix(backend, SchemeDocker+"://")
		if image == "" || strings.ContainsAny(image, " \t\n") {
			return Label{}, fmt.Errorf("label %q: invalid docker image", s)
		}
		return Label{Name: name, Scheme: SchemeDocker, Image: image}, nil
	}
	return Label{}, fmt.Errorf("label %q: unsupported backend, use docker://image or host", s)
}

// ParseLabels parses a list and rejects duplicate names.
func ParseLabels(list []string) (Labels, error) {
	out := make(Labels, 0, len(list))
	seen := map[string]bool{}
	for _, s := range list {
		l, err := ParseLabel(s)
		if err != nil {
			return nil, err
		}
		if seen[l.Name] {
			return nil, fmt.Errorf("label %q is declared twice", l.Name)
		}
		seen[l.Name] = true
		out = append(out, l)
	}
	return out, nil
}

// Wire returns the form sent to the server in Register and Declare.
func (l Label) Wire() string {
	if l.Scheme == SchemeHost {
		return l.Name + ":host"
	}
	return l.Name + ":docker://" + l.Image
}

// Labels is a parsed label list.
type Labels []Label

// Wire returns the wire form of every label.
func (ls Labels) Wire() []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Wire()
	}
	return out
}

// Names returns the label names.
func (ls Labels) Names() []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Name
	}
	return out
}

// Find returns the label called name.
func (ls Labels) Find(name string) (Label, bool) {
	for _, l := range ls {
		if l.Name == name {
			return l, true
		}
	}
	return Label{}, false
}

// Platform is the value act expects for a label: the docker image, or
// "-self-hosted" for the host backend.
func (l Label) Platform() string {
	if l.Scheme == SchemeHost {
		return "-self-hosted"
	}
	return l.Image
}

// PickPlatform chooses the backend of a job: the label the server matched
// (preferred), then the first runs-on entry this runner knows, then the
// first docker label as a fallback.
func (ls Labels) PickPlatform(matched string, runsOn []string) string {
	if l, ok := ls.Find(matched); ok {
		return l.Platform()
	}
	for _, name := range runsOn {
		if l, ok := ls.Find(name); ok {
			return l.Platform()
		}
	}
	for _, l := range ls {
		if l.Scheme == SchemeDocker {
			return l.Platform()
		}
	}
	return ""
}
