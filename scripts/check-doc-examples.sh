#!/bin/sh
set -eu

# Run from any checkout; examples resolve the SDK through this module itself.
repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo"

go test ./examples/authoring-go
cargo check --manifest-path examples/rust-logger/Cargo.toml
cargo check --manifest-path conformance/guests/rust-allhooks/Cargo.toml
