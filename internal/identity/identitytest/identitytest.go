// Package identitytest builds an identity service on a throwaway
// PostgreSQL for integration tests. Tests are skipped without Docker
// (see dbtest).
package identitytest

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbtest"
	"github.com/Ervisio/ErvisioRepo/internal/identity"
)

// Jobs records the jobs enqueued by the service.
type Jobs struct {
	mu   sync.Mutex
	args []river.JobArgs
}

// Enqueue implements identity.Enqueuer.
func (j *Jobs) Enqueue(_ context.Context, a river.JobArgs) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.args = append(j.args, a)
	return nil
}

// Take returns and forgets the recorded jobs.
func (j *Jobs) Take() []river.JobArgs {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := j.args
	j.args = nil
	return out
}

// Env is a migrated database with an identity stack on it.
type Env struct {
	Pool    *pgxpool.Pool
	Queries *dbgen.Queries
	Config  *config.Config
	Jobs    *Jobs
	*identity.Stack
}

// Config returns the default configuration with cheap argon2 parameters
// and mail enabled (so the mail flows run).
func Config() *config.Config {
	cfg := config.Default()
	cfg.Server.PublicURL = "https://git.example.com"
	cfg.Auth.Argon2Memory = 64 << 10
	cfg.Auth.Argon2Iterations = 1
	cfg.Auth.Argon2Parallelism = 1
	cfg.Mail.Enabled = true
	return &cfg
}

// New starts PostgreSQL, migrates it and builds the stack. mod may adjust
// the configuration first.
func New(t testing.TB, mod func(*config.Config)) *Env {
	t.Helper()
	ctx := context.Background()
	pool, err := db.Open(ctx, config.Database{URL: dbtest.URL(t), MaxConns: 8})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	return Rebuild(t, pool, mod)
}

// Rebuild builds a fresh stack (new limiter, same database) on pool.
func Rebuild(t testing.TB, pool *pgxpool.Pool, mod func(*config.Config)) *Env {
	t.Helper()
	cfg := Config()
	if mod != nil {
		mod(cfg)
	}
	jobs := &Jobs{}
	st, err := identity.Setup(cfg, pool, jobs, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &Env{Pool: pool, Queries: dbgen.New(pool), Config: cfg, Jobs: jobs, Stack: st}
}
