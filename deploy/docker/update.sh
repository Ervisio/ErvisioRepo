#!/bin/sh
# Ervisio Repo Docker update: moves an existing installation to a new
# release without touching its secrets or settings.
#
#   curl -fsSL https://raw.githubusercontent.com/Ervisio/ErvisioRepo/main/deploy/docker/update.sh | sh
#   curl -fsSL .../update.sh | sh -s -- --dir /opt/ervisio-repo/deploy/docker --version 1.23.0
#
# Steps (each prints a "==> step" marker, read by Admin > Updates):
#   backup  copies .env, compose.yaml and config/config.toml to backups/<time>/
#           and dumps the database (skip with --no-db-backup)
#   fetch   downloads compose.yaml and the scripts of the new release (not in
#           a git checkout: there run `git pull` yourself)
#   pull    sets ERVISIO_VERSION in .env (every other key is kept) and pulls
#   up      recreates the containers and waits until they are healthy
#   done
# On a failure after the backup, .env and compose.yaml are restored and the
# previous containers started again. config/master.key is never touched.
set -eu

REPO="${ERVISIO_REPO:-Ervisio/ErvisioRepo}"
dir=""
version=""
assume_yes=0
db_backup=1
force=0

usage() {
  cat <<USAGE
Usage: update.sh [--dir DIR] [--version X.Y.Z] [--yes] [--no-db-backup] [--force]

  --dir DIR        folder with compose.yaml and .env (default: found automatically)
  --version X.Y.Z  release to install (default: the latest release)
  --yes            do not ask for confirmation
  --no-db-backup   skip the database dump
  --force          also reinstall the running version or go back to an older one
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --dir) dir="${2:?--dir needs a value}"; shift ;;
    --version) version="${2:?--version needs a value}"; shift ;;
    --yes|-y) assume_yes=1 ;;
    --no-db-backup) db_backup=0 ;;
    --force) force=1 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

fetch() { # fetch URL FILE
  if command -v curl >/dev/null 2>&1; then curl -fsSL --retry 3 -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then wget -q -O "$2" "$1"
  else die "curl or wget is required"; fi
}

valid_version() { printf '%s' "$1" | grep -Eq '^[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6}$'; }

# older A B: true when version A < B.
older() {
  [ "$1" = "$2" ] && return 1
  first=$(printf '%s\n%s\n' "$1" "$2" | sort -t. -k1,1n -k2,2n -k3,3n | head -n1)
  [ "$first" = "$1" ]
}

command -v docker >/dev/null 2>&1 || die "docker is not installed"
docker compose version >/dev/null 2>&1 || die "the docker compose plugin is required"

# ---- where the installation lives
looks_installed() { [ -f "$1/compose.yaml" ] && [ -f "$1/.env" ]; }
if [ -z "$dir" ]; then
  if looks_installed "."; then dir=$(pwd)
  elif looks_installed /opt/ervisio-repo/deploy/docker; then dir=/opt/ervisio-repo/deploy/docker
  else
    cfg=$(docker compose ls --all --filter name=ervisiorepo --format json 2>/dev/null |
      sed -n 's/.*"ConfigFiles":"\([^",]*\)[",].*/\1/p' | head -n1)
    [ -n "$cfg" ] && dir=$(dirname "$cfg")
  fi
fi
[ -n "$dir" ] || die "cannot find the installation: pass --dir (the folder with compose.yaml and .env)"
cd "$dir" || die "cannot enter $dir"
dir=$(pwd)
looks_installed . || die "$dir has no compose.yaml and .env: run setup.sh first"
[ -s config/master.key ] || die "$dir/config/master.key is missing: refusing to update"
[ -f config/config.toml ] || die "$dir/config/config.toml is missing: refusing to update"

# One update at a time (the web updater and a terminal).
lock="$dir/.update.lock"
mkdir "$lock" 2>/dev/null || die "another update is running ($lock exists; remove it if no update is running)"
trap 'rmdir "$lock" 2>/dev/null || true' EXIT INT TERM

env_get() { sed -n "s/^$1=//p" .env | tail -n1; }

current=$(env_get ERVISIO_VERSION)
[ -n "$current" ] || current=$(sed -n 's/.*ERVISIO_VERSION:-\([0-9][0-9.]*\)}.*/\1/p' compose.yaml | head -n1)

# ---- target version
if [ -z "$version" ]; then
  tmp_json=$(mktemp)
  fetch "https://api.github.com/repos/$REPO/releases/latest" "$tmp_json" || { rm -f "$tmp_json"; die "cannot reach GitHub to find the latest release"; }
  version=$(sed -n 's/.*"tag_name": *"v\([0-9][0-9.]*\)".*/\1/p' "$tmp_json" | head -n1)
  rm -f "$tmp_json"
fi
version="${version#v}"
valid_version "$version" || die "not a release version: $version"
if [ -n "$current" ] && [ "$force" -eq 0 ]; then
  [ "$current" = "$version" ] && { say "Ervisio Repo $current is already installed (use --force to reinstall)."; exit 0; }
  older "$version" "$current" && die "$version is older than the installed $current (use --force to go back)"
fi

say "Ervisio Repo update in $dir: ${current:-unknown} -> $version"
if [ "$assume_yes" -eq 0 ] && [ -t 0 ]; then
  printf 'Continue? [y/N] '
  read -r answer
  case "$answer" in y|Y|yes|YES) ;; *) say "Cancelled."; exit 1 ;; esac
fi

# Services to pull and recreate: everything enabled, but never the updater
# when this script runs inside it (it would kill itself).
services=$(docker compose config --services)
if [ "${ERVISIO_IN_UPDATER:-}" = 1 ]; then
  services=$(printf '%s\n' "$services" | grep -vx updater || true)
fi
services=$(printf '%s\n' "$services" | tr '\n' ' ')

# install_file SRC DST: replace DST atomically, keeping its owner and mode.
install_file() {
  owner=$(stat -c %u:%g "$2" 2>/dev/null || true)
  mode=$(stat -c %a "$2" 2>/dev/null || echo 600)
  cp "$1" "$2.new"
  chmod "$mode" "$2.new"
  [ -n "$owner" ] && chown "$owner" "$2.new" 2>/dev/null || true
  mv -f "$2.new" "$2"
}

# env_set KEY VALUE: change or add one key of .env, keeping every other line.
env_set() {
  esc=$(printf '%s' "$2" | sed 's/[\\|&]/\\&/g')
  tmp=$(mktemp ./.env.XXXXXX)
  if grep -q "^$1=" .env; then sed "s|^$1=.*|$1=$esc|" .env > "$tmp"
  else cat .env > "$tmp"; printf '%s=%s\n' "$1" "$2" >> "$tmp"; fi
  install_file "$tmp" .env
  rm -f "$tmp"
}

# ---- backup
say "==> backup"
stamp=$(date -u +%Y%m%dT%H%M%SZ)
backup="backups/$stamp"
mkdir -p "$backup"
chmod 700 backups "$backup"
cp -p .env compose.yaml config/config.toml "$backup/"
if [ "$db_backup" -eq 1 ]; then
  if docker compose ps --status running --services 2>/dev/null | grep -qx postgres; then
    say "Dumping the database to $backup/database.sql.gz"
    docker compose exec -T postgres pg_dump -U ervisio_repo -d ervisio_repo > "$backup/database.sql" ||
      die "the database dump failed (use --no-db-backup to skip it)"
    gzip "$backup/database.sql"
  else
    say "PostgreSQL is not running: no database dump."
  fi
fi
# Keep the five newest backups.
ls -1d backups/2* 2>/dev/null | sort -r | tail -n +6 | while read -r old; do rm -rf "$old"; done
# Run by the updater container (root), keep them owned like .env.
env_owner=$(stat -c %u:%g .env 2>/dev/null || true)
[ -n "$env_owner" ] && chown -R "$env_owner" backups 2>/dev/null || true

rollback() {
  say "Update failed: restoring .env and compose.yaml from $backup"
  install_file "$backup/.env" .env
  install_file "$backup/compose.yaml" compose.yaml
  # shellcheck disable=SC2086 # services is a list
  docker compose up -d $services || true
  say "The previous version is starting again. The database dump is in $dir/$backup."
  exit 1
}

# ---- fetch
say "==> fetch"
if [ -e ../../.git ]; then
  say "This is a git checkout: compose.yaml and the scripts are left as they are (run git pull to update them)."
else
  raw="https://raw.githubusercontent.com/$REPO/v$version/deploy/docker"
  work=$(mktemp -d)
  fetch "$raw/compose.yaml" "$work/compose.yaml" || { rm -rf "$work"; die "cannot download compose.yaml of $version"; }
  grep -q '^name: ervisiorepo' "$work/compose.yaml" || { rm -rf "$work"; die "the downloaded compose.yaml is not an Ervisio Repo stack"; }
  for f in setup.sh setup.bat update.sh updater.sh; do
    # Older releases do not ship every script: keep the local one then.
    if fetch "$raw/$f" "$work/$f" 2>/dev/null && [ -s "$work/$f" ]; then
      [ -f "$f" ] || cp "$work/$f" "$f"
      install_file "$work/$f" "$f"
    fi
  done
  install_file "$work/compose.yaml" compose.yaml
  rm -rf "$work"
  chmod +x setup.sh update.sh 2>/dev/null || true
fi

# ---- pull
say "==> pull"
env_set ERVISIO_VERSION "$version"
[ -n "$(env_get ERVISIO_DEPLOY_DIR)" ] || env_set ERVISIO_DEPLOY_DIR "$dir"
# shellcheck disable=SC2086
docker compose pull $services || rollback

# ---- up
say "==> up"
# shellcheck disable=SC2086
docker compose up -d --wait --wait-timeout 300 $services || rollback
running=$(docker compose exec -T app ervisio-repo version 2>/dev/null | head -n1 || true)
case "$running" in
  "ervisio-repo $version"|"ervisio-repo $version "*) ;;
  *) say "The app reports '$running', expected $version."; rollback ;;
esac

say "==> done"
say "Ervisio Repo $version is running. Backup: $dir/$backup"
