#!/bin/sh
# Runs after install and upgrade (deb: configure, rpm: post).
set -eu

LIB=/usr/lib/ervisio-repo
ETC=/etc/ervisio-repo
USER_NAME=ervisio-repo

if ! getent passwd "$USER_NAME" >/dev/null 2>&1; then
  nologin=/usr/sbin/nologin
  [ -x "$nologin" ] || nologin=/sbin/nologin
  useradd --system --home-dir /var/lib/ervisio-repo --no-create-home \
    --shell "$nologin" --user-group "$USER_NAME"
fi

install -d -o "$USER_NAME" -g "$USER_NAME" -m 0750 /var/lib/ervisio-repo /var/lib/ervisio-repo/data
install -d -o root -g "$USER_NAME" -m 0750 "$ETC"

# Newest installed version wins the "current" symlink.
newest=$(ls -1 "$LIB/versions" | sort -V | tail -n 1)
if [ -n "$newest" ]; then
  ln -sfn "$LIB/versions/$newest" "$LIB/current.new"
  mv -T "$LIB/current.new" "$LIB/current"
fi

# Master key: created once, never overwritten.
if [ ! -s "$ETC/master.key" ]; then
  (umask 077; head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' > "$ETC/master.key")
fi
chown "root:$USER_NAME" "$ETC/master.key"
chmod 0640 "$ETC/master.key"

# Best effort: a role and a database on a local PostgreSQL (peer auth).
if command -v psql >/dev/null 2>&1 && command -v runuser >/dev/null 2>&1 \
  && getent passwd postgres >/dev/null 2>&1 \
  && runuser -u postgres -- psql -qtAc 'select 1' >/dev/null 2>&1; then
  pg() { runuser -u postgres -- psql -qtA -c "$1"; }
  if [ "$(pg "select 1 from pg_roles where rolname='$USER_NAME'" || true)" != "1" ]; then
    pg "create role \"$USER_NAME\" login" || true
  fi
  if [ "$(pg "select 1 from pg_database where datname='ervisio_repo'" || true)" != "1" ]; then
    pg "create database ervisio_repo owner \"$USER_NAME\"" || true
  fi
fi

if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
  if systemctl is-active --quiet ervisio-repo.service; then
    systemctl restart ervisio-repo.service || true
  fi
fi

cat <<MSG

Ervisio Repo is installed. Next steps:
  1. Edit $ETC/config.toml (set server.public_url).
  2. runuser -u ervisio-repo -- ervisio-repo migrate up --config $ETC/config.toml
  3. systemctl enable --now ervisio-repo
  4. Open the URL and register: the first account becomes the administrator.
  5. Optional: ervisio-repo caddy setup --host <your-domain>

MSG
