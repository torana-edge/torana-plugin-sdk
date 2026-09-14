//! Conformance guest that exports EVERY hook.
//!
//! The rust-logger example advertises only before-request. This guest advertises
//! every ABI-v1 hook so the host can compare Go and Rust export semantics.
//!
//! Every ordinary handler invocation passes through. A reserved request model
//! exercises the typed respond_request host call across the compiled WASI
//! boundary.

use torana_plugin_sdk::{
    export_plugin_v1, pbv1, respond_text, Plugin, RequestResult, HOOK_AFTER_RESPONSE,
    HOOK_BEFORE_REQUEST, HOOK_ON_HTTP_REQUEST, HOOK_ON_STREAM_CHUNK, HOOK_ON_TICK,
};
struct AllHooks;
impl Plugin for AllHooks {
    const SUPPORTED_HOOKS: u32 = HOOK_BEFORE_REQUEST
        | HOOK_AFTER_RESPONSE
        | HOOK_ON_STREAM_CHUNK
        | HOOK_ON_HTTP_REQUEST
        | HOOK_ON_TICK;

    fn before_request(request: pbv1::ChatRequest) -> Result<RequestResult, String> {
        if request.model == "__respond_request_conformance__" {
            respond_text("compiled Rust response").map_err(|error| error.to_string())?;
        }
        Ok(RequestResult::pass())
    }
}
export_plugin_v1!(AllHooks);

fn main() {}
