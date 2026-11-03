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

Phase 2, organizations and teams:

- Migration 00050: organization roles `owner`, `admin`, `member`, `billing` and a base repository permission; `teams`, `team_members`, `team_repos` (repository foreign key arrives in phase 3), `org_invites`, `quota_defaults`, `quotas`, `usage_counters`, and the organization scope of `audit_log`. Queries in `orgs.sql`, `teams.sql`, `invites.sql`, `quotas.sql`.
- `internal/authz`: organization and team role checks and the effective repository permission (`read`, `triage`, `write`, `maintain`, `admin`), with table and fuzz tests.
- `internal/orgs`: create, configure and delete organizations; members and roles (only owners grant ownership, the last owner cannot leave); teams and team members; email invitations with single-use expiring tokens, mailed by a River job (en/it template) or returned once when mail is disabled; all changes audited.
- `internal/quota`: repository and storage limits per user and organization, with instance defaults and a reusable check.
- REST: `/api/v1/orgs/*` (organizations, members, teams, invitations, quota, organization audit log) and `GET /api/v1/admin/audit` (paginated instance audit log for instance admins).

Phase 2, two-factor authentication:

- Migration 00030: `mfa_totp`, `mfa_recovery_codes`, `webauthn_handles`, `webauthn_credentials`, `mfa_challenges` and `orgs.require_2fa`; queries in `mfa.sql`.
- `internal/auth/mfa`: TOTP (RFC 6238, standard library, secrets sealed with a key derived from `security.master_key_file` when set), WebAuthn credentials and passkeys (go-webauthn v0.18.2) for passwordless login and as a second factor, ten single-use recovery codes stored as SHA-256, and the two-step login: a password login of an account with a factor answers 202 with an `mfa_pending` challenge (HttpOnly `__Host-erepo_mfa` cookie, 5 minutes, 5 attempts) and no session.
- `[auth] require_2fa = "off" | "admins" | "all"` and the organization `require_2fa` flag: an account that must have a factor and has none enrolls an authenticator app during login; its last factor cannot be removed.
- REST: `/api/v1/auth/mfa/*` (challenge, TOTP, recovery code, WebAuthn, enrollment, passkey login) and `/api/v1/user/security/*` (status, TOTP setup and removal, recovery codes, passkeys). Adding or removing a factor needs the password or a code.

Phase 2, OIDC sign-in, access tokens and keys:

- OIDC providers (`[[auth.oidc]]`, go-oidc v3 and x/oauth2) with PKCE, state and nonce; browser flow at `/auth/oidc/{provider}/login` and `/callback`, errors reported to the SPA as `?oidc_error=`.
- Account linking only from a signed-in session, never by matching email (`sec_auth_oidc_takeover` test); `GET /user/oidc`, `POST /user/oidc/{provider}/link`, `DELETE /user/oidc/{id}` (the last sign-in method cannot be removed); `GET /auth/oidc/providers`.
- Personal access tokens (`erp_` prefix, SHA-256 hash only, scopes, mandatory expiry) at `/user/tokens`, accepted as `Authorization: Bearer` on operations that declare token scopes; token callers skip the CSRF check and never reach admin endpoints.
- SSH keys (x/crypto/ssh, unique SHA256 fingerprint, RSA at least 3072 bits) and armored GPG keys (ProtonMail/go-crypto, key id, subkeys, emails) at `/user/keys/ssh` and `/user/keys/gpg`.
- Migration `00040_oidc_tokens_keys`, integration test against Dex in testcontainers.

Phase 2, web interface:

- Sign-in pages (login, registration, email verification, password reset, second factor with TOTP, recovery code or security key, TOTP enrollment when 2FA is required, passkey login, OIDC buttons and error messages) and the account settings (profile with organizations, security with re-authentication, tokens, SSH and GPG keys, linked accounts).
- Organization pages: members, teams, invitations with the invite link, settings (owners: display name, description, base permission, `require2fa`), and `/org-invite?token=` to accept an invitation. Instance audit page at `/admin/audit`.
- `require2fa` in `Org` and `OrgUpdate` (owners only).
- The Vite dev server proxies `/auth/oidc` to the backend.
- Playwright visual baselines for every new page at 1280x800 and 390x844.

Phase 3, Git hosting:

- Repositories with CRUD, visibility, rename redirects and transfer; Git wrapper over `internal/sys`.
- Smart HTTP (protocol v2, PAT or session auth, gzip, rate limits) and SSH server on port 2222 (user `git`, ed25519 and RSA host keys).
- Pack journal: managed hooks over a unix socket, self-contained push segments, sync or async durability, compaction, orphan GC, git maintenance, 100 MiB file limit outside LFS.
- Hydration with progress (SSE and SSH stderr), eviction by TTL and watermarks with a 1 hour trash, admin pin/unpin/evict.
- `prewarm`, `restore` and `recovery-kit export/import` commands; daily pg_dump backup to storage, wal-g docs.
- Account API and screens for profile, password, sessions and emails; OIDC start rate limit; per-provider `require_local_2fa`.
- Self-hosted Figtree and JetBrains Mono fonts; repository list, creation, empty page with clone instructions and settings.

Phase 4, Git LFS:

- Batch API at `/<owner>/<repo>.git/info/lfs/objects/batch` (upload, download, verify) with the basic transfer: presigned S3 URLs when the primary supports them and is not encrypted, otherwise a streaming proxy that checks sha256 and size.
- Content-addressed, instance-wide deduplicated objects under `lfs/sha256/ab/cd/<oid>`; per-repository references in `lfs_objects` (migration 00100), charged to the owner's storage quota. A repository gains an object only by proving possession of its content.
- Locks API (create, list, verify, unlock; force unlock for repository admins), also over SSH.
- `git-lfs-authenticate` with short-lived `RemoteAuth` tokens (1 hour, stored hashed) and `git-lfs-transfer` (pure SSH protocol).
- Conservative GC job every 6 hours: expired tokens, rows of deleted repositories, quota moves after transfers, objects no longer referenced by any reachable pointer after a 14 day grace, unreferenced stored content.

Phase 4, code browsing:

- `internal/gitbrowse` on `internal/git`, always under the tier Read lease: ref resolution, trees with the last commit of each entry (time budget and cache), blobs (binary detection, size limit, safe content type, LFS pointers), README, blame, paginated history (optionally per path), commits with a parsed and truncated diff, branches with ahead/behind, tags, compare from the merge base, `git grep` with a timeout and a result limit.
- REST endpoints (tag `code`) under `/api/v1/repos/{owner}/{name}/`: `refs/resolve`, `tree`, `blob`, `readme`, `blame`, `commits`, `commits/{sha}`, `branches`, `tags`, `compare`, `search/code`, `POST contents`; plus `GET /api/v1/search/repos`, a name search that respects visibility.
- Streaming downloads outside the request timeout: `raw?ref=&path=` (nosniff, sandbox CSP, text always as `text/plain`) and `archive?ref=&format=zip|tar.gz` (`git archive`).
- Web edit (create, update, delete a file) without a working tree: `hash-object`, `mktree`, `commit-tree` and a compare-and-swap `update-ref` under the Write lease and inside a hook session, so the journal records it like a push; push permission, quota and stale `sha` (409) checked.

Phase 4, releases:

- Releases and assets (migration 00110) under `/api/v1/repos/{owner}/{name}/releases`: list, create, latest, update, delete; drafts visible only to writers; a missing tag is created at publish and kept when the release is deleted.
- Asset upload (raw body) and download (single Range, download counter) as streaming routes outside the request timeout; bytes stored with the `releases` storage policy (fallback `lfs`) and charged to the owner's quota.
- `releases_cleanup` job removes orphaned asset objects older than 2 hours.

Phase 4, web UI:

- Code view: repository sub-navigation, tree with last commit per entry, branch/tag switcher, breadcrumb, clone dialog, archive menu (zip, tar.gz) and README rendered by an in-house safe Markdown renderer (no raw HTML, same-ref relative links, images from `/raw`).
- File view: Shiki highlighting with the JavaScript regex engine (no WASM, CSP-safe) mapped to the `--syn-*` tokens, lazy-loaded per language; line links (`#L`), blame, Markdown preview, raw, download, image/LFS/binary notes; virtualized rows above 1500 lines.
- History (paginated, optionally per path), commit page with message and diff, branches with ahead/behind, tags, compare with swap.
- Own diff renderer: unified on narrow screens, unified or split on desktop, file list, collapsible files, `--ok-s`/`--err-s` rows, virtualized per file.
- In-repo code search (regexp, case, path) and a global repository search palette (Ctrl/Cmd+K) in all three shells.
- Web edit with lazy-loaded CodeMirror 6 (CSP nonce): create, edit and delete a file, commit to the branch or to a new branch, conflict dialog on a stale `sha` (409).
- Releases: list, detail with notes and assets, create/edit form, asset upload with progress and cancel, download, delete.
- New dependencies: `shiki`, the `@codemirror/*` packages needed for the editor and `@lezer/highlight` (previously transitive).

Phase 5, branch protection:

- Branch protection rules and tag rules (migration 00130) under `/api/v1/repos/{owner}/{name}/branch-protections` and `/tag-rules`: CRUD for repository admins, glob patterns (`*`, `**`, `?`, `[...]`) with the most specific rule winning, and `POST .../branch-protections/evaluate` to dry-run a ref update.
- Branch rules: restrict pushes to users and teams, block force pushes and deletions, require linear history, require signed commits (GPG and SSH signatures checked against the pusher's verified keys), required approvals, CODEOWNERS reviews, stale review dismissal and required status checks (strict mode: up to date with the base); admins may bypass.
- Tag rules: minimum permission (write, maintain, admin, none) to create, move and delete matching tags, plus allowed users and teams.
- Enforcement in the git hooks for SSH and HTTP pushes (pre-receive) and for server-side ref updates such as web edits (reference-transaction `prepared`); a rule that cannot be checked refuses the update, and the refusal is shown to the git client and returned as 403 by the web edit API.
- CODEOWNERS parser (`CODEOWNERS`, `.github/`, `docs/`) with `@user`, `@org/team` and verified email owners; `PullRequestStatus` reports approvals, missing checks and code owner groups for the pull request merge.
- The hook server now also runs without a remote storage tier, so the rules apply on every installation.

Phase 5, GitHub sync:

- GitHub connections (migration 00140, `/api/v1/github/connections`) owned by a user or an organization: GitHub App installations (JWT RS256 signed with the standard library, cached installation tokens) or fine-grained tokens, for github.com or a GitHub Enterprise Server (instance admins only). Private keys, tokens and webhook secrets are sealed with AES-256-GCM under a key derived from the master key; without `security.master_key_file` connections cannot be created. `POST .../check` tests the credentials.
- One-off import of the git data of a GitHub repository (`POST /api/v1/github/imports`, status at `/repos/{owner}/{name}/github/import`): a background job fetches all branches and tags into a new repository. The `ghsync.Importer` interface is the extension point for issues, pull requests and releases.
- Per-repository link (`/repos/{owner}/{name}/github/sync`) in one of three modes: pull mirror (read-only, pushes are refused in the git hooks), backup to GitHub (push after every local push, nightly check, optional repository creation) and bidirectional sync (fast-forwards in both directions, divergent branches kept at `refs/heads/sync-conflict/github/<branch>` and resolved with keep Ervisio, keep GitHub or merge request; `trustGithub` patterns let GitHub win). Deletions propagate only with `prune`; Git LFS objects are not sent and the link reports it.
- Scheduled runs (interval of 10 minutes to 7 days) with exponential backoff on failures, per-ref state and errors (`.../github/sync/refs`), manual run (`.../github/sync/run`) and debounced runs after local pushes.
- Webhook receiver `POST /api/v1/github/webhook/{connectionId}` with `X-Hub-Signature-256` verification (constant-time compare); a push schedules the mirror and sync links of the pushed repository.

Phase 5, issues:

- Issues (migration 00120) under `/api/v1/repos/{owner}/{name}/issues`: list with filters (state, labels, milestone, assignee, author, mentioned, text search, sort), create, edit, open/close with reason (completed, not planned, duplicate), lock/unlock, body edit history. Issues and pull requests share one number sequence per repository (`issues.NextNumber`).
- Comments with edit history and deletion, reactions (`+1`, `-1`, `laugh`, `hooray`, `confused`, `heart`, `rocket`, `eyes`) on issues and comments, timeline of events (labels, assignees, milestones, renames, state changes, locks, references).
- Labels and milestones (with due date and open/closed counts) per repository, managed by triagers.
- References in Markdown: `@user` mentions, `#N` and `owner/repo#N` cross-references and commit ids, outside code; references from repositories the viewer cannot read are redacted on the timeline.
- Closing keywords (`close`, `fix`, `resolve` and their forms) in commits pushed to the default branch close the referenced issues (`issues.CloseFromText`, also usable for merged pull requests).
- Issue templates and forms from `.github/ISSUE_TEMPLATE` of the default branch, with `config.yml` (blank issues, contact links).
- Issue attachments: upload (`POST .../issues/attachments?name=`, raw body, 25 MiB by default, charged to the owner quota), download (`GET .../issues/attachments/{id}/download`, range requests, images inline and everything else as a download with a sandboxed CSP) and deletion; a periodic job removes orphaned objects.
- Transactional event outbox (`internal/events`): producers emit events in the transaction of the change, a River job delivers them to the registered handlers with retries. Pushes (transports and web edits) publish `push` events through the hook server.

### Removed

- Organization role `billing` (existing members become `member`).

### Fixed

- Deleting a repository now releases the bytes of its release assets from the owner quota (they were summed after the cascade delete, so always as 0), and those of its issue attachments.

### Security

- The SPA `index.html` is served with a per-response CSP nonce in `style-src`, so the `<style>` elements injected by Radix (scroll lock) are allowed without `'unsafe-inline'`; `script-src` stays `'self'`.
- The admin API is protected by `RequireAdmin` instead of the phase 1 loopback guard; `ervisio-repo storage test` keeps working from the CLI.
- Login, password reset, resend and registration answer the same way, with comparable timing, whether or not the account or address exists (with mail enabled). Duplicate registrations send a notice to the address owner instead of failing.
- TOTP codes are accepted once per time step (no replay within the window), recovery codes once; wrong second factors count against the per-account backoff.
- Brute-force limits are in memory and per process. The per-account backoff can be used to slow down a victim's logins, up to `login_backoff_max`.
