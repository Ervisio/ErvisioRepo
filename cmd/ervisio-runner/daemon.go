package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Ervisio/ErvisioRepo/internal/runner"
	"github.com/Ervisio/ErvisioRepo/internal/runner/config"
)

// tokenFileName is the registration token file looked up next to the
// state file when the runner is not registered yet.
const tokenFileName = "registration-token"

func daemonCmd(ctx context.Context, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", config.DefaultPath, "path to the TOML configuration file (created from ERVISIO_RUNNER_* when missing)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if err := ensureDaemonConfig(*cfgPath, stderr); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	logger := newLogger(cfg.Log, stderr)
	st, err := loadOrRegister(ctx, cfg, logger)
	if err != nil {
		return err
	}
	d, err := runner.NewDaemon(cfg, st, version(), logger)
	if err != nil {
		return err
	}
	return d.Run(ctx)
}

// ensureDaemonConfig writes a default configuration from the
// ERVISIO_RUNNER_* environment when the file is missing. A path that cannot
// be written (the packaged /etc default) falls back to the built-in
// defaults.
func ensureDaemonConfig(path string, stderr io.Writer) error {
	capacity := 0
	if v := os.Getenv("ERVISIO_RUNNER_CAPACITY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 64 {
			return errors.New("ERVISIO_RUNNER_CAPACITY must be a number between 1 and 64")
		}
		capacity = n
	}
	host := os.Getenv("ERVISIO_RUNNER_HOST") == "true"
	written, err := ensureConfig(path, newConfigOptions{URL: os.Getenv("ERVISIO_RUNNER_URL"),
		Extra: splitList(os.Getenv("ERVISIO_RUNNER_LABELS")), Capacity: capacity, Host: host,
		DataDir: os.Getenv("ERVISIO_RUNNER_DATA_DIR")})
	switch {
	case err != nil && path == config.DefaultPath:
		fmt.Fprintf(stderr, "ervisio-runner: cannot write %s (%v), using the built-in defaults\n", path, err)
	case err != nil:
		return err
	case written:
		fmt.Fprintf(stderr, "ervisio-runner: wrote the default configuration to %s\n", path)
	}
	return nil
}

// loadOrRegister reads the state file; when it is missing and a
// registration token is available (ERVISIO_RUNNER_TOKEN,
// ERVISIO_RUNNER_TOKEN_FILE or a "registration-token" file next to the
// state file) it registers first. The token file is removed afterwards.
func loadOrRegister(ctx context.Context, cfg *config.Config, log *slog.Logger) (*runner.State, error) {
	if _, err := os.Stat(cfg.Runner.StateFile); err == nil || !errors.Is(err, os.ErrNotExist) {
		return runner.LoadState(cfg.Runner.StateFile)
	}
	token, tokenFile, err := registrationToken(cfg.Runner.StateFile)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return runner.LoadState(cfg.Runner.StateFile)
	}
	url := os.Getenv("ERVISIO_RUNNER_URL")
	if url == "" {
		url = cfg.Server.URL
	}
	if url == "" {
		return nil, errors.New("auto-registration needs ERVISIO_RUNNER_URL or server.url")
	}
	labels := cfg.Runner.Labels
	if extra := splitList(os.Getenv("ERVISIO_RUNNER_LABELS")); len(extra) > 0 && !cfg.LabelsSet {
		if labels, err = config.ExpandLabels(labels, extra); err != nil {
			return nil, fmt.Errorf("ERVISIO_RUNNER_LABELS: %w", err)
		}
	}
	st, err := register(ctx, cfg, registration{URL: url, Token: token, Name: os.Getenv("ERVISIO_RUNNER_NAME"),
		Labels: labels, Ephemeral: os.Getenv("ERVISIO_RUNNER_EPHEMERAL") == "true"})
	if err != nil {
		return nil, fmt.Errorf("auto-registration: %w", err)
	}
	if err := runner.SaveState(cfg.Runner.StateFile, st); err != nil {
		return nil, err
	}
	log.Info("runner registered", "name", st.Name, "id", st.ID, "state_file", cfg.Runner.StateFile)
	if tokenFile != "" {
		if err := os.Remove(tokenFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Warn("could not remove the registration token file", "path", tokenFile, "err", err)
		}
	}
	return st, nil
}

// registrationToken returns the token and, when read from a file, its path.
func registrationToken(stateFile string) (string, string, error) {
	if t := strings.TrimSpace(os.Getenv("ERVISIO_RUNNER_TOKEN")); t != "" {
		return t, "", nil
	}
	path := os.Getenv("ERVISIO_RUNNER_TOKEN_FILE")
	explicit := path != ""
	if !explicit {
		path = filepath.Join(filepath.Dir(stateFile), tokenFileName)
	}
	data, err := os.ReadFile(path) //nolint:gosec // operator-chosen token file
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !explicit {
			return "", "", nil
		}
		return "", "", fmt.Errorf("read registration token: %w", err)
	}
	return strings.TrimSpace(string(data)), path, nil
}
