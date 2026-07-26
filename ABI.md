# Torana Plugin ABI v1

The ABI is the contract between a Torana host and a WASI Preview 1 plugin.
It is versioned independently from the proxy. `abi_version: "v1"` means that
the exported functions and protobuf messages below are stable for all v1
releases.

## Guest exports

Every plugin exports `alloc(size) -> ptr` and `dealloc(ptr, size)`. Hooks use
`(request_id: i64, ptr: i32, len: i32) -> i64`; a non-zero result packs the
returned pointer in its high 32 bits and length in its low 32 bits. A zero
result is pass-through. Hosts own input buffers; guests own output buffers
until the host calls `dealloc`.

Returning zero is reserved for intentional pass-through. A handler or codec
error traps the guest call; the host discards that instance and applies the
operator-approved `failure_mode` (`pass` or `block`). The SDK helpers implement
this convention so ordinary handler failures cannot resemble success.

Supported v1 hooks are `run_before_request`, `run_after_response`,
`run_on_stream_chunk`, and `run_on_http_request`. Their protobuf messages are
defined in [`proto/torana/v1/torana.proto`](proto/torana/v1/torana.proto).

## Imports and grants

Hosts may expose `env.log`, `env.emit_metric`, and `env.host_call`. A manifest
permission is only a request: the operator must grant it to the immutable
artifact digest before the host exposes the corresponding capability.

Plugins must tolerate a missing or denied host call. They must not assume
filesystem, network, environment-variable, clock, or random access.

## Implementing this ABI in another language

Everything above is what a plugin must do. The rest of this section is what an
SDK author must get right, and every mistake here fails by returning plausible
empty output rather than an error — null bytes, dropped mutations, a silently
discarded field. Assert on content, never on the absence of an error.

### Linear memory
WASM plugins in Torana run inside a highly restricted sandbox (using `wazero`). 
The Go host (Torana) and the guest (the plugin) do NOT share variables, structs, or garbage collection. They only share a single, flat byte array called **Linear Memory**.

To pass a Protobuf byte array from the host to the plugin:
1. The host calls the plugin's `alloc(size)` function.
2. The plugin allocates memory and returns a 32-bit pointer.
3. The host writes the Protobuf byte array into the plugin's memory at that pointer.
4. The host calls the plugin's hook (e.g., `run_before_request(reqID, ptr, size)`).

### Memory allocation
**NEVER USE STATIC BUMP ALLOCATORS.**

**Wrong — causes OOM crashes:**
```typescript
let bump: u32 = 0;
export function alloc(size: u32): u32 {
  let ptr = bump;
  bump += size;
  return ptr;
}
```
*Why it fails:* The `bump` pointer only goes up. Even if the host calls `dealloc`, the memory is never reused. After a few megabytes of Protobuf requests, the plugin will crash the server.

**Correct:**
Use the standard library allocator for your language.
* **Go (standard Go, not TinyGo)**:
  ```go
  var memory map[uint64][]byte // For tracking allocations
  // Use standard make([]byte) and return unsafe.Pointer
  ```
  Torana's plugins are built with **standard Go**, compiled
  `GOOS=wasip1 GOARCH=wasm -buildmode=c-shared` for the reactor model — see
  [docs/PLUGIN_SEMANTICS.md](docs/PLUGIN_SEMANTICS.md), which is
  authoritative on the toolchain. Don't hand-roll this: the
  [torana-plugin-sdk](https://github.com/torana-edge/torana-plugin-sdk) module
  implements the allocator correctly, and getting it wrong returns null bytes
  rather than an error.
* **Rust**:
  ```rust
  use std::alloc::{alloc, dealloc, Layout};
  
  #[no_mangle]
  pub extern "C" fn alloc(size: u32) -> u32 {
      let layout = Layout::array::<u8>(size as usize).unwrap();
      unsafe { alloc(layout) as u32 }
  }
  
  #[no_mangle]
  pub extern "C" fn dealloc(ptr: u32, size: u32) {
      let layout = Layout::array::<u8>(size as usize).unwrap();
      unsafe { dealloc(ptr as *mut u8, layout) }
  }
  ```
* **AssemblyScript**:
  Export the built-in allocator wrappers.
  ```typescript
  export function alloc(size: u32): usize {
    return __alloc(size);
  }
  export function dealloc(ptr: usize): void {
    __free(ptr);
  }
  ```

### The 64-bit packed return
Hooks like `run_before_request(reqID: u64, ptr: u32, size: u32)` must return a **64-bit integer (`u64` or `uint64`)**.
Because WASM32 only supports 32-bit pointers, we pack the pointer and the length of the response into a single 64-bit integer.

* **Format**: `(pointer << 32) | size`
* **Pass-Through**: If you don't want to modify the request, return `0`.

**Stream hook return type**: `run_on_stream_chunk` returns a serialized
`torana.v1.StreamEventResult` (NOT a bare `StreamEvent`):
```proto
message StreamEventResult {
  bool handled = 1;              // must be true for the result to apply
  repeated StreamEvent events = 2; // empty = suppress, 1 = replace, n = fan-out
}
```
Returning `0` bytes still means pass-through. `handled=true` with zero
events suppresses the input event — this is how buffering plugins drop
argument fragments before re-emitting the assembled result at ToolCallEnd.

**Packing, in Rust:**
```rust
let out_ptr = alloc(output.len() as u32);
// ... copy data to out_ptr ...
return ((out_ptr as u64) << 32) | (output.len() as u64);
```

## Compatibility

Additive protobuf fields and new optional host calls are allowed in v1. An
export signature change, a changed packed-result layout, or a removed field is
an ABI-major change. Run this repository's conformance suite before publishing
an artifact.
