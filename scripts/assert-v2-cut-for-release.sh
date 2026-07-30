#!/usr/bin/env bash
# Pre-publish gate for the coordinated v2 cut.
#
# v0.3.0 and later must not ship while v1 is still in the tree, and must not
# ship while CI still protects only v1. The PR-time v0.3 tag guard cannot catch
# this: release.yml is triggered by the tag itself, so a check that lives only
# on pull_request notices the mistake after the immutable tag exists.
#
# This checks behaviour, not string presence: the same selected_path used to
# invoke buf must return proto/torana/v2, CI must actually run that script, both
# v2 sources must exist, and the generated Go v1 package must be gone (protoc
# does not delete obsolete .pb.go files).
#
# Usage: assert-v2-cut-for-release.sh <version> [repo-root]
# version is the semver without a leading v (e.g. 0.3.0).
set -euo pipefail

version=${1:?usage: assert-v2-cut-for-release.sh <version> [repo-root]}
root=$(cd "${2:-.}" && pwd)

# Strip a leading v if a caller passed the tag name.
version=${version#v}
# Drop pre-release / build metadata for the major.minor comparison.
base=${version%%-*}
base=${base%%+*}
IFS=. read -r major minor _ <<EOF
${base}.0.0
EOF
major=${major:-0}
minor=${minor:-0}

# Before the cut, v1 is expected to remain.
if [ "$major" -eq 0 ] && [ "$minor" -lt 3 ]; then
  echo "v${version} is before the v2 cut; v1 may still be present."
  exit 0
fi

fail=0
err() {
  echo "::error::$1" >&2
  fail=1
}

if test -f "$root/proto/torana/v1/torana.proto"; then
  err "v${version} requires proto/torana/v1 to be deleted before release."
  echo "::error::The coordinated cut must land first; Go module proxy tags are immutable." >&2
fi
if test -f "$root/rust/torana-plugin-sdk/proto/torana/v1/torana.proto"; then
  err "v${version} requires the Rust copy of proto/torana/v1 to be deleted."
fi

if ! test -f "$root/proto/torana/v2/torana.proto"; then
  err "v${version} requires canonical proto/torana/v2/torana.proto."
fi
if ! test -f "$root/rust/torana-plugin-sdk/proto/torana/v2/torana.proto"; then
  err "v${version} requires the Rust copy of proto/torana/v2/torana.proto."
fi

# Generated Go for v1 lives at pb/torana.pb.go (unversioned package). Removing
# the .proto does not delete it; a stale file keeps the old SDK compiling.
if test -f "$root/pb/torana.pb.go"; then
  err "v${version} requires pb/torana.pb.go (generated Go v1) to be deleted."
fi

breaking_script=$root/scripts/check-abi-breaking.sh
ci_yml=$root/.github/workflows/ci.yml

if ! test -x "$breaking_script" && ! test -f "$breaking_script"; then
  err "v${version} requires scripts/check-abi-breaking.sh."
else
  # --print-path must work without buf (release runs this before buf setup).
  selected=$("$breaking_script" --print-path 2>/dev/null || true)
  if [ "$selected" != "proto/torana/v2" ]; then
    err "check-abi-breaking.sh --print-path must return proto/torana/v2 (got: ${selected:-<empty>})."
  fi
fi

if ! test -f "$ci_yml"; then
  err "v${version} requires .github/workflows/ci.yml."
elif ! grep -Eq '^[[:space:]]*run:[[:space:]]*\./scripts/check-abi-breaking\.sh[[:space:]]*$' "$ci_yml"; then
  err "ci.yml must contain an actual run: ./scripts/check-abi-breaking.sh step."
fi

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "v${version}: v1 is gone, generated Go v1 is gone, and CI selects v2; release cut is allowed."
