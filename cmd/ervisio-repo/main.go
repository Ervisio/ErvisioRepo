// Command ervisio-repo is the ErvisioRepo server.
//
//	ervisio-repo serve   --config <path>
//	ervisio-repo migrate up|status --config <path>
//	ervisio-repo storage test <target> --config <path>
//	ervisio-repo version
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
	"text/tabwriter"

	"github.com/Ervisio/ErvisioRepo/internal/buildinfo"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db"
)

const defaultConfig = "/etc/ervisio-repo/config.toml"

const usage = `Usage:
  ervisio-repo serve   [--config <path>]   run the server
  ervisio-repo migrate up|status [--config <path>]
  ervisio-repo storage test <target> [--config <path>] [--timeout <dur>]
                                           write/read/list/delete round trip
  ervisio-repo version
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
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, errUsage):
		fmt.Fprintf(stderr, "%v\n\n%s", err, usage)
		return 2
	default:
		fmt.Fprintf(stderr, "ervisio-repo: %v\n", err)
		return 1
	}
}

func dispatch(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: missing command", errUsage)
	}
	cmd, args := args[0], args[1:]
	switch cmd {
	case "version":
		info := buildinfo.Get()
		fmt.Fprintf(stdout, "ervisio-repo %s", info.Version)
		if info.Commit != "" {
			fmt.Fprintf(stdout, " (%s)", info.Commit)
		}
		fmt.Fprintln(stdout)
		return nil
	case "serve":
		cfg, err := loadConfig("serve", args, stderr)
		if err != nil {
			return err
		}
		return serve(ctx, cfg, newLogger(cfg.Log, stderr), nil)
	case "migrate":
		if len(args) == 0 || (args[0] != "up" && args[0] != "status") {
			return fmt.Errorf("%w: migrate needs \"up\" or \"status\"", errUsage)
		}
		sub := args[0]
		cfg, err := loadConfig("migrate "+sub, args[1:], stderr)
		if err != nil {
			return err
		}
		return migrate(ctx, cfg, sub, newLogger(cfg.Log, stderr), stdout)
	case "storage":
		return storageCmd(ctx, args, stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return nil
	default:
		return fmt.Errorf("%w: unknown command %q", errUsage, cmd)
	}
}

func loadConfig(name string, args []string, stderr io.Writer) (*config.Config, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", defaultConfig, "path to the TOML configuration file")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", errUsage, err)
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("%w: unexpected arguments %v", errUsage, fs.Args())
	}
	return config.Load(*path)
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

func migrate(ctx context.Context, cfg *config.Config, sub string, log *slog.Logger, stdout io.Writer) error {
	pool, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	if sub == "up" {
		return db.Migrate(ctx, pool, log)
	}
	st, err := db.Status(ctx, pool)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "COMPONENT\tVERSION\tNAME\tSTATE\tAPPLIED AT")
	for _, m := range st {
		state, at := "pending", ""
		if m.Applied {
			state = "applied"
			if !m.AppliedAt.IsZero() {
				at = m.AppliedAt.UTC().Format("2006-01-02 15:04:05Z")
			}
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n", m.Component, m.Version, m.Name, state, at)
	}
	return tw.Flush()
}
