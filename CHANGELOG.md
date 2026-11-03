# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

Phase 0, foundations (see `docs/PLAN.md` section 11):

- Monorepo scaffold, `Makefile`, `CLAUDE.md`, MIT license, English and Italian README.
- TOML configuration with `EREPO_*` environment overrides and validation.
- `ervisio-repo serve` with `/healthz`, `/readyz`, `/metrics` (localhost only) and `GET /api/v1/meta`; `migrate` and `version` commands.
- Go 1.26 minimum (`go.mod`).
- PostgreSQL pool (pgx), embedded goose migrations, sqlc queries, River job queue.
- OpenAPI pipeline: `api/openapi.yaml` to a Go strict server (oapi-codegen) and a TypeScript client (openapi-typescript).
- Empty React SPA (Vite, TanStack Router and Query, i18n en/it) embedded in the server binary.
- Development environment in `deploy/docker/compose.dev.yaml`: PostgreSQL, MinIO (`bitnamilegacy/minio`, since the official images are no longer published), SFTP, Mailpit.
- GitHub Actions CI: gofmt, go vet, golangci-lint, govulncheck, race tests, ESLint, web tests, generated-code drift check.

Phase 1, storage layer:

- `blob.Store` interface with `local`, `s3` (AWS SDK v2, conditional writes, presigned URLs) and `sftp` (connection pool, lock-file conditional writes) drivers, plus a shared conformance suite run against local, MinIO and OpenSSH (`atmoz/sftp`) in testcontainers; `EREPO_STORAGE_BIG=1` raises the large object to 5 GiB.
- Wrappers: `crypt` (chunked AES-256-GCM with range reads on ciphertext), `retry` (backoff on transient errors), `metered` (Prometheus metrics; several targets share one registry).
- `tier` engine for content-addressed objects: per-data-type policies, primary-first writes with SHA-256 verification, LRU cache with budget, shares and TTL, fallback to the primary when the cache loses an object, replication and verification jobs (River), `storage_objects` table (migration 00010).
- Storage configuration keys: `prefix` (s3), `root` and `known_hosts_file` (sftp, required: host keys are always verified). Credential, key and known-hosts files must not be readable by group or others. The master key file accepts 32 bytes as hex or base64.
- `ervisio-repo storage test <target>`: write, stat, read, range read, list and delete probe on one target.
- Admin API `GET /api/v1/admin/storage` and `POST /api/v1/admin/storage/{target}/test`, reachable only through direct loopback requests with a loopback `Host` header (against DNS rebinding), and rejecting cross-site writes, until authentication lands in phase 2.
- Admin page `/admin/storage` with targets, policies, caches, warnings and the connection test result.

UI phase, design kit:

- Design tokens, three switchable layouts (console, sheet, lanes) and the app shell.
- Components on Radix primitives: Button, Badge, Card, Dialog, EmptyState, Icon, Input, Menu, Select, Table, Tabs, Toast; component catalog at `/_ui`.
- Playwright visual reference tests at 1280x800 and 390x844 (`npm --prefix web run test:visual`).

### Security

- The SPA `index.html` is served with a per-response CSP nonce in `style-src`, so the `<style>` elements injected by Radix (scroll lock) are allowed without `'unsafe-inline'`; `script-src` stays `'self'`.
