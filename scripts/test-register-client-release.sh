#!/usr/bin/env bash
set -euo pipefail

test_root="$(mktemp -d "${TMPDIR:-/tmp}/cineko-client-register-test.XXXXXX")"
readonly test_root
trap 'rm -rf "$test_root"' EXIT
readonly assets="$test_root/assets"
readonly payloads="$test_root/payloads.jsonl"
mkdir -p "$assets" "$test_root/bin"

printf 'darwin\n' >"$assets/cineko-client-v2.3.0-darwin-arm64.zip"
printf 'windows\n' >"$assets/cineko-client-v2.3.0-windows-amd64.zip"
printf 'linux\n' >"$assets/cineko-client-v2.3.0-linux-amd64.tar.gz"
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
cat "$source" >>"$FAKE_PAYLOADS"
printf '\n' >>"$FAKE_PAYLOADS"
printf '{}\n' >"$output"
printf '200'
SH
chmod +x "$test_root/bin/curl"

PATH="$test_root/bin:$PATH" \
FAKE_PAYLOADS="$payloads" \
CINEKO_CENTRAL_URL=https://central.example \
CINEKO_RELEASE_PUBLISH_TOKEN=publisher \
CINEKO_CLIENT_RELEASE_BASE=https://github.example/releases/download/v2.3.0 \
CINEKO_PROBE_BOOTSTRAP_PUBLIC_KEYS_JSON='{"primary":"public-key"}' \
CINEKO_MINIMUM_LAUNCHER_VERSION=1.1.9 \
CINEKO_BROWSER_REVISION=1228 \
CINEKO_PLAYWRIGHT_VERSION="$(bash scripts/playwright-version.sh driver)" \
	scripts/register-client-release.sh 2.3.0 2026-08-19T00:00:00Z "$assets" >/dev/null

jq -se --arg playwright "$(bash scripts/playwright-version.sh driver)" '
  length == 1 and
  (.[0].releases | length == 3) and
  all(.[0].releases[];
    .version == "2.3.0" and
    .minimumLauncherVersion == "1.1.9" and
    .minimumBrowserRevision == "1228" and
	.playwrightVersion == $playwright and
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
  CINEKO_MINIMUM_LAUNCHER_VERSION=1.1.9 \
  CINEKO_BROWSER_REVISION=1228 \
  CINEKO_PLAYWRIGHT_VERSION="$(bash scripts/playwright-version.sh driver)" \
	scripts/register-client-release.sh 2.3.0 2026-08-19T00:00:00Z "$assets" >/dev/null 2>&1; then
  printf 'registration accepted an incomplete Client release\n' >&2
  exit 1
fi
jq -se 'length == 1' "$payloads" >/dev/null

printf 'linux\n' >"$assets/cineko-client-v2.3.0-linux-amd64.tar.gz"
if PATH="$test_root/bin:$PATH" \
  FAKE_PAYLOADS="$payloads" \
  CINEKO_CENTRAL_URL=https://central.example \
  CINEKO_RELEASE_PUBLISH_TOKEN=publisher \
  CINEKO_CLIENT_RELEASE_BASE=https://github.example/releases/download/v2.3.0 \
  CINEKO_PROBE_BOOTSTRAP_PUBLIC_KEYS_JSON='{"primary":"public-key"}' \
  CINEKO_MINIMUM_LAUNCHER_VERSION=1.1.9 \
  CINEKO_BROWSER_REVISION=1228 \
  CINEKO_PLAYWRIGHT_VERSION=1.99.0 \
	scripts/register-client-release.sh 2.3.0 2026-08-19T00:00:00Z "$assets" >/dev/null 2>&1; then
  printf 'registration accepted a Playwright target that differs from the locked driver\n' >&2
  exit 1
fi
jq -se 'length == 1' "$payloads" >/dev/null
printf 'Client release registration checks passed\n'
