#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 ]]; then
  printf 'usage: %s REVISION CHROME_VERSION COMPATIBLE_PLAYWRIGHT_VERSIONS\n' "$0" >&2
  exit 2
fi
: "${CINEKO_RELEASE_PUBLISHED_AT:?required}"

readonly revision="$1"
readonly chrome_version="$2"
readonly compatible_versions="$3"
export CINEKO_PLAYWRIGHT_VERSIONS="$compatible_versions"
if [[ ! "$revision" =~ ^[1-9][0-9]*$ ]]; then
  printf 'revision must be a positive integer\n' >&2
  exit 2
fi
if [[ ! "$chrome_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  printf 'Chrome version must contain four numeric components\n' >&2
  exit 2
fi
if [[ ! "$CINEKO_RELEASE_PUBLISHED_AT" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T ]]; then
  printf 'CINEKO_RELEASE_PUBLISHED_AT must be an RFC3339 timestamp\n' >&2
  exit 2
fi

work_dir="$(mktemp -d)"
readonly work_dir
trap 'rm -rf "$work_dir"' EXIT
readonly official_base="https://storage.googleapis.com/chrome-for-testing-public/$chrome_version"
readonly release_contract="$work_dir/releasecontract"
GOWORK=off go build -mod=vendor -o "$release_contract" ./cmd/releasecontract
release_paths=()

IFS=',' read -r -a raw_versions <<< "$compatible_versions"
version_count=0
for raw_version in "${raw_versions[@]}"; do
  version="${raw_version//[[:space:]]/}"
  if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    printf 'compatible Playwright versions must be semantic versions\n' >&2
    exit 2
  fi
  version_count=$((version_count + 1))
done
if [[ "$version_count" -eq 0 ]]; then
  printf 'at least one compatible Playwright version is required\n' >&2
  exit 2
fi

publish_platform() {
  local platform="$1"
  local architecture="$2"
  local official_platform="$3"
  local executable="$4"
  local archive="$work_dir/chrome-$official_platform.zip"
  local url="$official_base/$official_platform/chrome-$official_platform.zip"

  curl --fail --silent --show-error --location --retry 5 --retry-all-errors \
    --output "$archive" "$url"
  unzip -Z1 "$archive" | grep -Fxq "$executable" || {
    printf 'official Chrome archive is missing executable: %s\n' "$executable" >&2
    return 1
  }
  local release_path="$work_dir/${platform}-${architecture}.json"
  "$release_contract" release browser "$revision" "$platform/$architecture" "$archive" "$executable" \
    "$url" "$CINEKO_RELEASE_PUBLISHED_AT" >"$release_path"
  release_paths+=("$release_path")
}

publish_platform darwin arm64 mac-arm64 \
  'chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing'
publish_platform linux amd64 linux64 'chrome-linux64/chrome'
publish_platform windows amd64 win64 'chrome-win64/chrome.exe'

readonly payload="$work_dir/payload.json"
"$release_contract" set browser "${release_paths[@]}" >"$payload"
if [[ -n "${CINEKO_BROWSER_RELEASE_PAYLOAD_OUT:-}" ]]; then
  cp "$payload" "$CINEKO_BROWSER_RELEASE_PAYLOAD_OUT"
  exit 0
fi
: "${CINEKO_CENTRAL_URL:?required}"
: "${CINEKO_RELEASE_PUBLISH_TOKEN:?required}"
curl --fail-with-body --retry 3 --retry-all-errors \
  -H "Authorization: Bearer $CINEKO_RELEASE_PUBLISH_TOKEN" \
  -H 'Content-Type: application/json' \
  --data-binary "@$payload" \
  --output "$work_dir/publish-response.json" \
  "${CINEKO_CENTRAL_URL%/}/v1/release-registry/browser"
"$release_contract" verify-response browser "$work_dir/publish-response.json"
