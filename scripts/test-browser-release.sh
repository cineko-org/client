#!/usr/bin/env bash
set -euo pipefail

test_root="$(mktemp -d "${TMPDIR:-/tmp}/cineko-browser-release-test.XXXXXX")"
readonly test_root
trap 'rm -rf "$test_root"' EXIT
readonly browsers_json="$test_root/browsers.json"
readonly payload="$test_root/payload.json"
readonly posted="$test_root/posted.json"
mkdir -p "$test_root/bin"

cat >"$browsers_json" <<'JSON'
{"browsers":[{"name":"chromium","revision":"1234","browserVersion":"151.0.7922.34"}]}
JSON
[[ "$(scripts/playwright-browser-version.sh "$browsers_json" revision)" == 1234 ]]
[[ "$(scripts/playwright-browser-version.sh "$browsers_json" version)" == 151.0.7922.34 ]]

jq -n '
  def release($platform; $architecture; $executable): {
    channel:"stable", platform:$platform, architecture:$architecture, revision:"1234",
    compatiblePlaywrightVersions:["1.62.1"],
    artifact:{
      url:("https://storage.googleapis.com/chrome-for-testing-public/151.0.7922.34/" + $platform + ".zip"),
      size:10, sha256:("a" * 64), executable:$executable
    },
    publishedAt:"2026-08-21T00:00:00Z"
  };
  {releases:[
    release("darwin";"arm64";"chrome"),
    release("linux";"amd64";"chrome"),
    release("windows";"amd64";"chrome.exe")
  ]}
' >"$payload"
cat >"$test_root/bin/curl" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
source=''
output=''
while [[ $# -gt 0 ]]; do
  if [[ "$1" == '--data-binary' ]]; then shift; source="${1#@}"; fi
  if [[ "$1" == '--output' ]]; then shift; output="$1"; fi
  shift
done
cp "$source" "$FAKE_POSTED"
printf '{}\n' >"$output"
printf '200'
SH
chmod +x "$test_root/bin/curl"

PATH="$test_root/bin:$PATH" \
FAKE_POSTED="$posted" \
CINEKO_CENTRAL_URL=https://central.example \
CINEKO_RELEASE_PUBLISH_TOKEN=publisher \
  scripts/register-browser-release.sh "$payload" >/dev/null
cmp -s "$payload" "$posted"

jq '.releases[2].artifact.sha256 = "bad"' "$payload" >"$payload.invalid"
if PATH="$test_root/bin:$PATH" \
  FAKE_POSTED="$posted" \
  CINEKO_CENTRAL_URL=https://central.example \
  CINEKO_RELEASE_PUBLISH_TOKEN=publisher \
  scripts/register-browser-release.sh "$payload.invalid" >/dev/null 2>&1; then
  printf 'browser registration accepted malformed metadata\n' >&2
  exit 1
fi
printf 'Browser release checks passed\n'
