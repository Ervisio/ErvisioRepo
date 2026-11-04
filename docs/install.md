# Installing Ervisio Repo

Two supported ways: a native Linux package, or Docker Compose. Italian version: [install.it.md](install.it.md).

The first account you register becomes the instance administrator. Both installs default to invite-only registration, which does not block that first account.

## Native (Debian, Ubuntu, RHEL family)

Requirements: systemd, `git`, and PostgreSQL 14 or later (local or remote).

```sh
curl -fsSL https://raw.githubusercontent.com/Ervisio/ErvisioRepo/main/deploy/install.sh | sudo sh
# a specific release:  ... | sudo sh -s -- --version 1.2.3
```

The script detects the distribution and architecture (amd64, arm64), downloads the `.deb` or `.rpm`, checks `SHA256SUMS` and its ed25519 signature (needs openssl 3), and installs it. You can also install a package from the releases page by hand.

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

Optional profiles:

```sh
docker compose --profile runner up -d    # CI runner, mounts /var/run/docker.sock
docker compose --profile backup up -d    # Postgres WAL archiving sidecar (see docs/backup.md)
```

The runner can start containers on the host through the Docker socket: enable it only on machines you trust with that access.

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
