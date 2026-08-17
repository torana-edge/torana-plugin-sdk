#!/usr/bin/env bash
# ABI-v2 compatibility against main. The public release line is v2-only, so the
# compatibility check is scoped to the canonical v2 schema.
#
# --print-path is consumed by the release preflight before buf is installed.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

if [ "${1:-}" = "--print-path" ]; then
  echo proto/torana/v2
  exit 0
fi

against=${1:-'https://github.com/torana-edge/torana-plugin-sdk.git#branch=main'}
command -v buf >/dev/null || { echo "buf is required" >&2; exit 1; }

test -f proto/torana/v2/torana.proto || {
  echo "::error::proto/torana/v2/torana.proto is missing" >&2
  exit 1
}
buf breaking --against "$against" --path proto/torana/v2
