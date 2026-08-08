# Rust ABI conformance

Run this after installing Rust, the `wasm32-wasip1` target, Cargo, and
`protoc`:

```bash
cargo test --manifest-path rust/torana-plugin-sdk/Cargo.toml
cargo build --target wasm32-wasip1 --manifest-path examples/rust-logger/Cargo.toml
```

The compiled Rust all-hooks guest and Go all-hooks guest must pass the same ABI
v2 host conformance suite in `conformance/host`.
