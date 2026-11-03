package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/githooks"
	"github.com/Ervisio/ErvisioRepo/internal/gitjournal"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/gittransport"
	"github.com/Ervisio/ErvisioRepo/internal/jobs"
)

// hookCmd is "ervisio-repo hook <name> [args]", run by git through the
// managed hook scripts. It loads no configuration: everything it needs is
// in the environment set by the server.
func hookCmd(ctx context.Context, args []string, stdin io.Reader, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "error: missing hook name")
		return 2
	}
	return githooks.RunHook(ctx, args[0], args[1:], stdin, stderr, os.Getenv)
}

// gitJournal is the write path of the git journal: the hook server and the
// journal jobs. Close it after the transports have stopped.
type gitJournal struct {
	Journal *gitjournal.Journal
	Hooks   *githooks.Server
	Jobs    jobs.Option
}

// setupGitJournal builds the journal and starts the hook server, or
// returns nil when the git tier has no remote store. Call
// Journal.SetEnqueuer once the job runner exists.
func setupGitJournal(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, t *gitstore.Tier, log *slog.Logger) (*gitJournal, error) {
	if t == nil || t.Store() == nil {
		return nil, nil
	}
	j, err := gitjournal.New(gitjournal.Options{
		Pool: pool, Tier: t,
		Durability:  cfg.Storage.Policy[gitstore.Policy].Durability,
		MaxPushSize: int64(cfg.Git.MaxPushSize),
		Log:         log.With("component", "git-journal"),
	})
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("git hooks: locate executable: %w", err)
	}
	srv, err := githooks.Start(ctx, githooks.Options{
		Dir: t.HotRoot(), Executable: exe, Handler: j, Log: log.With("component", "git-hooks"),
	})
	if err != nil {
		return nil, err
	}
	return &gitJournal{Journal: j, Hooks: srv, Jobs: gitjournal.JobsOption(j)}, nil
}

// Close stops the hook server.
func (g *gitJournal) Close() {
	if g != nil {
		_ = g.Hooks.Close()
	}
}

// TransportHooks adapts the hook server to gittransport.Hooks, or returns
// nil (no hooks) without a journal.
func (g *gitJournal) TransportHooks(t *gitstore.Tier) gittransport.Hooks {
	if g == nil {
		return nil
	}
	return transportHooks{srv: g.Hooks, tier: t}
}

type transportHooks struct {
	srv  *githooks.Server
	tier *gitstore.Tier
}

// HookEnv implements gittransport.Hooks. The transport holds the Write
// lease of the repository while git runs.
func (h transportHooks) HookEnv(ctx context.Context, p gittransport.Push) ([]string, func(), error) {
	sess, err := h.srv.BeginPath(ctx, p.Repo.ID, h.tier.HotPath(p.Repo.ID), p.Pusher.UserID, p.Transport)
	if err != nil {
		return nil, nil, err
	}
	return sess.Env(), sess.Close, nil
}
