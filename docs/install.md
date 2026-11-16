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

### Recovery

- `ervisio-repo storage overlay show [--config PATH]` lists the stored configuration (no credentials) and the objects each policy holds. `ervisio-repo storage overlay clear [--config PATH] [--force]` resets it, so the next start uses `config.toml` only. `clear` refuses when it would move the primary of a policy that holds objects; `--force` overrides that, only if those objects are also found under the primary of the file.
- **Corrupt stored configuration.** The server refuses to start and names the settings key `storage.overlay`. Do not delete the row by hand: it holds the primary of your policies. Use `storage overlay clear --force` after checking the primaries in `config.toml`.
- **Lost or rotated master key.** The stored credentials cannot be unsealed. The server starts with `config.toml` alone (it refuses only when that would move a primary). Restore the old key, or run `storage overlay clear` and enter the credentials again.
- **Stray objects.** At start the server counts objects per policy and target. If a policy has objects under a target that the stored configuration no longer makes its primary, the stored configuration is not applied.
