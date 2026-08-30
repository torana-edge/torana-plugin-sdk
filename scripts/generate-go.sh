#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
command -v protoc >/dev/null || { echo "protoc is required" >&2; exit 1; }
command -v protoc-gen-go >/dev/null || { echo "protoc-gen-go is required; install google.golang.org/protobuf/cmd/protoc-gen-go" >&2; exit 1; }

cd "$root"
protoc --go_out=. --go_opt=module=github.com/torana-edge/torana-plugin-sdk proto/torana/v1/torana.proto

# protoc-gen-go records the locally installed protoc version in a comment even
# when the generated descriptor and Go API are byte-identical. Normalize that
# non-semantic line so generation is reproducible across supported protoc
# patch releases used by contributors and CI.
sed -i -E 's#^//[[:space:]]+protoc[[:space:]]+v[^[:space:]]+$#// \tprotoc        normalized#' pb/v1/torana.pb.go
