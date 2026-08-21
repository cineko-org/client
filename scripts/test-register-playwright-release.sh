#!/usr/bin/env bash
set -euo pipefail

test_root="$(mktemp -d "${TMPDIR:-/tmp}/cineko-playwright-register-test.XXXXXX")"
readonly test_root
trap 'rm -rf "$test_root"' EXIT
readonly assets="$test_root/assets"
readonly payloads="$test_root/payloads.jsonl"
mkdir -p "$assets" "$test_root/bin"

printf 'darwin\n' >"$assets/cineko-playwright-1.62.1-darwin-arm64.tar.gz"
printf 'windows\n' >"$assets/cineko-playwright-1.62.1-windows-amd64.zip"
printf 'linux\n' >"$assets/cineko-playwright-1.62.1-linux-amd64.tar.gz"
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
SH
chmod +x "$test_root/bin/curl"

PATH="$test_root/bin:$PATH" \
FAKE_PAYLOADS="$payloads" \
CINEKO_CENTRAL_URL=https://central.example \
CINEKO_RELEASE_PUBLISH_TOKEN=publisher \
CINEKO_PLAYWRIGHT_RELEASE_BASE=https://github.example/releases/download/playwright-v1.62.1 \
  scripts/register-playwright-release.sh 1.62.1 2026-08-21T00:00:00Z "$assets" >/dev/null

jq -se '
  length == 1 and
  (.[0].releases | length == 3) and
  ([.[0].releases[] | .platform + "/" + .architecture] | sort ==
    ["darwin/arm64", "linux/amd64", "windows/amd64"]) and
  all(.[0].releases[];
    .version == "1.62.1" and
    (.artifact.url | startswith("https://github.example/releases/download/playwright-v1.62.1/")) and
    (.artifact.sha256 | length == 64)
  )
' "$payloads" >/dev/null

rm "$assets/cineko-playwright-1.62.1-linux-amd64.tar.gz"
if PATH="$test_root/bin:$PATH" \
  FAKE_PAYLOADS="$payloads" \
  CINEKO_CENTRAL_URL=https://central.example \
  CINEKO_RELEASE_PUBLISH_TOKEN=publisher \
  CINEKO_PLAYWRIGHT_RELEASE_BASE=https://github.example/releases/download/playwright-v1.62.1 \
  scripts/register-playwright-release.sh 1.62.1 2026-08-21T00:00:00Z "$assets" >/dev/null 2>&1; then
  printf 'registration accepted an incomplete Playwright release\n' >&2
  exit 1
fi
[[ "$(wc -l <"$payloads" | tr -d ' ')" == 1 ]]
printf 'Playwright release registration checks passed\n'
