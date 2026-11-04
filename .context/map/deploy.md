# deploy
Purpose: how ErvisioRepo is built, packaged, signed, installed (Docker or native deb/rpm) and published behind Caddy.
Entry points: Dockerfile, deploy/docker/compose.yaml, deploy/docker/setup.sh, deploy/docker/setup.bat, deploy/install.sh, packaging/nfpm.yaml, packaging/scripts/postinstall.sh, packaging/systemd/*.service, scripts/release/build.sh, scripts/release/sign/main.go, internal/caddy/apply.go:Apply, internal/caddy/instance.go:Detect, cmd/ervisio-repo/caddy.go:caddyCmd, Makefile (docker, package, release)
Flow (release): make web → scripts/release/build.sh (cross-build ervisio-repo, ervisio-runner, erepo per arch; tar.gz + nfpm deb/rpm with stable names ervisio-repo_<ver>_linux_<arch>.<ext>) → dist/SHA256SUMS → sign tool writes SHA256SUMS.sig (raw ed25519, PEM keys) → GitHub release.
Flow (native install): install.sh resolves version → downloads package + SHA256SUMS + .sig → openssl pkeyutl -rawin verify with embedded PUBKEY → checksum → apt/dnf install → postinstall.sh creates user, dirs, master.key, best-effort local PG role and flips /usr/lib/ervisio-repo/current symlink to the newest versions/<ver>.
Flow (Docker): setup.sh generates master.key and POSTGRES_PASSWORD (safe to re-run, keeps secrets), writes config and .env, starts compose (app, postgres, optional profiles: runner, walg).
Flow (Caddy): `ervisio-repo caddy setup|status|remove` → caddy.Detect (system caddy.service or Docker containers) → Apply: write ervisio-repo.caddy + import line (or inline marked block) → validate → reload → rollback files on failure.
Invariants:
- One Alpine image holds server, runner and CLI; runs as uid 1001, entrypoint tini, healthcheck on /healthz :3400.
- Binaries live in versions/<ver>/ and /usr/bin/* are symlinks to current/; upgrades switch the symlink, so units must ExecStart from current/.
- The runner service/slice are separate from the server's (resource isolation); keep both pairs in nfpm.
- master.key is created once and never overwritten (0640 root:ervisio-repo native, 600 in Docker).
- Caddy changes are transactional: read, write, validate, reload; on failure files are restored exactly and Caddy reloads. All commands go through internal/sys, never a shell.
- Release signature must stay verifiable with plain openssl 3 (raw ed25519 over SHA256SUMS), the sign tool format matches that.
Gotchas:
- deploy/install.sh PUBKEY is still the placeholder REPLACE_WITH_RELEASE_PUBLIC_KEY: signature check fails until the first release key is generated (sign genkey) and pasted in; ERVISIO_SKIP_VERIFY=1 bypasses it.
- make package passes VERSION from `git describe --dirty`; on a dirty tree the version contains "-dirty" and package/file names stop matching install.sh, so pass VERSION=x.y.z explicitly.
- make release only checks RELEASE_SIGNING_KEY is set; build.sh signs only when that var is non-empty.
- compose "walg" service is a placeholder (entrypoint prints a hint and sleeps); do not rely on it for backups.
- setup.bat still sets ERVISIO_SOCKET_DIR=./run (setup.sh switches to /run/ervisio-repo when the Ervisio plugin is enabled), so the Windows flow differs for the admin socket.
- build.sh writes stub scripts for missing optional binaries so nfpm does not fail; a stub exits 1 by design.
- Dockerfile and Makefile build ervisio-runner/erepo only if cmd/<name> has Go files (skips silently).
Related: map/plugin.md (drives setup.sh/install.sh, uses the Caddy flow in TS), map/runner.md, map/repo-core.md
