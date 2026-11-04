# plugin
Purpose: the Ervisio core plugin (id "repo") that installs and manages ErvisioRepo from the Ervisio web UI, plus the server-side admin API it talks to over a unix socket.
Entry points: cmd/ervisio-repo/serve.go (pluginapi.New + ListenAndServe on server.plugin_socket, only when set; cfg.File is set by config.Load and passed as the file for settings writes), plugin/src/index.ts, plugin/plugin/manifest.json (permissions, commands, sockets), plugin/src/api/{repo,install,caddy,caddy-core,docker}.ts, plugin/src/views/*.tsx (RepoPage shell, Overview, Maintenance, Pages+SettingRows+fields for settings), plugin/src/api/service.ts (docker/native start/stop/restart/logs/update), plugin/catalog-entry.json, internal/pluginapi/server.go:New, internal/pluginapi/server.go:Server.Handler, internal/pluginapi/handlers.go:Server.apply, internal/pluginapi/tomlpatch.go:setKey
Flow: Ervisio core loads dist/repo (index.js + manifest.json, built by `npm run build` with ervisio-plugin-pack) → plugin calls getSdk().http "repo" → unix socket server.plugin_socket (default /run/ervisio-repo/api.sock, mode 0660) → SocketHandler injects the x-ervisio-plugin secret → Handler routes /api/plugin/{status,settings,storage,runners,runners/token,runners/{id},public-url,config/check,mail/test} (contract: .context/plugin-contract.md; settings whitelist in internal/pluginapi/settings.go) → settings writes patch the TOML via setKey, re-parse with config.Parse, write file, set restartRequired.
Install flow: install.ts runs a transient systemd unit (ervisio-repo-install) that downloads the release and runs deploy/docker/setup.sh or deploy/install.sh; page follows unit state and journal.
Caddy flow: caddy.ts propose → apply: write ervisio-repo.caddy next to the Caddyfile (or an inline marked block when the config dir is not mounted) → `caddy validate` → reload → on failure restore files exactly → only then PUT public-url.
Invariants:
- The plugin API is never on the public listener; without a valid secret every route answers 404 (not 401) like unknown routes. Secret is 32 bytes, regenerated each process start, compared in constant time.
- Socket access control is the file permission; SocketHandler caps bodies at 1 MiB and serves only /api/plugin/.
- Every command the plugin runs must be declared in manifest.json capabilities.commands with arg patterns; adding a call in TS without a manifest entry fails at runtime. Caddy paths are restricted to /etc/caddy/[A-Za-z0-9._-]+.
- setKey keeps comments/CRLF and drops only a trailing comment on the replaced line; values go through tomlString (strconv.Quote) and control characters are rejected.
- Errors are application/problem+json (problem.go); the TS client maps `detail` to RepoError.message.
- caddy-core.ts is pure (no SDK, no I/O) so it can be unit-tested with node; keep it that way.
Gotchas:
- Settings changes only take effect after a server restart; responses carry restartRequired and the UI must say so.
- apply() returns 409 for a read-only config mount  and 501 when no ConfigFile is set; do not treat as generic 500.
- catalog-entry.json format for the Ervisio plugin catalog is unconfirmed; treat it as a best guess when changing fields (minCore, trust, visibleTo).
- plugin/dist and plugin/node_modules are gitignored; the SDK is a git dependency pinned at v0.3.0 (contract version 3).
- plugin-caddy code is adapted under MIT from Ervisio/plugin-caddy; keep plugin/LICENSE-plugin-caddy.
- Upstream for the reverse proxy comes from DefaultUpstream (config + ERVISIO_UPSTREAM_* env), because inside Docker the host cannot reach 127.0.0.1.
Related: map/deploy.md (setup.sh, install.sh, internal/caddy), map/repo-core.md
