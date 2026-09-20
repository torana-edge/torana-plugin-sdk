# Torana Plugin SDK for Rust

Build a Torana plugin in Rust without writing provider-specific request
parsers. Implement the typed `Plugin` trait, choose your hooks, and let the
SDK handle WASM memory, protobuf framing and classified host-call errors.

Start with [your first plugin](../../docs/FIRST_PLUGIN.md). Rust 1.85+,
`protoc`, and the `wasm32-wasip1` target are required.

```toml
[lib]
crate-type = ["cdylib"]

[dependencies]
torana-plugin-sdk = "=0.5.1"
```

Keep the exact version and your reviewed Cargo.lock so plugin builds remain
reproducible.

```rust
use torana_plugin_sdk::{export_plugin_v1, pbv1, Plugin, RequestResult, HOOK_BEFORE_REQUEST};

struct MyPlugin;
impl Plugin for MyPlugin {
    const SUPPORTED_HOOKS: u32 = HOOK_BEFORE_REQUEST;
    fn before_request(_: pbv1::ChatRequest) -> Result<RequestResult, String> {
        Ok(RequestResult::pass())
    }
}
export_plugin_v1!(MyPlugin);
```

```bash
rustup target add wasm32-wasip1
cargo build --release --target wasm32-wasip1
```

Declare the same hooks in `plugin.json`. Each hook has its own result family:
pass keeps input unchanged, replacement proposes a change, and an error lets
the host apply the approved failure policy. The lower-level two-argument
dispatcher macro remains available for custom dispatchers.

Plugins have no ambient filesystem or network. Model, pricing, file and HTTP
helpers address declared, operator-approved resources. `execution()` exposes
invocation facts, not credentials or destinations. Typed host refusals retain
stable error codes; do not branch on diagnostic strings.

The best-effort `debug`, `info`, `counter`, `histogram` and `gauge` helpers
use approved logging/metric imports. Void imports cannot acknowledge delivery
or report a missing permission.

Continue with the [authoring reference](../../docs/WRITING_A_PLUGIN.md#rust)
and [compiled logger example](../../examples/rust-logger).
