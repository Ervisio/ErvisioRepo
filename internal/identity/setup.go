package identity

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Ervisio/ErvisioRepo/internal/auth"
	"github.com/Ervisio/ErvisioRepo/internal/config"
	"github.com/Ervisio/ErvisioRepo/internal/db/dbgen"
	"github.com/Ervisio/ErvisioRepo/internal/mail"
)

// Stack is the account machinery built from the configuration.
type Stack struct {
	Hasher   *auth.Hasher
	Sessions *auth.Sessions
	Limiter  *auth.Limiter
	Service  *Service
}

// HasherParams converts the [auth] argon2 settings.
func HasherParams(a config.Auth) auth.Argon2Params {
	return auth.Argon2Params{
		MemoryKiB:   uint32(min(int64(a.Argon2Memory)/1024, 1<<32-1)), //nolint:gosec // clamped above
		Iterations:  uint32(max(a.Argon2Iterations, 0)),               //nolint:gosec // non-negative
		Parallelism: uint8(min(max(a.Argon2Parallelism, 0), 255)),     //nolint:gosec // clamped
	}
}

// Setup builds the hasher, sessions, limiter and service. jobs may be nil
// (mail jobs are dropped).
func Setup(cfg *config.Config, pool *pgxpool.Pool, jobs Enqueuer, log *slog.Logger, now func() time.Time) (*Stack, error) {
	a := cfg.Auth
	h, err := auth.NewHasher(HasherParams(a), a.Argon2MaxConcurrent)
	if err != nil {
		return nil, fmt.Errorf("password hasher: %w", err)
	}
	ss := &auth.Sessions{Store: dbgen.New(pool), Idle: time.Duration(a.SessionIdleTimeout), MaxAge: time.Duration(a.SessionMaxAge), Now: now}
	lim := auth.NewLimiter(auth.LimiterConfig{
		FreeFailuresAccount: a.LoginFreeFailuresAccount, FreeFailuresIP: a.LoginFreeFailuresIP,
		BackoffBase: time.Duration(a.LoginBackoffBase), BackoffMax: time.Duration(a.LoginBackoffMax),
		ActionsPerHour: a.ActionsPerIPPerHour,
	}, now)
	svc := New(Options{Pool: pool, Hasher: h, Sessions: ss, Limiter: lim, Jobs: jobs, Log: log, Config: cfg, Now: now})
	return &Stack{Hasher: h, Sessions: ss, Limiter: lim, Service: svc}, nil
}

// MailerFor builds the Mailer of the mail and cleanup jobs from cfg.
func MailerFor(cfg *config.Config, q *dbgen.Queries, s mail.Sender, log *slog.Logger) *Mailer {
	return &Mailer{
		Queries: q, Sender: s, PublicURL: cfg.Server.PublicURL,
		VerifyTTL: time.Duration(cfg.Auth.VerifyTokenTTL), ResetTTL: time.Duration(cfg.Auth.ResetTokenTTL),
		IdleTTL: time.Duration(cfg.Auth.SessionIdleTimeout), Log: log,
	}
}

// EnqueueFunc adapts a function to Enqueuer.
type EnqueueFunc func(ctx context.Context, args river.JobArgs) error

// Enqueue calls f.
func (f EnqueueFunc) Enqueue(ctx context.Context, args river.JobArgs) error { return f(ctx, args) }
