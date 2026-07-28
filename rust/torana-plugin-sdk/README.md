# Torana Plugin SDK for Rust

Rust bindings and safe hook wrappers for Torana's stable WASM Plugin ABI v1.
The crate requires Rust 1.85 or newer and `protoc`.

Add the crate and build for WASI Preview 1:

```toml
[lib]
crate-type = ["cdylib"]

[dependencies]
torana-plugin-sdk = "0.3"
```

```bash
rustup target add wasm32-wasip1
cargo build --release --target wasm32-wasip1
```

See the repository's
[Rust authoring guide](https://github.com/torana-edge/torana-plugin-sdk/blob/main/docs/WRITING_A_PLUGIN.md#rust)
for hooks, manifests, capabilities, and bundle installation.
