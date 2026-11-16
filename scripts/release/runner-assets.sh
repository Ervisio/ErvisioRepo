#!/usr/bin/env bash
# Cross-compiles ervisio-runner for the remote-runner installers
# (/runner/install.sh and /runner/install.ps1) and packs the GitHub release
# assets they download:
#
#   ervisio-runner_<ver>_<os>_<arch>.tar.gz   (linux, darwin)
#   ervisio-runner_<ver>_windows_<arch>.zip
#   ervisio-runner_<ver>_checksums.txt        (sha256, "<hash>  <file>")
#
#   VERSION=1.2.3 scripts/release/runner-assets.sh [out-dir]   (default dist/runner)
#
# Needs Go and zip (or python3 for the Windows archives). Attach the files
# to the v<ver> release by hand.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.."

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
VERSION="${VERSION#v}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
MODULE=github.com/Ervisio/ErvisioRepo
LDFLAGS="-s -w -X $MODULE/internal/buildinfo.version=$VERSION -X $MODULE/internal/buildinfo.commit=$COMMIT"
TARGETS="${TARGETS:-linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64}"
OUT="${1:-dist/runner}"

case "$VERSION" in
  [0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "warning: VERSION=$VERSION is not x.y.z, the installers will not find these assets" >&2 ;;
esac

mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

zip_dir() { # zip_dir ARCHIVE DIR
  if command -v zip >/dev/null; then
    (cd "$2" && zip -q -X "$1" ./*)
  else
    python3 -I -c 'import sys, zipfile, os
with zipfile.ZipFile(sys.argv[1], "w", zipfile.ZIP_DEFLATED) as z:
    for f in sorted(os.listdir(sys.argv[2])):
        z.write(os.path.join(sys.argv[2], f), f)' "$1" "$2"
  fi
}

assets=()
for t in $TARGETS; do
  os="${t%/*}"; arch="${t#*/}"
  dir="$work/$os-$arch"
  mkdir -p "$dir"
  exe=ervisio-runner; [ "$os" = windows ] && exe=ervisio-runner.exe
  echo "building $os/$arch"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "$LDFLAGS" -o "$dir/$exe" ./cmd/ervisio-runner
  cp LICENSE "$dir/LICENSE"
  name="ervisio-runner_${VERSION}_${os}_${arch}"
  if [ "$os" = windows ]; then
    rm -f "$OUT/$name.zip"
    zip_dir "$OUT/$name.zip" "$dir"
    assets+=("$name.zip")
  else
    tar -C "$dir" -czf "$OUT/$name.tar.gz" "$exe" LICENSE
    assets+=("$name.tar.gz")
  fi
done

sums="ervisio-runner_${VERSION}_checksums.txt"
(cd "$OUT" && { sha256sum "${assets[@]}" 2>/dev/null || shasum -a 256 "${assets[@]}"; } > "$sums")
echo "Runner assets in $OUT:"
(cd "$OUT" && ls -1 "${assets[@]}" "$sums")
