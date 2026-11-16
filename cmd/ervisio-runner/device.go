package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/runner"
	"github.com/Ervisio/ErvisioRepo/internal/runner/config"
)

type deviceInput struct {
	cfgPath, url, name string
	extra              []string
	capacity           int
	host               bool
}

type deviceResult struct {
	runner.DeviceGrant
	name     string
	labels   []string // full entries (name=backend)
	capacity int
}

// deviceRegister runs the device registration: it sends the labels and
// capacity this runner would use, waits for the approval and maps the
// approved label names back to full entries.
func deviceRegister(ctx context.Context, in deviceInput, out io.Writer) (deviceResult, error) {
	var full []string
	capacity := in.capacity
	insecure := false
	if cfg, err := config.Load(in.cfgPath); err == nil {
		full, insecure = cfg.Runner.Labels, cfg.Server.InsecureTLS
		if len(in.extra) > 0 {
			if full, err = newConfigLabels(in.host, in.extra); err != nil {
				return deviceResult{}, err
			}
		}
		if capacity == 0 {
			capacity = cfg.Runner.Capacity
		}
		if in.url == "" {
			in.url = cfg.Server.URL
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return deviceResult{}, err
	} else if full, err = newConfigLabels(in.host, in.extra); err != nil {
		return deviceResult{}, err
	}
	if in.url == "" {
		return deviceResult{}, fmt.Errorf("%w: --url is required", errUsage)
	}
	if capacity == 0 {
		capacity = config.DefaultCapacity()
	}
	names := make([]string, 0, len(full))
	for _, e := range full {
		if l, err := config.ParseLabel(e); err == nil {
			names = append(names, l.Name)
		}
	}
	g, err := runner.DeviceRegistration(ctx, runner.DeviceOptions{URL: in.url, InsecureTLS: insecure, Version: version(),
		Name: in.name, Labels: names, Capacity: capacity, Out: out})
	if err != nil {
		return deviceResult{}, err
	}
	res := deviceResult{DeviceGrant: g, name: g.Name, labels: full, capacity: g.Capacity}
	if res.name == "" {
		res.name = in.name
	}
	if res.capacity == 0 {
		res.capacity = capacity
	}
	if len(g.Labels) > 0 {
		if res.labels, err = selectLabels(full, g.Labels); err != nil {
			return deviceResult{}, fmt.Errorf("approved labels: %w", err)
		}
	}
	return res, nil
}

// selectLabels keeps the entries of full named in names, in that order;
// a name not in full gets the backend of the first entry.
func selectLabels(full, names []string) ([]string, error) {
	out := make([]string, 0, len(names))
	for _, n := range names {
		found := ""
		for _, e := range full {
			if l, err := config.ParseLabel(e); err == nil && strings.EqualFold(l.Name, n) {
				found = e
				break
			}
		}
		if found == "" {
			exp, err := config.ExpandLabels(full, []string{n})
			if err != nil {
				return nil, err
			}
			found = exp[len(exp)-1]
		}
		out = append(out, found)
	}
	return out, nil
}

// deregisterCmd removes this runner from the server and deletes its state
// file (used by the uninstaller).
func deregisterCmd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("deregister", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", config.DefaultPath, "path to the TOML configuration file")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	st, err := runner.LoadState(cfg.Runner.StateFile)
	if err != nil {
		return err
	}
	if err := runner.Deregister(ctx, st, version(), cfg.Server.InsecureTLS); err != nil {
		return err
	}
	if err := os.Remove(cfg.Runner.StateFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove state file: %w", err)
	}
	fmt.Fprintf(stdout, "runner %q removed from %s\n", st.Name, st.URL)
	return nil
}
