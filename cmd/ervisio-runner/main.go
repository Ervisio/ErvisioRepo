// Command ervisio-runner runs Ervisio Actions jobs with act.
//
//	ervisio-runner register --url <url> --token <token> [--name <n>] [--labels <l,...>] [--ephemeral]
//	ervisio-runner daemon [--config <path>]
//	ervisio-runner generate-config
//	ervisio-runner version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Ervisio/ErvisioRepo/internal/buildinfo"
	"github.com/Ervisio/ErvisioRepo/internal/runner/config"
)

const usage = `Usage:
  ervisio-runner register --url <url> --token <token> [--name <name>]
                          [--labels <name=docker://image,...>] [--ephemeral]
                          [--config <path>] [--state <path>]
                                           register this runner (token also via ERVISIO_RUNNER_TOKEN)
  ervisio-runner daemon [--config <path>]  poll for jobs and run them
  ervisio-runner generate-config           print a commented default configuration
  ervisio-runner version
`

// errUsage marks command-line mistakes (exit status 2).
var errUsage = errors.New("usage error")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	err := dispatch(ctx, args, stdout, stderr)
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, errUsage):
		fmt.Fprintf(stderr, "%v\n\n%s", err, usage)
		return 2
	default:
		fmt.Fprintf(stderr, "ervisio-runner: %v\n", err)
		return 1
	}
}

func dispatch(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: missing command", errUsage)
	}
	cmd, args := args[0], args[1:]
	switch cmd {
	case "register":
		return registerCmd(ctx, args, stdout, stderr)
	case "daemon":
		return daemonCmd(ctx, args, stderr)
	case "generate-config":
		if len(args) > 0 {
			return fmt.Errorf("%w: generate-config takes no arguments", errUsage)
		}
		_, err := io.WriteString(stdout, config.Sample)
		return err
	case "version":
		fmt.Fprintf(stdout, "ervisio-runner %s", version())
		if c := buildinfo.Get().Commit; c != "" {
			fmt.Fprintf(stdout, " (%s)", c)
		}
		fmt.Fprintln(stdout)
		return nil
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return nil
	default:
		return fmt.Errorf("%w: unknown command %q", errUsage, cmd)
	}
}

func version() string { return buildinfo.Get().Version }

// parseFlags parses fs and maps flag errors to errUsage.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%w: unexpected arguments %v", errUsage, fs.Args())
	}
	return nil
}

func newLogger(cfg config.Log, w io.Writer) *slog.Logger {
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	if cfg.Format == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}
