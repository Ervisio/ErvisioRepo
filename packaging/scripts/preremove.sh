#!/bin/sh
# Runs before removal (deb: remove|upgrade, rpm: $1 = 0 on erase, 1 on upgrade).
# Data, config and the master key are never deleted.
set -eu

case "${1:-}" in
  upgrade|1) exit 0 ;;
esac

if [ -d /run/systemd/system ]; then
  for unit in ervisio-runner.service ervisio-repo.service; do
    systemctl disable --now "$unit" >/dev/null 2>&1 || true
  done
fi

rm -f /usr/lib/ervisio-repo/current /usr/lib/ervisio-repo/current.new
