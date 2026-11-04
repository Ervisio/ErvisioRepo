# repo-core
Purpose: the pre-Actions core of ErvisioRepo: CLI subcommands, process/git/storage primitives, hot-tier repo leases, event outbox and River jobs.
Entry points: cmd/ervisio-repo/main.go:dispatch, internal/sys/sys.go:Run, internal/git/git.go:Run, internal/gitstore/tier.go:Tier.Acquire, internal/storage/tier/tier.go (Tier, object placement), internal/storage/blob/blob.go (Store), internal/events/event.go:Emit, internal/events/bus.go:Bus, internal/jobs/jobs.go:Runner, internal/gittier/hub.go (hydration progress), internal/statuses/service.go
Flow (git op): caller → gitstore.Tier.Acquire(ctx, repoID, Read|Write) → Lease with hot bare-repo path (hydrates a cold repo first via gittier) → internal/git.Run/Exec(path, args) → sys.Run (no shell) → lease.Release().
Flow (event): domain tx → events.Emit(ctx, q.WithTx(tx), ev, payload) writes events_outbox in the same tx → after commit Bus.Kick → River drain job → one events_deliver job per registered Handler for that kind → handler runs (webhooks, auto-merge, notifications).
Flow (data bytes): service → storage/tier (policy per data type) → blob.Store drivers local/s3/sftp wrapped by retry, crypt, metered; row in storage_objects is inserted only after the primary write is verified.
CLI subcommands besides serve (main.go usage): migrate up|status, storage, prewarm, restore, recovery-kit export|import, caddy setup|status|remove, version, hook (internal, run by managed git hooks, loads no config).
Invariants (CLAUDE.md rules 3-5):
- Processes start only in internal/sys (no shell, argv verbatim, env not inherited beyond Cmd.Env). Git runs only through internal/git, which fixes a clean env (no system/global config) and requires an absolute repo path.
- The repo path given to internal/git must come from gitstore Tier.Acquire (the only exception to the no-direct-FS rule); never cache the path past Release, eviction may remove it.
- All other data files go through internal/storage; no os.* on data paths.
- SQL only in internal/db/queries/*.sql compiled by sqlc into internal/db/dbgen; migrations are goose, forward only (no Down).
- Events are at-least-once: handlers must be idempotent. Emit under events.Quiet(ctx) records nothing (used by imports/sync to avoid webhook storms).
- Errors wrap with %w; REST errors are problem+json; slog without secrets.
Gotchas:
- CLAUDE.md says tier.Acquire, but it lives in internal/gitstore (Tier), not internal/storage/tier; the latter is the object-store placement tier. Do not confuse them.
- Do not call Acquire again for the same repo while holding a Write lease, and never open a transaction that takes LockRepoWrite on it: self-deadlock.
- A Read or Write lease blocks eviction; hold it only for the git command, and treat ErrBusy as HTTP 503 + Retry-After, ErrCold means no Hydrator configured.
- Emit needs a queries handle bound to the transaction of the change, otherwise the "event exists iff change committed" guarantee is lost; use Bus.Publish only for changes without their own tx.
- Outbox rows are pruned after 7 days; delivery retries are capped at 10 per handler.
- Tests exist but must not be extended or run (CLAUDE.md rule 6); verify with make build, go vet ./..., make lint, make check-generate.
- Generated code (dbgen, oapi, TS client) is versioned: edit api/openapi.yaml or queries then make generate, never the output.
Related: map/deploy.md (caddy CLI, install), map/runner.md, map/plugin.md
