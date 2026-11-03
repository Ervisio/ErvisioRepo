package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/credentials"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/githttp"
	"github.com/Ervisio/ErvisioRepo/internal/gitssh"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/gittier"
	"github.com/Ervisio/ErvisioRepo/internal/gittransport"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
	"github.com/Ervisio/ErvisioRepo/internal/repos"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// openGitTier builds the hot repository tier, or returns nil when no hot
// path is configured (no git storage policy). hydrator may be nil: cold
// repositories then answer ErrCold.
func openGitTier(cfg *config.Config, pool *pgxpool.Pool, storage *tier.Tier, hydrator gitstore.Hydrator, log *slog.Logger) (*gitstore.Tier, error) {
	if cfg.Storage.HotPath == "" {
		log.Warn("no git storage policy: repositories and the git transports are disabled")
		return nil, nil
	}
	var store *gitstore.Store
	if _, ok := cfg.Storage.Policy[gitstore.Policy]; ok && storage != nil {
		s, err := gitstore.NewStore(storage)
		if err != nil {
			return nil, err
		}
		store = s
	}
	t, err := gitstore.NewTier(gitstore.Options{Pool: pool, HotPath: cfg.Storage.HotPath, Store: store,
		Hydrator: hydrator, Log: log})
	if err != nil {
		return nil, fmt.Errorf("git tier: %w", err)
	}
	return t, nil
}

// gitTransports are the Smart HTTP handler and the SSH server (nil when
// disabled).
type gitTransports struct {
	HTTP http.Handler
	SSH  *gitssh.Server
}

// setupGitTransports builds the Smart HTTP and SSH transports on t (PLAN
// §5.1). hooks (internal/githooks, may be nil) journals every push.
func setupGitTransports(cfg *config.Config, pool *pgxpool.Pool, t *gitstore.Tier, rs *repos.Service, hooks gittransport.Hooks,
	ids *identity.Stack, creds *credentials.Service, proxies []netip.Prefix, log *slog.Logger,
) (*gitTransports, error) {
	g := cfg.Git
	ts, err := gittransport.New(gittransport.Options{
		Tier: t, MaxPushSize: int64(g.MaxPushSize), Hooks: hooks,
		IdleTimeout: time.Duration(g.IdleTimeout), MaxDuration: time.Duration(g.MaxDuration),
		Progress: func(ctx context.Context, w io.Writer) context.Context {
			return gittier.WithProgressWriter(ctx, w, "en")
		},
		Log: log.With("component", "git"),
	})
	if err != nil {
		return nil, err
	}
	requests := auth.NewLimiter(auth.LimiterConfig{ActionsPerHour: g.RequestsPerHour}, nil)
	h, err := githttp.New(githttp.Options{
		Transport: ts, Repos: rs, Tokens: &auth.Tokens{Store: dbgen.New(pool)}, Sessions: ids.Sessions,
		Cookies: auth.CookiesFor(cfg.Server.PublicURL), TrustedProxies: proxies,
		Failures: ids.Limiter, Requests: requests, Log: log.With("component", "git-http"),
	})
	if err != nil {
		return nil, err
	}
	out := &gitTransports{HTTP: h}
	if !cfg.SSH.Enabled {
		return out, nil
	}
	keys, err := gitssh.LoadHostKeys(cfg.SSH.HostKeyDir)
	if err != nil {
		return nil, fmt.Errorf("ssh host keys: %w", err)
	}
	out.SSH, err = gitssh.New(gitssh.Options{
		Transport: ts, Repos: rs, Keys: creds, HostKeys: keys,
		Failures: ids.Limiter, Requests: requests,
		MaxConnections: cfg.SSH.MaxConnections, LoginGraceTime: time.Duration(cfg.SSH.LoginGraceTime),
		IdleTimeout: time.Duration(g.IdleTimeout), Log: log.With("component", "git-ssh"),
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
