#!/usr/bin/env bash
set -euo pipefail

test_root="$(mktemp -d "${TMPDIR:-/tmp}/cineko-release-registry-http-test.XXXXXX")"
readonly test_root
trap 'rm -rf "$test_root"' EXIT
readonly payload="$test_root/payload.json"
readonly response="$test_root/response.json"
readonly attempts="$test_root/attempts"
readonly sleeps="$test_root/sleeps"
readonly stderr_log="$test_root/stderr.log"
mkdir -p "$test_root/bin"
printf '{}\n' >"$payload"

cat >"$test_root/bin/curl" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
output=''
while [[ $# -gt 0 ]]; do
  if [[ "$1" == '--output' ]]; then shift; output="$1"; fi
  shift
done
count=0
if [[ -s "$FAKE_ATTEMPTS" ]]; then read -r count <"$FAKE_ATTEMPTS"; fi
count=$((count + 1))
printf '%d\n' "$count" >"$FAKE_ATTEMPTS"
IFS=',' read -r -a outcomes <<<"$FAKE_OUTCOMES"
outcome="${outcomes[$((count - 1))]}"
if [[ "$outcome" == network ]]; then
  printf 'simulated network failure\n' >&2
  exit 7
fi
if [[ "$outcome" == 2* ]]; then
  printf '{}\n' >"$output"
else
  printf '{"error":{"code":"simulated_%s","message":"safe failure","retryable":false,"requestId":"req_test"}}\n' \
    "$outcome" >"$output"
fi
printf '%s' "$outcome"
SH
cat >"$test_root/bin/sleep" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$1" >>"$FAKE_SLEEPS"
SH
chmod +x "$test_root/bin/curl" "$test_root/bin/sleep"

run_publish() {
  PATH="$test_root/bin:$PATH" \
  FAKE_ATTEMPTS="$attempts" \
  FAKE_SLEEPS="$sleeps" \
  FAKE_OUTCOMES="$1" \
  CINEKO_CENTRAL_URL=https://central.example \
  CINEKO_RELEASE_PUBLISH_TOKEN=publisher \
    scripts/post-release-registry.sh playwright "$payload" "$response"
}

reset_case() {
  : >"$attempts"
  : >"$sleeps"
  : >"$stderr_log"
}

reset_case
if run_publish 409 2>"$stderr_log"; then
  printf 'HTTP 409 was accepted\n' >&2
  exit 1
fi
[[ "$(cat "$attempts")" == 1 ]]
[[ ! -s "$sleeps" ]]
grep -Fq 'non-retryable HTTP 409' "$stderr_log"
grep -Fq '"code":"simulated_409"' "$stderr_log"

reset_case
run_publish 500,503,200 2>"$stderr_log"
[[ "$(cat "$attempts")" == 3 ]]
[[ "$(paste -sd, "$sleeps")" == 1,2 ]]

reset_case
run_publish network,200 2>"$stderr_log"
[[ "$(cat "$attempts")" == 2 ]]
[[ "$(cat "$sleeps")" == 1 ]]

reset_case
if run_publish 429 2>"$stderr_log"; then
  printf 'HTTP 429 was accepted\n' >&2
  exit 1
fi
[[ "$(cat "$attempts")" == 1 ]]
[[ ! -s "$sleeps" ]]

reset_case
if run_publish 500,500,500,500 2>"$stderr_log"; then
  printf 'exhausted HTTP 500 retries were accepted\n' >&2
  exit 1
fi
[[ "$(cat "$attempts")" == 4 ]]
[[ "$(paste -sd, "$sleeps")" == 1,2,4 ]]
grep -Fq 'after 4 attempts' "$stderr_log"
grep -Fq '"code":"simulated_500"' "$stderr_log"

printf 'Release registry HTTP retry checks passed\n'
