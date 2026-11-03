package main

import (
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ervisio/ErvisioRepo/internal/backup"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/gitstore"
	"github.com/Ervisio/ErvisioRepo/internal/gittier"
	"github.com/Ervisio/ErvisioRepo/internal/jobs"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// gitTiering is the hot tier with hydration and eviction (PLAN §4.3).
type gitTiering struct {
	Tier     *gitstore.Tier
	Hydrator *gittier.Hydrator
	Evictor  *gittier.Evictor
	Admin    *gittier.Admin
}

// setupGitTiering builds the hot tier with its hydrator, evictor and admin
// service; it returns nil when no git storage policy is configured.
func setupGitTiering(cfg *config.Config, pool *pgxpool.Pool, storage *tier.Tier, log *slog.Logger) (*gitTiering, error) {
	h := gittier.New(gittier.Options{Pool: pool, Log: log.With("component", "hydrator"), Paranoid: cfg.Storage.Paranoid})
	t, err := openGitTier(cfg, pool, storage, h, log)
	if err != nil || t == nil {
		return nil, err
	}
	h.SetTier(t)
	budget, high, low := hotBudget(cfg)
	ev := gittier.NewEvictor(gittier.EvictorOptions{
		Pool: pool, Tier: t, Log: log.With("component", "evictor"),
		TTL: time.Duration(cfg.Storage.Policy[gitstore.Policy].CacheTTL), Budget: budget, High: high, Low: low,
	})
	return &gitTiering{Tier: t, Hydrator: h, Evictor: ev, Admin: gittier.NewAdmin(pool, ev)}, nil
}

// hotBudget returns the space of the hot tier: the budget of the local
// target holding it (the git cache, else the git primary), less the share
// reserved to LFS, with that target's watermarks.
func hotBudget(cfg *config.Config) (int64, float64, float64) {
	p := cfg.Storage.Policy[gitstore.Policy]
	for _, name := range []string{p.Cache, p.Primary} {
		for _, t := range cfg.Storage.Targets {
			if name != "" && t.Name == name && t.Kind == "local" {
				b := float64(t.Budget)
				if p.CacheShare > 0 && p.CacheShare < 1 {
					b *= 1 - p.CacheShare
				}
				return int64(b), t.HighWatermark, t.LowWatermark
			}
		}
	}
	return 0, 0, 0
}

// jobOptions registers the eviction pass.
func (g *gitTiering) jobOptions(log *slog.Logger) []jobs.Option {
	return []jobs.Option{gittier.JobsOption(g.Evictor, log)}
}

// backupJobOption registers the daily PostgreSQL dump when a backup
// storage policy is configured.
func backupJobOption(cfg *config.Config, storage *tier.Tier, log *slog.Logger) (jobs.Option, bool) {
	p, ok := cfg.Storage.Policy[backup.Policy]
	if !ok || storage == nil {
		return nil, false
	}
	class, err := storage.Class(backup.Policy)
	if err != nil {
		return nil, false
	}
	return backup.JobsOption(backup.JobOptions{Class: class, DatabaseURL: cfg.Database.URL,
		Retention: time.Duration(p.Retention), Log: log.With("component", "backup")}), true
}
