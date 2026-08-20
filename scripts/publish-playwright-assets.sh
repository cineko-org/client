#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 ]]; then
  printf 'usage: %s TAG VERSION ASSETS_DIR\n' "$0" >&2
  exit 2
fi
: "${GH_TOKEN:?required}"
: "${GITHUB_REPOSITORY:?required}"
: "${CINEKO_RELEASE_TARGET_SHA:?required}"
readonly tag="$1"
readonly version="${2#v}"
readonly assets_dir="$3"

if [[ "$tag" != "playwright-v${version}" || ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  printf 'Playwright release tag and version do not match\n' >&2
  exit 2
fi

readonly expected=(
  "cineko-playwright-${version}-darwin-arm64.tar.gz"
  "cineko-playwright-${version}-windows-amd64.zip"
  "cineko-playwright-${version}-linux-amd64.tar.gz"
)
for filename in "${expected[@]}"; do
  [[ -f "${assets_dir}/${filename}" ]] || {
    printf 'Playwright artifact is missing: %s\n' "$filename" >&2
    exit 1
  }
done

if ! gh release view "$tag" --repo "$GITHUB_REPOSITORY" >/dev/null 2>&1; then
  gh release create "$tag" --repo "$GITHUB_REPOSITORY" \
    --target "$CINEKO_RELEASE_TARGET_SHA" --title "Playwright Runtime v${version}" \
    --notes "Portable Playwright runtime required by Cineko Client." \
    --latest=false
fi

temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/cineko-playwright-assets.XXXXXX")"
readonly temporary_root
trap 'rm -rf "$temporary_root"' EXIT
for filename in "${expected[@]}"; do
  if gh release download "$tag" --repo "$GITHUB_REPOSITORY" --pattern "$filename" --dir "$temporary_root" 2>/dev/null; then
    if ! cmp -s "${assets_dir}/${filename}" "${temporary_root}/${filename}"; then
      printf 'immutable Playwright release asset differs: %s\n' "$filename" >&2
      exit 1
    fi
    continue
  fi
  gh release upload "$tag" "${assets_dir}/${filename}" --repo "$GITHUB_REPOSITORY"
done
