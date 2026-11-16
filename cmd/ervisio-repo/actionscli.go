package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Ervisio/ErvisioRepo/internal/actions"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db"
	"github.com/Ervisio/ErvisioRepo/internal/sys"
)

const (
	defaultRunnerConfig = "/etc/ervisio-repo/runner.toml"
	runnerDataDir       = "/var/lib/ervisio-runner"
)

// actionsCmd groups the Actions operator commands:
//
//	ervisio-repo actions runner-token [--scope instance|org/<o>|repo/<o>/<n>] [--config <path>]
//	ervisio-repo actions runner-setup [--name <n>] [--labels <l,...>] [--capacity <n>] [--url <url>] [--no-start]
func actionsCmd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: actions needs runner-token or runner-setup", errUsage)
	}
	switch args[0] {
	case "runner-token":
		return runnerTokenCmd(ctx, args[1:], stdout, stderr)
	case "runner-setup":
		return runnerSetupCmd(ctx, args[1:], stdout, stderr)
	}
	return fmt.Errorf("%w: unknown actions command %q", errUsage, args[0])
}

// runnerTokenCmd prints a new registration token on stdout (and its expiry
// on stderr), so scripts can capture it.
func runnerTokenCmd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("actions runner-token", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", defaultConfig, "path to the TOML configuration file")
	scope := fs.String("scope", "instance", "instance, org/<owner> or repo/<owner>/<name>")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w: unexpected arguments %v", errUsage, fs.Args())
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	tok, err := mintRunnerToken(ctx, cfg, *scope)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, tok.Token)
	fmt.Fprintf(stderr, "registration token for %s, valid until %s\n", tok.Scope, tok.ExpiresAt.Format(time.RFC3339))
	return nil
}

func mintRunnerToken(ctx context.Context, cfg *config.Config, scope string) (actions.RegistrationToken, error) {
	if !cfg.Actions.Enabled {
		return actions.RegistrationToken{}, errors.New("actions are disabled (actions.enabled = false)")
	}
	pool, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return actions.RegistrationToken{}, err
	}
	defer pool.Close()
	svc, err := actions.New(actions.Options{Pool: pool, Log: slog.New(slog.DiscardHandler), Config: cfg.Actions,
		PublicURL: cfg.Server.PublicURL})
	if err != nil {
		return actions.RegistrationToken{}, fmt.Errorf("actions: %w", err)
	}
	tok, err := svc.OperatorRegistrationToken(ctx, scope)
	if err != nil {
		return actions.RegistrationToken{}, fmt.Errorf("registration token: %w", err)
	}
	return tok, nil
}

// runnerSetupCmd sets up the built-in runner of a native install: it
// registers ervisio-runner at instance scope against the local listener,
// writes /etc/ervisio-repo/runner.toml and starts ervisio-runner.service.
// Run it as root.
func runnerSetupCmd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("actions runner-setup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", defaultConfig, "path to the server TOML configuration file")
	runnerCfg := fs.String("runner-config", defaultRunnerConfig, "runner configuration to create")
	name := fs.String("name", "builtin", "runner name")
	labels := fs.String("labels", "", "extra labels (bare names join the defaults)")
	capacity := fs.Int("capacity", 0, "jobs run at the same time (default: CPUs/2)")
	url := fs.String("url", "", "server URL the runner dials (default: the local listener)")
	runAs := fs.String("user", "ervisio-repo", "system user of ervisio-runner.service")
	noStart := fs.Bool("no-start", false, "do not enable and start ervisio-runner.service")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w: unexpected arguments %v", errUsage, fs.Args())
	}
	if *capacity < 0 || *capacity > 64 {
		return fmt.Errorf("%w: --capacity must be between 1 and 64", errUsage)
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	if *url == "" {
		if *url, err = localURL(cfg.Server.Listen); err != nil {
			return err
		}
	}
	state := filepath.Join(runnerDataDir, ".runner")
	if _, err := os.Stat(state); err == nil {
		return fmt.Errorf("%s exists: the runner is already registered (delete it to register again)", state)
	}
	tok, err := mintRunnerToken(ctx, cfg, "instance")
	if err != nil {
		return err
	}
	argv := []string{runnerBinary(), "register", "--config", *runnerCfg, "--url", *url, "--name", *name,
		"--data-dir", runnerDataDir}
	if *labels != "" {
		argv = append(argv, "--labels", *labels)
	}
	if *capacity > 0 {
		argv = append(argv, "--capacity", strconv.Itoa(*capacity))
	}
	if err := sys.Exec(ctx, sys.Cmd{Argv: argv, Env: []string{"ERVISIO_RUNNER_TOKEN=" + tok.Token},
		Stdout: stdout, Stderr: stderr, Timeout: 2 * time.Minute}); err != nil {
		return fmt.Errorf("register runner: %w", err)
	}
	if err := chownRunner(*runAs, *runnerCfg); err != nil {
		return err
	}
	if *noStart {
		fmt.Fprintln(stdout, "start it with: systemctl enable --now ervisio-runner")
		return nil
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		fmt.Fprintln(stdout, "systemd not found: start ervisio-runner daemon --config "+*runnerCfg+" yourself")
		return nil
	}
	if _, err := sys.Run(ctx, "systemctl", "enable", "--now", "ervisio-runner.service"); err != nil {
		return fmt.Errorf("start ervisio-runner.service: %w", err)
	}
	fmt.Fprintln(stdout, "ervisio-runner.service is enabled and running")
	return nil
}

// localURL turns a listen address into a loopback URL.
func localURL(listen string) (string, error) {
	_, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		return "", fmt.Errorf("server.listen %q: pass --url", listen)
	}
	return "http://" + net.JoinHostPort("127.0.0.1", port), nil
}

// runnerBinary is ervisio-runner next to this executable, else from PATH.
func runnerBinary() string {
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "ervisio-runner")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "ervisio-runner"
}

// chownRunner hands the runner data directory to the service user and
// makes the config readable by its group.
func chownRunner(name, cfgPath string) error {
	u, err := user.Lookup(name)
	if err != nil {
		return fmt.Errorf("user %s: %w", name, err)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	root, err := os.OpenRoot(runnerDataDir)
	if err != nil {
		return fmt.Errorf("open %s: %w", runnerDataDir, err)
	}
	defer func() { _ = root.Close() }()
	err = fs.WalkDir(root.FS(), ".", func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return root.Lchown(p, uid, gid)
	})
	if err != nil {
		return fmt.Errorf("chown %s: %w", runnerDataDir, err)
	}
	if err := os.Chown(cfgPath, 0, gid); err != nil {
		return fmt.Errorf("chown %s: %w", cfgPath, err)
	}
	return nil
}
