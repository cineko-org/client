#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  printf 'usage: %s PAYLOAD_JSON\n' "$0" >&2
  exit 2
fi
: "${CINEKO_CENTRAL_URL:?required}"
: "${CINEKO_RELEASE_PUBLISH_TOKEN:?required}"
readonly payload="$1"

jq -e '
  .schemaVersion == 2 and
  (.payload.releases | length == 3) and
  ([.payload.releases[] | .platform + "/" + .arch] | sort ==
    ["darwin/arm64", "linux/amd64", "windows/amd64"]) and
  ([.payload.releases[].revision] | unique | length == 1) and
  all(.payload.releases[];
    .channel == "stable" and
    (.revision | test("^[1-9][0-9]*$")) and
    (.compatiblePlaywrightVersions | length > 0) and
    (.artifact.url | startswith("https://storage.googleapis.com/chrome-for-testing-public/")) and
    (.artifact.size > 0) and
    (.artifact.sha256 | test("^[0-9a-f]{64}$"))
  )
' "$payload" >/dev/null

curl --fail-with-body --retry 3 --retry-all-errors \
  --request POST \
  --header "Authorization: Bearer ${CINEKO_RELEASE_PUBLISH_TOKEN}" \
  --header 'Content-Type: application/json' \
  --header 'X-Cineko-Protocol: 3' \
  --data-binary "@$payload" \
  "${CINEKO_CENTRAL_URL%/}/v1/release-registry/browser" |
  jq -e '.generation | numbers | select(. >= 0)' >/dev/null

printf 'registered browser release set\n'
