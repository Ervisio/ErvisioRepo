# Backup and disaster recovery

ErvisioRepo keeps three things you need to rebuild an instance:

1. **The git journal** (storage class `git`): packs, indexes, ref snapshots and
   manifests on the remote targets. It is always current; hot copies on local
   disk are a cache.
2. **The PostgreSQL database**: users, permissions, repository metadata.
3. **The recovery kit**: configuration file, master key and SSH host keys.

## Database: daily pg_dump

When a storage policy named `backup` exists, the server runs `pg_dump -Fc`
every 24 hours and stores the dump under `backup/postgres/<UTC time>.dump`,
with the replicas of that policy. Dumps older than its `retention` are
deleted; the last 3 are always kept. `pg_dump` and `pg_restore` must be
installed on the server, matching the PostgreSQL major version.

## Database: continuous archiving with wal-g (optional)

For a recovery point of minutes instead of a day, archive the WAL with
[wal-g](https://github.com/wal-g/wal-g) on the PostgreSQL host:

```ini
# postgresql.conf
wal_level = replica
archive_mode = on
archive_command = 'wal-g wal-push %p'
archive_timeout = 60
```

Configure the wal-g bucket with its `WALG_*` environment variables, take a
base backup daily (`wal-g backup-push $PGDATA`) and prune with
`wal-g delete retain FULL 7 --confirm`. To restore, `wal-g backup-fetch` plus a
`restore_command = 'wal-g wal-fetch %f %p'`, then run
`ervisio-repo restore --skip-db` to reconcile the repositories.

## Recovery kit

```sh
ervisio-repo recovery-kit export --out kit.erk --config /etc/ervisio-repo/config.toml
```

The kit is encrypted with your passphrase (at least 12 characters; argon2id
and AES-256-GCM). Keep it offline, away from the backups: without the master
key, encrypted targets cannot be read.

## Restoring on a new machine

```sh
ervisio-repo recovery-kit import --in kit.erk          # config, master key, host keys
ervisio-repo restore --config /etc/ervisio-repo/config.toml
ervisio-repo prewarm --all                             # optional
ervisio-repo serve
```

`restore` loads the latest dump (or `--dump <key>`), migrates the schema,
marks every repository cold, takes each `manifest_seq` from the remote
manifest (which may be newer than the dump), re-indexes the journal objects
the dump does not know and marks degraded the repositories whose manifest is
missing or invalid. Manifests with no repository row are listed as orphans.
Repositories are hydrated on first access, or ahead of time with `prewarm`.
