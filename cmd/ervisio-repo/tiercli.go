package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/storage/tier"
)

// offline is the database and storage of a maintenance command.
type offline struct {
	cfg     *config.Config
	log     *slog.Logger
	pool    *pgxpool.Pool
	storage *tier.Tier
	close   func()
}

// openOffline connects to the database (migrated) and to the storage targets.
// Objects stored meanwhile are left to the replication sweep of the server.
func openOffline(ctx context.Context, cfg *config.Config, stderr io.Writer) (*offline, error) {
	log := newLogger(cfg.Log, stderr)
	pool, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx, pool, log); err != nil {
		pool.Close()
		return nil, err
	}
	storage, stores, err := openStorage(ctx, cfg, dbgen.New(pool), prometheus.NewRegistry(), log, nil)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &offline{cfg: cfg, log: log, pool: pool, storage: storage, close: func() {
		tier.CloseStores(stores)
		pool.Close()
	}}, nil
}

// parseFlags parses fs, mapping parse errors to errUsage.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	return nil
}
