#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 ]]; then
  printf 'usage: %s CLIENT_RELEASE_SET BROWSER_RELEASE_SET PLAYWRIGHT_RELEASE_SET\n' "$0" >&2
  exit 2
fi

readonly client_set="$1"
readonly browser_set="$2"
readonly playwright_set="$3"
: "${CINEKO_RELEASES_S3_ENDPOINT:?required}"
: "${CINEKO_RELEASES_S3_ACCESS_KEY:?required}"
: "${CINEKO_RELEASES_S3_SECRET_KEY:?required}"
readonly bucket="${CINEKO_RELEASES_S3_BUCKET:-cineko-releases}"
export AWS_ACCESS_KEY_ID="$CINEKO_RELEASES_S3_ACCESS_KEY"
export AWS_SECRET_ACCESS_KEY="$CINEKO_RELEASES_S3_SECRET_KEY"
export AWS_DEFAULT_REGION="${CINEKO_RELEASES_S3_REGION:-us-east-1}"

for command in aws go; do
  command -v "$command" >/dev/null || {
    printf '%s is required on the release publisher runner\n' "$command" >&2
    exit 2
  }
done

temporary_directory="$(mktemp -d "${TMPDIR:-/tmp}/cineko-runtime-channel.XXXXXX")"
readonly temporary_directory
trap 'rm -rf "$temporary_directory"' EXIT
readonly release_contract="$temporary_directory/releasecontract"
GOWORK=off go build -mod=vendor -o "$release_contract" ./cmd/releasecontract

for target in darwin/arm64 linux/amd64 windows/amd64; do
  platform="${target%/*}"
  architecture="${target#*/}"
  platform_key="${platform}-${architecture}"
  manifest="$temporary_directory/${platform_key}.json"
  "$release_contract" runtime "$target" "$client_set" "$browser_set" "$playwright_set" >"$manifest"
  aws --endpoint-url "$CINEKO_RELEASES_S3_ENDPOINT" s3api put-object \
    --bucket "$bucket" --key "channels/stable/${platform_key}/runtime.json" \
    --body "$manifest" --content-type 'application/json; charset=utf-8' \
    --cache-control no-store >/dev/null
done

printf 'published compatible stable runtime manifests\n'
