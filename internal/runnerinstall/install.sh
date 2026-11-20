#!/bin/sh
# Ervisio runner installer for Linux and macOS, served by Ervisio Repo at
# /runner/install.sh. On a Linux server, one command sets everything up:
#
#   curl -fsSL https://<host>/runner/install.sh | sudo sh
#
# It installs Docker when missing (Debian/Ubuntu, Fedora/RHEL/Rocky/Alma),
# downloads ervisio-runner, shows a code that an admin approves at
# https://<host>/runner/approve, creates the ervisio-runner service user
# and starts the systemd service. Pass options after "sh -s --":
#
#   curl -fsSL https://<host>/runner/install.sh | sudo sh -s -- --name build-1 --capacity 4
#
# Options:
#   --url URL          server URL (default: the server that served this script)
#   --token TOKEN      registration token, skips the approval (or ERVISIO_RUNNER_TOKEN)
#   --name NAME        runner name (default: host name)
#   --labels A,B       extra labels; bare names join the defaults
#   --capacity N       jobs run at the same time (default: CPUs/2, at least 1)
#   --no-docker        do not install or use Docker: run jobs on this machine
#                      (no isolation, trusted repositories only; alias: --host)
#   --version X.Y.Z    runner version (default: the server version)
#   --idle-shutdown D  on-demand machine: power it off after D without jobs
#                      (e.g. 15m; 0 turns it off). The server starts it again
#                      through the start URL set on the runner (Admin > Runners)
#   --upgrade          already installed: only replace the binary
#   --reconfigure      already installed: register again (new approval)
#   --yes              do not ask questions
#   --uninstall        remove the service, the user, the files and the
#                      runner on the server
set -eu

SERVER_VERSION="@@VERSION@@"
SERVER_URL="@@URL@@"
REPO_URL="https://github.com/Ervisio/ErvisioRepo"
SVC_USER="ervisio-runner"
PRUNE_SCRIPT=/usr/local/libexec/ervisio-runner-prune
PRUNE_UNIT=/etc/systemd/system/ervisio-runner-prune.service
PRUNE_TIMER=/etc/systemd/system/ervisio-runner-prune.timer
POWEROFF_UNIT=/etc/systemd/system/ervisio-runner-poweroff.service
POWEROFF_PATH=/etc/systemd/system/ervisio-runner-poweroff.path

url="$SERVER_URL"; token="${ERVISIO_RUNNER_TOKEN:-}"; name=""; labels=""; capacity=""
host=0; version="$SERVER_VERSION"; yes=0; uninstall=0; mode=""; idle=""

die() { echo "ervisio-runner install: $*" >&2; exit 1; }
say() { echo "==> $*"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --url) url="${2:?--url needs a value}"; shift ;;
    --token) token="${2:?--token needs a value}"; shift ;;
    --name) name="${2:?--name needs a value}"; shift ;;
    --labels) labels="${2:?--labels needs a value}"; shift ;;
    --capacity) capacity="${2:?--capacity needs a value}"; shift ;;
    --no-docker|--host) host=1 ;;
    --docker) ;; # accepted for compatibility: the service user joins the docker group
    --version) version="${2:?--version needs a value}"; shift ;;
    --idle-shutdown) idle="${2:?--idle-shutdown needs a value}"; shift ;;
    --upgrade) mode=upgrade ;;
    --reconfigure) mode=reconfigure ;;
    --yes|-y) yes=1 ;;
    --uninstall) uninstall=1 ;;
    --purge) ;; # accepted for compatibility: --uninstall removes everything
    -h|--help) echo "usage: install.sh [--url URL] [--token TOKEN] [--name N] [--labels A,B] [--capacity N] [--no-docker] [--version X.Y.Z] [--idle-shutdown 15m] [--upgrade|--reconfigure] [--yes] | --uninstall"; exit 0 ;;
    *) die "unknown option $1" ;;
  esac
  shift
done

case "$capacity" in ""|[1-9]|[1-5][0-9]|6[0-4]) ;; *) die "--capacity must be between 1 and 64" ;; esac
case "$idle" in
  ""|0|0s) ;;
  *[!0-9smh]*|[!0-9]*) die "--idle-shutdown takes a duration such as 15m or 1h (0 turns it off)" ;;
esac

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
state="$data_dir/.runner"

# tty is 1 when questions can be asked: piped into "sudo sh" stdin is the
# script, so answers come from /dev/tty, and only if it can be opened.
tty=0
if [ $yes -eq 0 ] && { : </dev/tty; } 2>/dev/null; then tty=1; fi

ask() { # ask PROMPT -> 0 for yes; never blocks without a terminal
  [ $tty -eq 1 ] || return 1
  printf '%s [y/N] ' "$1" > /dev/tty
  read -r reply < /dev/tty || return 1
  case "$reply" in y|Y|yes|YES) return 0 ;; esac
  return 1
}

systemctl_() { if [ $root -eq 1 ]; then systemctl "$@"; else systemctl --user "$@"; fi; }
has_systemd() { command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; }

stop_service() {
  if [ "$os" = darwin ]; then
    launchctl bootout "gui/$(id -u)" "$plist" 2>/dev/null || true
  elif has_systemd; then
    systemctl_ stop ervisio-runner.service 2>/dev/null || true
  fi
}

remove_poweroff() {
  [ $root -eq 1 ] && has_systemd || return 0
  systemctl disable --now ervisio-runner-poweroff.path 2>/dev/null || true
  rm -f "$POWEROFF_PATH" "$POWEROFF_UNIT"
}

remove_prune() {
  [ $root -eq 1 ] && has_systemd || return 0
  systemctl disable --now ervisio-runner-prune.timer 2>/dev/null || true
  rm -f "$PRUNE_TIMER" "$PRUNE_UNIT" "$PRUNE_SCRIPT"
}

# ---- uninstall ----
if [ $uninstall -eq 1 ]; then
  stop_service
  if [ -x "$bin" ] && [ -s "$state" ] && [ -f "$cfg" ]; then
    if "$bin" deregister --config "$cfg" </dev/null; then
      deregistered=1
    else
      deregistered=0
    fi
  else
    deregistered=0
  fi
  if [ "$os" = darwin ]; then
    rm -f "$plist"
  elif has_systemd; then
    systemctl_ disable ervisio-runner.service 2>/dev/null || true
    rm -f "$unit"
    remove_prune
    remove_poweroff
    systemctl_ daemon-reload 2>/dev/null || true
  fi
  rm -f "$bin"
  rm -rf "$cfg_dir" "$data_dir"
  if [ $root -eq 1 ] && id "$SVC_USER" >/dev/null 2>&1; then
    userdel "$SVC_USER" 2>/dev/null || true
  fi
  say "ervisio-runner removed from this machine."
  [ "$deregistered" -eq 1 ] || echo "Remove it from the Runners page of the server too (Admin > Runners, or the org/repository settings)."
  exit 0
fi

[ -n "$url" ] || die "--url is required"
url="${url%/}"
case "$url" in http://*|https://*) ;; *) die "--url must start with http:// or https://" ;; esac
command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

# ---- already installed? ----
installed=0
[ "$mode" = upgrade ] && ! { [ -x "$bin" ] && [ -s "$state" ]; } && mode=""
if [ -x "$bin" ] && [ -s "$state" ]; then
  installed=1
  if [ -z "$mode" ]; then
    mode=upgrade
    if [ $tty -eq 1 ]; then
      echo "ervisio-runner is already installed and registered ($state)."
      ask "Register it again (new approval, new name/labels/capacity)? No only upgrades the binary" && mode=reconfigure
    fi
  fi
fi

# ---- Docker ----
install_docker() {
  [ -r /etc/os-release ] || die "cannot detect the distribution (/etc/os-release missing): install Docker yourself, or pass --no-docker"
  # shellcheck disable=SC1091
  . /etc/os-release
  id_="${ID:-}"; like="${ID_LIKE:-}"
  say "Installing Docker Engine from download.docker.com"
  case "$id_ $like" in
    ubuntu*|*" ubuntu"*|debian*|*" debian"*|raspbian*)
      case "$id_" in
        ubuntu|debian) dist="$id_"; codename="${VERSION_CODENAME:-}" ;;
        raspbian) dist=debian; codename="${VERSION_CODENAME:-}" ;;
        *) case "$like" in
             *ubuntu*) dist=ubuntu; codename="${UBUNTU_CODENAME:-${VERSION_CODENAME:-}}" ;;
             *) dist=debian; codename="${DEBIAN_CODENAME:-${VERSION_CODENAME:-}}" ;;
           esac ;;
      esac
      [ -n "$codename" ] || die "cannot detect the release codename: install Docker yourself, or pass --no-docker"
      export DEBIAN_FRONTEND=noninteractive
      apt-get update -q </dev/null
      apt-get install -y -q ca-certificates curl </dev/null
      install -m 0755 -d /etc/apt/keyrings
      curl -fsSL "https://download.docker.com/linux/$dist/gpg" -o /etc/apt/keyrings/docker.asc
      chmod a+r /etc/apt/keyrings/docker.asc
      echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/$dist $codename stable" \
        > /etc/apt/sources.list.d/docker.list
      apt-get update -q </dev/null
      apt-get install -y -q docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin </dev/null
      ;;
    fedora*)
      curl -fsSL https://download.docker.com/linux/fedora/docker-ce.repo -o /etc/yum.repos.d/docker-ce.repo
      dnf -y install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin </dev/null \
        || die "dnf could not install Docker (podman/buildah conflicts? remove them or install Docker yourself)"
      ;;
    rhel*|centos*|rocky*|almalinux*|*" rhel"*|*" centos"*)
      repo=centos; [ "$id_" = rhel ] && repo=rhel
      curl -fsSL "https://download.docker.com/linux/$repo/docker-ce.repo" -o /etc/yum.repos.d/docker-ce.repo
      dnf -y install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin </dev/null \
        || die "dnf could not install Docker (podman/buildah conflicts? remove them or install Docker yourself)"
      ;;
    *) die "automatic Docker installation supports Debian, Ubuntu, Fedora, RHEL, Rocky and Alma; install Docker yourself and run again, or pass --no-docker" ;;
  esac
  systemctl enable --now docker
}

if [ $host -eq 0 ] && [ "$mode" != upgrade ]; then
  if ! command -v docker >/dev/null 2>&1; then
    if [ "$os" = linux ] && [ $root -eq 1 ]; then
      install_docker
    elif [ "$os" = darwin ]; then
      die "Docker is not installed. Install Docker Desktop and run again, or pass --no-docker to run jobs directly on this Mac (trusted repositories only)."
    else
      die "Docker is not installed. Run this with sudo to install it, or pass --no-docker."
    fi
  fi
  if [ $root -eq 1 ] && [ "$os" = linux ] && has_systemd; then
    systemctl is-active --quiet docker 2>/dev/null || systemctl enable --now docker 2>/dev/null || true
  fi
  docker info >/dev/null 2>&1 || die "this user cannot reach the Docker daemon (start Docker, join the docker group, or pass --no-docker)"
fi
if [ "$mode" = upgrade ] && [ -f "$cfg" ] && grep -q '=host' "$cfg" 2>/dev/null && ! grep -q 'docker://' "$cfg"; then
  host=1
fi

# ---- download ----
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

if [ $installed -eq 1 ]; then
  stop_service
fi
if [ "$mode" = reconfigure ]; then
  say "Removing the previous registration"
  "$bin" deregister --config "$cfg" </dev/null 2>/dev/null || echo "   (could not remove it on the server: delete the old runner from the Runners page)"
  rm -f "$state" "$cfg"
fi
mkdir -p "$bin_dir"
install -m 0755 "$tmp/ervisio-runner" "$bin"
say "Installed $bin ($("$bin" version))"

# ---- service user (Linux, root) ----
if [ $root -eq 1 ]; then
  if ! id "$SVC_USER" >/dev/null 2>&1; then
    nologin=/usr/sbin/nologin; [ -x "$nologin" ] || nologin=/sbin/nologin
    useradd --system --home-dir "$data_dir" --no-create-home --shell "$nologin" --user-group "$SVC_USER"
  fi
  if [ $host -eq 0 ]; then
    getent group docker >/dev/null 2>&1 || die "the docker group does not exist"
    # Members of the docker group are effectively root on this machine:
    # this is what lets the runner start job containers.
    usermod -aG docker "$SVC_USER"
  fi
fi

# ---- register: device approval, or the token ----
mkdir -p "$cfg_dir" "$data_dir"
if [ -s "$state" ]; then
  say "Already registered ($state kept)"
else
  set -- register --config "$cfg" --data-dir "$data_dir" --url "$url"
  [ -n "$name" ] && set -- "$@" --name "$name"
  [ -n "$labels" ] && set -- "$@" --labels "$labels"
  [ -n "$capacity" ] && set -- "$@" --capacity "$capacity"
  [ $host -eq 1 ] && set -- "$@" --host
  if [ -z "$token" ]; then
    say "Requesting approval from $url"
  fi
  # Without a token the binary prints a code to approve at
  # $url/runner/approve and waits (up to 15 minutes); it needs no input.
  ERVISIO_RUNNER_TOKEN="$token" "$bin" "$@" </dev/null || die "registration failed"
fi
# ---- idle shutdown (on-demand machine) ----
if [ -n "$idle" ]; then
  case "$idle" in 0|0s) idle_value=0s ;; *) idle_value="$idle" ;; esac
  if grep -q '^idle_shutdown *=' "$cfg"; then
    sed -i.bak "s/^idle_shutdown *=.*/idle_shutdown = \"$idle_value\"/" "$cfg" && rm -f "$cfg.bak"
  else
    sed -i.bak "/^job_timeout *=/a\\
idle_shutdown = \"$idle_value\"" "$cfg" && rm -f "$cfg.bak"
  fi
  grep -q "^idle_shutdown = \"$idle_value\"" "$cfg" || die "could not set idle_shutdown in $cfg"
fi
if [ $root -eq 1 ]; then
  chown -R "$SVC_USER:$SVC_USER" "$data_dir"
  chown root:"$SVC_USER" "$cfg"; chmod 0640 "$cfg"
fi

# ---- summary helpers ----
reg_name=$(sed -n 's/^ *"name": *"\(.*\)",*$/\1/p' "$state" 2>/dev/null | head -n 1)
reg_labels=$(awk '/"labels"/ {on=1; next} on && /\]/ {on=0} on' "$state" 2>/dev/null \
  | sed 's/^ *"//; s/[:=].*//' | paste -sd, - 2>/dev/null || true)
reg_capacity=$(sed -n 's/^capacity *= *\([0-9]*\).*/\1/p' "$cfg" 2>/dev/null | head -n 1)
summary() {
  echo
  say "ervisio-runner is ready"
  echo "   server:   $url"
  echo "   name:     ${reg_name:-?}"
  echo "   labels:   ${reg_labels:-?}"
  echo "   capacity: ${reg_capacity:-?} job(s) at a time"
  echo "   backend:  $( [ $host -eq 1 ] && echo 'this machine (no Docker)' || echo 'Docker containers' )"
  echo "   config:   $cfg"
}

# ---- service (macOS) ----
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
  summary
  echo "   service:  LaunchAgent dev.ervisio.runner (logs: $data_dir/logs/runner.log)"
  echo "   remove:   curl -fsSL $url/runner/install.sh | sh -s -- --uninstall"
  exit 0
fi

if ! has_systemd; then
  summary
  echo "   systemd not found: start the runner with: $bin daemon --config \"$cfg\""
  exit 0
fi

# ---- service (systemd) ----
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

# ---- Docker cleanup timer (root, Docker backend) ----
# Daily: images, containers, networks and build cache unused for 7 days,
# unused anonymous volumes, and runner caches/workspaces untouched for 14
# days. When the Docker data root is below 15% free it also drops every
# unused image regardless of age. Images of running containers are never
# touched (prune skips them), nor images labelled dev.ervisio.runner.keep=true
# (built on the machine, e.g. for a custom runner label). Logs go to the journal.
if [ $root -eq 1 ] && [ $host -eq 0 ]; then
  mkdir -p "$(dirname "$PRUNE_SCRIPT")"
  cat > "$PRUNE_SCRIPT" <<PRUNE
#!/bin/sh
# Installed by the Ervisio runner installer; removed by --uninstall.
set -u
data_dir="$data_dir"
command -v docker >/dev/null 2>&1 || { echo "docker not found, nothing to prune"; exit 0; }
echo "pruning Docker objects unused for 7 days"
docker system prune -af --filter until=168h --filter "label!=dev.ervisio.runner.keep=true"
docker volume prune -f
root=\$(docker info -f '{{.DockerRootDir}}' 2>/dev/null || echo /var/lib/docker)
[ -d "\$root" ] || root=/
free=\$(df -P "\$root" | awk 'NR==2 { gsub("%", "", \$5); print 100 - \$5 }')
echo "free space on \$root: \${free}%"
if [ -n "\$free" ] && [ "\$free" -lt 15 ]; then
  echo "below 15% free: pruning every unused image and the build cache"
  docker system prune -af --filter "label!=dev.ervisio.runner.keep=true"
  docker builder prune -af 2>/dev/null || true
fi
for d in "\$data_dir/cache" "\$data_dir/host"; do
  [ -d "\$d" ] || continue
  echo "removing entries of \$d untouched for 14 days"
  find "\$d" -mindepth 1 -maxdepth 1 -mtime +14 -exec rm -rf {} +
done
exit 0
PRUNE
  chmod 0755 "$PRUNE_SCRIPT"
  cat > "$PRUNE_UNIT" <<UNIT
[Unit]
Description=Ervisio runner Docker cleanup
After=docker.service
Requires=docker.service

[Service]
Type=oneshot
ExecStart=$PRUNE_SCRIPT
Nice=10
IOSchedulingClass=idle
UNIT
  cat > "$PRUNE_TIMER" <<UNIT
[Unit]
Description=Daily Ervisio runner Docker cleanup

[Timer]
OnCalendar=daily
RandomizedDelaySec=1h
Persistent=true

[Install]
WantedBy=timers.target
UNIT
elif [ $root -eq 1 ]; then
  remove_prune
fi

# ---- power-off unit (root): the idle runner cannot switch the machine off
# itself (unprivileged service user, NoNewPrivileges); it writes a request
# file that this unit watches. Only a fresh request counts.
idle_cfg=$(sed -n 's/^idle_shutdown *= *"\(.*\)".*/\1/p' "$cfg" 2>/dev/null | head -n 1)
if [ $root -eq 1 ] && [ -n "$idle_cfg" ] && [ "$idle_cfg" != 0s ] && [ "$idle_cfg" != 0 ]; then
  req="$data_dir/poweroff-requested"
  cat > "$POWEROFF_PATH" <<UNIT
[Unit]
Description=Ervisio runner idle power-off request

[Path]
PathExists=$req

[Install]
WantedBy=multi-user.target
UNIT
  cat > "$POWEROFF_UNIT" <<UNIT
[Unit]
Description=Ervisio runner idle power-off

[Service]
Type=oneshot
ExecStart=/bin/sh -c 'if [ -n "\$\$(find $req -mmin -5 2>/dev/null)" ]; then rm -f $req; exec systemctl poweroff; fi; rm -f $req'
UNIT
  poweroff_on=1
else
  remove_poweroff
  poweroff_on=0
fi

systemctl_ daemon-reload
systemctl_ enable ervisio-runner.service
systemctl_ restart ervisio-runner.service
if [ $root -eq 1 ] && [ $host -eq 0 ]; then
  systemctl enable --now ervisio-runner-prune.timer
fi
if [ $root -eq 1 ] && [ $poweroff_on -eq 1 ]; then
  systemctl enable --now ervisio-runner-poweroff.path
fi

summary
if [ $root -eq 1 ]; then
  echo "   status:   systemctl status ervisio-runner"
  echo "   logs:     journalctl -u ervisio-runner -f"
  [ $host -eq 0 ] && echo "   cleanup:  ervisio-runner-prune.timer, daily (journalctl -u ervisio-runner-prune)"
  [ $poweroff_on -eq 1 ] && echo "   idle:     powers the machine off after $idle_cfg without jobs (set the start URL in Admin > Runners)"
  echo "   remove:   curl -fsSL $url/runner/install.sh | sudo sh -s -- --uninstall"
else
  echo "   status:   systemctl --user status ervisio-runner"
  echo "   logs:     journalctl --user -u ervisio-runner -f"
  echo "   remove:   curl -fsSL $url/runner/install.sh | sh -s -- --uninstall"
  echo "   To keep it running after logout: sudo loginctl enable-linger $(id -un)"
fi
exit 0
