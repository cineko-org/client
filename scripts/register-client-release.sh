#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 4 ]]; then
  printf 'usage: %s VERSION PUBLISHED_AT ASSETS_DIR COMPATIBILITY_FILE\n' "$0" >&2
  exit 2
fi

readonly version="${1#v}"
readonly published_at="$2"
readonly assets_dir="$3"
readonly compatibility_file="$4"
: "${CINEKO_CENTRAL_URL:?required}"
: "${CINEKO_RELEASE_PUBLISH_TOKEN:?required}"
: "${CINEKO_CLIENT_RELEASE_BASE:?required}"
: "${CINEKO_PROBE_BOOTSTRAP_PUBLIC_KEYS_JSON:?required}"
readonly public_base="${CINEKO_CLIENT_RELEASE_BASE%/}"

if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  printf 'Client version must use semantic versioning: %s\n' "$version" >&2
  exit 2
fi
if [[ "$public_base" != https://* ]]; then
  printf 'Client release base must use HTTPS\n' >&2
  exit 2
fi

for command in curl jq sha256sum wc; do
  command -v "$command" >/dev/null || {
    printf '%s is required on the release publisher runner\n' "$command" >&2
    exit 2
  }
done

jq -e '
  (.minimumLauncherVersion | test("^[0-9]+\\.[0-9]+\\.[0-9]+$")) and
  (.minimumBrowserRevision | test("^[1-9][0-9]*$")) and
  (.playwrightVersion | test("^[0-9]+\\.[0-9]+\\.[0-9]+$")) and
  (.protocol | numbers | . > 0)
' "$compatibility_file" >/dev/null
jq -e 'type == "object" and length > 0 and all(to_entries[]; (.key | length > 0) and (.value | type == "string" and length > 0))' \
  <<<"$CINEKO_PROBE_BOOTSTRAP_PUBLIC_KEYS_JSON" >/dev/null

temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/cineko-client-register.XXXXXX")"
readonly temporary_root
trap 'rm -rf "$temporary_root"' EXIT
readonly releases_file="$temporary_root/releases.jsonl"
: >"$releases_file"

append_release() {
  local platform="$1"
  local arch="$2"
  local extension="$3"
  local executable="$4"
  local filename="cineko-client-v${version}-${platform}-${arch}.${extension}"
  local artifact_path="${assets_dir}/${filename}"

  if [[ ! -f "$artifact_path" ]]; then
    printf 'Client artifact is missing: %s\n' "$artifact_path" >&2
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
    --slurpfile compatibility "$compatibility_file" \
    --argjson probeBootstrapPublicKeys "$CINEKO_PROBE_BOOTSTRAP_PUBLIC_KEYS_JSON" \
    '{
      channel:$channel,
      platform:$platform,
      arch:$arch,
      version:$version,
      minimumLauncherVersion:$compatibility[0].minimumLauncherVersion,
      minimumBrowserRevision:$compatibility[0].minimumBrowserRevision,
      playwrightVersion:$compatibility[0].playwrightVersion,
      protocol:$compatibility[0].protocol,
      artifact:{url:$url,size:$size,sha256:$sha256,executable:$executable},
      probeBootstrapPublicKeys:$probeBootstrapPublicKeys,
      publishedAt:$publishedAt
    }' >>"$releases_file"
}

append_release darwin arm64 tar.gz 'Cineko.app/Contents/MacOS/Cineko'
append_release windows amd64 zip 'Cineko.exe'
append_release linux amd64 tar.gz 'Cineko'

payload="$(jq -sc '{schemaVersion:2,payload:{releases:.}}' "$releases_file")"
curl --fail-with-body --retry 3 --retry-all-errors \
  --request POST \
  --header "Authorization: Bearer ${CINEKO_RELEASE_PUBLISH_TOKEN}" \
  --header 'Content-Type: application/json' \
  --header 'X-Cineko-Protocol: 3' \
  --data "$payload" \
  "${CINEKO_CENTRAL_URL%/}/v1/release-registry/client" |
  jq -e '.generation | numbers | select(. >= 0)' >/dev/null

printf 'registered Client v%s for all supported platforms\n' "$version"
