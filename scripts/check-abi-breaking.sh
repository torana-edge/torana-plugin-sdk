#!/usr/bin/env bash
# ABI compatibility against main.
#
# While proto/torana/v1 still exists, protect only that path: v2 is unreleased
# and may still be reshaped under review.
#
# Once v1 is deleted (the coordinated cut PR), protect only v2. Comparing the
# full tree against a main that still contains v1 would report the intentional
# deletion as a breaking change and block the cut; scoping to v2 avoids that.
# After the cut merges, main has only v2 and the restriction can be simplified.
#
# --print-path prints the path that would be passed to buf, using the same
# selected_path function. The release preflight calls this before buf is
# installed, so it must not require the buf executable.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

selected_path() {
  if test -f proto/torana/v1/torana.proto; then
    echo proto/torana/v1
  else
    echo proto/torana/v2
  fi
}

if [ "${1:-}" = "--print-path" ]; then
  selected_path
  exit 0
fi

against=${1:-'https://github.com/torana-edge/torana-plugin-sdk.git#branch=main'}
command -v buf >/dev/null || { echo "buf is required" >&2; exit 1; }

path=$(selected_path)
if [ "$path" = proto/torana/v1 ]; then
  echo "Migration in progress: protecting proto/torana/v1 only."
  if git cat-file -e origin/main:proto/torana/v1/torana.proto 2>/dev/null; then
    buf breaking --against "$against" --path "$path"
  else
    echo "No ABI baseline exists on main yet; this PR establishes v1."
  fi
else
  echo "v1 deleted: protecting proto/torana/v2 (main may still contain v1)."
  test -f proto/torana/v2/torana.proto || {
    echo "::error::proto/torana/v1 is gone but proto/torana/v2/torana.proto is missing" >&2
    exit 1
  }
  buf breaking --against "$against" --path "$path"
fi
