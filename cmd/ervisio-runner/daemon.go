package main

import (
	"context"
	"flag"
	"io"

	"github.com/Ervisio/ErvisioRepo/internal/runner"
	"github.com/Ervisio/ErvisioRepo/internal/runner/config"
)

func daemonCmd(ctx context.Context, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", config.DefaultPath, "path to the TOML configuration file")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	logger := newLogger(cfg.Log, stderr)
	st, err := runner.LoadState(cfg.Runner.StateFile)
	if err != nil {
		return err
	}
	d, err := runner.NewDaemon(cfg, st, version(), logger)
	if err != nil {
		return err
	}
	return d.Run(ctx)
}
