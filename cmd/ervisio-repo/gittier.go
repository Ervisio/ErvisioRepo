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
// target holding it (the git cache, else the git primary, else the hot
// cache target), less the shares of the object caches kept on that target,
// with that target's watermarks.
func hotBudget(cfg *config.Config) (int64, float64, float64) {
	s := &cfg.Storage
	p := s.Policy[gitstore.Policy]
	var hot *config.Target
	for _, name := range []string{p.Cache, p.Primary} {
		for i := range s.Targets {
			if hot == nil && name != "" && s.Targets[i].Name == name && s.Targets[i].Kind == "local" {
				hot = &s.Targets[i]
			}
		}
	}
	if hot == nil {
		hot = s.HotTarget()
	}
	if hot == nil {
		return 0, 0, 0
	}
	b := float64(hot.Budget)
	if shares := s.CacheShares(hot.Name); shares > 0 && shares < 1 {
		b *= 1 - shares
	}
	return int64(b), hot.HighWatermark, hot.LowWatermark
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
