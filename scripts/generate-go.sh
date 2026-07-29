#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
command -v protoc >/dev/null || { echo "protoc is required" >&2; exit 1; }
command -v protoc-gen-go >/dev/null || { echo "protoc-gen-go is required; install google.golang.org/protobuf/cmd/protoc-gen-go" >&2; exit 1; }

cd "$root"
# v1 and v2 are both generated while the migration is in flight. v1 goes when
# the host, both SDKs and the official plugins are on v2 — see the ABI v2 plan.
protoc --go_out=. --go_opt=module=github.com/torana-edge/torana-plugin-sdk proto/torana/v1/torana.proto
protoc --go_out=. --go_opt=module=github.com/torana-edge/torana-plugin-sdk proto/torana/v2/torana.proto
