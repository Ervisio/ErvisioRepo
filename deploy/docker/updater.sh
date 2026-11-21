#!/bin/sh
# Ervisio Repo updater: entrypoint of the optional "updater" compose service.
# It is the only part of the stack, besides the runner, with the Docker
# socket; the server never gets it. The server writes
# /run/ervisio-repo/updater/request.json ({"version":"x.y.z",...}) when an
# admin starts an update (or an automatic update is due); this loop takes it,
# runs update.sh and reports in status.json and update.log, touching "alive"
# every few seconds so the admin page knows it is running.
set -u

# update.sh may replace this file: run from a private copy.
if [ "${ERVISIO_UPDATER_COPY:-}" != 1 ]; then
  cp "$0" /tmp/ervisio-updater.sh
  ERVISIO_UPDATER_COPY=1 exec sh /tmp/ervisio-updater.sh "$@"
fi

DIR="${ERVISIO_DEPLOY_DIR:-}"
[ -n "$DIR" ] && [ -f "$DIR/compose.yaml" ] || {
  echo "ERVISIO_DEPLOY_DIR must be the host folder of compose.yaml, mounted at the same path: re-run setup.sh" >&2
  sleep 60
  exit 1
}
Q=/run/ervisio-repo/updater
mkdir -p "$Q"
# The server (ERVISIO_UID) writes requests; status files are world-readable.
chown "${ERVISIO_UID:-1001}:${ERVISIO_GID:-1001}" "$Q"
chmod 770 "$Q"
command -v curl >/dev/null 2>&1 || apk add --no-cache curl >/dev/null 2>&1 || true

now() { date -u +%Y-%m-%dT%H:%M:%SZ; }

status() { # status STATE VERSION FROM STARTED FINISHED
  tmp="$Q/.status.$$"
  printf '{"state":"%s","version":"%s","from":"%s","startedAt":"%s","finishedAt":"%s"}\n' "$1" "$2" "$3" "$4" "$5" > "$tmp"
  chmod 644 "$tmp"
  mv -f "$tmp" "$Q/status.json"
}

field() { sed -n "s/.*\"$1\":\"\\([^\"]*\\)\".*/\\1/p" "$Q/status.json" 2>/dev/null | head -n1; }

# An update cut short by a restart of this container is reported as failed.
if [ "$(field state)" = running ]; then
  status failed "$(field version)" "$(field from)" "$(field startedAt)" "$(now)"
  echo "==> interrupted: the updater restarted during the update" >> "$Q/update.log"
fi
[ -f "$Q/status.json" ] || status idle "" "" "" ""

echo "Ervisio Repo updater ready (deploy folder $DIR)"
while :; do
  touch "$Q/alive"
  if [ -f "$Q/request.json" ]; then
    req=$(cat "$Q/request.json")
    rm -f "$Q/request.json"
    ver=$(printf '%s' "$req" | sed -n 's/.*"version":"\([0-9]\{1,6\}\.[0-9]\{1,6\}\.[0-9]\{1,6\}\)".*/\1/p')
    if [ -z "$ver" ]; then
      echo "ignored an invalid update request"
    else
      from=$(sed -n 's/^ERVISIO_VERSION=//p' "$DIR/.env" | tail -n1)
      started=$(now)
      echo "updating to $ver"
      : > "$Q/update.log"
      chmod 644 "$Q/update.log"
      status running "$ver" "$from" "$started" ""
      ( while :; do touch "$Q/alive"; sleep 10; done ) &
      beat=$!
      ERVISIO_IN_UPDATER=1 sh "$DIR/update.sh" --dir "$DIR" --version "$ver" --yes >> "$Q/update.log" 2>&1
      rc=$?
      kill "$beat" 2>/dev/null
      if [ "$rc" -eq 0 ]; then
        status done "$ver" "$from" "$started" "$(now)"
        echo "update to $ver done"
      else
        status failed "$ver" "$from" "$started" "$(now)"
        echo "update to $ver failed (exit $rc), see $Q/update.log"
      fi
    fi
  fi
  sleep 5
done
