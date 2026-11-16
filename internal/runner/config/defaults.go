package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Default job images, documented in deploy/runner-images.
const (
	ImageUbuntu2404 = "ghcr.io/catthehacker/ubuntu:act-24.04"
	ImageUbuntu2204 = "ghcr.io/catthehacker/ubuntu:act-22.04"
)

// ArchLabel is the GitHub-style architecture label of this machine.
func ArchLabel() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	case "386":
		return "x86"
	case "arm":
		return "arm"
	}
	return runtime.GOARCH
}

// OSLabel is the GitHub-style operating system label of this machine.
func OSLabel() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	}
	return "linux"
}

// DefaultLabels are the labels of a new runner with the container
// backend: the GitHub hosted names mapped to the act images, plus
// self-hosted, linux and the architecture (jobs run in Linux containers).
func DefaultLabels() []string {
	return []string{
		"ubuntu-latest=docker://" + ImageUbuntu2404,
		"ubuntu-24.04=docker://" + ImageUbuntu2404,
		"ubuntu-22.04=docker://" + ImageUbuntu2204,
		"self-hosted=docker://" + ImageUbuntu2404,
		"linux=docker://" + ImageUbuntu2404,
		ArchLabel() + "=docker://" + ImageUbuntu2404,
	}
}

// HostLabels are the labels of a runner that runs jobs directly on this
// machine: self-hosted, the OS and the architecture.
func HostLabels() []string {
	return []string{"self-hosted=host", OSLabel() + "=host", ArchLabel() + "=host"}
}

// DefaultCapacity is half the CPUs, at least one job.
func DefaultCapacity() int {
	return min(max(runtime.NumCPU()/2, 1), 64)
}

// ExpandLabels merges extra into base. A full entry (name=docker://image,
// name=host) replaces the base label with that name; a bare name gets the
// backend of the first base label.
func ExpandLabels(base []string, extra []string) ([]string, error) {
	out := append([]string(nil), base...)
	backend := "docker://" + ImageUbuntu2404
	if len(base) > 0 {
		if l, err := ParseLabel(base[0]); err == nil {
			backend = strings.TrimPrefix(l.Wire(), l.Name+":")
		}
	}
	for _, e := range extra {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.ContainsAny(e, "=:") {
			e += "=" + backend
		}
		l, err := ParseLabel(e)
		if err != nil {
			return nil, err
		}
		replaced := false
		for i, b := range out {
			if bl, err := ParseLabel(b); err == nil && strings.EqualFold(bl.Name, l.Name) {
				out[i], replaced = e, true
			}
		}
		if !replaced {
			out = append(out, e)
		}
	}
	return out, nil
}

// HasBareLabel reports whether list holds a name without a backend.
func HasBareLabel(list []string) bool {
	for _, l := range list {
		if l = strings.TrimSpace(l); l != "" && !strings.ContainsAny(l, "=:") {
			return true
		}
	}
	return false
}

// WriteDefault creates path with the rendered configuration unless it
// already exists; it reports whether the file was written.
func WriteDefault(path string, o RenderOptions) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("stat config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return false, fmt.Errorf("create config directory: %w", err)
	}
	data := Render(o)
	if _, err := Parse([]byte(data)); err != nil {
		return false, fmt.Errorf("default config: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640) //nolint:gosec // operator-chosen config path
	if err != nil {
		return false, fmt.Errorf("create config: %w", err)
	}
	if _, err := f.WriteString(data); err != nil {
		_ = f.Close()
		return false, fmt.Errorf("write config: %w", err)
	}
	if err := f.Close(); err != nil {
		return false, fmt.Errorf("close config: %w", err)
	}
	return true, nil
}
