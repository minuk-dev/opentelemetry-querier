#!/usr/bin/env bash
# Run from the repository root at the commit being released.
set -euo pipefail

version=${1:?Usage: scripts/tag-go-modules.sh v0.1.0}
if [[ ! "$version" =~ ^v[01]\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$ ]]; then
  printf 'Unsupported Go module release version: %s\n' "$version" >&2
  exit 1
fi

modules=$(go work edit -json | jq -r '.Use[].DiskPath')
for dir in $modules; do
  case "$dir" in
    ./test/*|./cmd/querier) continue ;;
  esac

  tag="${dir#./}/$version"
  if target=$(git rev-parse --verify --quiet "$tag^{commit}"); then
    if [[ "$target" != "$(git rev-parse HEAD)" ]]; then
      printf 'Tag %s already points to %s\n' "$tag" "$target" >&2
      exit 1
    fi
  else
    git tag "$tag"
  fi
  printf 'refs/tags/%s\n' "$tag"
done
