#!/usr/bin/env bash
# Builds the release artifacts into dist/: tarballs, .deb, .rpm, SHA256SUMS and
# (when RELEASE_SIGNING_KEY points to a private PEM) SHA256SUMS.sig.
#
#   VERSION=1.2.3 RELEASE_SIGNING_KEY=~/release.pem scripts/release/build.sh
#
# Requires the SPA to be built already (make web) and Go. nfpm runs through
# `go run`, so nothing else has to be installed.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.."

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
VERSION="${VERSION#v}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
NFPM_VERSION="${NFPM_VERSION:-v2.41.1}"
MODULE=github.com/Ervisio/ErvisioRepo
LDFLAGS="-s -w -X $MODULE/internal/buildinfo.version=$VERSION -X $MODULE/internal/buildinfo.commit=$COMMIT"
ARCHES="${ARCHES:-amd64 arm64}"
DIST=dist

rm -rf "$DIST"
mkdir -p "$DIST"

for arch in $ARCHES; do
  bin="$DIST/build/$arch"
  mkdir -p "$bin"
  for cmd in ervisio-repo ervisio-runner erepo; do
    if ls "cmd/$cmd"/*.go >/dev/null 2>&1; then
      CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags "$LDFLAGS" -o "$bin/$cmd" "./cmd/$cmd"
    else
      echo "skipping $cmd: cmd/$cmd has no Go files" >&2
    fi
  done
  # nfpm needs every declared file; a missing command becomes an empty stub.
  for cmd in ervisio-runner erepo; do
    [ -f "$bin/$cmd" ] || { printf '#!/bin/sh\necho "%s is not part of this build" >&2\nexit 1\n' "$cmd" > "$bin/$cmd"; chmod 755 "$bin/$cmd"; }
  done

  tar -C "$bin" -czf "$DIST/ervisio-repo_${VERSION}_linux_${arch}.tar.gz" .
  # nfpm does not expand env vars in contents, so render the config first.
  sed -e "s#\${VERSION}#$VERSION#g" -e "s#\${ARCH}#$arch#g" -e "s#\${BIN_DIR}#$bin#g" \
    packaging/nfpm.yaml > "$DIST/build/nfpm-$arch.yaml"
  for fmt in deb rpm; do
    # Stable file names so deploy/install.sh can build the download URL.
    go run "github.com/goreleaser/nfpm/v2/cmd/nfpm@$NFPM_VERSION" package \
      --config "$DIST/build/nfpm-$arch.yaml" --packager "$fmt" \
      --target "$DIST/ervisio-repo_${VERSION}_linux_${arch}.${fmt}"
  done
done
rm -rf "$DIST/build"

# Name the artifacts: a ./* glob races with the redirect and lists SHA256SUMS itself.
(cd "$DIST" && { sha256sum ./*.deb ./*.rpm ./*.tar.gz 2>/dev/null || shasum -a 256 ./*.deb ./*.rpm ./*.tar.gz; } | sed 's# \./# #; s#\*\./#*#' > SHA256SUMS)

if [ -n "${RELEASE_SIGNING_KEY:-}" ]; then
  go run ./scripts/release/sign sign "$RELEASE_SIGNING_KEY" "$DIST/SHA256SUMS" "$DIST/SHA256SUMS.sig"
else
  echo "RELEASE_SIGNING_KEY not set: SHA256SUMS is unsigned" >&2
fi
echo "Artifacts in $DIST/"
