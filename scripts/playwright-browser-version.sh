#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  printf 'usage: %s BROWSERS_JSON revision|version\n' "$0" >&2
  exit 2
fi
readonly browsers_json="$1"
readonly field="$2"

case "$field" in
  revision) jq_filter='.browsers[] | select(.name == "chromium") | .revision' ;;
  version) jq_filter='.browsers[] | select(.name == "chromium") | .browserVersion' ;;
  *) exit 2 ;;
esac
value="$(jq -er "$jq_filter | strings | select(length > 0)" "$browsers_json")"
readonly value
case "$field" in
  revision) [[ "$value" =~ ^[1-9][0-9]*$ ]] ;;
  version) [[ "$value" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] ;;
esac || {
  printf 'invalid Chromium %s in Playwright browsers.json\n' "$field" >&2
  exit 1
}
printf '%s\n' "$value"
