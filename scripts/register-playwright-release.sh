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

for command in curl go; do
  command -v "$command" >/dev/null || {
    printf '%s is required on the release publisher runner\n' "$command" >&2
    exit 2
  }
done

temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/cineko-playwright-register.XXXXXX")"
readonly temporary_root
trap 'rm -rf "$temporary_root"' EXIT
readonly release_contract="$temporary_root/releasecontract"
GOWORK=off go build -mod=vendor -o "$release_contract" ./cmd/releasecontract
release_paths=()

append_release() {
  local platform="$1"
  local architecture="$2"
  local extension="$3"
  local executable="$4"
  local filename="cineko-playwright-${version}-${platform}-${architecture}.${extension}"
  local artifact_path="${assets_dir}/${filename}"

  if [[ ! -f "$artifact_path" ]]; then
    printf 'Playwright artifact is missing: %s\n' "$artifact_path" >&2
    return 1
  fi

  local release_path="$temporary_root/${platform}-${architecture}.json"
  "$release_contract" release playwright "$version" "$platform/$architecture" "$artifact_path" "$executable" \
    "${public_base}/${filename}" "$published_at" >"$release_path"
  release_paths+=("$release_path")
}

append_release darwin arm64 tar.gz node
append_release windows amd64 zip node.exe
append_release linux amd64 tar.gz node

readonly payload="$temporary_root/playwright-release-set.json"
"$release_contract" set playwright "${release_paths[@]}" >"$payload"
readonly response="$temporary_root/publish-response.json"
scripts/post-release-registry.sh playwright "$payload" "$response"
"$release_contract" verify-response playwright "$response"

printf 'registered Playwright %s for all supported platforms\n' "$version"
