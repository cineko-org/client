#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]]; then printf 'usage: %s <metadata.json>...\n' "$0" >&2; exit 2; fi
: "${CINEKO_RELEASES_S3_ENDPOINT:?required}"
: "${CINEKO_RELEASES_S3_ACCESS_KEY:?required}"
: "${CINEKO_RELEASES_S3_SECRET_KEY:?required}"
: "${CINEKO_CENTRAL_URL:?required}"
: "${CINEKO_RELEASE_PUBLISH_TOKEN:?required}"
: "${CINEKO_RELEASES_PUBLIC_BASE_URL:?required}"
readonly bucket="${CINEKO_RELEASES_S3_BUCKET:-cineko-releases}"
readonly public_base_url="${CINEKO_RELEASES_PUBLIC_BASE_URL%/}"
export AWS_ACCESS_KEY_ID="$CINEKO_RELEASES_S3_ACCESS_KEY"
export AWS_SECRET_ACCESS_KEY="$CINEKO_RELEASES_S3_SECRET_KEY"
export AWS_DEFAULT_REGION="${CINEKO_RELEASES_S3_REGION:-us-east-1}"

for command in aws curl go jq openssl; do
  command -v "$command" >/dev/null || {
    printf '%s is required on the release publisher runner\n' "$command" >&2
    exit 2
  }
done

temporary_directory="$(mktemp -d)"
readonly temporary_directory
batch_payload="$temporary_directory/batch.json"
readonly batch_payload
cleanup() { rm -rf "$temporary_directory"; }
trap cleanup EXIT
readonly release_contract="$temporary_directory/releasecontract"
GOWORK=off go build -mod=vendor -o "$release_contract" ./cmd/releasecontract

"$release_contract" verify-artifacts "$@"
component="$("$release_contract" component "$@")"
readonly component
"$release_contract" set "$component" "$@" > "$batch_payload"
readonly publish_plan="$temporary_directory/publish-plan.tsv"
"$release_contract" plan "$component" "$public_base_url" "$@" >"$publish_plan"

while IFS=$'\t' read -r artifact object_key public_url expected_size expected_sha256; do
  expected_sha256_base64="$(openssl dgst -sha256 -binary "$artifact" | openssl base64 -A)"
  if ! object_metadata="$(aws --endpoint-url "$CINEKO_RELEASES_S3_ENDPOINT" s3api head-object \
    --bucket "$bucket" --key "$object_key" --checksum-mode ENABLED --output json 2>/dev/null)"; then
    # The conditional write prevents concurrent publishers from replacing an
    # immutable key. A losing identical publisher is accepted only if the
    # authoritative object metadata below matches.
    aws --endpoint-url "$CINEKO_RELEASES_S3_ENDPOINT" s3api put-object \
      --bucket "$bucket" --key "$object_key" --body "$artifact" \
      --checksum-algorithm SHA256 --checksum-sha256 "$expected_sha256_base64" \
      --metadata "sha256=$expected_sha256" --if-none-match '*' >/dev/null 2>&1 || true
    object_metadata="$(aws --endpoint-url "$CINEKO_RELEASES_S3_ENDPOINT" s3api head-object \
      --bucket "$bucket" --key "$object_key" --checksum-mode ENABLED --output json)"
  fi
  remote_size="$(jq -er '.ContentLength' <<<"$object_metadata")"
  remote_sha256="$(jq -r '.ChecksumSHA256 // empty' <<<"$object_metadata")"
  if [[ "$remote_size" != "$expected_size" ]]; then
    printf 'immutable release object size mismatch: %s\n' "$object_key" >&2
    exit 1
  fi
  if [[ -z "$remote_sha256" ]]; then
    # Compatibility for immutable objects published before S3 checksums were
    # enabled. New objects always use the authoritative ChecksumSHA256 field.
    remote_sha256="$(jq -r '.Metadata.sha256 // empty' <<<"$object_metadata")"
    if [[ "$remote_sha256" != "$expected_sha256" ]]; then
      printf 'immutable legacy release object checksum mismatch: %s\n' "$object_key" >&2
      exit 1
    fi
  elif [[ "$remote_sha256" != "$expected_sha256_base64" ]]; then
    printf 'immutable release object checksum mismatch: %s\n' "$object_key" >&2
    exit 1
  fi
  if [[ "$public_url" != "${public_base_url%/}/$object_key" ]]; then
    printf 'public CDN URL does not match immutable object key: %s\n' "$public_url" >&2
    exit 1
  fi
done <"$publish_plan"

readonly response="$temporary_directory/publish-response.json"
curl --fail-with-body --retry 3 --retry-all-errors \
  -H "Authorization: Bearer $CINEKO_RELEASE_PUBLISH_TOKEN" \
  -H 'Content-Type: application/json' \
  --data-binary "@$batch_payload" \
  --output "$response" \
  "${CINEKO_CENTRAL_URL%/}/v1/release-registry/$component"
"$release_contract" verify-response "$component" "$response"
