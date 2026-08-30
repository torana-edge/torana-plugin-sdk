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

buf breaking --against "$against" --path proto/torana/v1
