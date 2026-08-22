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
curl --fail-with-body --retry 3 --retry-all-errors \
  --request POST \
  --header "Authorization: Bearer ${CINEKO_RELEASE_PUBLISH_TOKEN}" \
  --header 'Content-Type: application/json' \
  --data-binary "@$payload" \
  --output "$response" \
  "${CINEKO_CENTRAL_URL%/}/v1/release-registry/browser"
"$release_contract" verify-response browser "$response"

printf 'registered browser release set\n'
