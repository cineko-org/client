#!/usr/bin/env bash
set -euo pipefail

readonly document="docs/behavior-contract.md"
sources=()
while IFS= read -r source; do
	sources+=("$source")
done < <(git ls-files '*.go' '*.ts' '*.tsx' | grep -Ev '(^|/)vendor/|/assets/')

while IFS= read -r value; do
	case "$value" in
		/api/|/api/client|/api/types|/api/collections|/api/data|/api/seats|/api/test|/api/v1/booking/searchIfSeatData|/api/webhooks/*|/v1/slots|/v1/sessions*|/v1/devices/*|/v1/executions/*|/v1/catalog/auditoriums/*|/v1/catalog/seat-map-versions/*|/v1/)
			# Implementation-only upstream paths and dynamic Central path prefixes
			# are verified below as exact external templates where applicable.
			continue
			;;
	esac
	grep -Fq "$value" "$document" || {
		printf 'Client behavior contract is missing service point %s\n' "$value" >&2
		exit 1
	}
done < <(grep -Eho '/(api|v1)/[A-Za-z0-9_./:-]*' "${sources[@]}" | sort -u)

readonly templates=(
	'/v1/devices/{installationId}'
	'/v1/executions/{executionId}/heartbeat'
	'/v1/executions/{executionId}/result'
	'/v1/catalog/auditoriums/{auditoriumId}/seat-map'
	'/v1/catalog/auditoriums/{auditoriumId}/seat-map:request'
	'/v1/catalog/seat-map-versions/{versionId}'
	'/v1/presets'
	'/v1/presets/{id}'
	'/v1/monitors'
	'/v1/monitors/{id}'
	'/v1/reservations'
	'/v1/reservations/{id}'
	'/v1/external-operations'
	'/v1/external-operations/{id}'
	'/v1/app-events'
	'/v1/app-events/{id}'
)
for value in "${templates[@]}"; do
	grep -Fq "$value" "$document" || {
		printf 'Client behavior contract is missing templated service point %s\n' "$value" >&2
		exit 1
	}
done

readonly execution_event='execution.ready.v1'
grep -Fq 'const executionReadyEventType = "'"$execution_event"'"' internal/adapters/storage/centralhttp/event_stream.go || {
	printf 'Client event stream is missing canonical event type %s\n' "$execution_event" >&2
	exit 1
}

readonly installation_header='X-Cineko-Installation-Id'
grep -Eq 'installationIDHeader[[:space:]]*=[[:space:]]*"'"$installation_header"'"' internal/adapters/storage/centralhttp/store.go || {
	printf 'Client catalog store is missing canonical installation header %s\n' "$installation_header" >&2
	exit 1
}
grep -Fq "\`$installation_header\`" "$document" || {
	printf 'Client behavior contract is missing catalog header %s\n' "$installation_header" >&2
	exit 1
}
grep -Fq "\`$execution_event\`" "$document" || {
	printf 'Client behavior contract is missing event type %s\n' "$execution_event" >&2
	exit 1
}

while IFS= read -r value; do
	grep -Fq "\`$value\`" "$document" || {
		printf 'Client behavior contract is missing state %s\n' "$value" >&2
		exit 1
	}
done < <({
	git grep -Eho '[A-Za-z]+Status = "[a-z_]+"' -- 'internal/domain/*.go' 'internal/domain/**/*.go' |
		sed -E 's/.*"([a-z_]+)"/\1/'
	grep -Eo "status: '[a-z_]+'( \| '[a-z_]+')*" frontend/src/api/types.ts |
		grep -Eo "'[a-z_]+'" | tr -d "'"
	git grep -Eho 'Status(:| =)[[:space:]]*"[a-z_]+"' -- '*.go' ':!vendor/**' ':!*_test.go' |
		sed -E 's/.*"([a-z_]+)"/\1/'
} | sort -u)
