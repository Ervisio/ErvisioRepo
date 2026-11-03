package db

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// MigrationStatus describes one known migration.
type MigrationStatus struct {
	Component string // "app" or "river"
	Version   int64
	Name      string
	Applied   bool
	AppliedAt time.Time // zero when unknown or pending
}

// Migrate applies all pending migrations: River's first (the queue tables are
// independent from ours), then the application schema. Both run under an
// advisory lock, so concurrent starts are safe.
func Migrate(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	rm, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{Logger: log})
	if err != nil {
		return fmt.Errorf("river migrator: %w", err)
	}
	res, err := rm.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		return fmt.Errorf("river migrations: %w", err)
	}
	for _, v := range res.Versions {
		log.Info("migration applied", "component", "river", "version", v.Version, "name", v.Name)
	}

	return withGoose(pool, func(p *goose.Provider) error {
		results, err := p.Up(ctx)
		if err != nil {
			return fmt.Errorf("app migrations: %w", err)
		}
		for _, r := range results {
			log.Info("migration applied", "component", "app",
				"version", r.Source.Version, "name", r.Source.Path, "duration", r.Duration)
		}
		return nil
	})
}

// Status reports every known migration and whether it has been applied.
func Status(ctx context.Context, pool *pgxpool.Pool) ([]MigrationStatus, error) {
	var out []MigrationStatus
	err := withGoose(pool, func(p *goose.Provider) error {
		st, err := p.Status(ctx)
		if err != nil {
			return fmt.Errorf("app migration status: %w", err)
		}
		for _, s := range st {
			out = append(out, MigrationStatus{
				Component: "app",
				Version:   s.Source.Version,
				Name:      s.Source.Path,
				Applied:   s.State == goose.StateApplied,
				AppliedAt: s.AppliedAt,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	rm, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		return nil, fmt.Errorf("river migrator: %w", err)
	}
	existing, err := rm.ExistingVersions(ctx)
	if err != nil {
		return nil, fmt.Errorf("river migration status: %w", err)
	}
	applied := make(map[int]bool, len(existing))
	for _, m := range existing {
		applied[m.Version] = true
	}
	for _, m := range rm.AllVersions() {
		out = append(out, MigrationStatus{
			Component: "river", Version: int64(m.Version), Name: m.Name, Applied: applied[m.Version],
		})
	}
	return out, nil
}

func withGoose(pool *pgxpool.Pool, fn func(*goose.Provider) error) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("migrations fs: %w", err)
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("migration lock: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, fsys,
		goose.WithSessionLocker(locker), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	return fn(p)
}
