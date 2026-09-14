#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
command -v protoc >/dev/null || { echo "protoc is required" >&2; exit 1; }
command -v protoc-gen-go >/dev/null || { echo "protoc-gen-go is required; install google.golang.org/protobuf/cmd/protoc-gen-go" >&2; exit 1; }

cd "$root"

# The Rust crate needs its OWN copy of the proto: a crates.io package cannot
# read files outside its archive. That copy must never drift from the canonical
# one, and CI enforces it by comparing descriptor sets — but only after a push.
# Syncing it here makes drift impossible to introduce locally, and CI's existing
# `generate-go.sh && git diff --exit-code` now catches a stale copy in the same
# check that catches stale bindings.
crate_proto="rust/torana-plugin-sdk/proto/torana/v1/torana.proto"
mkdir -p "$(dirname "$crate_proto")"
cp proto/torana/v1/torana.proto "$crate_proto"

protoc --go_out=. --go_opt=module=github.com/torana-edge/torana-plugin-sdk proto/torana/v1/torana.proto

# protoc-gen-go records the locally installed protoc version in a comment even
# when the generated descriptor and Go API are byte-identical. Normalize that
# non-semantic line so generation is reproducible across supported protoc
# patch releases used by contributors and CI.
generated=$(mktemp)
trap 'rm -f "$generated"' EXIT
sed -E 's#^//[[:space:]]+protoc[[:space:]]+v[^[:space:]]+$#// \tprotoc        normalized#' pb/v1/torana.pb.go > "$generated"
cat "$generated" > pb/v1/torana.pb.go
