#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <base-sha> <head-sha>" >&2
  exit 2
fi

base_sha=$1
head_sha=$2
allowed='^(\.release-please-manifest\.json|CHANGELOG\.md|VERSION|frontend/package(-lock)?\.json|wails\.json)$'

changed_files=()
while IFS= read -r path; do
  changed_files+=("$path")
done < <(git diff --name-only "$base_sha" "$head_sha" --)
if [[ ${#changed_files[@]} -eq 0 ]]; then
  echo "Release Please PR has no changes" >&2
  exit 1
fi
for path in "${changed_files[@]}"; do
  if [[ ! $path =~ $allowed ]]; then
    echo "Release Please PR changes a non-release file: $path" >&2
    exit 1
  fi
done

version=$(git show "$head_sha:VERSION" | tr -d '\r\n')
if [[ ! $version =~ ^[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]]; then
  echo "VERSION is not a semantic version: $version" >&2
  exit 1
fi

check_version() {
  local source=$1
  local actual=$2
  if [[ $actual != "$version" ]]; then
    echo "$source version $actual does not match VERSION $version" >&2
    exit 1
  fi
}

check_version manifest "$(git show "$head_sha:.release-please-manifest.json" | jq -er '.["."] | strings')"
check_version package "$(git show "$head_sha:frontend/package.json" | jq -er '.version | strings')"
check_version lock "$(git show "$head_sha:frontend/package-lock.json" | jq -er '.version | strings')"
check_version lock_root "$(git show "$head_sha:frontend/package-lock.json" | jq -er '.packages[""].version | strings')"
check_version wails "$(git show "$head_sha:wails.json" | jq -er '.info.productVersion | strings')"

if ! git show "$head_sha:CHANGELOG.md" | grep -F "## [$version]" >/dev/null; then
  echo "CHANGELOG.md does not contain release $version" >&2
  exit 1
fi

echo "Release Please metadata is consistent for $version"
