#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 ]]; then
  printf 'usage: %s COMPONENT PAYLOAD_JSON RESPONSE_JSON\n' "$0" >&2
  exit 2
fi

: "${CINEKO_CENTRAL_URL:?required}"
: "${CINEKO_RELEASE_PUBLISH_TOKEN:?required}"

readonly component="$1"
readonly payload="$2"
readonly response="$3"
readonly endpoint="${CINEKO_CENTRAL_URL%/}/v1/release-registry/${component}"
readonly max_attempts=4

for command in curl jq; do
  command -v "$command" >/dev/null || {
    printf '%s is required on the release publisher runner\n' "$command" >&2
    exit 2
  }
done

# Emit only the public API error fields instead of forwarding an arbitrary body.
print_central_error() {
  if [[ ! -s "$response" ]]; then
    printf 'Central returned an empty error response\n' >&2
    return
  fi
  if ! jq -ce '
    if (.error | type) != "object" then error("missing error object") else
      {error: {
        code: (.error.code // "unknown"),
        message: (.error.message // "Central request failed"),
        retryable: (.error.retryable // false),
        requestId: (.error.requestId // "")
      }}
    end
  ' "$response" >&2; then
    printf 'Central returned a non-ProtoJSON error response\n' >&2
  fi
}

attempt=1
# Replaying the immutable payload is safe only after transport failures and 5xx responses.
while (( attempt <= max_attempts )); do
  truncate -s 0 "$response"
  http_status=''
  if ! http_status="$(curl --silent --show-error \
    --request POST \
    --header "Authorization: Bearer ${CINEKO_RELEASE_PUBLISH_TOKEN}" \
    --header 'Content-Type: application/json' \
    --data-binary "@$payload" \
    --output "$response" \
    --write-out '%{http_code}' \
    "$endpoint")"; then
    if (( attempt == max_attempts )); then
      printf 'Central release registration failed after %d network attempts\n' "$attempt" >&2
      exit 1
    fi
    printf 'Central release registration network failure; retrying (%d/%d)\n' \
      "$attempt" "$max_attempts" >&2
  elif [[ "$http_status" =~ ^2[0-9][0-9]$ ]]; then
    exit 0
  elif [[ "$http_status" =~ ^5[0-9][0-9]$ ]]; then
    if (( attempt == max_attempts )); then
      printf 'Central release registration failed with HTTP %s after %d attempts\n' \
        "$http_status" "$attempt" >&2
      print_central_error
      exit 1
    fi
    printf 'Central release registration returned HTTP %s; retrying (%d/%d)\n' \
      "$http_status" "$attempt" "$max_attempts" >&2
  else
    printf 'Central release registration failed with non-retryable HTTP %s\n' \
      "${http_status:-unknown}" >&2
    print_central_error
    exit 1
  fi

  sleep "$((1 << (attempt - 1)))"
  attempt=$((attempt + 1))
done
