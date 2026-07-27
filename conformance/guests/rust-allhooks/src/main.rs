//! Conformance guest that exports EVERY hook.
//!
//! The rust-logger example exports only `run_before_request`, so the other
//! hooks are simply absent from the module — which makes it impossible to test
//! what they do with a payload they cannot decode. Exporting all of them is
//! what lets the host harness assert Go and Rust behave identically.
//!
//! Every handler passes through. The hooks exist to be reached, not to do
//! anything: what is under test is what the SDK does before calling them.

use torana_plugin_sdk::{
    export_after_response, export_before_request, export_http_request, export_stream_chunk,
    export_tick, pb,
};

fn before_request(_request: &mut pb::ChatRequest) -> bool {
    false // pass-through
}

fn after_response(_response: &mut pb::ChatRequest) -> Result<bool, String> {
    Ok(false)
}

fn stream_chunk(_event: &pb::StreamEvent) -> Result<Option<pb::StreamEventResult>, String> {
    Ok(None)
}

fn http_request(_request: &pb::HttpRequest) -> Result<Option<pb::HttpResponse>, String> {
    Ok(None)
}

fn tick(_tick: &pb::TickRequest) -> Result<Option<pb::TickResult>, String> {
    Ok(None)
}

export_before_request!(before_request);
export_after_response!(after_response);
export_stream_chunk!(stream_chunk);
export_http_request!(http_request);
export_tick!(tick);

fn main() {}
