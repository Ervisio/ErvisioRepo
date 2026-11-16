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

### Remote runners

In Admin > Runners click **New runner**: the dialog creates a registration token (valid 1h) and shows the command for each platform with the server URL, the token and the optional name, extra labels and capacity.

- **Linux** (systemd): `curl -fsSL https://git.example.com/runner/install.sh | sudo sh -s -- --url https://git.example.com --token errt_…` installs `/usr/local/bin/ervisio-runner`, creates the user `ervisio-runner` (added to the `docker` group after you confirm, or with `--docker`), writes `/etc/ervisio-runner/config.toml`, registers and starts `ervisio-runner.service`. Without `sudo` it installs into `~/.local/bin` with a systemd user unit (`loginctl enable-linger` keeps it running after logout).
- **macOS** (launchd): the same command without `sudo` installs `~/.local/bin/ervisio-runner` and the LaunchAgent `dev.ervisio.runner` (logs in `~/Library/Application Support/ervisio-runner/logs`). Jobs need Docker Desktop running.
- **Windows**: in an elevated PowerShell, `& ([scriptblock]::Create((irm https://git.example.com/runner/install.ps1))) -Url https://git.example.com -Token errt_…` installs `%ProgramFiles%\ErvisioRunner\ervisio-runner.exe`, keeps config and state in `%ProgramData%\ervisio-runner` (SYSTEM and Administrators only) and registers the scheduled task `ErvisioRunner` (at boot, as SYSTEM, restarted on failure). A scheduled task is used because the runner is not a native Windows service and needs no extra tools. Jobs need Docker Desktop with Linux containers.
- **Docker**: the dialog prints a `docker run … --entrypoint ervisio-runner ghcr.io/ervisio/ervisio-repo:<version> daemon …` command; the runner registers from `ERVISIO_RUNNER_URL`/`ERVISIO_RUNNER_TOKEN` (also `_NAME`, `_LABELS`, `_CAPACITY`) and keeps its state in the `ervisio-runner` volume.

The scripts are served by the server at `/runner/install.sh` and `/runner/install.ps1` and download the runner of the server's version from the GitHub release (`ervisio-runner_<ver>_<os>_<arch>.tar.gz`, `.zip` on Windows), checking it against `ervisio-runner_<ver>_checksums.txt`. `--version X.Y.Z` picks another release.

**Host mode.** Without Docker the installers stop. `--host` (`-HostMode` on Windows) runs jobs directly on the machine as the runner user, with labels `self-hosted`, the OS and the architecture: no isolation, so use it only for trusted repositories.

**Security.** A runner with the Docker socket can start containers on its host, which is equivalent to root there: use dedicated machines. Fork pull requests from outside contributors wait for approval and never get secrets.

**Uninstall.** `curl -fsSL https://git.example.com/runner/install.sh | sudo sh -s -- --uninstall` (add `--purge` to delete config and credential), `-Uninstall [-Purge]` on Windows, `docker rm -f ervisio-runner` for Docker. Then delete the runner in Admin > Runners.

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
