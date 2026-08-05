# Torana Plugin SDK for Rust

Historical Rust bindings and wrappers for Torana's WASM Plugin ABI v1.
The current Torana Edge host accepts ABI v2 only, so this crate is not a
supported or compatible authoring path. It remains in the repository pending a
complete v2 port or removal. Do not use it for a current Edge deployment.

For archival builds, the crate requires Rust 1.85 or newer and `protoc`.

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
