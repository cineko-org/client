#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  printf 'usage: %s PAYLOAD_JSON\n' "$0" >&2
  exit 2
fi
: "${CINEKO_CENTRAL_URL:?required}"
: "${CINEKO_RELEASE_PUBLISH_TOKEN:?required}"
readonly payload="$1"
temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/cineko-browser-register.XXXXXX")"
readonly temporary_root
trap 'rm -rf "$temporary_root"' EXIT
readonly release_contract="$temporary_root/releasecontract"
GOWORK=off go build -mod=vendor -o "$release_contract" ./cmd/releasecontract
"$release_contract" verify-set browser "$payload"

readonly response="$temporary_root/publish-response.json"
scripts/post-release-registry.sh browser "$payload" "$response"
"$release_contract" verify-response browser "$response"

printf 'registered browser release set\n'
