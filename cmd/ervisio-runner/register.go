package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/runner"
	"github.com/Ervisio/ErvisioRepo/internal/runner/config"
)

func registerCmd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("register", flag.ContinueOnError)
	fs.SetOutput(stderr)
	url := fs.String("url", "", "public URL of ErvisioRepo (or ERVISIO_RUNNER_URL)")
	token := fs.String("token", "", "registration token (or ERVISIO_RUNNER_TOKEN)")
	name := fs.String("name", "", "runner name (default: host name)")
	labels := fs.String("labels", "", "comma-separated labels: name=docker://image, name=host, or a bare name added to the defaults")
	ephemeral := fs.Bool("ephemeral", false, "run a single job, then exit")
	cfgPath := fs.String("config", config.DefaultPath, "path to the TOML configuration file (created when missing)")
	statePath := fs.String("state", "", "state file to write (default: runner.state_file)")
	capacity := fs.Int("capacity", 0, "jobs run at the same time, written to a new config (default: CPUs/2)")
	host := fs.Bool("host", false, "new config: run jobs directly on this machine instead of Docker containers")
	dataDir := fs.String("data-dir", "", "new config: directory of the state file and caches (default "+config.DefaultDataDir+")")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *token == "" {
		*token = os.Getenv("ERVISIO_RUNNER_TOKEN")
	}
	if *token == "" {
		return fmt.Errorf("%w: --token is required", errUsage)
	}
	if *url == "" {
		*url = os.Getenv("ERVISIO_RUNNER_URL")
	}
	if *capacity < 0 || *capacity > 64 {
		return fmt.Errorf("%w: --capacity must be between 1 and 64", errUsage)
	}
	extra := splitList(*labels)
	if *dataDir == "" && *statePath != "" {
		*dataDir = filepath.Dir(*statePath)
	}
	written, err := ensureConfig(*cfgPath, newConfigOptions{URL: *url, Extra: extra, Capacity: *capacity, Host: *host,
		DataDir: *dataDir})
	if err != nil {
		return err
	}
	if written {
		fmt.Fprintf(stdout, "wrote %s\n", *cfgPath)
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if *url == "" {
		*url = cfg.Server.URL
	}
	if *url == "" {
		return fmt.Errorf("%w: --url is required", errUsage)
	}
	list := cfg.Runner.Labels
	switch {
	case written || len(extra) == 0:
	case config.HasBareLabel(extra):
		if list, err = config.ExpandLabels(cfg.Runner.Labels, extra); err != nil {
			return fmt.Errorf("%w: --labels: %v", errUsage, err)
		}
	default:
		list = extra
	}
	st, err := register(ctx, cfg, registration{URL: *url, Token: *token, Name: *name, Labels: list, Ephemeral: *ephemeral})
	if err != nil {
		return err
	}
	if *statePath == "" {
		*statePath = cfg.Runner.StateFile
	}
	if err := runner.SaveState(*statePath, st); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "registered runner %q (id %s) with labels %s\nstate saved to %s\n",
		st.Name, st.ID, strings.Join(labelNames(st.Labels), ", "), *statePath)
	return nil
}

type registration struct {
	URL, Token, Name string
	Labels           []string
	Ephemeral        bool
}

// register exchanges a registration token for a runner credential.
func register(ctx context.Context, cfg *config.Config, r registration) (*runner.State, error) {
	if r.Name == "" {
		h, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("%w: --name is required (host name unavailable: %v)", errUsage, err)
		}
		r.Name = h
	}
	ls, err := cfg.CheckLabels(r.Labels)
	if err != nil {
		return nil, fmt.Errorf("%w: labels: %v", errUsage, err)
	}
	rctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	return runner.Register(rctx, runner.RegisterOptions{
		URL:         r.URL,
		Token:       strings.TrimSpace(r.Token),
		Name:        r.Name,
		Labels:      ls,
		Ephemeral:   r.Ephemeral,
		InsecureTLS: cfg.Server.InsecureTLS,
		Version:     version(),
	})
}

type newConfigOptions struct {
	URL      string
	Extra    []string
	Capacity int
	Host     bool
	DataDir  string
}

// ensureConfig writes a default configuration at path when no file exists
// there. Bare extra labels join the defaults; full ones replace them.
func ensureConfig(path string, o newConfigOptions) (bool, error) {
	if _, err := os.Stat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	base := config.DefaultLabels()
	if o.Host {
		base = config.HostLabels()
	}
	labels := base
	switch {
	case len(o.Extra) == 0:
	case config.HasBareLabel(o.Extra):
		var err error
		if labels, err = config.ExpandLabels(base, o.Extra); err != nil {
			return false, fmt.Errorf("%w: labels: %v", errUsage, err)
		}
	default:
		labels = o.Extra
	}
	if o.Capacity == 0 {
		o.Capacity = config.DefaultCapacity()
	}
	if o.DataDir == "" {
		o.DataDir = config.DefaultDataDir
	}
	return config.WriteDefault(path, config.RenderOptions{URL: o.URL, Labels: labels, Capacity: o.Capacity,
		Host: o.Host, DataDir: o.DataDir})
}

func splitList(s string) []string {
	var out []string
	for _, l := range strings.Split(s, ",") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// labelNames returns the names of wire labels (name:backend).
func labelNames(wire []string) []string {
	out := make([]string, len(wire))
	for i, w := range wire {
		out[i], _, _ = strings.Cut(w, ":")
	}
	return out
}
