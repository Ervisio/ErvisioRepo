package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/gittier"
)

// prewarmCmd hydrates cold repositories ahead of use (PLAN §4.3):
//
//	ervisio-repo prewarm --all | <owner>/<repo> [--config <path>]
func prewarmCmd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("prewarm", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", defaultConfig, "path to the TOML configuration file")
	all := fs.Bool("all", false, "hydrate every cold repository")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *all == (fs.NArg() == 1) || fs.NArg() > 1 {
		return fmt.Errorf("%w: prewarm needs --all or one <owner>/<repo>", errUsage)
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	o, err := openOffline(ctx, cfg, stderr)
	if err != nil {
		return err
	}
	defer o.close()
	tiering, err := setupGitTiering(cfg, o.pool, o.storage, o.log)
	if err != nil {
		return err
	}
	if tiering == nil {
		return errors.New("prewarm: no git storage policy configured")
	}
	type target struct {
		id   uuid.UUID
		name string
	}
	var targets []target
	q := dbgen.New(o.pool)
	if *all {
		rows, err := q.ListReposByState(ctx, gitstore.StateCold)
		if err != nil {
			return fmt.Errorf("list cold repositories: %w", err)
		}
		for _, r := range rows {
			targets = append(targets, target{r.ID, r.OwnerName + "/" + r.Name})
		}
	} else {
		owner, name, ok := strings.Cut(fs.Arg(0), "/")
		if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
			return fmt.Errorf("%w: %q is not <owner>/<repo>", errUsage, fs.Arg(0))
		}
		r, err := q.GetRepoByPath(ctx, dbgen.GetRepoByPathParams{Owner: owner, Name: name})
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("repository %s/%s not found", owner, name)
		}
		if err != nil {
			return fmt.Errorf("load repository: %w", err)
		}
		targets = append(targets, target{r.Repo.ID, owner + "/" + name})
	}
	failed := 0
	for _, t := range targets {
		fmt.Fprintf(stderr, "%s\n", t.name)
		if err := tiering.Hydrator.Hydrate(gittier.WithProgressWriter(ctx, stderr, "en"), t.id); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			failed++
			fmt.Fprintf(stderr, "%s: %v\n", t.name, err)
		}
	}
	fmt.Fprintf(stdout, "%d hydrated, %d failed\n", len(targets)-failed, failed)
	if failed > 0 {
		return fmt.Errorf("prewarm: %d repositories failed", failed)
	}
	return nil
}
