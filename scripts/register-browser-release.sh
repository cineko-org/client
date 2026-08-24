#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  printf 'usage: %s PAYLOAD_JSON\n' "$0" >&2
  exit 2
fi
readonly payload="$1"
temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/cineko-browser-register.XXXXXX")"
readonly temporary_root
trap 'rm -rf "$temporary_root"' EXIT
readonly release_contract="$temporary_root/releasecontract"
GOWORK=off go build -mod=vendor -o "$release_contract" ./cmd/releasecontract
"$release_contract" verify-set browser "$payload"

cat "$payload"
