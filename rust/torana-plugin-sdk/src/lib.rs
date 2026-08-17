//! Rust bindings and WASI Preview 1 trampolines for Torana Plugin ABI v2.
//!
//! The SDK deliberately exposes only the host calls granted by Torana. A
//! plugin cannot gain a capability by importing a function that the operator
//! did not grant.

use core::alloc::Layout;
use core::{ptr, slice};

#[doc(hidden)]
pub use prost;

pub mod pbv2 {
    include!(concat!(env!("OUT_DIR"), "/torana.v2.rs"));
}

/// Error returned by an ABI-v2 host call.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum HostCallError {
    /// The host refused the operation with a stable, machine-readable code.
    Refused(pbv2::HostError),
    /// The host returned a frame that violates the ABI-v2 result contract.
    Protocol(String),
}

impl core::fmt::Display for HostCallError {
    fn fmt(&self, f: &mut core::fmt::Formatter<'_>) -> core::fmt::Result {
        match self {
            Self::Refused(err) => write!(f, "host call refused ({})", err.code),
            Self::Protocol(message) => f.write_str(message),
        }
    }
}

impl std::error::Error for HostCallError {}

/// ABI-v2 hook bitmap bits. Bit N corresponds to `pbv2::Hook` value N.
pub const HOOK_BEFORE_REQUEST: u32 = 1 << (pbv2::Hook::BeforeRequest as u32);
pub const HOOK_AFTER_RESPONSE: u32 = 1 << (pbv2::Hook::AfterResponse as u32);
pub const HOOK_ON_STREAM_CHUNK: u32 = 1 << (pbv2::Hook::OnStreamChunk as u32);
pub const HOOK_ON_HTTP_REQUEST: u32 = 1 << (pbv2::Hook::OnHttpRequest as u32);
pub const HOOK_ON_TICK: u32 = 1 << (pbv2::Hook::OnTick as u32);
const ALL_V2_HOOKS: u32 = HOOK_BEFORE_REQUEST
    | HOOK_AFTER_RESPONSE
    | HOOK_ON_STREAM_CHUNK
    | HOOK_ON_HTTP_REQUEST
    | HOOK_ON_TICK;

/// Returns the hook selected by a valid ABI-v2 input envelope.
pub fn hook_of(input: &pbv2::HookInput) -> Result<pbv2::Hook, String> {
    use pbv2::hook_input::Payload;
    match input.payload.as_ref() {
        Some(Payload::ChatRequest(_)) => Ok(pbv2::Hook::BeforeRequest),
        Some(Payload::AfterResponse(_)) => Ok(pbv2::Hook::AfterResponse),
        Some(Payload::StreamEvent(_)) => Ok(pbv2::Hook::OnStreamChunk),
        Some(Payload::HttpRequest(_)) => Ok(pbv2::Hook::OnHttpRequest),
        Some(Payload::TickRequest(_)) => Ok(pbv2::Hook::OnTick),
        None => Err("torana sdk: HookInput requires a payload".to_owned()),
    }
}

fn validate_hook_result(hook: pbv2::Hook, result: &pbv2::HookResult) -> Result<(), String> {
    use pbv2::hook_result::Action;
    let valid = matches!(
        (hook, result.action.as_ref()),
        (pbv2::Hook::BeforeRequest, Some(Action::ReplaceRequest(_)))
            | (pbv2::Hook::AfterResponse, Some(Action::ReplaceResponse(_)))
            | (pbv2::Hook::OnStreamChunk, Some(Action::EmitEvents(_)))
            | (pbv2::Hook::OnStreamChunk, Some(Action::Suppress(_)))
            | (pbv2::Hook::OnHttpRequest, Some(Action::ServeHttp(_)))
            | (pbv2::Hook::OnTick, Some(Action::TickOutcome(_)))
    );
    if valid {
        Ok(())
    } else {
        Err(format!(
            "torana sdk: HookResult action does not match {}",
            hook.as_str_name()
        ))
    }
}

/// Decodes, dispatches, validates, and encodes one ABI-v2 hook invocation.
///
/// Returning `Ok(None)` from `handler` is the sole pass-through representation.
/// This ordinary Rust function owns the contract logic so it is testable off
/// WASM; [`export_plugin_v2!`] only supplies the two required exports.
#[doc(hidden)]
pub fn __dispatch_v2<E: core::fmt::Display>(
    input: &[u8],
    hooks: u32,
    handler: fn(pbv2::HookInput) -> Result<Option<pbv2::HookResult>, E>,
) -> Result<Vec<u8>, String> {
    use prost::Message;

    if hooks == 0 || hooks & !ALL_V2_HOOKS != 0 {
        return Err(
            "torana sdk: supported hook bitmap is empty or contains unknown bits".to_owned(),
        );
    }
    let input = pbv2::HookInput::decode(input)
        .map_err(|err| format!("torana sdk: decode run_hook: {err}"))?;
    let hook = hook_of(&input)?;
    if hooks & (1 << hook as u32) == 0 {
        return Err(format!(
            "torana sdk: dispatched unregistered hook {}",
            hook.as_str_name()
        ));
    }
    let Some(result) = handler(input).map_err(|err| format!("torana plugin: {err}"))? else {
        return Ok(Vec::new());
    };
    validate_hook_result(hook, &result)?;
    let mut output = Vec::new();
    result
        .encode(&mut output)
        .map_err(|err| format!("torana sdk: encode run_hook: {err}"))?;
    if output.is_empty() {
        return Err("torana sdk: non-pass HookResult encoded to empty bytes".to_owned());
    }
    Ok(output)
}

/// Exports the two functions required by ABI v2: `supported_hooks` and
/// `run_hook`. The handler receives the typed HookInput and returns `Ok(None)`
/// for pass-through or one hook-appropriate HookResult.
#[macro_export]
macro_rules! export_plugin_v2 {
    ($hooks:expr, $handler:path) => {
        #[no_mangle]
        pub extern "C" fn supported_hooks() -> u32 {
            $hooks
        }

        #[no_mangle]
        pub extern "C" fn run_hook(ptr: u32, len: u32) -> u64 {
            let output = $crate::__dispatch_v2($crate::__input(ptr, len), $hooks, $handler)
                .unwrap_or_else(|error| panic!("{error}"));
            $crate::__result(&output)
        }
    };
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
/// Allocation failure TRAPS rather than returning a sentinel. ABI v2 defines
/// no failure value for `alloc`, and the host treats 0 as a valid pointer — it
/// would write the payload at linear-memory offset 0, over the guest's own
/// memory, and then call the hook with `ptr = 0`.
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

fn decode_host_call_result(bytes: &[u8]) -> Result<Vec<u8>, HostCallError> {
    use pbv2::host_call_result::Result as ResultArm;
    use prost::Message;

    validate_host_call_result_wire(bytes)?;
    let result = pbv2::HostCallResult::decode(bytes)
        .map_err(|err| HostCallError::Protocol(format!("decode HostCallResult: {err}")))?;
    match result.result {
        Some(ResultArm::Value(value)) => Ok(value),
        Some(ResultArm::Error(error)) => {
            if !matches!(error.code, 1..=6) {
                return Err(HostCallError::Protocol(format!(
                    "HostError code {} is not classified by this SDK",
                    error.code
                )));
            }
            Err(HostCallError::Refused(error))
        }
        None => Err(HostCallError::Protocol(
            "HostCallResult requires a result arm".to_owned(),
        )),
    }
}

fn validate_host_call_result_wire(mut bytes: &[u8]) -> Result<(), HostCallError> {
    while !bytes.is_empty() {
        let (key, key_len) = read_varint(bytes)?;
        bytes = &bytes[key_len..];
        let field = key >> 3;
        let wire = key & 7;
        if !matches!(field, 1 | 2) || wire != 2 {
            return Err(HostCallError::Protocol(
                "HostCallResult carries an unknown or malformed result arm".to_owned(),
            ));
        }
        let (length, length_len) = read_varint(bytes)?;
        bytes = &bytes[length_len..];
        let length = usize::try_from(length).map_err(|_| {
            HostCallError::Protocol("HostCallResult field length overflows usize".to_owned())
        })?;
        if length > bytes.len() {
            return Err(HostCallError::Protocol(
                "HostCallResult field is truncated".to_owned(),
            ));
        }
        bytes = &bytes[length..];
    }
    Ok(())
}

fn read_varint(bytes: &[u8]) -> Result<(u64, usize), HostCallError> {
    let mut value = 0u64;
    for (index, byte) in bytes.iter().copied().take(10).enumerate() {
        if index == 9 && byte > 1 {
            break;
        }
        value |= u64::from(byte & 0x7f) << (index * 7);
        if byte & 0x80 == 0 {
            return Ok((value, index + 1));
        }
    }
    Err(HostCallError::Protocol(
        "HostCallResult contains an invalid varint".to_owned(),
    ))
}

/// Invokes a host command with protobuf arguments and decodes the ABI-v2
/// `HostCallResult` envelope. Empty successful values remain distinguishable
/// from typed refusals; callers must branch on [`HostCallError::Refused`]'s
/// code, never its diagnostic message.
pub fn host_call_v2<M: prost::Message>(
    command: &str,
    arguments: &M,
) -> Result<Vec<u8>, HostCallError> {
    let arguments = arguments.encode_to_vec();
    let packed = unsafe {
        raw_host_call(
            command.as_ptr() as u32,
            command.len() as u32,
            arguments.as_ptr() as u32,
            arguments.len() as u32,
        )
    };
    if packed == 0 {
        return Err(HostCallError::Protocol(
            "host_call returned no HostCallResult frame".to_owned(),
        ));
    }
    let ptr = (packed >> 32) as u32;
    let len = packed as u32;
    let bytes = __input(ptr, len).to_vec();
    dealloc(ptr, len);
    decode_host_call_result(&bytes)
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

    #[test]
    fn input_of_an_empty_or_null_buffer_is_an_empty_slice() {
        assert!(__input(0, 0).is_empty());
        assert!(__input(0, 10).is_empty());
        assert!(__input(10, 0).is_empty());
    }

    fn pass(_input: pbv2::HookInput) -> Result<Option<pbv2::HookResult>, String> {
        Ok(None)
    }

    fn replace(input: pbv2::HookInput) -> Result<Option<pbv2::HookResult>, String> {
        let request = match input.payload {
            Some(pbv2::hook_input::Payload::ChatRequest(request)) => request,
            _ => return Err("wrong payload".to_owned()),
        };
        Ok(Some(pbv2::HookResult {
            action: Some(pbv2::hook_result::Action::ReplaceRequest(request)),
        }))
    }

    fn wrong_action(_input: pbv2::HookInput) -> Result<Option<pbv2::HookResult>, String> {
        Ok(Some(pbv2::HookResult {
            action: Some(pbv2::hook_result::Action::Suppress(pbv2::Suppress {})),
        }))
    }

    fn before_request_input() -> Vec<u8> {
        use prost::Message;
        pbv2::HookInput {
            abi_minor: 0,
            request_id: 7,
            payload: Some(pbv2::hook_input::Payload::ChatRequest(pbv2::ChatRequest {
                model: "rust-v2".to_owned(),
                ..Default::default()
            })),
        }
        .encode_to_vec()
    }

    #[test]
    fn v2_pass_through_is_exactly_empty_output() {
        assert_eq!(
            __dispatch_v2(&before_request_input(), HOOK_BEFORE_REQUEST, pass).unwrap(),
            Vec::<u8>::new()
        );
    }

    #[test]
    fn v2_replacement_round_trips_through_the_single_action_envelope() {
        use prost::Message;
        let output = __dispatch_v2(&before_request_input(), HOOK_BEFORE_REQUEST, replace).unwrap();
        let result = pbv2::HookResult::decode(output.as_slice()).unwrap();
        let Some(pbv2::hook_result::Action::ReplaceRequest(request)) = result.action else {
            panic!("expected replace_request")
        };
        assert_eq!(request.model, "rust-v2");
    }

    #[test]
    fn v2_dispatch_rejects_undeclared_and_mismatched_hooks() {
        let err = __dispatch_v2(&before_request_input(), HOOK_ON_TICK, pass).unwrap_err();
        assert!(err.contains("unregistered"), "{err}");
        let err =
            __dispatch_v2(&before_request_input(), HOOK_BEFORE_REQUEST, wrong_action).unwrap_err();
        assert!(err.contains("does not match"), "{err}");
    }

    #[test]
    fn v2_host_call_result_keeps_empty_success_distinct_from_refusal() {
        use prost::Message;
        let success = pbv2::HostCallResult {
            result: Some(pbv2::host_call_result::Result::Value(Vec::new())),
        };
        assert_eq!(
            decode_host_call_result(&success.encode_to_vec()).unwrap(),
            Vec::<u8>::new()
        );

        let refusal = pbv2::HostCallResult {
            result: Some(pbv2::host_call_result::Result::Error(pbv2::HostError {
                code: pbv2::ErrorCode::PermissionDenied as i32,
                message: "diagnostic only".to_owned(),
            })),
        };
        assert_eq!(
            decode_host_call_result(&refusal.encode_to_vec()),
            Err(HostCallError::Refused(pbv2::HostError {
                code: pbv2::ErrorCode::PermissionDenied as i32,
                message: "diagnostic only".to_owned(),
            }))
        );
    }

    #[test]
    fn v2_host_call_result_rejects_empty_and_unclassified_frames() {
        use prost::Message;
        let err = decode_host_call_result(&[]).unwrap_err();
        assert!(matches!(err, HostCallError::Protocol(_)));

        for code in [pbv2::ErrorCode::Unspecified as i32, 99] {
            let frame = pbv2::HostCallResult {
                result: Some(pbv2::host_call_result::Result::Error(pbv2::HostError {
                    code,
                    message: String::new(),
                })),
            }
            .encode_to_vec();
            assert!(matches!(
                decode_host_call_result(&frame),
                Err(HostCallError::Protocol(_))
            ));
        }

        // Unknown top-level field 3, length-delimited empty payload. Prost
        // normally discards it, but the v2 contract treats it as a future
        // result arm this build cannot classify.
        assert!(matches!(
            decode_host_call_result(&[0x1a, 0x00]),
            Err(HostCallError::Protocol(_))
        ));
    }
}
