# Ervisio Repo

Self-hosted Git forge with built-in CI (Actions-compatible), OCI registry, LFS and tiered object storage. One Go binary, one PostgreSQL database, an embedded React SPA.

Part of the Ervisio ecosystem. Licensed under MIT.

## Status

Phase 0 (foundations) and phase 1 (storage layer) are done: config, HTTP server, database, OpenAPI pipeline, SPA with the UI kit, dev environment, CI, and tiered object storage on local disk, S3 and SFTP. No Git features yet. The roadmap is in [docs/PLAN.md](docs/PLAN.md).

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

## Storage

Storage targets (`local`, `s3`, `sftp`) and per-data-type policies are set in the `[storage]` section of the configuration; see `docs/PLAN.md` section 4 for a full example. Notes:

- `sftp` targets need `known_hosts_file` (absolute path, host keys are always verified); `root` sets the remote base directory.
- `s3` targets take an optional `prefix` (ending with `/`). Credentials come from `credentials_file`, or from the standard `AWS_*` environment variables.
- Targets with `encrypt = true` need `security.master_key_file`: 32 bytes as hex or base64, mode `0600`.
- Key, credential and known-hosts files must not be readable by group or others.

Test a target end to end (write, stat, read, range read, list, delete):

```sh
ervisio-repo storage test <target> --config /etc/ervisio-repo/config.toml
```

The same test, with the status of policies and caches, is on the admin page `/admin/storage`. Until authentication lands (phase 2), the admin API answers only to direct loopback requests.

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
