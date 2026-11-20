# Installing Ervisio Repo

Two supported ways: a native Linux package, or Docker Compose. Italian version: [install.it.md](install.it.md).

The first account you register becomes the instance administrator. Both installs default to invite-only registration, which does not block that first account.

## Native (Debian, Ubuntu, RHEL family)

Requirements: systemd, `git`, and PostgreSQL 14 or later (local or remote).

```sh
curl -fsSL https://raw.githubusercontent.com/Ervisio/ErvisioRepo/main/deploy/install.sh | sudo sh
# a specific release:  ... | sudo sh -s -- --version 1.2.3
```

The script detects the distribution and architecture (amd64 only for now), downloads the `.deb` or `.rpm`, checks `SHA256SUMS` and its ed25519 signature (needs openssl 3), and installs it. You can also install a package from the releases page by hand.

What the package sets up:

- Binaries in `/usr/lib/ervisio-repo/versions/<version>`, a `current` symlink, and links in `/usr/bin` (`ervisio-repo`, `ervisio-runner`, `erepo`).
- System user `ervisio-repo`, data in `/var/lib/ervisio-repo`, config in `/etc/ervisio-repo/config.toml` (kept on upgrades) and a generated `master.key` next to it. Back the key up: without it stored secrets cannot be recovered.
- Hardened units `ervisio-repo.service` and `ervisio-runner.service` with their own cgroup slices. The runner only starts once `/etc/ervisio-repo/runner.toml` exists.
- With a local PostgreSQL running, a role and a database named `ervisio_repo` are created for the `ervisio-repo` user (peer authentication).

Then:

```sh
sudo nano /etc/ervisio-repo/config.toml        # set server.public_url
sudo runuser -u ervisio-repo -- ervisio-repo migrate up --config /etc/ervisio-repo/config.toml
sudo systemctl enable --now ervisio-repo
```

Open the public URL and register. Removing the package keeps data, config and the master key.

## Docker Compose

Requirements: Docker Engine 24+ (or Docker Desktop) with the Compose plugin.

```sh
cd deploy/docker
./setup.sh                      # Windows: setup.bat
./setup.sh --non-interactive --url https://git.example.com --admin-email me@example.com --ervisio no
```

`setup.sh` writes `.env`, `config/config.toml` and `config/master.key`, builds the image and starts the stack `ervisiorepo` (`postgres` and `app`). Re-running it keeps existing secrets and config. `--ervisio yes` enables the admin socket used by the Caddy plugin, shared at `${ERVISIO_SOCKET_DIR}` (mounted at `/run/ervisio-repo`).

The app listens on `127.0.0.1:3400` (web and API) and `127.0.0.1:2222` (SSH); change `BIND_ADDRESS`, `APP_PORT` and `SSH_PORT` in `.env`. Two networks exist: `private` (Postgres, internal) and `ervisiorepo_edge`, which only the `app` joins (alias `ervisiorepo-app`) so a Caddy container can reach it.

`setup.sh` also asks whether to start the built-in Actions runner (`--runner yes|no`, default yes); see [Runners](#runners). Optional profile:

```sh
docker compose --profile backup up -d    # Postgres WAL archiving sidecar (see docs/backup.md)
```

## Runners

Runners execute Actions jobs. They are provided by the instance operator, like GitHub-hosted runners: runners registered in **Admin > Runners** form a shared pool that takes the jobs of every repository, so users only write `runs-on: ubuntu-latest` and need no setup. Organization and repository runners (their own Settings > Actions pages) are an optional extra for people who want dedicated machines.

Every new runner gets these labels: `ubuntu-latest`, `ubuntu-24.04`, `ubuntu-22.04`, `self-hosted`, `linux` and `x64` or `arm64`, all running jobs in Docker containers from the images in `deploy/runner-images` (`ghcr.io/catthehacker/ubuntu:act-*`). Extra labels (`--labels gpu,big`) join the defaults with the same image; `name=docker://image` maps a label to another image.

**Load sharing.** Runners only dial out and long-poll the server. A job goes to the first runner that asks for it and has every `runs-on` label; a runner asks only while it has a free slot, so a busy runner never holds jobs back from idle ones. `capacity` (jobs at the same time) defaults to half the CPUs, at least 1; set it with `--capacity N` at install or `runner.capacity` in the config. Several machines with the same labels share the queue: add a runner on the server and others on separate hosts to spread the load.

### Built-in runner on the server

- **Docker Compose**: `./setup.sh` (or `./setup.sh --runner yes`) writes `COMPOSE_PROFILES=runner` to `.env`, starts `app`, mints an instance registration token with `ervisio-repo actions runner-token` inside the `app` container and stores it in `data/runner/registration-token` (mode 0600). The `runner` container (`ervisio-runner daemon`) registers itself as `builtin` on first start, deletes the token file and keeps its credential and generated `config.toml` in `data/runner`. Tokens expire after `actions.registration_token_ttl` (1h), so no long-lived shared secret exists. `RUNNER_LABELS` and `RUNNER_CAPACITY` in `.env` set extra labels and capacity before the first start.
- **Native packages**: `sudo ervisio-repo actions runner-setup` registers the runner `builtin` against the local listener, writes `/etc/ervisio-repo/runner.toml` and enables `ervisio-runner.service` (needs Docker and the `docker` group). Options: `--labels`, `--capacity`, `--url`, `--no-start`.

Admin > Runners shows the built-in runner status at the top. To print a token for any scope from the server shell: `ervisio-repo actions runner-token --scope instance|org/<owner>|repo/<owner>/<name>`.

Job containers clone the repository from the server's `public_url`: it must be reachable from the runner hosts (not `localhost`).

### Add a runner on another Linux server

On the new server (Debian, Ubuntu, Fedora, RHEL, Rocky or Alma, amd64 or arm64), run one command:

```sh
curl -fsSL https://git.example.com/runner/install.sh | sudo sh
```

1. The script detects the distribution and, if Docker is missing, installs Docker Engine from the official repository (`download.docker.com`, apt or dnf) and enables it. `--no-docker` skips Docker and runs jobs on the host (see Host mode).
2. It downloads `ervisio-runner` of the server's version from the GitHub release and checks its SHA-256.
3. It prints a code and a link: open `https://git.example.com/runner/approve` (signed in) and enter the code, or follow the direct link `…/runner/approve?code=ABCD-EFGH`. The page shows the machine (host name, OS, architecture, CPUs, memory, Docker, runner version, request IP) and lets you change the name, labels and capacity and pick the scope: **Instance** (admins), an organization (its owners) or a repository (its admins). Approve or Deny. Nothing has to be typed on the server: the script waits without a terminal too, and the code expires after 15 minutes.
4. On approval the script receives a single-use registration token (never a user token), registers the runner, writes `/etc/ervisio-runner/config.toml` (capacity defaults to half the CPUs, at least 1; labels `ubuntu-latest`, `ubuntu-24.04`, `ubuntu-22.04`, `self-hosted`, `linux`, the architecture), creates the system user `ervisio-runner` in the `docker` group and enables `ervisio-runner.service`.
5. It installs `ervisio-runner-prune.timer` (daily, randomized by up to 1h, catches up after downtime): it removes Docker images, containers, networks and build cache unused for 7 days, unused anonymous volumes, runner caches and workspaces untouched for 14 days, and every unused image when the Docker data root has less than 15% free. Images of running containers are never removed. Logs: `journalctl -u ervisio-runner-prune`. Not installed with `--no-docker`.
6. It prints a summary: name, labels, capacity, `systemctl status ervisio-runner` and the uninstall command.

Options go after `sh -s --`: `--name`, `--labels gpu,big`, `--capacity N`, `--no-docker`, `--version X.Y.Z`, `--token errt_…` (skips the approval, for automation), `--url` (defaults to the server that served the script), `--yes`. Re-running the script on an installed machine asks whether to register again; without a terminal (or with `--upgrade`) it only replaces the binary; `--reconfigure` registers again.

Device codes are rate-limited per IP (20 per hour) and wrong codes on the approval page back off; approvals and denials are in the audit log.

### Other platforms and non-interactive installs

**New runner** in Admin > Runners (or the org/repository Actions settings) shows the one-liner above. Under **Advanced / non-interactive** it creates a registration token (valid 1h) and shows token commands:

- **Linux** (systemd): `curl -fsSL https://git.example.com/runner/install.sh | sudo sh -s -- --token errt_…`. Without `sudo` it installs into `~/.local/bin` with a systemd user unit (`loginctl enable-linger` keeps it running after logout) and does not install Docker.
- **macOS** (launchd): `curl -fsSL https://git.example.com/runner/install.sh | sh` (device approval) or with `-s -- --token errt_…`; it installs `~/.local/bin/ervisio-runner` and the LaunchAgent `dev.ervisio.runner` (logs in `~/Library/Application Support/ervisio-runner/logs`). Jobs need Docker Desktop running.
- **Windows**: in an elevated PowerShell, `irm https://git.example.com/runner/install.ps1 | iex` (device approval) or `& ([scriptblock]::Create((irm https://git.example.com/runner/install.ps1))) -Token errt_…` installs `%ProgramFiles%\ErvisioRunner\ervisio-runner.exe`, keeps config and state in `%ProgramData%\ervisio-runner` (SYSTEM and Administrators only) and registers the scheduled task `ErvisioRunner` (at boot, as SYSTEM, restarted on failure). Docker Desktop with Linux containers is not installed automatically.
- **Docker**: a `docker run … --entrypoint ervisio-runner ghcr.io/ervisio/ervisio-repo:<version> daemon …` command; the runner registers from `ERVISIO_RUNNER_URL`/`ERVISIO_RUNNER_TOKEN` (also `_NAME`, `_LABELS`, `_CAPACITY`) and keeps its state in the `ervisio-runner` volume.

The scripts are served at `/runner/install.sh` and `/runner/install.ps1` with the server URL and version embedded, and download `ervisio-runner_<ver>_<os>_<arch>.tar.gz` (`.zip` on Windows) checked against `ervisio-runner_<ver>_checksums.txt`.

**Status, pause and load.** The runner lists show status (online, idle, busy, paused, offline), running jobs / capacity (reported by the runner), labels, version and last contact. **Pause** lets a runner finish its running jobs without taking new ones (for maintenance); **Resume** brings it back. Capacity is set at install (`--capacity`) or in `runner.capacity` of the config (restart the service).

**Host mode.** `--no-docker` (or `--host`; `-HostMode` on Windows) runs jobs directly on the machine as the runner user, with labels `self-hosted`, the OS and the architecture: no isolation, so use it only for trusted repositories.

**Security.** The `ervisio-runner` user is in the `docker` group, which is equivalent to root on that host: use dedicated machines. Fork pull requests from outside contributors wait for approval and never get secrets.

**Uninstall.** `curl -fsSL https://git.example.com/runner/install.sh | sudo sh -s -- --uninstall` stops and removes the service, the cleanup timer, the user, the binary, config and state, and removes the runner from the server (`ervisio-runner deregister`); if the server cannot be reached, delete it in Admin > Runners. `-Uninstall` on Windows; `docker rm -f ervisio-runner` for Docker (then delete it in the UI).

### On-demand runners (machines switched off while idle)

An expensive machine (for example an EC2 instance) can stay off and start only when a job needs it.

1. Make something that starts the machine on an HTTPS call protected by a token, e.g. an AWS Lambda with a function URL that calls `ec2:StartInstances` and checks a header such as `x-token`.
2. Install the runner on the machine with `--idle-shutdown 15m`: after 15 minutes without jobs it stops taking work, lets running jobs finish and powers the machine off (a root unit, `ervisio-runner-poweroff.path`, does the power-off; the runner user stays unprivileged). `--idle-shutdown 0` turns it off again.
3. In Admin > Runners open the ⋯ menu of the runner, **On-demand start…**: start URL, token header (`x-token`; empty sends `Authorization: Bearer`), token (stored encrypted with the master key) and cooldown (default 180 s).

While the runner is offline (no contact for 75 s) and a queued job matches its labels, the server POSTs to the start URL, at most once per cooldown; if an online runner could take the job, it waits 90 s first. A refused call (4xx such as 401/403, e.g. blocked hours) is retried after 10 minutes, other failures (409 while the machine is still stopping, network, 5xx) after a minute. **Start now** in the same menu calls the URL at once. The runner row shows an **On demand** chip and the outcome of the last call. Only instance runners can be on demand.

**Cross-compiling for Windows and macOS.** Docker on Linux runs only Linux containers. `deploy/runner-images/cross/Dockerfile` adds mingw-w64, zig, clang, NSIS, osslsigncode and wine to the job image: build it on the runner machine (`docker build -t ervisio/runner-cross:24.04 deploy/runner-images/cross`) and add a label such as `"highpriority-cross=docker://ervisio/runner-cross:24.04"` to `runner.labels`. It produces Windows and macOS binaries, but cannot sign or notarize for Apple or test on the real systems.

## Publishing behind Caddy

```sh
sudo ervisio-repo caddy setup --host git.example.com
ervisio-repo caddy status
ervisio-repo caddy remove
```

`caddy` looks for a Caddy installed as a system service or running in a Docker container, writes a managed block (`ervisio-repo.caddy` next to the Caddyfile, imported from it) with large uploads, no timeouts on Git streams and compression that skips Git content types, validates it, and reloads Caddy. If validation or reload fails, the previous configuration is restored. Useful flags:

- `--dry-run`: print the change without writing.
- `--instance system|<container>`: choose one when several Caddy instances run.
- `--upstream` (default `127.0.0.1:3400`), `--container-upstream` (default `ervisiorepo-app:3400`), `--network` (default `ervisiorepo_edge`): how Caddy reaches the server. A Caddy container is joined to the edge network automatically.

When no usable Caddy is found, the command prints the snippet to paste yourself. Afterwards set `server.public_url` to the new `https://` address and restart the service.

## External storage from the web admin

Besides `[[storage.target]]` in `config.toml`, an instance admin can add S3-compatible and SFTP targets in **Admin > Storage** and change where each data type (`git`, `lfs`, `actions`, ...) is stored.

- **Credentials.** The S3 access and secret keys, the SFTP private key (with its optional passphrase) or password, and the known hosts lines are typed in the form and sealed in the database with the master key (`security.master_key_file`). They are never shown again: the page only says whether each one is stored, and a blank field keeps the stored value. Without a master key credentials cannot be saved. Do not lose the key: stored credentials cannot be read without it. An SFTP target signs in with a private key or a password (**Sign-in method**); saving one replaces the other.
- **Test connection** works on the form before saving, with the same write, read, list and delete round trip as `ervisio-repo storage test`.
- **Policies.** Replicas and cache can be changed at any time. The primary can change only while the policy holds no objects, because objects are not moved between targets. A cache must be a local target.
- **Restart.** Changes apply the next time the server starts; the page says so until then.
- **Limits.** Targets from `config.toml` are read-only here and their names cannot be reused. Name, type and encryption of a target cannot change after creation. A target cannot be deleted while a policy uses it or while objects are stored on it. While objects are stored on a target, its location (endpoint, region, bucket, prefix, host, port, user, root) is locked; only the credentials can be replaced. When the endpoint or host changes, the credentials (and known hosts) must be typed again. `local` and WebDAV targets cannot be created here. Connections made from the server to the targets refuse link-local, cloud metadata (`169.254.0.0/16`, `fd00:ec2::/32`), unspecified and multicast addresses; private addresses are allowed. `ervisio-repo storage test` only knows the targets of the file.
- **Replicas added later.** A new replica receives new objects. Objects stored before are copied only when the periodic verification finds them missing there, so a full copy of old data is slow. A target removed from a policy is simply ignored; its objects stay where they are.
- **If the stored configuration cannot be applied at start** (for example the master key changed), the server logs a warning and starts with `config.toml` alone, unless it moves a primary: then it refuses to start, because objects would become unfindable.
- **Status.** When the stored configuration was not applied, Admin > Storage shows why. A target deleted while running stays listed as "Removed after restart".

### Hot cache

Storage is instance infrastructure, managed only by admins. When a data type keeps its data on a remote target (S3, SFTP or WebDAV), the server keeps what is in use on the local disk automatically, without a cache to configure per data type:

- **What stays local.** Everything read or written recently. Git repositories are cloned to the local disk on first access (fetch, clone, push, browsing in the web UI) and stay there while used. LFS objects, release assets, registry blobs and Actions artifacts and caches are copied to the local disk when written or first read. Writes are acknowledged only once they are safe on the remote primary (and, for git with `durability = "sync"`, in its remote journal): the local copy is never the only one.
- **When it moves to remote.** After a period without reads or writes, the local copy is removed and the data is served from the remote storage again, then copied back at the next access. Defaults: 14 days for repositories (bringing a cold repository back costs a full download of it), 7 days for LFS objects, release assets and registry blobs, 3 days for Actions data (artifacts are downloaded right after the run, caches are read again by every run that uses them). The last use is recorded at most once a minute per repository or object, so reads cost no extra writes.
- **Disk budget.** With a `budget` on the local target, about 35% of it is reserved to cached files (LFS 15%, registry 10%, releases 5%, Actions 5%, only for the data types that use the hot cache) and the rest to repositories. Above `high_watermark` (default 90%) the least used data moves to remote until `low_watermark` (default 75%). Pinned repositories never move.
- **Setting.** **Admin > Storage > Hot cache** turns it on or off (default on) and sets one "move to remote after N days of inactivity" for every data type (empty: the defaults above). A data type can use its own value, or turn its cache off, in its policy dialog (**Cache**: Automatic, No cache or a local target). The same in `config.toml`: `[storage.hot_cache] enabled = true`, `ttl = "10d"`; per data type `cache = "none"` or `cache_ttl = "30d"` in `[storage.policy.<type>]`. Changes apply after a restart.
- **Visibility.** The Hot cache card shows the disk used by repositories and cached files against the budget, the repositories on the local disk with their size, last use and when they move to remote, and looks up where a repository is stored (search or paste its URL). **Size limit** caps the local disk used by repositories and cached files (default 20 GB, `max_size`); above it the least recently used leave the disk. Repositories larger than **Do not cache repositories larger than** (default 2 GB, `large_repo`), or all repositories when the cache is off, are fetched for each use and removed right after. Users who are not admins always see repositories as available: where the data lives is not shown to them.
- **Local-only setups.** When a data type's primary is a local target nothing changes: there is no remote copy to fall back on, so nothing is removed. Avatars are not cached (they are small and served with long-lived HTTP caching).

### Avatars

Users upload an avatar in **Settings > Profile** (PNG, JPEG or GIF up to 2 MiB; the server crops it to a square, scales it to 460x460 and stores a PNG without metadata). Images are content-addressed (`avatars/<sha256>.png`), so their URLs never change.

- **Where.** In **Admin > Storage > Avatars** choose the local storage (the first `local` target, the default) or any running target (S3 or SFTP). The change applies at once to new uploads; old avatars are not moved, and the server keeps serving them from whichever running target holds them.
- **Serving.** `GET /avatars/<sha256>.png` on this server, public, with `Cache-Control: public, max-age=31536000, immutable`.
- **CDN.** Set **Public base URL** to have new avatar URLs point at `<base>/avatars/<sha256>.png`. The CDN can use this server as origin (path `/avatars/`) or the bucket of the S3 target directly (objects under `avatars/`, after the target prefix; this does not work with an encrypted target). Avatar URLs are absolute on `server.public_url` otherwise.

### Recovery

- `ervisio-repo storage overlay show [--config PATH]` lists the stored configuration (no credentials) and the objects each policy holds. `ervisio-repo storage overlay clear [--config PATH] [--force]` resets it, so the next start uses `config.toml` only. `clear` refuses when it would move the primary of a policy that holds objects; `--force` overrides that, only if those objects are also found under the primary of the file.
- **Corrupt stored configuration.** The server refuses to start and names the settings key `storage.overlay`. Do not delete the row by hand: it holds the primary of your policies. Use `storage overlay clear --force` after checking the primaries in `config.toml`.
- **Lost or rotated master key.** The stored credentials cannot be unsealed. The server starts with `config.toml` alone (it refuses only when that would move a primary). Restore the old key, or run `storage overlay clear` and enter the credentials again.
- **Stray objects.** At start the server counts objects per policy and target. If a policy has objects under a target that the stored configuration no longer makes its primary, the stored configuration is not applied.
