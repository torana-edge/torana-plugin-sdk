//! Rust bindings for the stable Torana Plugin ABI v1.
//!
//! The SDK deliberately exposes only the host calls granted by Torana. A
//! plugin cannot gain a capability by importing a function that the operator
//! did not grant.

use core::{mem, slice};
use prost::Message;

#[doc(hidden)]
pub use prost;

pub mod pb {
    include!(concat!(env!("OUT_DIR"), "/torana.v1.rs"));
}

pub const LOG_DEBUG: i32 = 0;
pub const LOG_INFO: i32 = 1;

#[link(wasm_import_module = "env")]
extern "C" {
    #[link_name = "log"]
    fn host_log(level: i32, ptr: u32, len: u32);
    #[link_name = "emit_metric"]
    fn host_emit_metric(kind: i32, ptr: u32, len: u32, value: f64, labels_ptr: u32, labels_len: u32);
    #[link_name = "host_call"]
    fn raw_host_call(cmd_ptr: u32, cmd_len: u32, args_ptr: u32, args_len: u32) -> u64;
}

/// Logs a bounded diagnostic string when the host granted `env.log`.
pub fn log(message: &str, level: i32) {
    if message.is_empty() { return; }
    unsafe { host_log(level, message.as_ptr() as u32, message.len() as u32) }
}

/// Allocates a host-visible byte buffer. Hosts call `dealloc` after consuming
/// a non-zero hook result.
#[no_mangle]
pub extern "C" fn alloc(size: u32) -> u32 {
    if size == 0 { return 0; }
    let mut bytes = Vec::<u8>::with_capacity(size as usize);
    let ptr = bytes.as_mut_ptr();
    mem::forget(bytes);
    ptr as u32
}

#[no_mangle]
pub extern "C" fn dealloc(ptr: u32, size: u32) {
    if ptr == 0 { return; }
    unsafe { drop(Vec::from_raw_parts(ptr as *mut u8, 0, size as usize)); }
}

#[doc(hidden)]
pub fn input(ptr: u32, len: u32) -> &'static [u8] {
    if ptr == 0 || len == 0 { return &[]; }
    unsafe { slice::from_raw_parts(ptr as *const u8, len as usize) }
}

#[doc(hidden)]
pub fn result(bytes: &[u8]) -> u64 {
    if bytes.is_empty() { return 0; }
    let ptr = alloc(bytes.len() as u32);
    unsafe { slice::from_raw_parts_mut(ptr as *mut u8, bytes.len()).copy_from_slice(bytes); }
    ((ptr as u64) << 32) | bytes.len() as u64
}

pub const METRIC_COUNTER: i32 = 0;
pub const METRIC_HISTOGRAM: i32 = 1;
pub const METRIC_GAUGE: i32 = 2;

pub fn emit_metric(name: &str, kind: i32, value: f64, labels: &serde_json::Value) {
    let labels = labels.to_string();
    unsafe {
        host_emit_metric(
            kind,
            name.as_ptr() as u32,
            name.len() as u32,
            value,
            labels.as_ptr() as u32,
            labels.len() as u32,
        )
    }
}

pub fn host_call(command: &str, arguments: &str) -> Option<String> {
    let packed = unsafe {
        raw_host_call(
            command.as_ptr() as u32,
            command.len() as u32,
            arguments.as_ptr() as u32,
            arguments.len() as u32,
        )
    };
    if packed == 0 {
        return None;
    }
    let ptr = (packed >> 32) as u32;
    let len = packed as u32;
    let value = String::from_utf8_lossy(input(ptr, len)).into_owned();
    dealloc(ptr, len);
    Some(value)
}

pub fn plugin_config() -> serde_json::Value {
    host_call("env.plugin_config", "")
        .and_then(|value| serde_json::from_str(&value).ok())
        .unwrap_or_else(|| serde_json::json!({}))
}

fn set_verdict(
    request: &mut pb::ChatRequest,
    key: &str,
    value: serde_json::Value,
) -> Result<(), serde_json::Error> {
    let mut meta = if request.torana_meta_json.is_empty() {
        serde_json::Map::new()
    } else {
        serde_json::from_slice::<serde_json::Map<String, serde_json::Value>>(
            &request.torana_meta_json,
        )?
    };
    meta.insert(key.to_owned(), value);
    request.torana_meta_json = serde_json::to_vec(&meta)?;
    Ok(())
}

pub fn block_request(
    request: &mut pb::ChatRequest,
    status: i32,
    code: &str,
    message: &str,
) -> Result<(), serde_json::Error> {
    set_verdict(
        request,
        "_block",
        serde_json::json!({"status": status, "code": code, "message": message}),
    )
}

pub fn route_request(
    request: &mut pb::ChatRequest,
    provider: &str,
    model: &str,
) -> Result<(), serde_json::Error> {
    set_verdict(
        request,
        "_route",
        serde_json::json!({"provider": provider, "model": model}),
    )
}

pub fn respond_request(
    request: &mut pb::ChatRequest,
    content: &str,
) -> Result<(), serde_json::Error> {
    set_verdict(request, "_respond", serde_json::json!({"content": content}))
}

/// Generates the ABI-v1 request trampoline for a stateless handler. `true`
/// returns the encoded mutated request; `false` means pass-through.
#[macro_export]
macro_rules! export_before_request {
    ($handler:path) => {
        #[no_mangle]
        pub extern "C" fn run_before_request(_request_id: u64, ptr: u32, len: u32) -> u64 {
            use $crate::prost::Message;
            let mut request = $crate::pb::ChatRequest::decode($crate::input(ptr, len))
                .expect("torana sdk: decode run_before_request");
            if !$handler(&mut request) { return 0; }
            let mut out = Vec::new();
            request.encode(&mut out).expect("torana sdk: encode run_before_request");
            $crate::result(&out)
        }
    };
}

#[macro_export]
macro_rules! export_before_request_result {
    ($handler:path) => {
        #[no_mangle]
        pub extern "C" fn run_before_request(_request_id: u64, ptr: u32, len: u32) -> u64 {
            use $crate::prost::Message;
            let mut request = $crate::pb::ChatRequest::decode($crate::input(ptr, len))
                .expect("torana sdk: decode run_before_request");
            if !$handler(&mut request).expect("torana plugin: run_before_request") { return 0; }
            let mut out = Vec::new();
            request.encode(&mut out).expect("torana sdk: encode run_before_request");
            $crate::result(&out)
        }
    };
}

#[macro_export]
macro_rules! export_after_response {
    ($handler:path) => {
        #[no_mangle]
        pub extern "C" fn run_after_response(_request_id: u64, ptr: u32, len: u32) -> u64 {
            use $crate::prost::Message;
            let mut response = $crate::pb::ChatRequest::decode($crate::input(ptr, len))
                .expect("torana sdk: decode run_after_response");
            if !$handler(&mut response).expect("torana plugin: run_after_response") { return 0; }
            let mut out = Vec::new();
            response.encode(&mut out).expect("torana sdk: encode run_after_response");
            $crate::result(&out)
        }
    };
}

#[macro_export]
macro_rules! export_stream_chunk {
    ($handler:path) => {
        #[no_mangle]
        pub extern "C" fn run_on_stream_chunk(_request_id: u64, ptr: u32, len: u32) -> u64 {
            use $crate::prost::Message;
            let event = $crate::pb::StreamEvent::decode($crate::input(ptr, len))
                .expect("torana sdk: decode run_on_stream_chunk");
            let Some(response) = $handler(&event).expect("torana plugin: run_on_stream_chunk") else {
                return 0;
            };
            let mut out = Vec::new();
            response.encode(&mut out).expect("torana sdk: encode run_on_stream_chunk");
            $crate::result(&out)
        }
    };
}

#[macro_export]
macro_rules! export_http_request {
    ($handler:path) => {
        #[no_mangle]
        pub extern "C" fn run_on_http_request(_request_id: u64, ptr: u32, len: u32) -> u64 {
            use $crate::prost::Message;
            let request = $crate::pb::HttpRequest::decode($crate::input(ptr, len))
                .expect("torana sdk: decode run_on_http_request");
            let Some(response) = $handler(&request).expect("torana plugin: run_on_http_request") else {
                return 0;
            };
            let mut out = Vec::new();
            response.encode(&mut out).expect("torana sdk: encode run_on_http_request");
            $crate::result(&out)
        }
    };
}

/// Exports the `run_on_tick` hook, which the host fires periodically with no
/// request in flight. Requires the `env.background_tick` permission.
///
/// The handler returns `Ok(None)` for "nothing to do this tick". A returned
/// `TickResult` must have `handled = true`, or the host cannot distinguish it
/// from doing nothing — an all-defaults message encodes to zero bytes.
#[macro_export]
macro_rules! export_tick {
    ($handler:path) => {
        #[no_mangle]
        pub extern "C" fn run_on_tick(_request_id: u64, ptr: u32, len: u32) -> u64 {
            use $crate::prost::Message;
            let tick = $crate::pb::TickRequest::decode($crate::input(ptr, len))
                .expect("torana sdk: decode run_on_tick");
            let Some(result) = $handler(&tick).expect("torana plugin: run_on_tick") else {
                return 0;
            };
            let mut out = Vec::new();
            result.encode(&mut out).expect("torana sdk: encode run_on_tick");
            $crate::result(&out)
        }
    };
}
