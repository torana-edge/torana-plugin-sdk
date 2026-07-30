#!/usr/bin/env bash
# Fixture tests for assert-v2-cut-for-release.sh — both states the gate cares about.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
assert=$root/scripts/assert-v2-cut-for-release.sh
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

fail=0
check() {
  local name=$1 expected=$2
  shift 2
  set +e
  out=$("$assert" "$@" 2>&1)
  status=$?
  set -e
  if [ "$expected" = pass ] && [ "$status" -eq 0 ]; then
    echo "ok - $name"
  elif [ "$expected" = fail ] && [ "$status" -ne 0 ]; then
    echo "ok - $name"
  else
    echo "not ok - $name (expected $expected, got exit $status)" >&2
    echo "$out" >&2
    fail=1
  fi
}

# --- fixture: migration still in progress (v1 present, CI protects only v1) ---
mig=$tmpdir/migrating
mkdir -p "$mig/proto/torana/v1" \
         "$mig/rust/torana-plugin-sdk/proto/torana/v1" \
         "$mig/scripts" \
         "$mig/.github/workflows"
touch "$mig/proto/torana/v1/torana.proto"
touch "$mig/rust/torana-plugin-sdk/proto/torana/v1/torana.proto"
# Hard-coded v1-only check — the hole the release gate must refuse.
cat >"$mig/.github/workflows/ci.yml" <<'EOF'
- run: buf breaking --against main --path proto/torana/v1
EOF
# No check-abi-breaking.sh with a v2 branch.

check "pre-cut tag allows v1" pass 0.2.0 "$mig"
check "v0.3 refuses while v1 present" fail 0.3.0 "$mig"
check "v1.0 refuses while v1 present" fail 1.0.0 "$mig"

# --- fixture: cut complete (v1 gone, CI protects v2) ---
cut=$tmpdir/cut
mkdir -p "$cut/proto/torana/v2" \
         "$cut/rust/torana-plugin-sdk/proto/torana/v2" \
         "$cut/scripts" \
         "$cut/.github/workflows"
touch "$cut/proto/torana/v2/torana.proto"
touch "$cut/rust/torana-plugin-sdk/proto/torana/v2/torana.proto"
cat >"$cut/scripts/check-abi-breaking.sh" <<'EOF'
if test -f proto/torana/v1/torana.proto; then
  buf breaking --against "$against" --path proto/torana/v1
else
  buf breaking --against "$against" --path proto/torana/v2
fi
EOF
cat >"$cut/.github/workflows/ci.yml" <<'EOF'
- run: ./scripts/check-abi-breaking.sh
EOF

check "v0.3 allowed after cut" pass 0.3.0 "$cut"
check "tag name with leading v" pass v0.3.0 "$cut"

# --- fixture: v1 gone but CI still only mentions v1 ---
half=$tmpdir/half
mkdir -p "$half/proto/torana/v2" \
         "$half/rust/torana-plugin-sdk/proto/torana/v2" \
         "$half/.github/workflows"
touch "$half/proto/torana/v2/torana.proto"
cat >"$half/.github/workflows/ci.yml" <<'EOF'
- run: buf breaking --against main --path proto/torana/v1
EOF

check "v0.3 refuses when CI still only protects v1" fail 0.3.0 "$half"

if [ "$fail" -ne 0 ]; then
  echo "assert-v2-cut-for-release_test.sh: FAILED" >&2
  exit 1
fi
echo "assert-v2-cut-for-release_test.sh: all states ok"
