#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 ]]; then
	printf 'usage: %s BROWSER_TAG CANDIDATE_MANIFEST EXISTING_MANIFEST\n' "$0" >&2
	exit 2
fi

readonly browser_tag="$1"
readonly candidate_manifest="$2"
readonly existing_manifest="$3"
if [[ ! "$browser_tag" =~ ^browser-r[1-9][0-9]*-([0-9a-f]{64})$ ]]; then
	printf 'browser tag does not contain a valid semantic fingerprint: %s\n' "$browser_tag" >&2
	exit 2
fi
readonly expected_fingerprint="${BASH_REMATCH[1]}"

fingerprint() {
	GOWORK=off go run -mod=vendor ./cmd/releasecontract fingerprint browser "$1"
}

candidate_fingerprint="$(fingerprint "$candidate_manifest")"
readonly candidate_fingerprint
existing_fingerprint="$(fingerprint "$existing_manifest")"
readonly existing_fingerprint
if [[ "$candidate_fingerprint" != "$expected_fingerprint" ]]; then
	printf 'candidate browser manifest does not match its content-addressed tag\n' >&2
	exit 1
fi
if [[ "$existing_fingerprint" != "$expected_fingerprint" ]]; then
	printf 'existing browser manifest does not match its content-addressed tag\n' >&2
	exit 1
fi
