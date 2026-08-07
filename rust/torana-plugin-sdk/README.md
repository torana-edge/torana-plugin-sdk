# Torana Plugin SDK for Rust

Rust bindings, safe memory plumbing, typed host-call results, and a single-hook
dispatcher for Torana's WASM Plugin ABI v2. The crate requires Rust 1.85 or
newer and `protoc`.

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

Declare the hooks your dispatcher handles and export the v2 surface:

```rust
use torana_plugin_sdk::{export_plugin_v2, pbv2, HOOK_BEFORE_REQUEST};

fn dispatch(input: pbv2::HookInput) -> Result<Option<pbv2::HookResult>, String> {
    match input.payload {
        Some(pbv2::hook_input::Payload::ChatRequest(request)) => {
            println!("model: {}", request.model);
            Ok(None) // exact pass-through
        }
        _ => Err("received an undeclared hook".into()),
    }
}

export_plugin_v2!(HOOK_BEFORE_REQUEST, dispatch);
```

`Ok(None)` is the only pass-through spelling. A returned `HookResult` must use
the action belonging to the dispatched hook. Use `host_call_v2` for
protobuf-framed host calls and branch on `HostCallError::Refused(error).code`,
never the diagnostic text.

See the repository's
[Rust authoring guide](https://github.com/torana-edge/torana-plugin-sdk/blob/main/docs/WRITING_A_PLUGIN.md#rust)
for hooks, manifests, capabilities, and bundle installation.
