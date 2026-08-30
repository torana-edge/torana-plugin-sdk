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
buf breaking --against "$against" --path proto/torana/v1
