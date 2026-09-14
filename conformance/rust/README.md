# Rust ABI conformance

Run this after installing Rust, the `wasm32-wasip1` target, Cargo, and
`protoc`:

```bash
cargo test --manifest-path rust/torana-plugin-sdk/Cargo.toml
cargo build --target wasm32-wasip1 --manifest-path examples/rust-logger/Cargo.toml
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o /tmp/go-logger.wasm ./examples/go-logger
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o /tmp/go-allhooks.wasm ./conformance/guests/go-allhooks
cargo build --target wasm32-wasip1 --target-dir /tmp/rust-allhooks-target --manifest-path conformance/guests/rust-allhooks/Cargo.toml
```

The logger artifacts are the examples shipped by the release workflow. The
all-hooks artifacts let the same host probe every dispatcher path in both
languages. All four are loaded by `conformance/host`, which verifies the exact
ABI revision and exports, hook bitmap against its manifest, hook execution,
and Rust logger host import. CI sets `TORANA_E2E=1`, so a missing guest or
manifest fails instead of silently skipping artifact coverage.
