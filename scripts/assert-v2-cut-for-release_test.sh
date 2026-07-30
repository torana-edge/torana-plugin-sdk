#!/usr/bin/env bash
# Fixture tests for assert-v2-cut-for-release.sh — behaviour, not string presence.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
assert=$root/scripts/assert-v2-cut-for-release.sh
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

fail=0

# run_check NAME pass|fail SUBSTR VERSION ROOT
# If SUBSTR is non-empty, output must contain it — so a fixture cannot pass
# for the wrong reason.
run_check() {
  local name=$1 expected=$2 substr=$3 version=$4 repo=$5
  set +e
  out=$("$assert" "$version" "$repo" 2>&1)
  status=$?
  set -e
  local ok=0
  if [ "$expected" = pass ] && [ "$status" -eq 0 ]; then
    ok=1
  elif [ "$expected" = fail ] && [ "$status" -ne 0 ]; then
    ok=1
  fi
  if [ "$ok" -eq 1 ] && [ -n "$substr" ]; then
    if ! printf '%s\n' "$out" | grep -q -- "$substr"; then
      ok=0
      echo "not ok - $name (exit matched $expected but missing reason: $substr)" >&2
      echo "$out" >&2
      fail=1
      return
    fi
  fi
  if [ "$ok" -eq 1 ]; then
    echo "ok - $name"
  else
    echo "not ok - $name (expected $expected, got exit $status)" >&2
    echo "$out" >&2
    fail=1
  fi
}

# Minimal check-abi-breaking.sh that shares selected_path with --print-path.
install_breaking_script() {
  local dest=$1
  cat >"$dest" <<'EOF'
#!/usr/bin/env bash
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
path=$(selected_path)
# fixtures never invoke buf
echo "would protect $path"
EOF
  chmod +x "$dest"
}

install_ci_invoking_breaking() {
  mkdir -p "$1/.github/workflows"
  cat >"$1/.github/workflows/ci.yml" <<'EOF'
jobs:
  go:
    steps:
      - name: ABI compatibility
        run: ./scripts/check-abi-breaking.sh
EOF
}

# --- migration still in progress ---
mig=$tmpdir/migrating
mkdir -p "$mig/proto/torana/v1" \
         "$mig/rust/torana-plugin-sdk/proto/torana/v1" \
         "$mig/scripts" \
         "$mig/pb" \
         "$mig/.github/workflows"
touch "$mig/proto/torana/v1/torana.proto"
touch "$mig/rust/torana-plugin-sdk/proto/torana/v1/torana.proto"
touch "$mig/pb/torana.pb.go"
cat >"$mig/.github/workflows/ci.yml" <<'EOF'
- run: buf breaking --against main --path proto/torana/v1
EOF

run_check "pre-cut tag allows v1" pass "" 0.2.0 "$mig"
run_check "v0.3 refuses while v1 present" fail "proto/torana/v1 to be deleted" 0.3.0 "$mig"
run_check "v1.0 refuses while v1 present" fail "proto/torana/v1 to be deleted" 1.0.0 "$mig"

# --- fully completed cut ---
cut=$tmpdir/cut
mkdir -p "$cut/proto/torana/v2" \
         "$cut/rust/torana-plugin-sdk/proto/torana/v2" \
         "$cut/scripts"
touch "$cut/proto/torana/v2/torana.proto"
touch "$cut/rust/torana-plugin-sdk/proto/torana/v2/torana.proto"
install_breaking_script "$cut/scripts/check-abi-breaking.sh"
install_ci_invoking_breaking "$cut"

run_check "fully completed cut" pass "" 0.3.0 "$cut"
run_check "tag name with leading v" pass "" v0.3.0 "$cut"

# --- stale generated Go v1 package ---
stale=$tmpdir/stale-pb
mkdir -p "$stale/proto/torana/v2" \
         "$stale/rust/torana-plugin-sdk/proto/torana/v2" \
         "$stale/scripts" \
         "$stale/pb"
touch "$stale/proto/torana/v2/torana.proto"
touch "$stale/rust/torana-plugin-sdk/proto/torana/v2/torana.proto"
touch "$stale/pb/torana.pb.go"
install_breaking_script "$stale/scripts/check-abi-breaking.sh"
install_ci_invoking_breaking "$stale"

run_check "stale generated Go v1 package" fail "pb/torana.pb.go" 0.3.0 "$stale"

# --- missing canonical v2 ---
nov2=$tmpdir/no-canonical-v2
mkdir -p "$nov2/rust/torana-plugin-sdk/proto/torana/v2" \
         "$nov2/scripts"
touch "$nov2/rust/torana-plugin-sdk/proto/torana/v2/torana.proto"
install_breaking_script "$nov2/scripts/check-abi-breaking.sh"
install_ci_invoking_breaking "$nov2"

run_check "missing canonical v2 source" fail "canonical proto/torana/v2" 0.3.0 "$nov2"

# --- missing Rust v2 ---
norust=$tmpdir/no-rust-v2
mkdir -p "$norust/proto/torana/v2" \
         "$norust/scripts"
touch "$norust/proto/torana/v2/torana.proto"
install_breaking_script "$norust/scripts/check-abi-breaking.sh"
install_ci_invoking_breaking "$norust"

run_check "missing Rust v2 source" fail "Rust copy of proto/torana/v2" 0.3.0 "$norust"

# --- script mentions v2 in a comment but --print-path selects v1 ---
lie=$tmpdir/lie
mkdir -p "$lie/proto/torana/v2" \
         "$lie/rust/torana-plugin-sdk/proto/torana/v2" \
         "$lie/scripts"
touch "$lie/proto/torana/v2/torana.proto"
touch "$lie/rust/torana-plugin-sdk/proto/torana/v2/torana.proto"
cat >"$lie/scripts/check-abi-breaking.sh" <<'EOF'
#!/usr/bin/env bash
# mentions proto/torana/v2 only in this comment
set -euo pipefail
if [ "${1:-}" = "--print-path" ]; then
  echo proto/torana/v1
  exit 0
fi
buf breaking --against "$1" --path proto/torana/v1
EOF
chmod +x "$lie/scripts/check-abi-breaking.sh"
install_ci_invoking_breaking "$lie"

run_check "script mentions v2 but selects v1" fail "--print-path must return proto/torana/v2" 0.3.0 "$lie"

# --- correct script exists but CI does not invoke it ---
orphan=$tmpdir/orphan
mkdir -p "$orphan/proto/torana/v2" \
         "$orphan/rust/torana-plugin-sdk/proto/torana/v2" \
         "$orphan/scripts" \
         "$orphan/.github/workflows"
touch "$orphan/proto/torana/v2/torana.proto"
touch "$orphan/rust/torana-plugin-sdk/proto/torana/v2/torana.proto"
install_breaking_script "$orphan/scripts/check-abi-breaking.sh"
# Orphaned: script exists, ci.yml only mentions the string in a comment.
cat >"$orphan/.github/workflows/ci.yml" <<'EOF'
# ./scripts/check-abi-breaking.sh
- run: buf breaking --against main --path proto/torana/v1
EOF

run_check "correct script but CI does not invoke it" fail "run: ./scripts/check-abi-breaking.sh" 0.3.0 "$orphan"

# Reproduce the round-4 false pass: string mention only, stale pb.go, selects v1.
falsepass=$tmpdir/falsepass
mkdir -p "$falsepass/proto/torana/v2" \
         "$falsepass/rust/torana-plugin-sdk/proto/torana/v2" \
         "$falsepass/scripts" \
         "$falsepass/pb" \
         "$falsepass/.github/workflows"
touch "$falsepass/proto/torana/v2/torana.proto"
touch "$falsepass/rust/torana-plugin-sdk/proto/torana/v2/torana.proto"
touch "$falsepass/pb/torana.pb.go"
cat >"$falsepass/scripts/check-abi-breaking.sh" <<'EOF'
#!/usr/bin/env bash
# proto/torana/v2 appears only here
set -euo pipefail
if [ "${1:-}" = "--print-path" ]; then
  echo proto/torana/v1
  exit 0
fi
buf breaking --against main --path proto/torana/v1
EOF
chmod +x "$falsepass/scripts/check-abi-breaking.sh"
cat >"$falsepass/.github/workflows/ci.yml" <<'EOF'
# --path proto/torana/v2
- run: echo noop
EOF

run_check "round-4 false-pass fixture now fails" fail "pb/torana.pb.go" 0.3.0 "$falsepass"

if [ "$fail" -ne 0 ]; then
  echo "assert-v2-cut-for-release_test.sh: FAILED" >&2
  exit 1
fi
echo "assert-v2-cut-for-release_test.sh: all states ok"
