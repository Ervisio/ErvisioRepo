# Disaster recovery guide

Rebuild an ErvisioRepo instance on a fresh host after losing the old one. Command reference and background: [backup.md](backup.md). Install details: [install.md](install.md). Italian version: [dr.it.md](dr.it.md).

## Scope, RPO and RTO

In scope: total loss of the host, and partial losses (database, storage, one repository, keys). Out of scope: multi-node failover; there is a single control node and recovery is from remote storage.

These are planning assumptions, not guarantees. Measure your own with the drill below.

| Item | Assumption |
| --- | --- |
| RPO, git data | Close to zero. The journal on the remote targets is always current. |
| RPO, database | Up to 24 h with the daily `pg_dump`; minutes with wal-g archiving (optional, see backup.md). |
| RTO | Dominated by provisioning the host and restoring the dump. Repositories are marked cold and hydrate on first access, so size does not delay start-up. |

Anything created between the last dump and the failure (users, tokens, PRs) is lost unless you use wal-g. Git pushes survive: `restore` re-indexes journal objects newer than the dump.

## What to back up

| What | Where it lives | How it is protected |
| --- | --- | --- |
| Git journal (storage class `git`) | Remote targets (S3, SFTP, WebDAV) | Replicas of the `git` policy. Hot copies in `storage.hot_path` are only a cache. |
| PostgreSQL | `database.url` | Daily `pg_dump -Fc` into the `backup` policy (needs a storage policy named `backup`), optional wal-g. |
| Configuration | `/etc/ervisio-repo/config.toml` (Docker: `deploy/docker/config/config.toml`) | Recovery kit. |
| Master key | `security.master_key_file` (default `/etc/ervisio-repo/master.key`) | Recovery kit. Never overwritten once created. |
| SSH host keys | `ssh.host_key_dir` (only when `ssh.enabled`) | Recovery kit. |
| Target credentials | Files named by `credentials_file`, `key_file`, `known_hosts_file`, `password_file` in `config.toml` | NOT in the kit (TODO: confirm; the kit holds the config file, the master key and the host keys only). Back them up separately. |
| OIDC client secrets, SMTP password | Files named by `client_secret_file`, `password_file` | Same as above: back up separately. |
| Docker `.env` | `deploy/docker/.env` (holds `POSTGRES_PASSWORD`) | Not in the kit. Back it up, or set a new password on the new host. |
| Runner config | `/etc/ervisio-repo/runner.toml` | Not in the kit; re-register runners if lost. |

Secrets and keys: there is no separate "seal key" setting. Credentials sealed in the database (GitHub connections, 2FA secrets) are encrypted with keys derived from the master key, and encrypted targets (`encrypt = true`) also depend on it. Without the master key those values and the encrypted objects cannot be read. Actions secrets are sealed the same way (HKDF-SHA256 from `security.master_key_file`), so a lost master key also loses every repository and organization secret.

Export the kit after setup and after every change to the config, and keep it offline, away from the backups:

```sh
ervisio-repo recovery-kit export --out kit.erk --config /etc/ervisio-repo/config.toml
```

The passphrase is read from stdin, or from `--passphrase-file`; minimum 12 characters.

## Restore on a fresh host

Prerequisites: the kit and its passphrase, the target credential files, network access to the remote targets, `pg_dump` and `pg_restore` matching the PostgreSQL major version, and an empty PostgreSQL database (`database.url`).

### Packaged install (deb/rpm)

```sh
# 1. Install the same or a newer version
curl -fsSL https://raw.githubusercontent.com/Ervisio/ErvisioRepo/main/deploy/install.sh | sudo sh
# 2. Put the original config, master key and host keys back
sudo ervisio-repo recovery-kit import --in kit.erk
#    --root <dir> installs under another directory; --force overwrites different files
# 3. Restore the target credential files referenced by config.toml, then check each target
sudo runuser -u ervisio-repo -- ervisio-repo storage test <target> --config /etc/ervisio-repo/config.toml
# 4. Restore the database from the latest dump and reconcile repositories
sudo runuser -u ervisio-repo -- ervisio-repo restore --config /etc/ervisio-repo/config.toml
#    a specific dump: --dump <key>;  database restored by wal-g: --skip-db
# 5. Optional: warm the busiest repositories
sudo runuser -u ervisio-repo -- ervisio-repo prewarm --all --config /etc/ervisio-repo/config.toml
# 6. Start
sudo systemctl enable --now ervisio-repo
```

The package creates a new `master.key` at install; the kit import in step 2 must put the original one back. Without `--force`, a differing existing file makes the import fail. Use `--force` only after confirming the new key was never used. Check ownership afterwards (`master.key` is `0640 root:ervisio-repo`). TODO: confirm that `recovery-kit import` restores owner and mode on the packaged layout.

Run `restore` as the `ervisio-repo` user so files it writes into the hot path have the right owner.

### Docker Compose

```sh
cd deploy/docker
# 1. Put the original files in place before the first start
mkdir -p config
cp /path/to/restored/config.toml config/config.toml     # from the kit
cp /path/to/restored/master.key  config/master.key
# 2. Start only Postgres
docker compose up -d postgres
# 3. Restore database and reconcile repositories inside the app image
docker compose run --rm --no-deps app restore --config /etc/ervisio-repo/config.toml
# 4. Start everything
docker compose up -d --wait
```

Alternative to copying by hand: run `ervisio-repo recovery-kit import --in kit.erk --root ./restored` on any machine with the binary, then copy `restored/` content into `deploy/docker/config/`.

TODO: verify the image entrypoint passes `restore` through (the entrypoint is `tini`; `command` is `serve`), the `database.url` the compose Postgres expects (`.env` `POSTGRES_PASSWORD` must match it), and that running `setup.sh` first does not generate a different `master.key` (it keeps an existing one).

## Verification checklist

- [ ] `ervisio-repo version` shows the expected build; `curl -fsS http://127.0.0.1:3400/healthz` succeeds.
- [ ] `ervisio-repo migrate status --config ...` shows no pending migrations.
- [ ] `restore` printed no `degraded:` lines. Each one names a repository whose manifest is missing or invalid; investigate before use.
- [ ] No unexpected `orphan journal` lines (journal data with no repository row; the repository was created after the dump).
- [ ] `ervisio-repo storage test <target>` passes for every target.
- [ ] Log in as admin; 2FA and an OIDC login (if configured) work. If not, the master key is wrong.
- [ ] Clone a repository over HTTPS and over SSH. The SSH host key fingerprint must match the old one, otherwise clients warn.
- [ ] Push a commit to a repository, then clone it again.
- [ ] Open a repository that was cold: it hydrates on first access. With `storage.paranoid = true` it is also `git fsck`ed.
- [ ] Registered runners reconnect (TODO: confirm runners survive a restore or must be re-registered).
- [ ] Set `server.public_url` and DNS/Caddy (`ervisio-repo caddy status`) to the new host.
- [ ] Export a fresh recovery kit from the restored instance.

## Partial failures

**Lost database, storage intact.** Install the config and master key (kit import), then `ervisio-repo restore`. You lose changes since the last dump. With wal-g: restore the base backup and WAL as described in backup.md, then `ervisio-repo restore --skip-db`.

**No dump available.** `restore` fails with "no dump in the backup class". Journal manifests alone cannot rebuild users, permissions or repository names. TODO: no supported way to rebuild the database from the journal alone; keep dumps on at least one replica.

**Lost storage target, database intact.** If a replica survives, point the policy `primary` at it, add a new empty target as replica, and run `ervisio-repo storage test <new>`. TODO: confirm there is a command or job that re-replicates existing objects to a new target; today assume only new writes reach it. If all copies of the journal are lost, the repositories exist only on the hot copy: back them up from `storage.hot_path` immediately.

**Lost local disk only.** Hot copies are a cache. Start the server; repositories hydrate from the journal on access. `ervisio-repo prewarm <owner>/<repo>` or `--all` does it ahead of time.

**Corrupted repository.** Mark it for re-hydration from the journal rather than repairing in place. TODO: no CLI command to evict one repository; confirm the admin API or UI action. With `storage.paranoid = true` a repository failing `git fsck` is refused at hydration, which limits the spread.

**Lost master key.** Without the key, encrypted targets and sealed credentials are unrecoverable. If you have the kit, import it. If you have neither, everything stored in `encrypt = true` targets is lost; unencrypted targets and the database remain readable, but GitHub connection credentials and 2FA secrets must be re-entered or reset.

**Lost SSH host keys.** Not fatal: the server generates new ones at first start, but every client sees a host key change warning. Restore from the kit to avoid it.

**Lost signing keys.** The release signing key (used by `scripts/release/sign`, public half embedded in `deploy/install.sh`) is a maintainer secret, not part of an instance. If lost, generate a new pair with the sign tool's `genkey`, publish the new public key in `install.sh`, and re-sign the releases. Commit-signing keys of users are stored per user in the UI, not on the server host. TODO: confirm no server-side signing key exists beyond the master key.

## Restore drill

Run it every quarter and after any change of storage layout.

1. Build a throw-away host (VM or a separate Compose project with different `BIND_ADDRESS`, `APP_PORT` and `SSH_PORT`). Never point it at production targets for writing: use a read-only credential, or restore a copy of the bucket.
2. Follow "Restore on a fresh host" using only the kit, the passphrase and the credential files you keep offline. If a step needs something else, add it to this guide.
3. Run the verification checklist and record the elapsed time as your measured RTO, and the age of the dump as your measured RPO.
4. Compare: repository count and a few `git rev-parse` heads per branch against production.
5. Destroy the host, and rotate any credential that was copied around.

Do not start the drill instance with the production `server.public_url` or webhook targets live; it would send webhooks and mail as production.
