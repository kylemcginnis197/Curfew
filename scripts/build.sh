#!/usr/bin/env bash
# Cross-compile Curfew for all supported platforms into ./dist.
set -euo pipefail

cd "$(dirname "$0")/.."
mkdir -p dist

VERSION="${1:-dev}"
LDFLAGS="-s -w"

targets=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
  "windows/amd64"
)

for t in "${targets[@]}"; do
  os="${t%/*}"; arch="${t#*/}"
  out="dist/curfew_${VERSION}_${os}_${arch}"
  [ "$os" = "windows" ] && out="${out}.exe"
  echo "building $out"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -ldflags "$LDFLAGS" -o "$out" ./cmd/curfew
done

echo "done -> ./dist"
