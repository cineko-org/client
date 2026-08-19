#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 ]]; then
  printf 'usage: %s <darwin/arm64|linux/amd64> <client-path> <client-executable>\n' "$0" >&2
  exit 2
fi

readonly platform="$1"
readonly client_path="$2"
readonly client_executable="$3"
readonly version="${CINEKO_VERSION:?CINEKO_VERSION is required}"
readonly release_dir="build/release"
readonly suffix="${platform//\//-}"
readonly archive="$release_dir/cineko-client-v${version#v}-${suffix}.tar.gz"

case "$platform" in darwin/arm64|linux/amd64) ;; *) exit 2 ;; esac
test -e "$client_path"
mkdir -p "$release_dir"
tar -chzf "$archive" -C "$(dirname "$client_path")" "$(basename "$client_path")"
node scripts/write-release-metadata.mjs client "$version" "$platform" "$archive" "$client_executable"
