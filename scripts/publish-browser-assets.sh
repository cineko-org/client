#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 || ! "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  printf 'usage: %s CHROME_VERSION ASSETS_DIR\n' "$0" >&2
  exit 2
fi
: "${GITHUB_REPOSITORY:?required}"
: "${CINEKO_RELEASE_TARGET_SHA:?required}"
readonly tag="chrome-v$1"
readonly assets_dir="$2"
readonly filenames=(chrome-mac-arm64.zip chrome-linux64.zip chrome-win64.zip)

for filename in "${filenames[@]}"; do
  [[ -s "$assets_dir/$filename" ]] || { printf 'missing browser archive: %s\n' "$filename" >&2; exit 1; }
done

if ! gh release view "$tag" --repo "$GITHUB_REPOSITORY" >/dev/null 2>&1; then
  gh release create "$tag" --repo "$GITHUB_REPOSITORY" --draft \
    --target "$CINEKO_RELEASE_TARGET_SHA" --title "Chrome for Testing $1" \
    --notes "Unmodified official Chrome for Testing archives used by Cineko." --latest=false
fi

release="$(gh api "repos/$GITHUB_REPOSITORY/releases/tags/$tag")"
for filename in "${filenames[@]}"; do
  asset="$(jq -c --arg name "$filename" '.assets[] | select(.name == $name)' <<<"$release")"
  if [[ -z "$asset" ]]; then
    [[ "$(jq -r '.draft' <<<"$release")" == true ]] || {
      printf 'published browser release is incomplete: %s\n' "$filename" >&2
      exit 1
    }
    gh release upload "$tag" "$assets_dir/$filename" --repo "$GITHUB_REPOSITORY"
  fi
done

# GitHub calculates the digest from the uploaded bytes. Never overwrite an
# existing archive, including when resuming an interrupted draft publication.
release="$(gh api "repos/$GITHUB_REPOSITORY/releases/tags/$tag")"
for filename in "${filenames[@]}"; do
  digest="sha256:$(sha256sum "$assets_dir/$filename" | awk '{print $1}')"
  size="$(wc -c <"$assets_dir/$filename" | tr -d '[:space:]')"
  jq -e --arg name "$filename" --arg digest "$digest" --argjson size "$size" \
    'any(.assets[]; .name == $name and .digest == $digest and .size == $size)' <<<"$release" >/dev/null || {
    printf 'immutable browser asset digest or size mismatch: %s\n' "$filename" >&2
    exit 1
  }
done
if [[ "$(jq -r '.draft' <<<"$release")" == true ]]; then
  gh release edit "$tag" --repo "$GITHUB_REPOSITORY" --draft=false --latest=false
fi
