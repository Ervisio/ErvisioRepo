#!/bin/sh
# Ervisio Repo installer for Debian/Ubuntu and RHEL-family Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/Ervisio/ErvisioRepo/main/deploy/install.sh | sudo sh
#   ... | sudo sh -s -- --version 1.2.3
#
# Downloads the release package, checks SHA256SUMS and its ed25519 signature,
# then installs it with apt/dpkg or dnf/rpm. Set ERVISIO_SKIP_VERIFY=1 only if
# openssl 3 is unavailable and you verified the files another way.
set -eu

REPO="Ervisio/ErvisioRepo"
BASE="${ERVISIO_RELEASE_BASE:-https://github.com/$REPO/releases}"
VERSION=""

# Release signing public key (ed25519). Private half kept offline by the maintainer.
PUBKEY='-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAp9H+DlnaYwvdIfRNk7libuyY4c7n23DfBNQ6G02YJ34=
-----END PUBLIC KEY-----'

die() { echo "install.sh: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --version) [ $# -ge 2 ] || die "--version needs a value"; VERSION="${2#v}"; shift ;;
    -h|--help) sed -n '2,10p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
  shift
done

[ "$(uname -s)" = "Linux" ] || die "only Linux is supported; use the Docker setup on other systems"
[ "$(id -u)" -eq 0 ] || die "run as root (use sudo)"

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "unsupported architecture: $(uname -m)" ;;
esac

[ -r /etc/os-release ] || die "cannot detect the distribution (/etc/os-release missing)"
# shellcheck disable=SC1091
. /etc/os-release
case " ${ID:-} ${ID_LIKE:-} " in
  *" debian "*|*" ubuntu "*) PKG=deb ;;
  *" rhel "*|*" fedora "*|*" centos "*|*" rocky "*|*" almalinux "*) PKG=rpm ;;
  *) die "unsupported distribution: ${ID:-unknown}" ;;
esac

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q -O "$2" "$1"; }
else
  die "curl or wget is required"
fi

if [ -z "$VERSION" ]; then
  # The latest release redirects to /tag/vX.Y.Z.
  if command -v curl >/dev/null 2>&1; then
    loc=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$BASE/latest") || die "cannot resolve the latest release"
    VERSION="${loc##*/}"; VERSION="${VERSION#v}"
  fi
  [ -n "$VERSION" ] || die "cannot resolve the latest release; pass --version"
fi

FILE="ervisio-repo_${VERSION}_linux_${ARCH}.${PKG}"
URL="$BASE/download/v${VERSION}"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM

echo "Downloading Ervisio Repo $VERSION ($ARCH, $PKG)"
fetch "$URL/$FILE" "$TMP/$FILE" || die "download failed: $URL/$FILE"
fetch "$URL/SHA256SUMS" "$TMP/SHA256SUMS" || die "download failed: $URL/SHA256SUMS"

if [ "${ERVISIO_SKIP_VERIFY:-0}" = "1" ]; then
  echo "WARNING: signature verification skipped" >&2
else
  fetch "$URL/SHA256SUMS.sig" "$TMP/SHA256SUMS.sig" || die "download failed: $URL/SHA256SUMS.sig"
  command -v openssl >/dev/null 2>&1 || die "openssl is required to verify the signature"
  printf '%s\n' "$PUBKEY" > "$TMP/release.pub"
  openssl pkeyutl -verify -pubin -inkey "$TMP/release.pub" -rawin \
    -in "$TMP/SHA256SUMS" -sigfile "$TMP/SHA256SUMS.sig" >/dev/null 2>&1 \
    || die "signature check failed (needs openssl 3 and an untampered release)"
fi

want=$(awk -v f="$FILE" '$2 == f || $2 == "*" f { print $1 }' "$TMP/SHA256SUMS")
[ -n "$want" ] || die "$FILE is not listed in SHA256SUMS"
if command -v sha256sum >/dev/null 2>&1; then
  have=$(sha256sum "$TMP/$FILE" | awk '{print $1}')
else
  have=$(shasum -a 256 "$TMP/$FILE" | awk '{print $1}')
fi
[ "$want" = "$have" ] || die "checksum mismatch for $FILE"
echo "Checksum and signature verified"

case "$PKG" in
  deb)
    if command -v apt-get >/dev/null 2>&1; then apt-get install -y "$TMP/$FILE"
    else dpkg -i "$TMP/$FILE"; fi ;;
  rpm)
    if command -v dnf >/dev/null 2>&1; then dnf install -y "$TMP/$FILE"
    elif command -v yum >/dev/null 2>&1; then yum install -y "$TMP/$FILE"
    else rpm -Uvh "$TMP/$FILE"; fi ;;
esac

echo
echo "Ervisio Repo $VERSION is installed. Next steps are printed above:"
echo "  edit /etc/ervisio-repo/config.toml, run 'runuser -u ervisio-repo -- ervisio-repo migrate up --config /etc/ervisio-repo/config.toml',"
echo "  then 'systemctl enable --now ervisio-repo'."
