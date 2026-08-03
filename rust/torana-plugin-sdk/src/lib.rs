//! Rust bindings for the stable Torana Plugin ABI v1.
//!
//! The SDK deliberately exposes only the host calls granted by Torana. A
//! plugin cannot gain a capability by importing a function that the operator
//! did not grant.

use core::alloc::Layout;
use core::{ptr, slice};

#[doc(hidden)]
pub use prost;

pub mod pb {
    include!(concat!(env!("OUT_DIR"), "/torana.v1.rs"));
}

pub mod pbv2 {
    include!(concat!(env!("OUT_DIR"), "/torana.v2.rs"));
}

pub const LOG_DEBUG: i32 = 0;
pub const LOG_INFO: i32 = 1;

#[link(wasm_import_module = "env")]
extern "C" {
    #[link_name = "log"]
    fn host_log(level: i32, ptr: u32, len: u32);
    #[link_name = "emit_metric"]
    fn host_emit_metric(
        kind: i32,
        ptr: u32,
        len: u32,
        value: f64,
        labels_ptr: u32,
        labels_len: u32,
    );
    #[link_name = "host_call"]
    fn raw_host_call(cmd_ptr: u32, cmd_len: u32, args_ptr: u32, args_len: u32) -> u64;
}

/// Logs a bounded diagnostic string when the host granted `env.log`.
pub fn log(message: &str, level: i32) {
    if message.is_empty() {
        return;
    }
    unsafe { host_log(level, message.as_ptr() as u32, message.len() as u32) }
}

// Allocation goes through `std::alloc` with an explicit `Layout`, which is the
// pattern docs/WASM_PLUGIN_GUIDE.md already documents and this crate did not
// follow.
//
// The previous version paired `Vec::with_capacity(n)` + `mem::forget` with
// `Vec::from_raw_parts(ptr, 0, n)`. That requires n to be the vector's ACTUAL
// capacity, and `with_capacity` is only obliged to allocate *at least* n. It
// happens to allocate exactly n for `u8` today, so nothing has gone wrong yet —
// but the deallocation is only correct by coincidence, and freeing with a
// layout that does not match the allocation is undefined behaviour. Naming the
// layout in both places removes the coincidence.

/// Allocates `size` bytes and returns a host-visible pointer.
///
/// The buffer is uninitialised. Hosts call [`dealloc`] with the same size after
/// consuming a non-zero hook result.
///
/// Allocation failure TRAPS rather than returning a sentinel. ABI.md defines no
/// failure value for `alloc`, and the host treats 0 as a valid pointer — it
/// would write the payload at linear-memory offset 0, over the guest's own
/// memory, and then call the hook with `ptr = 0`. Trapping is also what the
/// previous `Vec::with_capacity` did, via `handle_alloc_error`.
#[no_mangle]
pub extern "C" fn alloc(size: u32) -> u32 {
    alloc_bytes(size as usize) as u32
}

/// Frees a buffer previously returned by [`alloc`]. `size` must be the size
/// that was passed to `alloc`.
#[no_mangle]
pub extern "C" fn dealloc(ptr: u32, size: u32) {
    dealloc_bytes(ptr as *mut u8, size as usize);
}

// The pointer-level halves exist so the allocator contract can be tested on the
// host, where a real pointer does not fit in the u32 the wasm ABI uses.

fn alloc_bytes(size: usize) -> *mut u8 {
    if size == 0 {
        // Zero bytes needs no allocation, and a zero-sized layout is UB to
        // pass to the allocator. No caller dereferences this.
        return ptr::null_mut();
    }
    let Ok(layout) = Layout::array::<u8>(size) else {
        // Only reachable for a size beyond isize::MAX. Unrecoverable, and
        // there is no ABI value to report it with.
        std::process::abort();
    };
    let p = unsafe { std::alloc::alloc(layout) };
    if p.is_null() {
        // Out of memory. Returning null here would be reported to the host as
        // a pass-through by __result, which means the plugin's output is
        // dropped and the ORIGINAL request continues upstream — a redaction
        // plugin would fail open. Trap so failure_mode applies.
        std::alloc::handle_alloc_error(layout);
    }
    p
}

fn dealloc_bytes(p: *mut u8, size: usize) {
    if p.is_null() || size == 0 {
        return;
    }
    if let Ok(layout) = Layout::array::<u8>(size) {
        unsafe { std::alloc::dealloc(p, layout) }
    }
}

/// Borrows a host-provided buffer as a slice.
///
/// # Safety
///
/// This is a safe function that dereferences a raw pointer, which it can only
/// do soundly because of who calls it: the generated hook wrappers, with `ptr`
/// and `len` exactly as the host passed them across the ABI. Calling it with
/// any other values is undefined behaviour.
///
/// It is deliberately NOT an `unsafe fn`. Marking it so would force an `unsafe`
/// block into every macro expansion, and therefore into plugins that declare
/// `#![forbid(unsafe_code)]` — which would break them for no gain in real
/// safety. The proper fix is for the macros to hand over a lifetime-bound
/// slice; that is a larger change than this one.
///
/// The leading underscores mark it as ABI plumbing rather than API.
#[doc(hidden)]
pub fn __input(ptr: u32, len: u32) -> &'static [u8] {
    if ptr == 0 || len == 0 {
        return &[];
    }
    unsafe { slice::from_raw_parts(ptr as *const u8, len as usize) }
}

/// Copies `bytes` into a freshly allocated host-visible buffer and packs the
/// pointer and length into the u64 the ABI returns.
#[doc(hidden)]
pub fn __result(bytes: &[u8]) -> u64 {
    // Zero is reserved by the ABI for a DELIBERATE pass-through. An empty
    // payload is the only thing that means, so it is the only thing that
    // returns 0 here — allocation failure traps inside alloc_bytes rather than
    // arriving as a null this function could not tell apart from "no change".
    if bytes.is_empty() {
        return 0;
    }
    let (dst, len) = copy_to_owned_buffer(bytes);
    pack(dst as u32, len as u32)
}

/// Copies `bytes` into a fresh buffer, returning the pointer and length.
///
/// Split out from [`__result`] so it can be tested: off wasm32 a real pointer
/// does not fit in the u32 the ABI packs it into, so a test that round-trips
/// through `pack` would reconstruct a truncated pointer and segfault.
fn copy_to_owned_buffer(bytes: &[u8]) -> (*mut u8, usize) {
    if bytes.is_empty() {
        return (ptr::null_mut(), 0);
    }
    // Non-null: alloc_bytes traps on failure rather than returning null.
    let dst = alloc_bytes(bytes.len());
    // copy_nonoverlapping rather than building a &mut [u8] first. The buffer is
    // UNINITIALISED, and constructing a reference to uninitialised memory is
    // undefined behaviour even when nothing reads it before the write — which
    // is exactly what `slice::from_raw_parts_mut(..).copy_from_slice(..)` did.
    unsafe { ptr::copy_nonoverlapping(bytes.as_ptr(), dst, bytes.len()) };
    (dst, bytes.len())
}

// Compatibility shims for the pre-rename names. These are #[doc(hidden)] but
// `pub`, and under Cargo's 0.x rules a patch bump is a compatible update — so a
// crate calling them directly would break on `cargo update` with no version
// signal. Two lines each is cheaper than that.

#[doc(hidden)]
#[deprecated(note = "renamed to __input: this is ABI plumbing, not API")]
pub fn input(ptr: u32, len: u32) -> &'static [u8] {
    __input(ptr, len)
}

#[doc(hidden)]
#[deprecated(note = "renamed to __result: this is ABI plumbing, not API")]
pub fn result(bytes: &[u8]) -> u64 {
    __result(bytes)
}

/// Packs a pointer and length into the single u64 an ABI hook returns:
/// pointer in the high 32 bits, length in the low 32.
fn pack(ptr: u32, len: u32) -> u64 {
    ((ptr as u64) << 32) | len as u64
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
    let value = String::from_utf8_lossy(__input(ptr, len)).into_owned();
    dealloc(ptr, len);
    Some(value)
}

/// The exact envelope the host returns when a capability is refused.
///
/// It is valid JSON, so anything that merely parses the host's reply accepts it
/// as data. Every wrapper that can be denied has to recognise it.
pub const PERMISSION_DENIED: &str = r#"{"status":"error","message":"permission denied"}"#;

/// Reports whether a host-call reply is the refusal envelope rather than a
/// result.
pub fn is_permission_denied(reply: &str) -> bool {
    reply == PERMISSION_DENIED
}

/// Returns this plugin's configuration blob, or an empty object when none is
/// set or `env.plugin_config` was not granted.
pub fn plugin_config() -> serde_json::Value {
    host_call("env.plugin_config", "")
        // Without this the refusal envelope parses cleanly and is handed back
        // AS the configuration: a valid JSON object with none of the plugin's
        // expected fields, so it silently runs on defaults instead of
        // reporting a missing grant. The Go SDK checks this and Rust did not,
        // which made it a divergence rather than a shared gap.
        .filter(|value| !is_permission_denied(value))
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
            let mut request = $crate::pb::ChatRequest::decode($crate::__input(ptr, len))
                .expect("torana sdk: decode run_before_request");
            if !$handler(&mut request) {
                return 0;
            }
            let mut out = Vec::new();
            request
                .encode(&mut out)
                .expect("torana sdk: encode run_before_request");
            $crate::__result(&out)
        }
    };
}

#[macro_export]
macro_rules! export_before_request_result {
    ($handler:path) => {
        #[no_mangle]
        pub extern "C" fn run_before_request(_request_id: u64, ptr: u32, len: u32) -> u64 {
            use $crate::prost::Message;
            let mut request = $crate::pb::ChatRequest::decode($crate::__input(ptr, len))
                .expect("torana sdk: decode run_before_request");
            if !$handler(&mut request).expect("torana plugin: run_before_request") {
                return 0;
            }
            let mut out = Vec::new();
            request
                .encode(&mut out)
                .expect("torana sdk: encode run_before_request");
            $crate::__result(&out)
        }
    };
}

/// Exports `run_after_response`, called with a completed non-streaming response.
///
/// The handler receives a `pb::ChatRequest`, which is deliberate rather than a
/// mistake — there is no `pb::ChatResponse` in the v1 contract. Torana
/// normalises a provider's reply into the same message shape it uses for a
/// request: the assistant's turn arrives as `messages`, its tool calls as
/// `tool_calls`, and provider metadata under `torana_meta_json["_response"]`.
///
/// One shape means a plugin that rewrites message content works identically in
/// both directions, and the host's provider adapters have one target instead of
/// two. Return `Ok(false)` for pass-through.
#[macro_export]
macro_rules! export_after_response {
    ($handler:path) => {
        #[no_mangle]
        pub extern "C" fn run_after_response(_request_id: u64, ptr: u32, len: u32) -> u64 {
            use $crate::prost::Message;
            let mut response = $crate::pb::ChatRequest::decode($crate::__input(ptr, len))
                .expect("torana sdk: decode run_after_response");
            if !$handler(&mut response).expect("torana plugin: run_after_response") {
                return 0;
            }
            let mut out = Vec::new();
            response
                .encode(&mut out)
                .expect("torana sdk: encode run_after_response");
            $crate::__result(&out)
        }
    };
}

#[macro_export]
macro_rules! export_stream_chunk {
    ($handler:path) => {
        #[no_mangle]
        pub extern "C" fn run_on_stream_chunk(_request_id: u64, ptr: u32, len: u32) -> u64 {
            use $crate::prost::Message;
            let event = $crate::pb::StreamEvent::decode($crate::__input(ptr, len))
                .expect("torana sdk: decode run_on_stream_chunk");
            let Some(response) = $handler(&event).expect("torana plugin: run_on_stream_chunk")
            else {
                return 0;
            };
            // handled=false means "I did not act". The host discards the payload
            // either way and the Go SDK returns 0 here, so encoding a result
            // that will be thrown away both wastes an allocation and made the
            // two SDKs put different bytes on the wire for the same handler
            // return value.
            if !response.handled {
                return 0;
            }
            let mut out = Vec::new();
            response
                .encode(&mut out)
                .expect("torana sdk: encode run_on_stream_chunk");
            $crate::__result(&out)
        }
    };
}

#[macro_export]
macro_rules! export_http_request {
    ($handler:path) => {
        #[no_mangle]
        pub extern "C" fn run_on_http_request(_request_id: u64, ptr: u32, len: u32) -> u64 {
            use $crate::prost::Message;
            let request = $crate::pb::HttpRequest::decode($crate::__input(ptr, len))
                .expect("torana sdk: decode run_on_http_request");
            let Some(response) = $handler(&request).expect("torana plugin: run_on_http_request")
            else {
                return 0;
            };
            // handled=false means "I did not act". The host discards the payload
            // either way and the Go SDK returns 0 here, so encoding a result
            // that will be thrown away both wastes an allocation and made the
            // two SDKs put different bytes on the wire for the same handler
            // return value.
            if !response.handled {
                return 0;
            }
            let mut out = Vec::new();
            response
                .encode(&mut out)
                .expect("torana sdk: encode run_on_http_request");
            $crate::__result(&out)
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
            let tick = $crate::pb::TickRequest::decode($crate::__input(ptr, len))
                .expect("torana sdk: decode run_on_tick");
            let Some(result) = $handler(&tick).expect("torana plugin: run_on_tick") else {
                return 0;
            };
            // handled=false means "I did not act". The host discards the payload
            // either way and the Go SDK returns 0 here, so encoding a result
            // that will be thrown away both wastes an allocation and made the
            // two SDKs put different bytes on the wire for the same handler
            // return value.
            if !result.handled {
                return 0;
            }
            let mut out = Vec::new();
            result
                .encode(&mut out)
                .expect("torana sdk: encode run_on_tick");
            $crate::__result(&out)
        }
    };
}

#[cfg(test)]
mod tests {
    use super::*;

    // CI has run `cargo test` on this crate all along, against zero tests — so
    // the green tick meant only that the crate compiled. These cover the ABI
    // plumbing that every hook goes through.
    //
    // They exercise the pointer-level halves rather than the extern "C"
    // wrappers, because off wasm32 a real pointer does not fit in the u32 the
    // ABI uses and `ptr as u32` would truncate it.

    #[test]
    fn alloc_dealloc_round_trip() {
        for size in [1usize, 7, 64, 4096] {
            let p = alloc_bytes(size);
            assert!(!p.is_null(), "alloc_bytes({size}) returned null");

            // Write and read back every byte. This does NOT prove the layout
            // was large enough — it cannot, since both use the same `size` —
            // but it does exercise the alloc/write/read/free cycle, and a
            // dealloc whose layout disagreed with the allocation corrupts the
            // allocator, which the repeated cycles below surface.
            unsafe { ptr::write_bytes(p, 0xAB, size) };
            let seen = unsafe { slice::from_raw_parts(p, size) };
            assert!(seen.iter().all(|&b| b == 0xAB));

            // Frees with a layout built the same way it was allocated. This is
            // the pairing the Vec::with_capacity version only got right by
            // coincidence.
            dealloc_bytes(p, size);
        }
    }

    // Repeated alloc/free at varying sizes. A dealloc whose Layout disagreed
    // with the allocation corrupts allocator bookkeeping, and the corruption
    // shows up on a later allocation rather than at the bad free — so the loop
    // is the point.
    #[test]
    fn repeated_alloc_free_cycles_do_not_corrupt_the_allocator() {
        for round in 0..64 {
            let size = 1 + (round * 37) % 4096;
            let p = alloc_bytes(size);
            assert!(!p.is_null());
            unsafe { ptr::write_bytes(p, round as u8, size) };
            let seen = unsafe { slice::from_raw_parts(p, size) };
            assert!(
                seen.iter().all(|&b| b == round as u8),
                "round {round} readback"
            );
            dealloc_bytes(p, size);
        }
    }

    #[test]
    fn empty_payload_is_the_only_pass_through() {
        // The ABI reserves 0 for a deliberate no-op. Anything non-empty must
        // produce a real buffer; allocation failure traps instead of landing
        // here as a 0 the host would read as "plugin made no change".
        assert_eq!(__result(&[]), 0);
        assert_ne!(__result(&[0u8]), 0);
    }

    #[test]
    fn zero_size_allocation_is_a_null_pointer() {
        // Zero is the ABI's "no buffer" signal, and a zero-sized layout would
        // be undefined behaviour to pass to the allocator.
        assert!(alloc_bytes(0).is_null());
        assert_eq!(alloc(0), 0);
    }

    #[test]
    fn dealloc_tolerates_null_and_zero() {
        dealloc_bytes(ptr::null_mut(), 16);
        dealloc_bytes(ptr::null_mut(), 0);
        dealloc(0, 16);
    }

    #[test]
    fn pack_puts_the_pointer_high_and_the_length_low() {
        assert_eq!(pack(0x0000_1234, 0x0000_0056), 0x0000_1234_0000_0056);
        // The full u32 range must survive: a sign-extending cast would corrupt
        // any pointer above 2GiB, which is reachable in a 4GiB wasm memory.
        assert_eq!(pack(0xFFFF_FFFF, 0xFFFF_FFFF), 0xFFFF_FFFF_FFFF_FFFF);
        assert_eq!(pack(0x8000_0000, 1), 0x8000_0000_0000_0001);
    }

    #[test]
    fn empty_result_is_pass_through() {
        // Zero is reserved by the ABI for "the plugin made no change".
        assert_eq!(__result(&[]), 0);
    }

    #[test]
    fn result_copies_the_payload_verbatim() {
        // Every byte value, so a copy that dropped or mangled one is visible.
        let payload: Vec<u8> = (0u8..=255).collect();

        let (p, len) = copy_to_owned_buffer(&payload);
        assert!(!p.is_null());
        assert_eq!(len, payload.len());

        let copied = unsafe { slice::from_raw_parts(p, len) };
        assert_eq!(copied, payload.as_slice());

        dealloc_bytes(p, len);
    }

    #[test]
    fn copying_nothing_yields_no_buffer() {
        let (p, len) = copy_to_owned_buffer(&[]);
        assert!(p.is_null());
        assert_eq!(len, 0);
    }

    // The host signals a refused capability with this exact JSON. It is valid
    // JSON, so anything that merely parses the reply accepts it as data — which
    // is how plugin_config came to hand it back as the plugin's configuration.
    #[test]
    fn permission_denied_envelope_is_recognised() {
        assert!(is_permission_denied(PERMISSION_DENIED));
        assert!(is_permission_denied(
            r#"{"status":"error","message":"permission denied"}"#
        ));
    }

    #[test]
    fn permission_denied_does_not_over_match() {
        // Mistaking a plugin's own data for a refusal would discard a
        // legitimate result.
        for reply in [
            "",
            "{}",
            r#"{"status":"ok"}"#,
            r#"{"status":"error","message":"something else"}"#,
            r#"{"message":"permission denied"}"#,
            r#"prefix{"status":"error","message":"permission denied"}"#,
        ] {
            assert!(!is_permission_denied(reply), "over-matched {reply}");
        }
    }

    #[test]
    fn input_of_an_empty_or_null_buffer_is_an_empty_slice() {
        assert!(__input(0, 0).is_empty());
        assert!(__input(0, 10).is_empty());
        assert!(__input(10, 0).is_empty());
    }
}
