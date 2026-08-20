#!/usr/bin/env bash
set -euo pipefail

test_root="$(mktemp -d "${TMPDIR:-/tmp}/cineko-client-register-test.XXXXXX")"
readonly test_root
trap 'rm -rf "$test_root"' EXIT
readonly assets="$test_root/assets"
readonly payloads="$test_root/payloads.jsonl"
readonly compatibility="$test_root/compatibility.json"
mkdir -p "$assets" "$test_root/bin"

printf 'darwin\n' >"$assets/cineko-client-v2.3.0-darwin-arm64.tar.gz"
printf 'windows\n' >"$assets/cineko-client-v2.3.0-windows-amd64.zip"
printf 'linux\n' >"$assets/cineko-client-v2.3.0-linux-amd64.tar.gz"
cat >"$compatibility" <<'JSON'
{"minimumLauncherVersion":"1.1.9","minimumBrowserRevision":"1228","playwrightVersion":"1.62.1","protocol":3}
JSON
cat >"$test_root/bin/curl" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
payload=''
while [[ $# -gt 0 ]]; do
  if [[ "$1" == '--data' ]]; then shift; payload="$1"; fi
  shift
done
printf '%s\n' "$payload" >>"$FAKE_PAYLOADS"
printf '{"generation":7}\n'
SH
chmod +x "$test_root/bin/curl"

PATH="$test_root/bin:$PATH" \
FAKE_PAYLOADS="$payloads" \
CINEKO_CENTRAL_URL=https://central.example \
CINEKO_RELEASE_PUBLISH_TOKEN=publisher \
CINEKO_CLIENT_RELEASE_BASE=https://github.example/releases/download/v2.3.0 \
CINEKO_PROBE_BOOTSTRAP_PUBLIC_KEYS_JSON='{"primary":"public-key"}' \
  scripts/register-client-release.sh 2.3.0 2026-08-19T00:00:00Z "$assets" "$compatibility" >/dev/null

jq -se '
  length == 1 and
  .[0].schemaVersion == 2 and
  (. [0].payload.releases | length == 3) and
  all(.[0].payload.releases[];
    .version == "2.3.0" and .protocol == 3 and
    .minimumLauncherVersion == "1.1.9" and
    .minimumBrowserRevision == "1228" and
    .playwrightVersion == "1.62.1" and
    .probeBootstrapPublicKeys.primary == "public-key" and
    (.artifact.url | startswith("https://github.example/releases/download/v2.3.0/")) and
    (.artifact.sha256 | length == 64)
  )
' "$payloads" >/dev/null

rm "$assets/cineko-client-v2.3.0-linux-amd64.tar.gz"
if PATH="$test_root/bin:$PATH" \
  FAKE_PAYLOADS="$payloads" \
  CINEKO_CENTRAL_URL=https://central.example \
  CINEKO_RELEASE_PUBLISH_TOKEN=publisher \
  CINEKO_CLIENT_RELEASE_BASE=https://github.example/releases/download/v2.3.0 \
  CINEKO_PROBE_BOOTSTRAP_PUBLIC_KEYS_JSON='{"primary":"public-key"}' \
  scripts/register-client-release.sh 2.3.0 2026-08-19T00:00:00Z "$assets" "$compatibility" >/dev/null 2>&1; then
  printf 'registration accepted an incomplete Client release\n' >&2
  exit 1
fi
[[ "$(wc -l <"$payloads" | tr -d ' ')" == 1 ]]

jq '.playwrightVersion = "1.99.0"' "$compatibility" >"$compatibility.tmp"
mv "$compatibility.tmp" "$compatibility"
printf 'linux\n' >"$assets/cineko-client-v2.3.0-linux-amd64.tar.gz"
if PATH="$test_root/bin:$PATH" \
  FAKE_PAYLOADS="$payloads" \
  CINEKO_CENTRAL_URL=https://central.example \
  CINEKO_RELEASE_PUBLISH_TOKEN=publisher \
  CINEKO_CLIENT_RELEASE_BASE=https://github.example/releases/download/v2.3.0 \
  CINEKO_PROBE_BOOTSTRAP_PUBLIC_KEYS_JSON='{"primary":"public-key"}' \
  scripts/register-client-release.sh 2.3.0 2026-08-19T00:00:00Z "$assets" "$compatibility" >/dev/null 2>&1; then
  printf 'registration accepted a Playwright target that differs from the locked driver\n' >&2
  exit 1
fi
[[ "$(wc -l <"$payloads" | tr -d ' ')" == 1 ]]
printf 'Client release registration checks passed\n'
