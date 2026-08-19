#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 ]]; then
  printf 'usage: %s REVISION CHROME_VERSION COMPATIBLE_PLAYWRIGHT_VERSIONS\n' "$0" >&2
  exit 2
fi
: "${CINEKO_RELEASE_PUBLISHED_AT:?required}"
: "${CINEKO_CENTRAL_URL:?required}"
: "${CINEKO_RELEASE_PUBLISH_TOKEN:?required}"

readonly revision="$1"
readonly chrome_version="$2"
readonly compatible_versions="$3"
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
readonly releases="$work_dir/releases.jsonl"
readonly official_base="https://storage.googleapis.com/chrome-for-testing-public/$chrome_version"

IFS=',' read -r -a raw_versions <<< "$compatible_versions"
playwright_versions='[]'
for raw_version in "${raw_versions[@]}"; do
  version="${raw_version//[[:space:]]/}"
  if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    printf 'compatible Playwright versions must be semantic versions\n' >&2
    exit 2
  fi
  playwright_versions="$(jq --arg version "$version" '. + [$version] | unique' <<< "$playwright_versions")"
done
if [[ "$(jq 'length' <<< "$playwright_versions")" -eq 0 ]]; then
  printf 'at least one compatible Playwright version is required\n' >&2
  exit 2
fi

publish_platform() {
  local platform="$1"
  local arch="$2"
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
  local size sha256
  size="$(wc -c < "$archive" | tr -d ' ')"
  sha256="$(sha256sum "$archive" | awk '{print $1}')"
  jq -cn \
    --arg platform "$platform" \
    --arg arch "$arch" \
    --arg revision "$revision" \
    --arg url "$url" \
    --arg sha256 "$sha256" \
    --arg executable "$executable" \
    --arg publishedAt "$CINEKO_RELEASE_PUBLISHED_AT" \
    --argjson size "$size" \
    --argjson compatiblePlaywrightVersions "$playwright_versions" \
    '{
      channel: "stable",
      platform: $platform,
      arch: $arch,
      revision: $revision,
      compatiblePlaywrightVersions: $compatiblePlaywrightVersions,
      artifact: {url: $url, size: $size, sha256: $sha256, executable: $executable},
      publishedAt: $publishedAt
    }' >> "$releases"
}

publish_platform darwin arm64 mac-arm64 \
  'chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing'
publish_platform linux amd64 linux64 'chrome-linux64/chrome'
publish_platform windows amd64 win64 'chrome-win64/chrome.exe'

readonly payload="$work_dir/payload.json"
jq -s '{schemaVersion: 2, payload: {releases: sort_by(.platform, .arch)}}' "$releases" > "$payload"
curl --fail-with-body --retry 3 --retry-all-errors \
  -H "Authorization: Bearer $CINEKO_RELEASE_PUBLISH_TOKEN" \
  -H 'Content-Type: application/json' \
  -H "X-Cineko-Protocol: ${CINEKO_PROTOCOL_VERSION:-3}" \
  --data-binary "@$payload" \
  "${CINEKO_CENTRAL_URL%/}/v1/release-registry/browser"
