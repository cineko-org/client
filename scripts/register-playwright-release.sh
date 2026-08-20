#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 ]]; then
  printf 'usage: %s VERSION PUBLISHED_AT ASSETS_DIR\n' "$0" >&2
  exit 2
fi

readonly version="${1#v}"
readonly published_at="$2"
readonly assets_dir="$3"
: "${CINEKO_CENTRAL_URL:?required}"
: "${CINEKO_RELEASE_PUBLISH_TOKEN:?required}"
: "${CINEKO_PLAYWRIGHT_RELEASE_BASE:?required}"
readonly public_base="${CINEKO_PLAYWRIGHT_RELEASE_BASE%/}"

if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  printf 'Playwright version must use semantic versioning: %s\n' "$version" >&2
  exit 2
fi
if [[ "$public_base" != https://* ]]; then
  printf 'Playwright release base must use HTTPS\n' >&2
  exit 2
fi

for command in curl jq sha256sum wc; do
  command -v "$command" >/dev/null || {
    printf '%s is required on the release publisher runner\n' "$command" >&2
    exit 2
  }
done

temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/cineko-playwright-register.XXXXXX")"
readonly temporary_root
trap 'rm -rf "$temporary_root"' EXIT
readonly releases_file="$temporary_root/releases.jsonl"
: >"$releases_file"

append_release() {
  local platform="$1"
  local arch="$2"
  local extension="$3"
  local executable="$4"
  local filename="cineko-playwright-${version}-${platform}-${arch}.${extension}"
  local artifact_path="${assets_dir}/${filename}"

  if [[ ! -f "$artifact_path" ]]; then
    printf 'Playwright artifact is missing: %s\n' "$artifact_path" >&2
    return 1
  fi

  local size sha256
  size="$(wc -c <"$artifact_path" | tr -d '[:space:]')"
  sha256="$(sha256sum "$artifact_path" | awk '{print $1}')"
  jq -cn \
    --arg channel stable \
    --arg platform "$platform" \
    --arg arch "$arch" \
    --arg version "$version" \
    --arg url "${public_base}/${filename}" \
    --arg sha256 "$sha256" \
    --arg executable "$executable" \
    --arg publishedAt "$published_at" \
    --argjson size "$size" \
    '{
      channel:$channel,
      platform:$platform,
      arch:$arch,
      version:$version,
      artifact:{url:$url,size:$size,sha256:$sha256,executable:$executable},
      publishedAt:$publishedAt
    }' >>"$releases_file"
}

append_release darwin arm64 tar.gz node
append_release windows amd64 zip node.exe
append_release linux amd64 tar.gz node

payload="$(jq -sc '{schemaVersion:2,payload:{releases:.}}' "$releases_file")"
curl --fail-with-body --retry 3 --retry-all-errors \
  --request POST \
  --header "Authorization: Bearer ${CINEKO_RELEASE_PUBLISH_TOKEN}" \
  --header 'Content-Type: application/json' \
  --header 'X-Cineko-Protocol: 3' \
  --data "$payload" \
  "${CINEKO_CENTRAL_URL%/}/v1/release-registry/playwright" |
  jq -e '.generation | numbers | select(. >= 0)' >/dev/null

printf 'registered Playwright %s for all supported platforms\n' "$version"
