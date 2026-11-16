#!/bin/sh
# Ervisio runner installer for Linux and macOS, served by Ervisio Repo at
# /runner/install.sh. Usage:
#
#   curl -fsSL https://<host>/runner/install.sh | sh -s -- --url https://<host> --token <errt_...>
#
# Options:
#   --url URL          server URL (default: the server that served this script)
#   --token TOKEN      registration token (or ERVISIO_RUNNER_TOKEN)
#   --name NAME        runner name (default: host name)
#   --labels A,B       extra labels; bare names join the defaults
#   --capacity N       jobs run at the same time (default: CPUs/2)
#   --docker           Linux as root: add the runner user to the docker group
#   --host             run jobs directly on this machine (no Docker)
#   --version X.Y.Z    runner version (default: the server version)
#   --yes              do not ask questions
#   --uninstall        stop and remove the service and the binary
#   --purge            with --uninstall: also delete the config and the state
set -eu

SERVER_VERSION="@@VERSION@@"
SERVER_URL="@@URL@@"
REPO_URL="https://github.com/Ervisio/ErvisioRepo"
SVC_USER="ervisio-runner"

url="$SERVER_URL"; token="${ERVISIO_RUNNER_TOKEN:-}"; name=""; labels=""; capacity=""
docker_ok=0; host=0; version="$SERVER_VERSION"; yes=0; uninstall=0; purge=0

die() { echo "ervisio-runner install: $*" >&2; exit 1; }
say() { echo "==> $*"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --url) url="${2:?--url needs a value}"; shift ;;
    --token) token="${2:?--token needs a value}"; shift ;;
    --name) name="${2:?--name needs a value}"; shift ;;
    --labels) labels="${2:?--labels needs a value}"; shift ;;
    --capacity) capacity="${2:?--capacity needs a value}"; shift ;;
    --docker) docker_ok=1 ;;
    --host) host=1 ;;
    --version) version="${2:?--version needs a value}"; shift ;;
    --yes|-y) yes=1 ;;
    --uninstall) uninstall=1 ;;
    --purge) purge=1 ;;
    -h|--help) echo "usage: install.sh --url URL --token TOKEN [--name N] [--labels A,B] [--capacity N] [--docker|--host] [--version X.Y.Z] [--yes] | --uninstall [--purge]"; exit 0 ;;
    *) die "unknown option $1" ;;
  esac
  shift
done

os=$(uname -s); arch=$(uname -m)
case "$os" in Linux) os=linux ;; Darwin) os=darwin ;; *) die "unsupported OS $os (use install.ps1 on Windows)" ;; esac
case "$arch" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) die "unsupported architecture $arch" ;; esac

root=0; [ "$(id -u)" -eq 0 ] && root=1
if [ "$os" = darwin ] && [ $root -eq 1 ]; then
  die "on macOS run this without sudo: the runner is installed as a LaunchAgent of your user"
fi

# Install layout.
if [ $root -eq 1 ]; then
  bin_dir=/usr/local/bin
  cfg_dir=/etc/ervisio-runner
  data_dir=/var/lib/ervisio-runner
  unit=/etc/systemd/system/ervisio-runner.service
elif [ "$os" = darwin ]; then
  bin_dir="$HOME/.local/bin"
  cfg_dir="$HOME/Library/Application Support/ervisio-runner"
  data_dir="$cfg_dir"
  plist="$HOME/Library/LaunchAgents/dev.ervisio.runner.plist"
else
  bin_dir="$HOME/.local/bin"
  cfg_dir="${XDG_CONFIG_HOME:-$HOME/.config}/ervisio-runner"
  data_dir="${XDG_DATA_HOME:-$HOME/.local/share}/ervisio-runner"
  unit="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/ervisio-runner.service"
fi
bin="$bin_dir/ervisio-runner"
cfg="$cfg_dir/config.toml"

ask() { # ask PROMPT -> 0 for yes
  [ $yes -eq 1 ] && return 1
  [ -r /dev/tty ] || return 1
  printf '%s [y/N] ' "$1" > /dev/tty
  read -r reply < /dev/tty || return 1
  case "$reply" in y|Y|yes|YES) return 0 ;; esac
  return 1
}

systemctl_() { if [ $root -eq 1 ]; then systemctl "$@"; else systemctl --user "$@"; fi; }

if [ $uninstall -eq 1 ]; then
  if [ "$os" = darwin ]; then
    launchctl bootout "gui/$(id -u)" "$plist" 2>/dev/null || true
    rm -f "$plist"
  elif command -v systemctl >/dev/null 2>&1; then
    systemctl_ disable --now ervisio-runner.service 2>/dev/null || true
    rm -f "$unit"
    systemctl_ daemon-reload 2>/dev/null || true
  fi
  rm -f "$bin"
  if [ $purge -eq 1 ]; then
    rm -rf "$cfg_dir" "$data_dir"
    [ $root -eq 1 ] && userdel "$SVC_USER" 2>/dev/null || true
  fi
  say "ervisio-runner removed. Delete it from the Runners page of the server too."
  exit 0
fi

[ -n "$url" ] || die "--url is required"
url="${url%/}"
case "$url" in http://*|https://*) ;; *) die "--url must start with http:// or https://" ;; esac
command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

# Backend: Docker containers unless --host.
if [ $host -eq 0 ]; then
  command -v docker >/dev/null 2>&1 || die "Docker is not installed. Install Docker and run again, or pass --host to run jobs directly on this machine (no isolation: only for trusted repositories)."
  if [ $root -eq 1 ] && [ "$os" = linux ] && [ $docker_ok -eq 0 ]; then
    echo "The runner user '$SVC_USER' must join the 'docker' group to start job containers."
    echo "Members of that group are effectively root on this machine."
    ask "Add $SVC_USER to the docker group?" && docker_ok=1
    [ $docker_ok -eq 1 ] || die "pass --docker to allow it, or --host to run jobs without Docker"
  fi
  if [ $root -eq 0 ] && ! docker info >/dev/null 2>&1; then
    die "this user cannot reach the Docker daemon (join the docker group, start Docker Desktop, or pass --host)"
  fi
fi

# Version: the server's own, or the latest release for development builds.
case "$version" in
  [0-9]*.[0-9]*.[0-9]*) ;;
  *)
    latest=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$REPO_URL/releases/latest") || die "cannot resolve the latest release"
    version="${latest##*/v}"
    case "$version" in [0-9]*.[0-9]*.[0-9]*) ;; *) die "cannot resolve the latest release, pass --version" ;; esac
    ;;
esac
version="${version#v}"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
asset="ervisio-runner_${version}_${os}_${arch}.tar.gz"
base="$REPO_URL/releases/download/v$version"
say "Downloading $asset"
curl -fsSL -o "$tmp/$asset" "$base/$asset" || die "download failed: $base/$asset"
curl -fsSL -o "$tmp/checksums.txt" "$base/ervisio-runner_${version}_checksums.txt" || die "checksum download failed"
want=$(awk -v f="$asset" '$2 == f || $2 == "*" f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || die "$asset is not listed in the checksums file"
if command -v sha256sum >/dev/null 2>&1; then got=$(sha256sum "$tmp/$asset" | awk '{print $1}')
else got=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}'); fi
[ "$want" = "$got" ] || die "checksum mismatch for $asset"
tar -xzf "$tmp/$asset" -C "$tmp" ervisio-runner
mkdir -p "$bin_dir"
install -m 0755 "$tmp/ervisio-runner" "$bin"
say "Installed $bin ($("$bin" version))"

# Service user (Linux, root).
if [ $root -eq 1 ]; then
  if ! id "$SVC_USER" >/dev/null 2>&1; then
    nologin=/usr/sbin/nologin; [ -x "$nologin" ] || nologin=/sbin/nologin
    useradd --system --home-dir "$data_dir" --no-create-home --shell "$nologin" --user-group "$SVC_USER"
  fi
  if [ $host -eq 0 ] && [ $docker_ok -eq 1 ]; then
    getent group docker >/dev/null 2>&1 || die "the docker group does not exist"
    usermod -aG docker "$SVC_USER"
  fi
fi

# Register (writes the config when missing and the state file).
mkdir -p "$cfg_dir" "$data_dir"
if [ -s "$data_dir/.runner" ]; then
  say "Already registered ($data_dir/.runner kept); run with --uninstall --purge to start over"
else
  [ -n "$token" ] || die "--token is required"
  set -- register --config "$cfg" --data-dir "$data_dir" --url "$url"
  [ -n "$name" ] && set -- "$@" --name "$name"
  [ -n "$labels" ] && set -- "$@" --labels "$labels"
  [ -n "$capacity" ] && set -- "$@" --capacity "$capacity"
  [ $host -eq 1 ] && set -- "$@" --host
  ERVISIO_RUNNER_TOKEN="$token" "$bin" "$@"
fi
if [ $root -eq 1 ]; then
  chown -R "$SVC_USER:$SVC_USER" "$data_dir"
  chown root:"$SVC_USER" "$cfg"; chmod 0640 "$cfg"
fi

# Service.
if [ "$os" = darwin ]; then
  mkdir -p "$(dirname "$plist")" "$data_dir/logs"
  cat > "$plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>dev.ervisio.runner</string>
  <key>ProgramArguments</key>
  <array><string>$bin</string><string>daemon</string><string>--config</string><string>$cfg</string></array>
  <key>EnvironmentVariables</key>
  <dict><key>PATH</key><string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin</string></dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Background</string>
  <key>StandardOutPath</key><string>$data_dir/logs/runner.log</string>
  <key>StandardErrorPath</key><string>$data_dir/logs/runner.log</string>
</dict>
</plist>
PLIST
  launchctl bootout "gui/$(id -u)" "$plist" 2>/dev/null || true
  launchctl bootstrap "gui/$(id -u)" "$plist"
  say "Started the LaunchAgent dev.ervisio.runner (logs: $data_dir/logs/runner.log)"
  exit 0
fi

command -v systemctl >/dev/null 2>&1 || {
  say "systemd not found: start the runner with: $bin daemon --config \"$cfg\""
  exit 0
}
mkdir -p "$(dirname "$unit")"
if [ $root -eq 1 ]; then
  cat > "$unit" <<UNIT
[Unit]
Description=Ervisio runner
After=network-online.target docker.service
Wants=network-online.target

[Service]
User=$SVC_USER
Group=$SVC_USER
ExecStart=$bin daemon --config $cfg
Restart=on-failure
RestartSec=5
TimeoutStopSec=660
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
ReadWritePaths=$data_dir

[Install]
WantedBy=multi-user.target
UNIT
else
  cat > "$unit" <<UNIT
[Unit]
Description=Ervisio runner
After=network-online.target

[Service]
ExecStart=$bin daemon --config "$cfg"
Restart=on-failure
RestartSec=5
TimeoutStopSec=660

[Install]
WantedBy=default.target
UNIT
fi
systemctl_ daemon-reload
systemctl_ enable --now ervisio-runner.service
say "ervisio-runner.service is running (journalctl $( [ $root -eq 1 ] || echo '--user ' )-u ervisio-runner -f)"
[ $root -eq 0 ] && echo "To keep it running after logout: sudo loginctl enable-linger $(id -un)"
exit 0
