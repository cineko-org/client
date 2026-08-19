#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then printf 'usage: %s <darwin/arm64|linux/amd64>\n' "$0" >&2; exit 2; fi
readonly platform="$1"
readonly version="${CINEKO_PLAYWRIGHT_VERSION:?CINEKO_PLAYWRIGHT_VERSION is required}"
readonly suffix="${platform//\//-}"
case "$platform" in
  darwin/arm64) readonly cache_root="${HOME}/Library/Caches" ;;
  linux/amd64) readonly cache_root="${XDG_CACHE_HOME:-${HOME}/.cache}" ;;
  *) exit 2 ;;
esac
readonly source="${cache_root}/ms-playwright-go/${version#v}"
readonly release_dir="build/release"
readonly archive="$release_dir/cineko-playwright-${version#v}-${suffix}.tar.gz"
test -x "$source/node"
test -f "$source/package/cli.js"
mkdir -p "$release_dir"
tar -chzf "$archive" -C "$source" node package
node scripts/write-release-metadata.mjs playwright "$version" "$platform" "$archive" node
