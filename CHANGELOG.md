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

Phase 5, pull requests:

- Pull requests (migration 00150) under `/api/v1/repos/{owner}/{name}/pulls`: an issue of kind `pull` plus head and base refs, from the same repository or a fork (`headRepo`); list with state, base, head (`owner:branch`), author and sort filters; create (draft or ready), edit title, body, base, draft and open/closed state; commits, changed files and diff against the merge base.
- Test merge on every push to the head or base branch (`git merge-tree`), kept in `refs/pull/<n>/head` and `refs/pull/<n>/merge`, with the mergeable state and the conflicting files; a push to the head records a `head_updated` timeline event and a `synchronize` event.
- `GET .../mergeability` combines the conflict check with the branch protection requirements (approvals, code owners, required checks, up-to-date base). `POST .../merge` merges with the `merge`, `squash` or `rebase` method, through the managed hooks, optionally checking the expected head and deleting the head branch; unmet requirements answer 405. Auto-merge (`PUT`/`DELETE .../auto-merge`) merges as soon as a push, status or review completes the requirements.
- Reviews: pending reviews with draft comments, submit as approve, request changes or comment, delete a pending review, dismiss (writers); requested reviewers. Reviews feed the branch protection approvals and code owner checks.
- Inline review comments on the diff (single or multi-line, left or right side), replies, edit, delete, resolve and unresolve threads; comments are re-anchored on new pushes or marked outdated. Suggestions (```` ```suggestion ```` blocks) can be applied as a commit on the head branch.
- Forks (`/api/v1/repos/{owner}/{name}/forks`): create a fork with the branches and tags of the parent under the caller or an organization, list the forks of a repository.

Phase 5, webhooks and notifications:

- Outgoing webhooks (migration 00160) per repository (`/api/v1/repos/{owner}/{name}/hooks`, repository admins) and per organization (`/api/v1/orgs/{org}/hooks`, owners and instance admins): GitHub format (`X-GitHub-Event`, `X-GitHub-Delivery`, `X-Hub-Signature-256`, GitHub-shaped payloads) or the native `ervisio` envelope; events from the outbox (push, issues, comments, pull requests, releases) plus `ping`.
- Deliveries run as River jobs with exponential backoff; the delivery history keeps truncated request and response, with redelivery and ping endpoints. Secrets are sealed with the master key (AES-GCM, like the GitHub sync tokens) and never returned or logged.
- SSRF guard at dial time: loopback, private, link-local and other non-public addresses are refused unless the hook allows private targets or the address is in the `webhooks` CIDR allowlist; redirects are not followed.
- Releases publish `release` events (published, edited, deleted) through the outbox.
- Notifications (`internal/notify`) under `/api/v1/notifications`: one thread per user and subject (issue, pull request, release) with a reason (review requested, assigned, mentioned, author, comment, state change, subscribed), read/done state, unread count, mark-all-read, per-thread subscriptions and per-repository watch levels (`/repos/{owner}/{name}/subscription`: watching, participating, ignoring). Recipients that cannot read the repository are skipped.
- Email notifications through `internal/mail` (templates `notification` in English and Italian), sent unless the thread was read or superseded meanwhile, with an opt-out in the notification settings.

Phase 5, web UI:

- Issues: list with state, label, milestone and sort filters plus search, detail with the conversation (comments, reactions, timeline events, lock), sidebar for assignees, labels, milestone and repository watch level, and a new issue form with issue templates.
- Pull requests: list, detail with conversation, reviews (dismiss) and merge box (mergeability checks, merge method, delete branch, auto-merge), commits tab, and files tab on the diff viewer with review comment threads (reply, resolve, edit, delete), pending reviews and "apply suggestion".
- Notifications inbox (`/notifications`) with unread, participating and all filters, mark read, done, ignore and mark all read, plus an inbox entry in the navigation and email opt-out under the account settings.
- Repository settings sections for branch protection, tag rules, webhooks (deliveries, details, redeliver, ping) and GitHub (connections, sync link and status with divergence resolution, import); organization webhooks for owners.
- Three list layouts chosen per user and kept in `localStorage`: dense rows (default), filter column, and split view with a preview pane and `j`/`k` navigation. The filter column layout shows a symmetric six-column strip on the detail pages; the split view adds a previous and next bar (`wk-pnav`). Label chips and avatars pick black or white text with at least 4.5:1 contrast.
- Strings in the new `work`, `issues`, `pulls`, `notifications`, `protection`, `hooks` and `github` catalogs, in English and Italian; styles in `web/src/styles/work.css`.

Phase 5, GitHub import of issues, pull requests and releases:

- The GitHub import (`POST /api/v1/github/imports`, optional `include` subset of `labels`, `milestones`, `pulls`, `issues`, `releases`; all by default) now also imports labels, milestones, issues with comments, assignees and reactions, pull requests with their reviews and review comments, and releases with their assets (streamed into the release storage class and charged to the owner quota).
- Original numbers and timestamps are kept; the shared issue and pull request counter is raised past the highest GitHub number. Content from GitHub users without a linked identity is attributed to the importing user with an "Originally by @login on GitHub" line; approvals and change requests from unlinked reviewers become comments, so they never satisfy branch protection.
- `refs/pull/<n>/head` is fetched from GitHub, so closed and fork pull requests keep their head commit.
- Imports are idempotent across retries through the `github_import_map` table (migration 00170, GitHub id to local id per kind); GitHub rate limits (`Retry-After`, `X-RateLimit-Reset`) and pagination (`Link`) are honored.
- Event bus, webhooks and notifications are suppressed while an import runs (`events.Quiet`), including the pushes of the git import.

Phase 5, GitHub compatible REST API:

- `/api/v3` (`internal/ghapi`) serves a subset of the GitHub REST API over the existing services and permissions: repositories, contents, commits, branches, tags and refs, pull requests with reviews, review comments and requested reviewers, issues with comments, labels, assignees and milestones, releases with asset upload (`/api/uploads/...`, the `upload_url`) and download (`Accept: application/octet-stream`, ranges), plus `/user`, `/meta` and `/rate_limit`. Commit statuses and checks are served by the statuses service (see below). GraphQL is served by the subset described below.
- GitHub-shaped JSON (snake_case, `node_id`, URLs from the public URL), `Link` pagination with `page`/`per_page`, and errors as `{"message", "documentation_url"}`, the documented exception to `application/problem+json`. Stable numeric ids for UUID objects in `ghapi_ids` (migration 00175).
- Compatibility table and the list of working `gh` commands in `docs/api-v3.md` and `docs/api-v3.it.md`.

Phase 5, commit statuses and checks:

- Commit statuses (`state`, `context`, `description`, `target_url`; at most 1000 per commit and context) and check runs grouped in check suites per commit and reporter, with output and annotations (50 per request, 1000 per run), stored by `internal/statuses` (tables `commit_statuses`, `check_suites`, `check_runs`, migration 00180). Refs are resolved to their commit; writing needs the write permission, reading the read permission.
- The latest status of each context and the latest check run of each name are the required status checks of branch protection (`success`, `neutral` and `skipped` runs pass); a new status, check run or completed suite re-evaluates auto-merge.
- `/api/v1`: `GET/POST /repos/{owner}/{name}/statuses/{sha}`, `GET .../commits/{sha}/status`, `.../commits/{sha}/check-runs`, `.../commits/{sha}/check-suites`, `POST .../check-runs`, `GET/PATCH .../check-runs/{checkRunId}`, `GET .../check-runs/{checkRunId}/annotations`, `GET .../check-suites/{checkSuiteId}` and `.../check-suites/{checkSuiteId}/check-runs`.
- `/api/v3`: the GitHub-shaped statuses and check runs and suites endpoints replace the stubs (`POST .../check-suites` still answers 501); see `docs/api-v3.md`.
- Events `status` (`created`), `check_run` (`created`, `completed`) and `check_suite` (`completed`) on the bus, delivered to webhooks with GitHub-shaped payloads; hooks can subscribe to `check_run` and `check_suite`.

Phase 5, GitHub compatible GraphQL for `gh`:

- `POST /api/graphql` and `/api/v3/graphql` (`internal/ghapi/graphql`, standard library lexer, parser and executor) serve the subset of the GitHub GraphQL API used by `gh`: `viewer`, `repository`, `repositoryOwner`, `node`/`nodes`, `search` (issues and pull requests with `repo:` and the usual qualifiers, `@me`), minimal `__type`/`__schema` introspection, and the issue, comment, pull request, review, auto-merge, label and assignee mutations. Global ids are the REST `node_id` values; the same services and permission checks as `/api/v3` apply; errors as `{"errors":[{"type","message","path"}]}` with partial data. Reading needs `repo:read`, mutations `repo:write`.
- Authenticated `/api` answers carry `X-OAuth-Scopes` with the classic GitHub scope names, so `gh auth login --hostname` and `gh auth status` accept the token (it needs `repo:write` and `user:read`).
- `gh repo view/list`, `gh pr list/view/create/checkout/merge/status/comment`, `gh issue list/view/create/close/reopen/comment`, `gh release list` and `gh label list` are expected to work; matrix and known gaps in `docs/api-v3.md` and `docs/api-v3.it.md`.

Phase 5, pull request follow-ups:

- Fork networks (migration `00190_fork_networks.sql`): forks of a public repository share a local, append-only object pool (`<hot>/pools/<aa>/<network>.git`, refs `refs/net/<parent>/...`) through `objects/info/alternates`; compaction repacks only local objects (`repack -l`) and the pool is dropped with the last member. Journals stay self-contained, so a fork hydrated from its journal is a full copy. Forks of private repositories and forks created before this change keep a full copy.
- Fork lifecycle: fork creation is charged to the owner quota, a half-created fork is removed when the copy fails, and deleting a repository (new delete hook in `internal/repos`) closes the open pull requests of other repositories whose head it holds, removes their `refs/pull/<n>/merge` (the head ref stays readable) and drops the fork network pool when it was the last member.
- `PUT /api/v1/repos/{owner}/{name}/pulls/{number}/update-branch` (`merge` or `rebase`, optional `expectedHeadSha`): brings the head branch up to date with the base as the caller, through a hook session (head branch protection applies); the push then synchronizes the pull request. The merge box offers it when the branch is behind.
- Cross-references: `#n` and `owner/name#n` in pull request bodies and in review comments published outside a review are recorded like issue references; `GET /api/v1/repos/{owner}/{name}/issues/{number}/linked-pulls` lists the pull requests referencing an issue (filtered by read access) and the issue detail shows them.
- Review comments on the base (LEFT) side are re-anchored when the merge base moves.
- `GET /api/v1/repos/{owner}/{name}/pulls` accepts `q` (title and body) and `reviewRequested`; the pull request list has a search box and a "Review requested from you" view.
- The merge box lists the commit statuses and latest check runs of the head commit.
- `/api/v3`: `GET .../pulls/{n}` serves `Accept: application/vnd.github.diff` and `.patch` (so `gh pr diff` works), PR files carry the blob `sha` and `patch`, and `PUT .../pulls/{n}/update-branch` is implemented.

Phase 5, notifications, pickers and paging follow-ups:

- `GET/PUT/DELETE /api/v1/notifications/subject?repo=owner/name&number=|release=`: the caller's subscription to one issue, pull request or release (explicit or from the default rules); the issue and pull request sidebars and the release page have a subscribe/unsubscribe button, the sidebar also a reset to the repository setting.
- `GET /api/v1/notifications` accepts `repo=owner/name`; `GET /api/v1/notifications/repos` returns the per-repository thread and unread counts. The inbox filters by repository on the server and its counts no longer depend on the loaded page.
- `GET /api/v1/webhooks/events`: the catalogue of webhook events; the webhook form uses it instead of a hardcoded list.
- `X-Has-More: true|false` header on the issue list, the notifications and the webhook deliveries (response bodies unchanged); the UI no longer asks for one extra row there.
- `GET /api/v1/search/users?q=&repo=&limit=`: username or display name prefix search for the assignee and reviewer pickers. New setting `security.user_directory`: `shared` (default) finds only the people the caller shares an organization or a repository with, plus the people of `repo`; `instance` finds every active user. Administrators always search the whole instance, restricted users never do. The assignee picker has a "Search people" entry.
- `GET /api/v1/github/imports`: the GitHub imports the caller started, on repositories it can still read (`X-Has-More` paging); listed in the GitHub settings page.
- `/api/v3`: `GET /repos/{owner}/{repo}/readme` (and `readme/{dir}`), `POST /repos/{owner}/{repo}/git/refs`, `PATCH` and `DELETE /repos/{owner}/{repo}/git/refs/{ref}`; refs containing `/` are accepted in the commit and status endpoints. Limits: the readme has no `vnd.github.html` rendering and no `.github`/`docs` fallback, and only regular files are served; ref writes are limited to `heads/` and `tags/` with a full sha, deleting the default branch is refused, a branch protection refusal is a 422 and a concurrent change a 409, the returned object type is always `commit`, and no quota check is made. In `commits/{ref}/...` a ref whose last segment is `status`, `statuses`, `check-runs` or `check-suites` is read as that sub-resource, as on GitHub.

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
