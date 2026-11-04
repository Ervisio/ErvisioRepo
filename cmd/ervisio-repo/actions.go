package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/actions"
	"github.com/Ervisio/ErvisioRepo/internal/actions/artifacts"
	"github.com/Ervisio/ErvisioRepo/internal/actions/blobsvc"
	"github.com/Ervisio/ErvisioRepo/internal/actions/cache"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/events"
	"github.com/Ervisio/ErvisioRepo/internal/jobs"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// setupActions builds Ervisio Actions (PLAN §6); it returns nil when
// actions.enabled is false. The service is wired by wireActions once the
// repository services exist.
func setupActions(cfg *config.Config, pool *pgxpool.Pool, storage *tier.Tier, log *slog.Logger) (*actions.Service, error) {
	if !cfg.Actions.Enabled {
		return nil, nil
	}
	key, err := optionalMasterKey(cfg.Security.MasterKeyFile)
	if err != nil {
		return nil, err
	}
	return actions.New(actions.Options{Pool: pool, Log: log, Config: cfg.Actions, PublicURL: cfg.Server.PublicURL,
		MasterKey: key, Storage: storage})
}

// actionsJobTokens is the job token lookup of the auth stores; nil (not a
// typed nil) when Actions is off.
func actionsJobTokens(a *actions.Service) auth.JobTokenLookup {
	if a == nil {
		return nil
	}
	return a
}

// wireActions connects Actions to the other services, subscribes it to
// the event bus and starts its notification listener.
func wireActions(ctx context.Context, a *actions.Service, d actions.Deps, bus *events.Bus) {
	if a == nil {
		return
	}
	a.Wire(d)
	bus.Register("actions.trigger", a.EventHandler(), actions.EventKinds...)
	go a.Listen(ctx)
}

// setupActionsResults builds the cache and artifact services the actions
// toolkit talks to with the runtime token; nil when Actions is off or no
// storage policy holds the actions data.
func setupActionsResults(cfg *config.Config, pool *pgxpool.Pool, storage *tier.Tier, a *actions.Service,
	log *slog.Logger) (http.Handler, []jobs.Option, error) {
	if a == nil || storage == nil {
		return nil, nil, nil
	}
	if _, err := storage.Class("actions"); errors.Is(err, tier.ErrUnknownPolicy) {
		log.Warn("no actions storage policy, the actions cache and artifacts are disabled", "component", "actions")
		return nil, nil, nil
	}
	key, err := optionalMasterKey(cfg.Security.MasterKeyFile)
	if err != nil {
		return nil, nil, err
	}
	signer, err := blobsvc.NewSigner(key, cfg.Server.PublicURL)
	if err != nil {
		return nil, nil, fmt.Errorf("actions results: %w", err)
	}
	cacheSvc, err := cache.New(cache.Options{Pool: pool, Storage: storage, Verifier: a, Signer: signer, Log: log,
		MaxRepoSize: int64(cfg.Actions.CacheMaxRepoSize), TTL: time.Duration(cfg.Actions.CacheTTL)})
	if err != nil {
		return nil, nil, err
	}
	artSvc, err := artifacts.New(artifacts.Options{Pool: pool, Storage: storage, Verifier: a, Signer: signer,
		Log: log, Retention: time.Duration(cfg.Actions.ArtifactRetention)})
	if err != nil {
		return nil, nil, err
	}
	mux := http.NewServeMux()
	ch, ah := cacheSvc.Handler(), artSvc.Handler()
	mux.Handle(cache.TwirpPrefix, ch)
	mux.Handle(cache.V1Prefix, ch)
	mux.Handle(blobsvc.BlobPrefix+"cache/", ch)
	mux.Handle(artifacts.TwirpPrefix, ah)
	mux.Handle(blobsvc.BlobPrefix+"artifact/", ah)
	return mux, []jobs.Option{cacheSvc.JobsOption(), artSvc.JobsOption()}, nil
}
