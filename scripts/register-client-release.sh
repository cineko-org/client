#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 ]]; then
	printf 'usage: %s VERSION PUBLISHED_AT ASSETS_DIR\n' "$0" >&2
  exit 2
fi

readonly version="${1#v}"
readonly published_at="$2"
readonly assets_dir="$3"
: "${CINEKO_CLIENT_RELEASE_BASE:?required}"
: "${CINEKO_MINIMUM_LAUNCHER_VERSION:?required}"
: "${CINEKO_BROWSER_REVISION:?required}"
: "${CINEKO_PLAYWRIGHT_VERSION:?required}"
readonly public_base="${CINEKO_CLIENT_RELEASE_BASE%/}"

if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  printf 'Client version must use semantic versioning: %s\n' "$version" >&2
  exit 2
fi
if [[ "$public_base" != https://* ]]; then
  printf 'Client release base must use HTTPS\n' >&2
  exit 2
fi

for command in go jq; do
  command -v "$command" >/dev/null || {
    printf '%s is required on the release publisher runner\n' "$command" >&2
    exit 2
  }
done

[[ "${CINEKO_MINIMUM_LAUNCHER_VERSION#v}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
	printf 'minimum Launcher version must use semantic versioning\n' >&2
	exit 2
}
[[ "$CINEKO_BROWSER_REVISION" =~ ^[1-9][0-9]*$ ]] || {
	printf 'browser revision must be a positive integer\n' >&2
	exit 2
}
[[ "${CINEKO_PLAYWRIGHT_VERSION#v}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
	printf 'Playwright version must use semantic versioning\n' >&2
	exit 2
}
locked_playwright_version="$(bash scripts/playwright-version.sh driver)"
readonly locked_playwright_version
requested_playwright_version="${CINEKO_PLAYWRIGHT_VERSION#v}"
readonly requested_playwright_version
if [[ "$requested_playwright_version" != "$locked_playwright_version" ]]; then
  printf 'Client Playwright target %s does not match the locked driver %s\n' \
    "$requested_playwright_version" "$locked_playwright_version" >&2
  exit 1
fi
temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/cineko-client-register.XXXXXX")"
readonly temporary_root
trap 'rm -rf "$temporary_root"' EXIT
readonly release_contract="$temporary_root/releasecontract"
GOWORK=off go build -mod=vendor -o "$release_contract" ./cmd/releasecontract

export CINEKO_MINIMUM_LAUNCHER_VERSION="${CINEKO_MINIMUM_LAUNCHER_VERSION#v}"
export CINEKO_BROWSER_REVISION
export CINEKO_PLAYWRIGHT_VERSION="$requested_playwright_version"
release_paths=()

append_release() {
  local platform="$1"
  local architecture="$2"
  local extension="$3"
  local executable="$4"
  local filename="cineko-client-v${version}-${platform}-${architecture}.${extension}"
  local artifact_path="${assets_dir}/${filename}"

  if [[ ! -f "$artifact_path" ]]; then
    printf 'Client artifact is missing: %s\n' "$artifact_path" >&2
    return 1
  fi

  local release_path="$temporary_root/${platform}-${architecture}.json"
  "$release_contract" release client "$version" "$platform/$architecture" "$artifact_path" "$executable" \
    "${public_base}/${filename}" "$published_at" >"$release_path"
	if [[ -n "${CINEKO_RELEASE_METADATA_DIR:-}" ]]; then
		mkdir -p "$CINEKO_RELEASE_METADATA_DIR"
		local published_path="${CINEKO_RELEASE_METADATA_DIR%/}/client-release-${platform}-${architecture}.json"
		cp "$release_path" "$published_path"
		release_path="$published_path"
	fi
	release_paths+=("$release_path")
}

append_release darwin arm64 zip 'Cineko.app/Contents/MacOS/Cineko'
append_release windows amd64 zip 'Cineko.exe'
append_release linux amd64 tar.gz 'Cineko'

readonly payload="$temporary_root/client-release-set.json"
"$release_contract" set client "${release_paths[@]}" >"$payload"
if [[ -n "${CINEKO_RELEASE_PAYLOAD_OUT:-}" ]]; then
  cp "$payload" "$CINEKO_RELEASE_PAYLOAD_OUT"
else
  cat "$payload"
fi

printf 'generated Client v%s release metadata for all supported platforms\n' "$version" >&2
