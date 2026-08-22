#!/usr/bin/env bash
set -euo pipefail

test_root="$(mktemp -d "${TMPDIR:-/tmp}/cineko-browser-release-test.XXXXXX")"
readonly test_root
trap 'rm -rf "$test_root"' EXIT
readonly browsers_json="$test_root/browsers.json"

cat >"$browsers_json" <<'JSON'
{"browsers":[{"name":"chromium","revision":"1234","browserVersion":"151.0.7922.34"}]}
JSON
[[ "$(scripts/playwright-browser-version.sh "$browsers_json" revision)" == 1234 ]]
[[ "$(scripts/playwright-browser-version.sh "$browsers_json" version)" == 151.0.7922.34 ]]

printf 'Browser metadata checks passed\n'
