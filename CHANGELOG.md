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

Phase 2 foundation, identity and organizations:

- Migration 00020: `namespaces` (one case-insensitive namespace for users and orgs), `users`, `emails`, `passwords`, `sessions`, `one_time_tokens`, `invites`, `orgs`, `org_members`, `audit_log`; sqlc queries in `identity.sql`, `sessions.sql`, `audit.sql`.
- `internal/auth`: argon2id hashing (parameters from `[auth]`, bounded concurrency, rehash on parameter change), 256-bit opaque tokens stored as SHA-256, database sessions with idle and absolute expiry, `__Host-` cookies (HttpOnly, Secure, SameSite=Lax) rotated at login, CSRF protection, per-account and per-IP backoff, `Principal` in the request context, `RequireUser`/`RequireAdmin` middleware and OpenAPI scope enforcement. Test helpers in `internal/auth/authtest`.
- `internal/identity`: registration with `open`, `invite` or `closed` policy (the first account becomes instance admin), reserved names, login, logout, email verification, password reset and invites, all audited; mail and cleanup jobs on River. Test helpers in `internal/identity/identitytest`.
- `internal/mail`: SMTP sender (`[mail]` section, STARTTLS, implicit TLS, or none for Mailpit) with English and Italian templates.
- `internal/audit`: append-only audit log of authentication events.
- REST: `POST /api/v1/auth/register`, `/auth/login`, `/auth/logout`, `/auth/email/verify`, `/auth/email/resend`, `/auth/password/reset`, `/auth/password/reset/confirm`, `GET /api/v1/user`, `POST /api/v1/admin/invites`.
- Web API client sends the CSRF header on unsafe requests.
- Development config sends mail to Mailpit.

### Security

- The SPA `index.html` is served with a per-response CSP nonce in `style-src`, so the `<style>` elements injected by Radix (scroll lock) are allowed without `'unsafe-inline'`; `script-src` stays `'self'`.
- The admin API is protected by `RequireAdmin` instead of the phase 1 loopback guard; `ervisio-repo storage test` keeps working from the CLI.
- Login, password reset, resend and registration answer the same way, with comparable timing, whether or not the account or address exists (with mail enabled). Duplicate registrations send a notice to the address owner instead of failing.
- Brute-force limits are in memory and per process. The per-account backoff can be used to slow down a victim's logins, up to `login_backoff_max`.
