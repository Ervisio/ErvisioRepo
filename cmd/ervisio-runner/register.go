package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/runner"
	"github.com/Ervisio/ErvisioRepo/internal/runner/config"
)

func registerCmd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("register", flag.ContinueOnError)
	fs.SetOutput(stderr)
	url := fs.String("url", "", "public URL of ErvisioRepo")
	token := fs.String("token", "", "registration token (or ERVISIO_RUNNER_TOKEN)")
	name := fs.String("name", "", "runner name (default: host name)")
	labels := fs.String("labels", "", "comma-separated labels, name=docker://image or name=host (default: from the config)")
	ephemeral := fs.Bool("ephemeral", false, "run a single job, then exit")
	cfgPath := fs.String("config", config.DefaultPath, "path to the TOML configuration file")
	statePath := fs.String("state", "", "state file to write (default: runner.state_file)")
	if err := parseFlags(fs, args); err != nil {
		return err
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
	if *token == "" {
		*token = os.Getenv("ERVISIO_RUNNER_TOKEN")
	}
	if *token == "" {
		return fmt.Errorf("%w: --token is required", errUsage)
	}
	if *name == "" {
		h, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("%w: --name is required (host name unavailable: %v)", errUsage, err)
		}
		*name = h
	}
	list := cfg.Runner.Labels
	if *labels != "" {
		list = nil
		for _, l := range strings.Split(*labels, ",") {
			if l = strings.TrimSpace(l); l != "" {
				list = append(list, l)
			}
		}
	}
	ls, err := cfg.CheckLabels(list)
	if err != nil {
		return fmt.Errorf("%w: --labels: %v", errUsage, err)
	}
	if *statePath == "" {
		*statePath = cfg.Runner.StateFile
	}

	rctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	st, err := runner.Register(rctx, runner.RegisterOptions{
		URL:         *url,
		Token:       *token,
		Name:        *name,
		Labels:      ls,
		Ephemeral:   *ephemeral,
		InsecureTLS: cfg.Server.InsecureTLS,
		Version:     version(),
	})
	if err != nil {
		return err
	}
	if err := runner.SaveState(*statePath, st); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "registered runner %q (id %s) with labels %s\nstate saved to %s\n",
		st.Name, st.ID, strings.Join(ls.Names(), ", "), *statePath)
	return nil
}
