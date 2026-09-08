#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 4 ]]; then
  printf 'usage: %s CLIENT_RELEASE_SET BROWSER_RELEASE_SET PLAYWRIGHT_RELEASE_SET ASSETS_DIR\n' "$0" >&2
  exit 2
fi

readonly client_set="$1"
readonly browser_set="$2"
readonly playwright_set="$3"
readonly assets_dir="$4"
readonly release_contract="$assets_dir/releasecontract"
GOWORK=off go build -mod=vendor -o "$release_contract" ./cmd/releasecontract
trap 'rm -f "$release_contract"' EXIT

for target in darwin/arm64 linux/amd64 windows/amd64; do
  platform="${target%/*}"
  architecture="${target#*/}"
  "$release_contract" runtime "$target" "$client_set" "$browser_set" "$playwright_set" \
    >"$assets_dir/runtime-$platform-$architecture.json"
done

printf 'generated compatible GitHub runtime release manifests\n'
