#!/usr/bin/env bash
# Pre-publish gate for the coordinated v2 cut.
#
# v0.3.0 and later must not ship while v1 is still in the tree, and must not
# ship while CI still protects only v1. The PR-time v0.3 tag guard cannot catch
# this: release.yml is triggered by the tag itself, so a check that lives only
# on pull_request notices the mistake after the immutable tag exists.
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

if test -f "$root/proto/torana/v1/torana.proto"; then
  echo "::error::v${version} requires proto/torana/v1 to be deleted before release." >&2
  echo "::error::The coordinated cut must land first; Go module proxy tags are immutable." >&2
  fail=1
fi
if test -f "$root/rust/torana-plugin-sdk/proto/torana/v1/torana.proto"; then
  echo "::error::v${version} requires the Rust copy of proto/torana/v1 to be deleted." >&2
  fail=1
fi

# CI must protect v2 once v1 is gone. The transitional check-abi-breaking.sh
# switches on file presence; a hard-coded --path proto/torana/v1 with no v2
# path is the failure mode this catches.
breaking_script=$root/scripts/check-abi-breaking.sh
ci_yml=$root/.github/workflows/ci.yml
protects_v2=0
if test -f "$breaking_script" && grep -q 'proto/torana/v2' "$breaking_script"; then
  protects_v2=1
fi
if test -f "$ci_yml" && grep -q -- '--path proto/torana/v2' "$ci_yml"; then
  protects_v2=1
fi
if [ "$protects_v2" -ne 1 ]; then
  echo "::error::v${version} requires CI to protect proto/torana/v2 after the v1 deletion." >&2
  echo "::error::Refusing to publish while compatibility still checks only v1." >&2
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "v${version}: v1 is gone and CI protects v2; release cut is allowed."
