//! Conformance guest that exports EVERY hook.
//!
//! The rust-logger example advertises only before-request. This guest advertises
//! every ABI-v1 hook so the host can compare Go and Rust export semantics.
//!
//! Every handler passes through. The hooks exist to be reached, not to do
//! anything: what is under test is what the SDK does before calling them.

use torana_plugin_sdk::{
    export_plugin_v1, pbv1, HOOK_AFTER_RESPONSE, HOOK_BEFORE_REQUEST,
    HOOK_ON_HTTP_REQUEST, HOOK_ON_STREAM_CHUNK, HOOK_ON_TICK,
};

fn dispatch(_input: pbv1::HookInput) -> Result<Option<pbv1::HookResult>, String> {
    Ok(None)
}

export_plugin_v1!(
    HOOK_BEFORE_REQUEST
        | HOOK_AFTER_RESPONSE
        | HOOK_ON_STREAM_CHUNK
        | HOOK_ON_HTTP_REQUEST
        | HOOK_ON_TICK,
    dispatch
);

fn main() {}
