#!/usr/bin/env bash
set -euo pipefail

readonly document="docs/behavior-contract.md"

list_sources() {
	git ls-files --cached --others --exclude-standard -- "$@" |
		sort -u |
		grep -Ev '(^|/)(vendor|build|node_modules|storybook-static)/|/assets/'
}

sources=()
while IFS= read -r source; do
	case "$source" in
		*_test.go|*.test.ts|*.test.tsx) continue ;;
	esac
	if [[ -f "$source" ]]; then
		sources+=("$source")
	fi
done < <(list_sources '*.go' '*.ts' '*.tsx')

# Contract checks inspect the complete working tree, including newly-created
# task sources. Tests and generated/build trees are intentionally excluded so
# negative fixtures and third-party code cannot satisfy or trip production
# policy checks.
contract_sources=()
while IFS= read -r source; do
	case "$source" in
		*_test.go|*.test.ts|*.test.tsx|*.test.mjs|scripts/test-*.sh|scripts/verify-behavior-contract.sh) continue ;;
	esac
	if [[ -f "$source" ]]; then
		contract_sources+=("$source")
	fi
done < <(list_sources '*.go' '*.ts' '*.tsx' '*.sh' '*.mjs' '*.proto')

owned_boundary_sources=()
proto_owned_sources=()
for source in "${contract_sources[@]}"; do
	case "$source" in
		desktop_*.go|frontend/src/api/*|internal/adapters/eventhook/*|internal/adapters/storage/centralhttp/*|internal/application/*|internal/interfaces/webui/*|cmd/releasecontract/*)
			owned_boundary_sources+=("$source")
			;;
	esac
	case "$source" in
		desktop_identity.go|internal/adapters/eventhook/*|cmd/releasecontract/*)
			# Local OS identity manifests and third-party webhook/release APIs are
			# external wire formats, not Cineko-owned service contracts.
			;;
		desktop_*.go|internal/adapters/storage/centralhttp/*|internal/application/*|internal/interfaces/webui/*)
			proto_owned_sources+=("$source")
			;;
	esac
done

if ((${#owned_boundary_sources[@]} > 0)) &&
	grep -En '^[[:space:]]*(export[[:space:]]+)?(interface|type)[[:space:]]+[A-Za-z0-9_]*(DTO|Request|Response|Payload|Envelope)([[:space:]]|$)|^type[[:space:]]+[A-Za-z0-9_]*(DTO|Request|Response|Payload|Envelope)[[:space:]]+struct([[:space:]]|$)' \
		"${owned_boundary_sources[@]}"; then
	printf 'Client owned boundaries must use generated protobuf messages directly, not custom DTO aliases\n' >&2
	exit 1
fi

if ((${#proto_owned_sources[@]} > 0)) &&
	grep -En '"encoding/json"|json:"' "${proto_owned_sources[@]}"; then
	printf 'Client owned service boundaries must use generated ProtoJSON, not custom JSON carriers\n' >&2
	exit 1
fi

for retired_source in internal/application/proto.go internal/application/resources.go; do
	if [[ -e "$retired_source" ]]; then
		printf 'Client application services must not keep domain/protobuf conversion layers: %s\n' "$retired_source" >&2
		exit 1
	fi
done

if grep -REn '^type[[:space:]]+(Preset|MonitorJob|Reservation|BookingDraft|CancellationDraft|ExternalOperation)[[:space:]]+struct' internal/domain; then
	printf 'Client domain must not duplicate protobuf-owned state or mutation resources\n' >&2
	exit 1
fi

application_sources=()
while IFS= read -r source; do
	case "$source" in
		*_test.go) continue ;;
	esac
	[[ -f "$source" ]] && application_sources+=("$source")
done < <(list_sources 'internal/application/*.go')
if ((${#application_sources[@]} > 0)) &&
	grep -En 'domain\.(Preset|MonitorJob|Reservation|AppEvent)([^A-Za-z0-9_]|$)' "${application_sources[@]}"; then
	printf 'Client application state must use generated protobuf resources directly\n' >&2
	exit 1
fi
if grep -En 'domain\.' internal/application/ports.go; then
	printf 'Client application ports must expose generated protobuf messages, not private domain carriers\n' >&2
	exit 1
fi
if grep -En '^type[[:space:]]+(TheaterRef|AuditoriumObservation|ShowtimeQuery)[[:space:]]+struct' internal/application/ports.go; then
	printf 'Client application ports must not define duplicate catalog command or result carriers\n' >&2
	exit 1
fi

if grep -REn '^type[[:space:]]+(EventTone|Target)[[:space:]]+struct|^type[[:space:]]+EventTone[[:space:]]+string' \
	internal/domain internal/adapters/eventhook; then
	printf 'Client event boundaries must use generated AppEvent and WebhookTarget messages directly\n' >&2
	exit 1
fi

if ((${#contract_sources[@]} > 0)) &&
	grep -En '^type[[:space:]]+[A-Za-z0-9_]+[[:space:]]*=[[:space:]]*[A-Za-z0-9_]+pb\.' "${contract_sources[@]}"; then
	printf 'Client sources must not rename generated protobuf messages through Go type aliases\n' >&2
	exit 1
fi

if ((${#contract_sources[@]} > 0)) &&
	grep -En 'github\.com/cineko-org/contracts/v[0-9]+|github\.com/cineko-org/contracts/internal/protocol' "${contract_sources[@]}"; then
	printf 'Client sources must import only the latest generated protobuf packages\n' >&2
	exit 1
fi

if ((${#owned_boundary_sources[@]} > 0)); then
	owned_aliases="$(
		grep -En '^[[:space:]]*(export[[:space:]]+)?type[[:space:]]+[A-Za-z0-9_]+[[:space:]]*=' "${owned_boundary_sources[@]}" || true
	)"
	if [[ -n "$owned_aliases" ]]; then
		printf '%s\n' "$owned_aliases" >&2
		printf 'Client owned boundaries must not rename generated protobuf messages through type aliases\n' >&2
		exit 1
	fi
fi

if [[ -e release-compatibility.json ]] ||
	grep -RFn 'release-compatibility.json' .github scripts docs --exclude='verify-behavior-contract.sh'; then
	printf 'Client release compatibility must be written directly to the generated ClientRelease contract\n' >&2
	exit 1
fi

if grep -En '(^|[^A-Za-z0-9_])(LaunchConfig|ProtoBody|TransportOptions)([^A-Za-z0-9_]|$)' \
	internal/adapters/storage/centralhttp/store.go desktop_launch.go frontend/src/api/client.ts; then
	printf 'Client launch and frontend transport boundaries must not duplicate generated protobuf carriers\n' >&2
	exit 1
fi

grep -Fq 'envelope *clientpb.LaunchEnvelope' internal/adapters/storage/centralhttp/store.go || {
	printf 'Client launch boundary must accept the generated LaunchEnvelope directly\n' >&2
	exit 1
}
grep -Fq 'centralstore.OpenLaunched(ctx, payload' desktop_launch.go || {
	printf 'Desktop startup must pass the generated LaunchEnvelope without field copying\n' >&2
	exit 1
}

release_sources=()
for source in "${contract_sources[@]}"; do
	case "$source" in
		scripts/*release*.sh|scripts/*release*.mjs) release_sources+=("$source") ;;
	esac
done
if ((${#release_sources[@]} > 0)) && grep -En 'jq[[:space:]]+-[A-Za-z]*[nc]([[:space:]]|$)' "${release_sources[@]}"; then
	printf 'Client release publishers must not hand-build owned API JSON\n' >&2
	exit 1
fi

if ((${#contract_sources[@]} > 0)) &&
	grep -En '(^|[^A-Za-z0-9_])(schemaVersion|schema_version|protocolVersion|protocol_version)([^A-Za-z0-9_]|$)' "${contract_sources[@]}"; then
	printf 'Client sources must use the latest generated protobuf contract without schema or protocol version fields\n' >&2
	exit 1
fi

proto_sources=()
while IFS= read -r source; do
	[[ -f "$source" ]] && proto_sources+=("$source")
done < <(list_sources '*.proto')
if ((${#proto_sources[@]} > 0)) && grep -En '^[[:space:]]*reserved([[:space:]]|$)' "${proto_sources[@]}"; then
	printf 'Client-owned protobuf contracts must not reserve fields; consumers use only the latest contract\n' >&2
	exit 1
fi

grep -Fq 'github.com/cineko-org/contracts/gen/go/cineko/release' cmd/releasecontract/main.go || {
	printf 'Client release publisher is not using the generated release contract\n' >&2
	exit 1
}
grep -Fq 'github.com/cineko-org/contracts/gen/go/cineko/service' cmd/releasecontract/main.go || {
	printf 'Client release response validation is not using the generated service contract\n' >&2
	exit 1
}
for source in frontend/src/api/client.ts frontend/src/api/desktop.ts; do
	if [[ -f "$source" ]] && grep -En 'JSON\.stringify\(' "$source" | grep -Fv 'toJson('; then
		printf 'Client API serialization must pass generated protobuf messages through ProtoJSON: %s\n' "$source" >&2
		exit 1
	fi
done

while IFS= read -r value; do
	case "$value" in
		/api/|/api/client|/api/desktop|/api/proto|/api/resources|/api/types|/api/collections|/api/data|/api/seats|/api/test|/api/catalog/sync|/api/catalog/auditoriums|/api/v1/booking/searchIfSeatData|/api/webhooks/*|/v1/slots|/v1/sessions*|/v1/devices/*|/v1/executions/*|/v1/catalog/auditoriums/*|/v1/)
			# Implementation-only upstream paths and dynamic Central path prefixes
			# are verified below as exact external templates where applicable. The
			# retired local catalog scan paths occur only in negative route tests.
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
	'/v1/catalog/auditoriums/{auditoriumId}/seat-map:resolve'
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

readonly execution_event='execution.ready'
grep -Fq 'const executionReadyEventType = "'"$execution_event"'"' internal/adapters/storage/centralhttp/event_stream.go || {
	printf 'Client event stream is missing canonical event type %s\n' "$execution_event" >&2
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
	for source in "${sources[@]}"; do
		[[ "$source" == internal/domain/*.go && "$source" != *_test.go ]] || continue
		grep -Eho '[A-Za-z]+Status = "[a-z_]+"' "$source" || true
	done
	for source in "${sources[@]}"; do
		[[ "$source" == *.go && "$source" != *_test.go ]] || continue
		grep -Eho 'Status(:| =)[[:space:]]*"[a-z_]+"' "$source" || true
	done
} | sed -E 's/.*"([a-z_]+)"/\1/' | sort -u)
