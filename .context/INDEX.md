# Context index
- now.md: current task, plan, next step. Always loaded.
- decisions.md: architectural decisions and why. Read before structural changes.
- map/runner.md: ervisio-runner daemon, job execution on the act library, reporter, labels, runner.proto. Read before touching cmd/ervisio-runner, internal/runner, api/runner.
- map/actions.md: Actions server (internal/actions): triggers, planning, scheduling, runner service, tokens, secrets, logs. Read before touching internal/actions or Actions wiring in serve.go.
- map/plugin.md: Ervisio core plugin (TS) and the pluginapi admin socket API. Read before touching plugin/, internal/pluginapi.
- map/deploy.md: Docker, compose, packaging, release signing, install scripts, Caddy publishing. Read before touching Dockerfile, deploy/, packaging/, scripts/release, internal/caddy, cmd/ervisio-repo/caddy.go, Makefile.
- map/repo-core.md: CLI, sys/git/storage primitives, repo leases, event outbox, jobs. Read before touching internal/sys, internal/git, internal/gitstore, internal/storage, internal/events, internal/jobs, internal/statuses, cmd/ervisio-repo.
