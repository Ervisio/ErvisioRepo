# Plugin admin API contract (phase "Base solida", 2026-11-04)

Socket API in `internal/pluginapi`, consumed by `plugin/src/api/repo.ts`. All JSON, camelCase envelope.
Errors: `application/problem+json` `{title, status, detail}`; validation errors (422) add
`"errors": [{"path": "git.max_push_size", "msg": "must be at least 1MiB"}]` (from `config.FieldError`).

## GET /api/plugin/status
```json
{ "version": "1.0.3", "commit": "abc", "startedAt": "2026-11-04T10:00:00Z",
  "publicUrl": "...", "upstream": {...}, "sshEnabled": true, "sshListen": ":2222",
  "configPath": "/etc/ervisio-repo/config.toml", "configWritable": true,
  "configError": "",               // non-empty when the file on disk does not parse
  "restartRequired": false, "actionsEnabled": true, "mailEnabled": false, "storageConfigured": true,
  "counts": { "repos": 42, "users": 17, "orgs": 3 } }
```
`configWritable` is a real check (open the file for writing / dir writable), not `File != nil`.
`restartRequired` is also true when the file on disk differs from the running config for any editable key.

## GET /api/plugin/settings
Flat keys `section.key` from the whitelist below.
```json
{ "values":   { "git.max_push_size": "2GB", "ssh.enabled": true, "webhooks.allowed_cidrs": ["10.0.0.0/8"] },
  "defaults": { ...same keys, values of config.Default() },
  "configPath": "...", "configWritable": true, "configError": "", "restartRequired": false }
```
`values` come from the file on disk. If the file does not parse: 200 with `configError` set and
`values` from the running config (so the UI can show the error banner, never a silent fallback).
Sizes and durations are strings in canonical form (`Size.String`, `Duration.String`); ints are numbers;
bools are booleans; lists are string arrays.

## PUT /api/plugin/settings
Body `{ "values": { "section.key": value, ... } }` (only changed keys). Unknown/non-whitelisted key → 422 with
that path. Writes all keys, re-parses with `config.Parse`, 422 with per-field `errors` on failure (nothing written),
409 when the file is read-only. Serialized with a mutex. Answers the same document as GET.

Whitelist:
- server: public_url, ssh_listen, trusted_proxies (list)
- log: level, format
- security: registration, user_directory
- auth: password_min_length, session_idle_timeout, session_max_age, require_verified_email,
  login_free_failures_account, login_free_failures_ip, login_backoff_base, login_backoff_max,
  actions_per_ip_per_hour, require_2fa, token_max_ttl, invite_ttl, verify_token_ttl, reset_token_ttl
- mail: enabled, host, port, tls, username, password_file, from, helo_name, timeout
- git: max_push_size, idle_timeout, max_duration, requests_per_hour
- ssh: enabled, max_connections, login_grace_time
- actions: enabled, default_actions_url, max_job_timeout, runner_offline_after, registration_token_ttl,
  max_jobs_per_run, cache_max_repo_size, cache_ttl, artifact_retention
- webhooks: allow_private, allowed_cidrs (list), timeout, max_attempts

`server.public_url`: any absolute http(s) URL (the old https-or-loopback rule is dropped).
`PUT /api/plugin/public-url` stays for the Address page.

## POST /api/plugin/config/check
Parses the file on disk. `{ "ok": true, "errors": [] }` or `{ "ok": false, "errors": [{path,msg}] }`.

## POST /api/plugin/mail/test
Body `{ "to": "a@b.c" }`. Sends a test email with the mail settings **from the file on disk** (so the user can
test before restarting). 200 `{ "sent": true }`; 422 when mail is disabled/invalid; 502 with the SMTP error as detail.

## Runners
- `GET /api/plugin/runners` (unchanged shape).
- `POST /api/plugin/runners/token` → `{ "token": "...", "expiresAt": "...", "command": "ervisio-runner register --url <public_url> --token <token>" }`
  (instance-scope registration token, same path the web admin uses). Token never logged.
- `DELETE /api/plugin/runners/{id}` → 204.

## Done in the plugin, not the API
- Restart/stop/start: Docker (`containers/{id}/restart|stop|start`, compose service `app`) or native
  (`systemctl restart|stop|start ervisio-repo.service` commands).
- Logs: Docker `containers/{id}/logs` via `httpStream` (follow) ; native `journalctl -u ervisio-repo -n 300 -o cat` polled.
- Update: available when `sdk.plugin.version` > `status.version`. Docker: command `update-docker {version}` in a
  systemd-run unit `ervisio-repo-update`: back up config.toml next to itself with a timestamp, set
  `ERVISIO_VERSION=<v>` in `/opt/ervisio-repo/deploy/docker/.env`, `docker compose pull app`, `docker compose up -d`.
  Native: `install-native {version}`.
- Server down: status unreachable but an install exists (compose `app` container present, or
  `ervisio-repo.service` LoadState=loaded) → down banner with last log lines and Start, never the install wizard.
