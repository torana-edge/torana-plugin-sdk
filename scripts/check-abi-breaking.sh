#!/usr/bin/env bash
# ABI-v1 compatibility against main, scoped to the canonical schema.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

against=${1:-'https://github.com/torana-edge/torana-plugin-sdk.git#branch=main'}
command -v buf >/dev/null || { echo "buf is required" >&2; exit 1; }

test -f proto/torana/v1/torana.proto || {
  echo "::error::proto/torana/v1/torana.proto is missing" >&2
  exit 1
}

# The clean public v1 contract is being established by one pre-release PR.
# Once that schema exists on main, every subsequent PR takes the normal
# compatibility path below. Explicit comparison targets are never skipped.
if [[ $# -eq 0 ]] && ! git cat-file -e refs/remotes/origin/main:proto/torana/v1/torana.proto 2>/dev/null; then
  echo "establishing the initial v1 compatibility baseline"
  exit 0
fi

if buf breaking --against "$against" --path proto/torana/v1; then
  exit 0
fi

# Before 1.0, SemVer permits compatibility breaks in a new minor release.
# Keep patch releases and same-minor development strict, but let a deliberate
# X.Y.0 minor bump establish the next pre-release baseline. The release tag and
# Rust crate version are coordinated by RELEASING.md and the release workflow.
# Explicit comparison targets remain strict so local callers can audit any
# chosen baseline without this release-policy exception.
if [[ $# -eq 0 ]]; then
  crate=$(sed -n 's/^version = "\([0-9][0-9.]*\)"/\1/p' rust/torana-plugin-sdk/Cargo.toml | head -1)
  latest=$(git tag -l 'v*' --sort=-v:refname | head -1 | sed 's/^v//')
  IFS=. read -r crate_major crate_minor crate_patch <<<"$crate"
  IFS=. read -r latest_major latest_minor latest_patch <<<"$latest"
  if [[ "$crate_major" == 0 && "$latest_major" == 0 && "$crate_patch" == 0 &&
        "$crate_minor" =~ ^[0-9]+$ && "$latest_minor" =~ ^[0-9]+$ &&
        "$crate_minor" -eq $((latest_minor + 1)) ]]; then
    echo "allowing the intentional pre-1.0 ABI break for v$crate (latest release: v$latest)"
    exit 0
  fi
fi

exit 1
