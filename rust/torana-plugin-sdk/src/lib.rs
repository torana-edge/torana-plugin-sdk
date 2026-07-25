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

extern "C" {
    #[link_name = "log"]
    fn host_log(level: i32, ptr: u32, len: u32);
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

/// Generates the ABI-v1 request trampoline for a stateless handler. `true`
/// returns the encoded mutated request; `false` means pass-through.
#[macro_export]
macro_rules! export_before_request {
    ($handler:path) => {
        #[no_mangle]
        pub extern "C" fn run_before_request(_request_id: u64, ptr: u32, len: u32) -> u64 {
            use $crate::prost::Message;
            let mut request = match $crate::pb::ChatRequest::decode($crate::input(ptr, len)) {
                Ok(value) => value,
                Err(_) => return 0,
            };
            if !$handler(&mut request) { return 0; }
            let mut out = Vec::new();
            if request.encode(&mut out).is_err() { return 0; }
            $crate::result(&out)
        }
    };
}
