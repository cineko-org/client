#!/usr/bin/env bash
set -euo pipefail

test_root="$(mktemp -d "${TMPDIR:-/tmp}/cineko-playwright-publish-test.XXXXXX")"
readonly test_root
trap 'rm -rf "$test_root"' EXIT
readonly assets="$test_root/assets"
readonly remote="$test_root/remote"
readonly calls="$test_root/calls"
mkdir -p "$assets" "$remote" "$test_root/bin"

for platform in darwin-arm64 windows-amd64 linux-amd64; do
  extension=tar.gz
  [[ "$platform" == windows-amd64 ]] && extension=zip
  printf '%s\n' "$platform" >"$assets/cineko-playwright-1.62.1-${platform}.${extension}"
done

cat >"$test_root/bin/gh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_CALLS"
shift # release
command="$1"
shift
case "$command" in
  view)
    [[ -f "$FAKE_REMOTE/.created" ]]
    ;;
  create)
    touch "$FAKE_REMOTE/.created"
    ;;
  upload)
    tag="$1"
    artifact="$2"
    cp "$artifact" "$FAKE_REMOTE/$(basename "$artifact")"
    ;;
  download)
    tag="$1"
    shift
    pattern=''
    directory=''
    while [[ $# -gt 0 ]]; do
      case "$1" in
        --pattern) shift; pattern="$1" ;;
        --dir) shift; directory="$1" ;;
      esac
      shift
    done
    [[ -f "$FAKE_REMOTE/$pattern" ]]
    mkdir -p "$directory"
    cp "$FAKE_REMOTE/$pattern" "$directory/$pattern"
    ;;
  *) exit 2 ;;
esac
SH
chmod +x "$test_root/bin/gh"

run_publisher() {
  PATH="$test_root/bin:$PATH" \
  FAKE_CALLS="$calls" \
  FAKE_REMOTE="$remote" \
  GH_TOKEN=test-token \
  GITHUB_REPOSITORY=cineko-org/client \
  CINEKO_RELEASE_TARGET_SHA=0123456789abcdef \
    scripts/publish-playwright-assets.sh playwright-v1.62.1 1.62.1 "$assets"
}

run_publisher
[[ "$(rg -c '^release create ' "$calls")" == 1 ]]
[[ "$(rg -c '^release upload ' "$calls")" == 3 ]]

run_publisher
[[ "$(rg -c '^release create ' "$calls")" == 1 ]]
[[ "$(rg -c '^release upload ' "$calls")" == 3 ]]

printf 'changed\n' >"$assets/cineko-playwright-1.62.1-linux-amd64.tar.gz"
if run_publisher >/dev/null 2>&1; then
  printf 'publisher replaced or accepted a changed immutable asset\n' >&2
  exit 1
fi
[[ "$(rg -c '^release upload ' "$calls")" == 3 ]]
printf 'Playwright immutable asset publishing checks passed\n'
