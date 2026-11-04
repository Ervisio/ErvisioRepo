package main

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/actions"
	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/events"
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
