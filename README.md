# Ervisio Repo

Self-hosted Git forge with built-in CI (Actions-compatible), OCI registry, LFS and tiered object storage. One Go binary, one PostgreSQL database, an embedded React SPA.

Part of the Ervisio ecosystem. Licensed under MIT.

## Status

Phase 0 (foundations): config, HTTP server, database, OpenAPI pipeline, empty SPA, dev environment, CI. No Git features yet. The roadmap is in [docs/PLAN.md](docs/PLAN.md).

## Requirements

- Go (version in `go.mod`)
- Node 24
- Docker with Compose (dev services and integration tests)
- GNU make

## Quickstart

```sh
make tools   # pinned sqlc and golangci-lint
make dev     # services, Go server on :3400, Vite on :5173
```

Open <http://localhost:5173>. Mailpit is on <http://localhost:8025>, the MinIO console on <http://localhost:9001>. Stop the services with `make dev-down`.

Other targets: `make help`.

## Layout

| Path | Content |
| --- | --- |
| `api/` | OpenAPI contract and runner protocol |
| `cmd/` | `ervisio-repo` (server), `ervisio-runner`, `erepo` (CLI) |
| `internal/` | Server packages |
| `web/` | React SPA (Vite) |
| `plugin/` | Ervisio plugin |
| `deploy/` | Dev and production deployment files |
| `packaging/` | Distribution packages |
| `test/` | End-to-end and conformance tests |
| `docs/` | Design and operations docs |

Agent and contributor rules are in [CLAUDE.md](CLAUDE.md).
