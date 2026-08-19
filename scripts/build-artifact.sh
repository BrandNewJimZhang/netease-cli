#!/usr/bin/env bash
# Build the release artifact for one platform and print its digest.
#
# The two things a marketplace entry needs are the binary and its
# sha256, so this prints the digest rather than leaving the publisher to
# remember a second command — a published hash that does not match the
# published bytes fails on the user's machine, after the download.
#
# Usage: scripts/build-artifact.sh [<goos>-<goarch>]
# Defaults to this machine's platform. Output lands in dist/.

set -euo pipefail

cd "$(dirname "$0")/.."

target="${1:-}"
if [ -z "$target" ]; then
  target="$(go env GOOS)-$(go env GOARCH)"
fi
goos="${target%-*}"
goarch="${target##*-}"

mkdir -p dist
out="dist/netease-cli-${goos}-${goarch}"

# CGO off so the binary runs on a host without the build machine's libc.
CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath \
  -ldflags="-s -w" -o "$out" .

if command -v sha256sum >/dev/null 2>&1; then
  digest="$(sha256sum "$out" | cut -d' ' -f1)"
else
  digest="$(shasum -a 256 "$out" | cut -d' ' -f1)"
fi

# The registry's platform token normalises amd64/arm64; Go already
# spells them that way, so the two vocabularies agree as-is.
printf 'binary:   %s\n' "$out"
printf 'platform: %s-%s\n' "$goos" "$goarch"
printf 'sha256:   %s\n' "$digest"
