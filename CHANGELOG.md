# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [1.13.1] - 2026-11-16

### Fixed

- Runner device registration: the device code was hex while `PollDevice` only accepted the base64 token shape, so the first poll answered "the server no longer knows this code" and `install.sh` stopped before the approval. Device codes now use the same shape as the other secret tokens.

## [1.13.0] - 2026-11-16

### Added

- One-command runner install with device approval: `curl -fsSL https://<host>/runner/install.sh | sudo sh` on a Linux server installs Docker Engine when missing (official `download.docker.com` repositories, apt on Debian/Ubuntu, dnf on Fedora/RHEL/Rocky/Alma; `--no-docker` runs jobs on the host), downloads and verifies `ervisio-runner`, prints a code to approve at `/runner/approve` (also `?code=`), creates the `ervisio-runner` user in the `docker` group, enables the systemd service and prints a summary. Works when piped to `sudo sh` (questions only through `/dev/tty`, never blocks without a terminal); re-runs offer upgrade or `--reconfigure`; `--uninstall` removes service, user and files and deregisters the runner. `--token` keeps the non-interactive path; the server URL is embedded so `--url` is optional. `install.ps1` uses the same approval (`irm https://<host>/runner/install.ps1 | iex`).
- Runner device registration: runner protocol RPCs `RequestDevice` (machine facts: host name, OS, arch, CPUs, memory, Docker, version; requested name, labels, capacity; 20 codes per IP per hour, 15-minute codes) and `PollDevice` (`pending`, `slow_down`, `approved`, `denied`, `expired`); approval returns a single-use registration token (never a user token) for the scope chosen on the SPA page `/runner/approve`: instance (admins), `org:<name>` (owners) or `repo:<owner>/<name>` (repository admins), with editable name, labels and capacity. v1 API `GET|POST /actions/runner-devices/{userCode}`; approvals and denials are audited. `ervisio-runner register` without a token runs this flow; `ervisio-runner deregister` (RPC `Deregister`) removes the runner from the server.
- Docker cleanup on runner hosts: `install.sh` installs `ervisio-runner-prune.timer` (daily, `RandomizedDelaySec=1h`, `Persistent=true`): Docker objects unused for 7 days (`docker system prune -af --filter until=168h`), unused volumes, runner caches/workspaces untouched for 14 days, and every unused image when the Docker data root drops below 15% free; logs in the journal; not installed with `--no-docker`, removed on `--uninstall`.
- Runner pause and load: `PUT …/actions/runners/{id}/paused` at instance, org and repository scope (audited); a paused runner finishes its jobs and gets no new ones. Runners report their capacity on Declare and Heartbeat; `ActionsRunner` gains `paused`, `status` (offline, paused, idle, online, busy), `running` and `capacity`. Runner lists show status, running/capacity, labels, version and last seen with Pause/Resume/Remove. Migration 00320 adds `actions_runners.paused`/`capacity`, `actions_registration_tokens.single_use` and `actions_runner_devices`.
- "New runner" dialog leads with the one-line install and the approval link; token commands for Linux, macOS, Windows and Docker moved under "Advanced / non-interactive" (the token is created only when opened).
- `setup.bat --runner yes|no` (default yes) enables the built-in runner like `setup.sh`.

## [1.12.0] - 2026-11-16

### Added

- Actions runners made easy to set up. The instance pool (Admin > Runners) is the default for every repository; org and repository runners stay as an optional extra. `ervisio-runner daemon` registers itself on first start from `ERVISIO_RUNNER_URL`, `ERVISIO_RUNNER_TOKEN` (or `_TOKEN_FILE`, or a `registration-token` file next to the state file, deleted after use), `_NAME`, `_LABELS`, `_CAPACITY`, and writes a default config when none exists; `register` also creates the config (`--capacity`, `--host`, `--data-dir`) and accepts bare extra labels. Default labels: `ubuntu-latest`, `ubuntu-24.04`, `ubuntu-22.04`, `self-hosted`, `linux`, `x64`/`arm64` on the act images; default capacity is half the CPUs.
- Built-in runner: `ervisio-repo actions runner-token [--scope instance|org/<o>|repo/<o>/<n>]` prints a registration token from the server shell; `ervisio-repo actions runner-setup` registers the `builtin` runner of a native install, writes `/etc/ervisio-repo/runner.toml` and starts `ervisio-runner.service`. Docker: `setup.sh --runner yes|no` (default yes) enables the compose `runner` profile (`COMPOSE_PROFILES`), mints the token and the runner registers itself; Admin > Runners shows its status.
- Remote runners: Admin/Org/Repo > Runners > "New runner" dialog with Linux, macOS, Windows and Docker commands (server URL, fresh token and expiry, name, extra labels, capacity). The server serves `/runner/install.sh` (systemd unit with a dedicated `ervisio-runner` user or user unit, launchd agent on macOS, `--host`, `--docker`, `--uninstall [--purge]`) and `/runner/install.ps1` (scheduled task at boot as SYSTEM); both download `ervisio-runner_<ver>_<os>_<arch>` release assets and verify their SHA-256. `scripts/release/runner-assets.sh` builds those assets (linux, darwin, windows; amd64, arm64) and the checksums file.

- Automatic hot cache: when a data type's primary is remote (S3, SFTP, WebDAV), recently read or written data stays on the local disk and moves back to the remote storage after inactivity, with no cache to configure per data type. LFS, releases, registry and Actions data get the local hot target as cache with a share of its budget (15%, 5%, 10%, 5%) unless their policy sets one or says `cache = "none"`; idle TTL defaults: 14 days for git repositories, 7 for LFS, release assets and registry blobs, 3 for Actions data. `[storage.hot_cache] enabled` (default true) and `ttl` (one value for every data type) in `config.toml`, also editable in Admin > Storage > Hot cache (stored in the storage overlay, applies after a restart); per data type override of the TTL in the policy dialog. `storage.hot_path` now defaults to `<first local target>/.hot` when the git policy has no local target.
- Admin > Storage "Hot cache" card: on/off and idle days, disk used by repositories and cached files against the budget, watermarks, and the repositories on the local disk (size, last use, when they move to remote, pinned) with pin, unpin and evict, paged and searchable. v1 API: `GET /admin/storage/hot-repos`, `PUT /admin/storage/hot-cache`; `StorageConfig.hotCache` and `StoragePolicyConfig.effectiveCache`, `cacheAuto`, `cacheTtlSeconds`, `cacheTtlOverrideSeconds`; `StoragePolicyInput.cacheTtlSeconds`.
- The last use of a cached object is saved as the file time of its local copy (at most once a minute per object), so idle TTLs survive restarts; re-uploading an object already stored counts as a use.

- GitHub-style Apps (apps plan A2). Users and organization owners register Apps under Settings > Developer settings > Apps (organizations: tab "Apps"), following `security.app_registration`: name, slug (bot `<slug>[bot]`, linked to the App), description, homepage, logo URL, private or public, a permission matrix (repository `contents`, `metadata` always read, `issues`, `pull_requests`, `checks`, `statuses`, `actions`, `secrets`, `variables`, `administration`, `workflows`, `deployments`; organization `members`, `organization_administration`; user `email_addresses`), events limited to what the permissions cover, webhook URL/secret/active, setup URL, OAuth callback URLs and client secrets (`client_id` `Iv23li…`). Private keys are RSA 2048 pairs generated on the server: the PEM is shown once, only the public key and its SHA-256 fingerprint are stored, up to ten per App, revocable. Migration 00310 adds `apps`, `app_keys`, `app_installations`, `app_installation_repos`, `app_installation_tokens`, `webhooks.app_id`, `check_suites.app_id` and `branch_protections.required_check_apps`.
- App installation: `/apps/{slug}/installations/new` (account, all or selected repositories, permissions in plain words) redirects to the setup URL with `installation_id` and `setup_action=install`. Settings > Installed Apps (user and organization) changes the repositories, suspends, resumes and uninstalls, and shows the permission upgrades waiting for approval; until accepted, installations keep the permissions they had, while removed permissions stop working at once.
- App authentication on `/api/v3`: App JWT (RS256, `iss` = App id or `client_id`, at most 10 minutes, 60 s skew) on `/app…`; installation tokens `erps_…` (one hour, optionally narrowed with `repositories`, `repository_ids` and `permissions`) that act as the App's bot on the installation's repositories, through `/api/v3` (every route mapped to an App permission, others `403` "Resource not accessible by integration") and git over HTTPS (`x-access-token:<token>`, LFS included), refused by `/api/v1` and GraphQL. User-to-server tokens of an App's OAuth client are limited to the repositories where the App is installed and to the App's permissions. Token creation and every installation change are audited.
- GitHub-compatible App endpoints: `GET /app`, `GET /app/installations`, `GET|DELETE /app/installations/{id}`, `PUT|DELETE /app/installations/{id}/suspended`, `POST /app/installations/{id}/access_tokens`, `GET /repos/{owner}/{repo}/installation`, `/orgs/{org}/installation`, `/users/{username}/installation`, `GET /installation/repositories`, `DELETE /installation/token`, `GET /user/installations`, `GET /user/installations/{id}/repositories` and `GET /apps/{slug}`.
- Check runs created with an installation token carry the App as `app` and only that App can update them; App slugs cannot be used as reporter names by others. Branch protection can pin a required check to an App (`requiredCheckApps` in `/api/v1`).
- App webhooks: the subscribed events of the installed repositories, filtered by the accepted permissions, with the `installation` object and the `X-GitHub-Hook-Installation-Target-Type`/`-ID` headers, plus `installation` (`created`, `deleted`, `suspend`, `unsuspend`, `new_permissions_accepted`) and `installation_repositories` (`added`, `removed`) events; recent deliveries and redelivery on the App page.
- v1 API: `/app-catalog`, `/user/apps`, `/orgs/{org}/apps`, `/apps/{slug}` (+ `/keys`, `/installations`, `/install-targets`, `/deliveries`), `/user/app-installations`, `/orgs/{org}/app-installations`, `/app-installations/{id}` (+ `/suspended`, `/permissions`).

### Changed

- Repository storage controls moved from repository settings to Admin > Storage, inside the new "Hot cache" card (search, pin, unpin, evict).
- `storageState` of the public `Repo` schema is the real state only for instance admins; every other caller gets `hot` (storage is instance infrastructure). `sizeBytes` stays public.
- The hot tier budget of git repositories is the local target budget less the cache shares of every data type cached on it (before: less the git `cache_share` only).

### Fixed

- Git push over HTTPS and SSH failed on every repository ("the remote end hung up unexpectedly"): the server passed `--strict`, which only `git upload-pack` accepts, to `git receive-pack`. A git command that fails before writing anything now answers 500 instead of an empty 200.

## [1.11.0] - 2026-11-16

### Added

- System bot accounts (apps plan A0): users whose namespace kind is `bot` and whose login ends in `[bot]` (a suffix normal names cannot have), with a `bots` table (`description`, nullable `app_id` for the App bots of phase A2). Migration 00290 creates `ervisio-sync[bot]` ("Ervisio Sync") and `ervisio-actions[bot]` ("Ervisio Actions"); `ervisio-sync` and `ervisio-actions` become reserved names. Bots cannot sign in (password, SSO, passkeys and sessions all refuse them), do not count as users for the first-admin rule and the instance counts, are left out of user search, own no repositories and cannot be collaborators. Admin > Users lists them with a "bot" badge, read-only (changes answer 409).
- The GitHub sync writes as `ervisio-sync[bot]` instead of an empty actor: activity, notifications and webhook senders show the bot. Synced pushes still start workflows only with "Run Actions on synced pushes", as before.
- Workflow job tokens act as `ervisio-actions[bot]`: they can now write on their own repository through `/api/v3` (issues, comments, pull requests, releases, statuses, check runs, contents and refs, with `contents: write`), and their pushes, comments, releases and statuses are attributed to the bot. Events of the bot never start workflows (loop protection unchanged).
- Bots in the APIs: REST v3 and webhook user objects have `"type": "Bot"`, GraphQL returns `Bot` (implements `Actor`), v1 `UserProfile` has `kind` (`user` | `bot`) and `description`, `AdminUser` has `isBot`. Bot commits use `<id>+<login>@users.noreply.<host>`, mapped back to the bot in contributor lists.
- UI: "bot" badge next to bot names in the activity feed, issue and pull request timelines and comments, workflow run actors, Admin > Users and the profile page, which shows the bot description instead of repositories. Old activity rows without an actor still show "ErvisioSync".
- OAuth apps (apps plan A1): users and organization owners register OAuth apps under Settings > Developer settings > OAuth Apps (organizations: settings tab "OAuth Apps"): name, description, homepage, optional logo URL, up to 10 callback URLs and "Enable device flow". The `client_id` is generated (`Ov23li…`); up to five client secrets are shown once, stored hashed, and list who created them and when they were last used. Editing, revoking secrets and deleting an app (which revokes every grant and token) are audited. Migration 00300 adds `oauth_apps`, `oauth_app_secrets`, `oauth_grants`, `oauth_codes` and `tokens.grant_id`; the built-in clients `eg` and `gh` become system rows of `oauth_apps` with unchanged behaviour.
- OAuth web application flow with GitHub's wire format: `GET /login/oauth/authorize` (SPA consent page; `client_id`, `redirect_uri` with GitHub's matching rules, `scope`, `state`, PKCE `S256`), skipped when the same or narrower scopes are already granted; single-use 10-minute codes; `POST /login/oauth/access_token` now also takes `code` with `client_secret` (form or HTTP Basic) or a PKCE `code_verifier`, answering `bad_verification_code`, `incorrect_client_credentials` or `redirect_uri_mismatch` like GitHub. Registered apps with the device flow enabled can use `/login/device/code`.
- Tokens issued to registered apps belong to the user's grant (at most ten live per app and user) and are listed under Settings > Applications with scopes and last use, not among the personal access tokens; "Revoke" deletes the grant and all its tokens.
- GitHub-compatible `/api/v3/applications/{client_id}/token` (`POST` check, `PATCH` reset, `DELETE`) and `DELETE /api/v3/applications/{client_id}/grant`, authenticated with the client id and secret.
- Instance setting `security.app_registration` (`everyone`, `org_owners`, `admins`) decides who may register OAuth apps (and the Apps of phase A2); exposed as `appRegistration` in `/api/v1/meta`.
- v1 API: `/user/oauth-apps`, `/orgs/{org}/oauth-apps`, `/oauth-apps/{clientId}` (+ `/secrets`), `/user/oauth-authorizations`, `/user/oauth/authorize`.

## [1.10.1] - 2026-11-16

### Fixed

- Web editor: committing a change to an existing branch failed with "invalid branch: reserved name" (the page sent the full ref `refs/heads/…`); the API now also accepts the full name.

## [1.10.0] - 2026-11-16

### Added

- Issue lock and delete over the API: `PUT`/`DELETE /repos/{owner}/{repo}/issues/{n}/lock` (`lock_reason`), GraphQL `lockLockable`, `unlockLockable` and `deleteIssue`, and a v1 `DELETE /repos/{owner}/{name}/issues/{number}` for repository admins (audited, issues only). The issue page has a "Delete issue" menu item with a confirmation dialog.
- Releases in the GitHub-compatible API: `POST /repos/{owner}/{repo}/releases/generate-notes` ("What's Changed" with pull request titles, authors and links, plus a "Full Changelog" line), `generate_release_notes` on create, and `make_latest` (`true`, `false`, `legacy`) on create and update; `GET .../releases/latest` honours it (new `releases.make_latest` column, migration 00280).
- Pull request merge: a repository admin can merge past the review and status check requirements of a branch protection rule that has "admins bypass" set; auto-merge never bypasses. `PUT .../pulls/{n}/merge` already took `merge_method`, `commit_title`, `commit_message` and `sha`.
- GraphQL `CheckRun.isRequired` and `StatusContext.isRequired` (`pullRequestNumber` argument), true for the required status checks of the base branch protection rule.
- Repository stars: `PUT`/`DELETE /repos/{owner}/{name}/star`, `GET /repos/{owner}/{name}/stargazers` and `GET /users/{username}/starred` (only repositories the caller can read). Every `Repo` now carries `starsCount` and `starred` (false when signed out), and explore accepts `sort=stars`. The GitHub-compatible API fills `stargazers_count`/`watchers_count` and serves `PUT`/`DELETE`/`GET /user/starred/{owner}/{repo}`, `GET /user/starred`, `GET /users/{username}/starred` and `GET /repos/{owner}/{repo}/stargazers` (with `application/vnd.github.star+json` support).
- Repository header on every repository page: owner avatar, owner / name, visibility, and Pin, Watch (notification level), Fork and Star (with the star count) buttons; signed out they lead to sign-in. On narrow screens the labels hide and icons and counts stay.
- "Code" drop-down on the repository home: HTTPS / SSH / CLI clone URL with copy, Download ZIP and Download tar.gz. It replaces the Clone dialog and the archive entries of the ⋯ menu. The About sidebar shows the star count.
- Profile README: the README of the public repository `username/username` appears above the repositories on the profile, with an edit pencil for the owner; owners without one get a card that creates the repository and its README.md.
- Avatar upload: `PUT /user/avatar` takes a PNG, JPEG or GIF (up to 2 MiB, 4096x4096), crops it to a centred square, scales it to 460x460 and stores a metadata-free PNG under `avatars/<sha256>.png`; `DELETE /user/avatar` clears the avatar. Images are served publicly and immutably at `/avatars/<sha256>.png`. The profile settings show a preview with Upload and Remove; the external URL field stays as an alternative.
- Avatar storage in Admin > Storage (`GET`/`PUT /admin/storage/avatars`): local storage or any running target (S3, SFTP), and an optional public base URL for a CDN. Applies at once to new uploads; existing avatars are not moved and stay reachable from any running target.
- OAuth device flow for command line clients (RFC 8628, GitHub compatible): `POST /login/device/code` and `POST /login/oauth/access_token` (form or JSON, JSON or form-encoded answers, GitHub error codes with `slow_down` backoff), an approval page at `/login/device` (`?user_code=` pre-fills the code) backed by `GET`/`POST /user/device/{userCode}`. Built-in clients: `eg` (`Ov23liErvisioEgCli01`) and the GitHub CLI. The device gets a personal access token named `eg on <host> (device)` with the requested GitHub scopes mapped to token scopes (default `repo read:org workflow`); expired codes are purged by the hourly identity cleanup. See docs/api-v3.md.
- GitHub sync: "Run Actions on synced pushes" (`runActions` on the sync link, off by default, mirror and sync modes). Commits arriving from GitHub start the workflows like a push, as `ervisio-sync[bot]`; pushes of workflow tokens and of Ervisio to GitHub still start nothing. See docs/github-sync.md.
- Import of GitHub Actions: `POST /repos/{owner}/{name}/actions/github/import` and the "Import from GitHub" card in the repository secrets and variables settings read the repository and environment variables (existing ones kept unless asked), the secret names (listed as "secrets to fill in" until set) and the latest 100 workflow runs. The repository import has a new `actions` kind doing the same. A missing permission is reported, not fatal.
- GitHub run history: imported runs are stored read-only (`actions_external_runs`, at most 500 per repository), refreshed after every sync run, and listed in the Actions run list with a GitHub badge linking to their logs on GitHub.
- GitHub-compatible API (`/api/v3`) for the `eg` CLI: forks (`GET`/`POST /repos/{owner}/{repo}/forks`, answering the existing fork on a repeat), `POST /repos/{owner}/{repo}/merge-upstream` (fast-forward only, `409` when the branch diverged), `fork`, `forks_count`, `parent` and `source` on repository objects (also `isFork`, `parent` and `forkCount` in GraphQL), `auto_init`, `gitignore_template` and `license_template` on repository creation, `GET /gitignore/templates[/{name}]` and `GET /licenses[/{license}]`, and `GET /search/repositories`, `/search/issues`, `/search/code` (`repo:` required) and `/search/users`. See docs/api-v3.md.
- Repository creation with a first commit: `autoInit`, `gitignoreTemplate` and `licenseTemplate` on `POST /user/repos` and `/orgs/{org}/repos` write one "Initial commit" with README.md, a .gitignore and a LICENSE from embedded templates (gitignore: C, Go, Java, macOS, Node, Python, Rust, Windows; licenses: MIT, Apache-2.0, GPL-3.0, BSD-3-Clause, MPL-2.0, Unlicense). The new repository dialog has an "Add a README file" box.
- `POST /repos/{owner}/{name}/merge-upstream`: fast-forwards a fork branch to its parent.
- GitHub-compatible Actions API (`/api/v3`) for `eg run`, `eg workflow`, `eg secret` and `eg variable` (and `gh`): workflows (list, get, `dispatches`), runs (filters `branch`, `event`, `status`, `actor`, `head_sha`, `created`; attempts, jobs with steps, `rerun`, `cancel`), run logs as a zip in GitHub's layout and job logs as text, artifacts (list, get, zip download through a signed URL, delete), repository secrets with `GET .../actions/secrets/public-key` and libsodium sealed-box uploads (key derived from the master key), and variables. Runs imported from GitHub are listed with `"external": true` and their GitHub `html_url`. Not available: workflow enable/disable, `rerun-failed-jobs`, run deletion.

### Fixed

- `/api/v3/`: the token settings link is `current_user_authorizations_html_url` and points at `/settings?section=tokens` (it was `/settings/tokens`, which the web UI does not serve).
- SFTP: a failed directory creation names the remote directory, and the form explains that a relative remote folder starts from the login directory (an absolute one such as `/erepo` fails on a Hetzner Storage Box, whose writable space is `/home`).
- Avatars set to an external https URL load: the Content-Security-Policy allowed only same-origin images.
- Repository tabs: Code no longer stays highlighted on Issues, Pull requests or Actions.
- Contributors in the repository sidebar link to their profile.
- An avatar URL that does not load shows the initials instead of a broken image.
- Home activity: events with no user behind them (GitHub sync, imports) show "ErvisioSync" with a bot badge instead of "Someone".
- Actions: workflows of repositories imported or synced from GitHub are listed. The workflow list came only from past runs, and the import (quiet) and the sync (no actor) never start one, so the list stayed empty; it now reads the workflow files of the default branch too.

## [1.9.0] - 2026-11-16

### Added

- Admin > Storage: **Fetch from the server** fills the known hosts of an SFTP target with the host keys the server offers (ed25519, ECDSA, RSA) and shows their SHA256 fingerprints, so no `ssh-keyscan` is needed. New endpoint `POST /admin/storage/host-keys`; it connects without signing in and refuses the same addresses as storage targets.

### Fixed

- Admin > Storage: a failed connection test now marks the wrong fields (for example an upper-case name or missing known hosts) instead of showing only "invalid storage configuration".

## [1.8.0] - 2026-11-16

### Added

- SFTP storage targets can sign in with a password instead of a private key. In Admin > Storage the form has a **Sign-in method** choice (private key or password); the password is sealed with the master key like the other credentials, and saving one method replaces the credential of the other. In `config.toml` an `sftp` target accepts `password_file`, alone or together with `key_file`. The server key is still always verified against the known hosts.

## [1.7.0] - 2026-11-16

### Added

- Public user profiles at `/{username}` (also reachable signed out) with the user's repositories, and a "Your profile" entry in the user menu. New endpoint `GET /users/{username}` (`UserProfile`: username, display name, avatar, join date); unknown, organization and suspended names answer 404.

### Changed

- Repository lists (organization, my repositories, profile, Explore) follow GitHub: filter bar with find, type and sort, and rows with visibility, description, last update and size. The organization page gets a GitHub-style header and a side column with people and teams.
- Settings pages (account, repository, organization, instance admin) follow GitHub's layout: grouped side menu with an accent bar, open sections titled on a rule, stacked fields, bordered lists and tables, and a danger zone in a red-bordered box.

### Fixed

- Public repositories can be browsed without signing in: code, history, branches, tags, compare, code search, releases, issues, pull requests and Actions runs no longer redirect anonymous visitors to the login page. A missing or private repository asks them to sign in instead. Write pages (edit, new file, new issue, release form, settings) still require a session.

## [1.6.0] - 2026-11-16

### Added

- External storage from the web admin: Admin > Storage can add, edit and delete S3-compatible and SFTP targets and change replicas, cache and (while a policy is empty) the primary of each policy. Credentials are typed in the form, sealed in the database with the master key and never returned by the API (only `hasSecretKey`-style flags). A connection test works on an unsaved form. Changes apply after a restart; targets of `config.toml` stay read-only. New endpoints: `GET /admin/storage/config`, `PUT` and `DELETE /admin/storage/targets/{name}`, `PUT /admin/storage/policies/{policy}`, `POST /admin/storage/test`. Safeguards: the primary of a policy with objects cannot change; a target that holds objects cannot be deleted and keeps its location locked (credentials can still be rotated); changing an address requires typing the credentials again; the server refuses link-local, cloud metadata, unspecified and multicast addresses; saves are one transaction with an advisory lock; at start objects are counted and a stored configuration that would hide them is not applied (the page shows the cause). CLI: `ervisio-repo storage overlay show` and `clear [--force]` for recovery.
- Repository home sidebar: language breakdown of the current ref, computed from
  file sizes (`GET /repos/{owner}/{name}/languages`). Vendored, generated and
  documentation files are left out.

## [1.5.0] - 2026-11-15

### Added

- Web: in the Console layout the repository home has a sidebar on the right with About (description, visibility, default branch, link to the README), Releases (the latest one and a link to the others) and Contributors (avatars and count). The description moves there, and the repository home uses a wider 1216 px column. Below 1000 px the sidebar sits under the README.
- API: `GET /repos/{owner}/{name}/contributors` lists the authors of a ref by number of commits, over the newest 50,000 commits. It never returns e-mail addresses; an author is linked to an account only through a verified address.

### Changed

- Release: the Docker image and the native packages are built for amd64 only for now, which removes the emulated arm64 build from the release. `install.sh` stops with a clear message on arm64. The image workflow can also be started by hand for an existing tag.

## [1.4.0] - 2026-11-15

### Changed

- Web: the default theme now uses GitHub colours, light and dark, with visible borders on inputs, cards and menus and tighter corners (6, 8 and 12 px). System, Light and Dark use this palette. The previous palettes stay available under Settings, Appearance as OLED (dark) and Soft (light). Accounts keep the theme they had saved: Light and Dark switch to the new colours, and you pick OLED or Soft to get the old ones back.
- Web: Home has two columns. Your repositories sit on the left with a filter, pinned ones first and a New button. On the right, Waiting for you groups assigned issues, review requests and failed runs in tabs, and recent activity is listed by day. The instance health card is gone: administrators see one warning line, linked to Administration, only when storage reports warnings or no runner is online.
- Web: in the GitHub palette, selected items in side menus use the blue accent in every section, comment headers are plain grey, settings pages share the width of the other repository pages and failed jobs have a red border. Comment counts and the diff Comment button use a speech-bubble icon. The run page no longer shows the layout switch.
- Web: in the Console layout the repository pages drop the tall header. The Private or Public label sits next to the repository name in the top bar and the description shows only on the repository home. Files are a table with fixed columns under the latest commit, the README has its own box and reads as a document, and the file view keeps Code, Blame, Raw and History in the toolbar with Edit, Download and Delete in a ⋯ menu. Repository pages use a 1060 px column.
- Web: the Actions run page lists the jobs in a column on the left and shows the steps of the selected job on the right. Each step opens on its own log, and the failed or running step starts open. The summary card becomes one line under the title, log timestamps are hidden until you choose Show timestamps, each step log has a search field and lines marked as errors are highlighted. The job graph, the compact graph and the time-bar view are gone.
- Web: issue and pull request pages have two columns. The conversation is on the left. On the right a Status box holds the state and the main action, with assignees, labels, milestone, reviewers and notifications under it, each with one edit button. For a pull request the Status box lists what blocks the merge and contains the merge button, auto-merge and Update branch; the merge box at the end of the conversation is gone. Edit and delete on a comment, and Lock conversation, moved into ⋯ menus. The new issue form uses the same two columns. On small screens Status comes first and the details open from one row.
- Web: settings and administration pages have a compact one-line title, a left menu with the same style everywhere and sections in bordered boxes with one row per setting. The danger zone has a red border and no fill. Administration no longer repeats its sections in the top bar, and the settings search shows only on menus with 13 or more items. These pages share the 1060 px column of the repository pages.
- API: `theme` in the user preferences accepts `oled` and `soft` (migration 00240).

## [1.3.0] - 2026-11-11

### Changed

- Web: the Console layout drops the acrylic rail and channel column for a single top bar. The first row holds the logo, the owner / repository switcher, search, notifications, New and the account menu; the second row holds the tabs of the current context. In a repository you see Code, Issues, Pull requests and Actions, and More opens Commits, Branches, Tags, Releases, Search and Settings. Pages sit in a centred 920 px column on a flat background, body text is set at 1.65 line height and code keeps its own tighter spacing.
- Web: in the Console layout the repository home shows only the repository name and description. The bar under it has the branch switcher, Clone and a ⋯ menu with History, Add file, the source archives, Pin, Fork and Settings. Files sit in a borderless list and the README reads as a page of its own. Foglio and Corsie are unchanged.

## [1.2.0] - 2026-11-06

### Changed

- Web: the Console layout has a new acrylic look. A rail of rounded tiles (home, you and your organizations, New, Explore; notifications, Administration, account) sits next to a 248 px channel column that follows the context: global navigation with pinned and recent repositories, repository sections grouped as Code, Collaboration, Automation and Manage, organization tabs or the administration pages, with the account card at the bottom. The page sits on a glass panel over slowly moving accent shapes (still with reduced motion); text weights are lighter. Below 720 px a bottom bar (Home, Explore, Notifications, Menu) replaces rail and column, which opens as a drawer. Foglio and Corsie are unchanged.

## [1.1.0] - 2026-11-06

### Changed

- Web: the Console shell now has a 264 px sidebar with an owner / repository switcher, grouped navigation (global, repository or organization), recent repositories and an admin-only Administration block. A top bar adds a search field (repository scope, `/` shortcut), a New menu (repository, organization), the notifications count and a user menu, which now holds Sign out. Below 900 px the sidebar opens as a drawer.
- Web: Home is a dashboard (needs your attention, activity, recent repositories, instance health for admins). The instance details moved to the new `/admin` overview. New `/orgs` page; `/_ui` is no longer linked from the navigation.
- Web: account, repository and organization settings share one layout: grouped sub-navigation with search, two-column rows with a changed marker, a sticky save bar that warns before leaving with unsaved changes, and a danger zone. Organization webhooks, secrets, variables and runners moved under Settings.
- Web: in the Console layout the repository pages no longer repeat their tab bar; Commits, Branches, Tags and Search joined the Repository group in the sidebar.

### Added

- Web: instance administration area at `/admin`, sharing the settings layout with a sub-navigation: Overview, Users (search, role and suspended filters, paging; a detail page to change the role, suspend or restore, and reset two-factor, each behind a confirmation with a clear message when the server refuses the change to yourself or the last admin), Invitations (create a single-use link shown once, list with status, revoke; replaces the old invite form on the overview), Instance settings in eight sections (Authentication, Registration, Email, Git & SSH, Actions, Webhooks, Appearance, Server) with a source badge, locked keys, per-key errors and a restart-required notice, and the existing Storage, Runners and Audit pages.
- Web: Home reads the dashboard endpoints: Needs your attention (assigned issues, review requests, failed runs, in tabs), the Activity feed and pinned repositories first in Recent repositories. New public `/explore` page (search, sort, paging; works signed out) with an Explore entry in the navigation. A pin toggle sits on the repository header and on repository list rows (a toast explains the 12-pin limit). Account settings gain an Appearance section (theme and layout); the effective layout is the user preference, then the instance default from `/meta`, then Console, and the theme is applied from the server. localStorage only caches the last values to avoid a flash on load.
- API: dashboard endpoints. `GET /user/issues` (assigned, created, mentioned or review-requested issues and pull requests), `GET /user/actions/runs` (recent runs, filter by status or conclusion), `GET /user/activity` (pushes, issues and pull requests opened, closed or merged, releases and comments), `GET /explore/repos` (public repositories, anonymous allowed, sort by updated or name, search), `GET /user/pins`, `PUT` and `DELETE /user/pins/{owner}/{name}` (at most 12 ordered pins) and `GET` and `PATCH /user/preferences` (theme and layout). All of them only cover repositories the caller can read. Migration 00230 adds `activity_events`, `user_pinned_repos`, `user_preferences` and the `repo_readable` SQL function.
- API: instance admin endpoints. `GET /admin/users` (search, role and suspended filters, paginated), `GET` and `PATCH /admin/users/{login}` (role, suspend; suspending signs the user out everywhere and revokes their tokens; an admin cannot demote or suspend themselves or the last active admin, 409), `DELETE /admin/users/{login}/2fa` (audited), `GET /admin/invites` (without tokens) and `DELETE /admin/invites/{id}`.
- API: `GET` and `PUT /admin/settings`. Instance settings edited from the web are stored in the database (migration 00220, `instance_settings`) and override `config.toml` at the next start; each key reports its value, the file value, the source, whether it is locked and whether a restart is pending. Keys listed in `[settings] locked` or set by an `EREPO_` variable cannot be overridden. An invalid stored override is skipped with a warning and never blocks the start.
- Config: `ui.default_layout` (`console`, `foglio` or `corsie`), exposed as `defaultLayout` in `GET /meta`.
- Plugin API: settings now show the effective values with the web overrides applied; saving a key from the plugin writes `config.toml` and removes its web override.
- Plugin: the Access page gains a Web interface card with the default layout (`ui.default_layout`).
- Web: repository Collaborators page, label and milestone editing and deletion, team editing and team members, organization deletion, a Fork button on repository pages, single-use sign-up invitations on the admin overview, and a Storage page (state, size, pin, unpin, evict) for instance admins in repository settings.

### Fixed

- Plugin: the settings pages crashed with "Cannot use 'in' operator … in undefined" against a server older than 1.0.4, whose `GET /settings` has no `values`; they now say the server must be updated.
- Plugin: the save bar and row hovers were transparent because `--raise` is not a core token; the plugin now defines it.

## [1.0.5] - 2026-11-04

### Fixed

- Plugin: the page failed to load with "React is not ready" because the settings context was created at import time, before `activate()` set React.

## [1.0.4] - 2026-11-04

### Added

- Plugin: new layout with an overview (service, repositories, space, runners, containers, access), a maintenance page (update to the plugin's version, start, stop, restart, configuration check, live logs) and settings pages for access, mail, Git and SSH, Actions, webhooks, logs and address.
- Plugin API: every common setting is editable (`GET`/`PUT /api/plugin/settings` with flat `section.key` values and per-field 422 errors), `POST /api/plugin/config/check`, `POST /api/plugin/mail/test`, `POST /api/plugin/runners/token`, `DELETE /api/plugin/runners/{id}`, and instance counts in `/status`.

### Fixed

- Plugin: a stopped server shows its last log lines and a start button instead of the install wizard; the dashboard widget no longer asks for the admin unlock; client checks match the server (durations with `d`/`w`, sizes with a unit).
- Plugin API: `configWritable` reflects whether the file can actually be written; a config file that does not parse is reported instead of silently showing the running values; settings writes are serialized; `public_url` accepts any http(s) URL.
- Sizes print in the unit that gives the smallest whole number (`2GiB` instead of `2147483648B`).
- `setup.sh` keeps an existing `BIND_ADDRESS` on re-run.

## [1.0.3] - 2026-11-04

### Fixed

- The server no longer exits at startup when Actions is enabled and no `actions` storage policy exists: the actions cache and artifacts are disabled with a warning. The generated and packaged configs now include `[storage.policy.actions]`.
- `setup.sh` and `setup.bat` build the app image from the checkout when the published image cannot be pulled, instead of starting whatever image is already tagged locally.

## [1.0.2] - 2026-11-04

### Fixed

- The Docker image build copies `web/.npmrc`, so `npm ci` in the web stage keeps `legacy-peer-deps` and no longer fails on the `openapi-typescript` peer range. v1.0.1 was tagged but never published because of this.

## [1.0.1] - 2026-11-04

### Added

- The Docker image is published as `ghcr.io/ervisio/ervisio-repo` (linux/amd64 and linux/arm64). `deploy/docker/compose.yaml` uses it, tagged with the release it ships with (`ERVISIO_VERSION`), and `setup.sh`/`setup.bat` pull it before starting; when the pull fails, compose builds the image from the checkout.
- The Ervisio core plugin (`repo`) is released as `repo-<version>.tar.gz` with its `.sha256` next to the server packages, and its version follows the server release.

### Fixed

- `scripts/release/build.sh` renders the nfpm config before packaging, since nfpm does not expand environment variables in `contents` paths.

## [1.0.0] - 2026-11-04

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

Phase 6, Actions (Docker backend):

- Runner protocol: ConnectRPC contract in `proto/` (registration, declare, fetch task, update task and log), served on `/api/actions/runner/*`.
- `ervisio-runner` daemon (`cmd/ervisio-runner`) built on the act library: registration with labels, Docker backend with cgroup limits, log streaming, systemd unit.
- Migration 00200: workflow runs, jobs, steps, logs, secrets, variables, runners and registration tokens. `internal/actions`: workflow parser, triggers and filters, matrix, `needs`, `concurrency`, `environment` approval, reusable workflows, River scheduling, job tokens, sealed secrets (HKDF from the master key), masking, log storage, check runs and commit statuses on PRs. Job tokens also work on `/api/v1` (aliased to the GitHub-compatible API).
- REST (`/api/v1`): runs, jobs, logs (SSE), re-run, re-run failed jobs, cancel, approve, dispatch, workflow badges, repository and organization secrets and variables, runners and registration tokens.
- Migration 00210: cache v2 and artifact v4 results services (Twirp) with blob storage on the tiered store, mounted next to the runner API.
- Web UI: run list, run page with job graph and live logs, runner administration and Actions settings (layout A from `design/wip/003-actions-run/DECISION.md`).
- Documentation: disaster recovery guide in English and Italian (`docs/dr.md`, `docs/dr.it.md`).

Phase 8, Ervisio integration and installation (reduced scope):

- Docker: multi-stage `Dockerfile`, `deploy/docker/compose.yaml`, `setup.sh` and `setup.bat`.
- Standalone: nfpm `.deb` and `.rpm` packages, systemd units, `deploy/install.sh` and signed release tooling (`scripts/release`).
- Plugin admin socket `/api/plugin/*` (per-process secret) to read and write the configuration; the Ervisio core plugin in `plugin/` installs standalone or Docker mode and offers a graphical configuration page.
- Caddy setup in Go (`ervisio-repo caddy setup`) and in the plugin, with the Git parameters of PLAN section 9.4.

Known limitations of Phase 6:

- The official `actions/upload-artifact@v4` and `actions/download-artifact@v4` refuse hosts other than github.com: use a compatible fork such as `christopherhx/gitea-upload-artifact` and its download counterpart.
- The proxy cache for remote actions (`_actions`) is not built (it needs a server-side git mirror).
- Artifact and cache downloads do not support `Range` requests.
- Changing Actions settings requires a server restart (no live reload).
- The UI has no per-environment secrets.

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
