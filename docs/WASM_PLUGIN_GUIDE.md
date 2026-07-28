# Implementing a Torana plugin: the WASM contract

**For AI coding agents and humans.** This document exists because getting the
WASM boundary right is the part models reliably get wrong — and every mistake
here fails by returning plausible empty output rather than an error. Null bytes,
dropped mutations, a silently discarded field. Nothing crashes; the plugin just
quietly does nothing.

If you are an agent generating or modifying a Torana plugin, read this first and
check your work against the checklist at the end. If you are writing a Go or Rust
plugin, the SDK already implements all of this — start at
[WRITING_A_PLUGIN.md](WRITING_A_PLUGIN.md) and you will not need most of it.

This document is a critical reference for implementing WebAssembly (WASM) plugins in Torana Edge. **AI Coding Agents MUST read this document before generating or modifying Torana WASM plugins.**

## 1. The Core Architecture (Linear Memory)
WASM plugins in Torana run inside a highly restricted sandbox (using `wazero`). 
The Go host (Torana) and the guest (the plugin) do NOT share variables, structs, or garbage collection. They only share a single, flat byte array called **Linear Memory**.

To pass a Protobuf byte array from the host to the plugin:
1. The host calls the plugin's `alloc(size)` function.
2. The plugin allocates memory and returns a 32-bit pointer.
3. The host writes the Protobuf byte array into the plugin's memory at that pointer.
4. The host calls the plugin's hook (e.g., `run_before_request(reqID, ptr, size)`).

## 2. The Golden Rule of Memory Allocation
**NEVER USE STATIC BUMP ALLOCATORS.**

### ❌ WRONG (Causes OOM Crashes):
```typescript
let bump: u32 = 0;
export function alloc(size: u32): u32 {
  let ptr = bump;
  bump += size;
  return ptr;
}
```
*Why it fails:* The `bump` pointer only goes up. Even if the host calls `dealloc`, the memory is never reused. After a few megabytes of Protobuf requests, the plugin will crash the server.

### ✅ CORRECT:
Use the standard library allocator for your language.
* **Go (standard Go, not TinyGo)**:
  ```go
  var memory map[uint64][]byte // For tracking allocations
  // Use standard make([]byte) and return unsafe.Pointer
  ```
  Torana's plugins are built with **standard Go**, compiled
  `GOOS=wasip1 GOARCH=wasm -buildmode=c-shared` for the reactor model — see
  [PLUGIN_SEMANTICS.md](PLUGIN_SEMANTICS.md), which is
  authoritative on the toolchain. Don't hand-roll this: the
  [torana-plugin-sdk](https://github.com/torana-edge/torana-plugin-sdk) module
  implements the allocator correctly, and getting it wrong returns null bytes
  rather than an error.
* **Rust**:
  ```rust
  use std::alloc::Layout;

  #[no_mangle]
  pub extern "C" fn alloc(size: u32) -> u32 {
      // A zero-sized layout is undefined behaviour to pass to the allocator,
      // and zero bytes needs no allocation.
      if size == 0 {
          return 0;
      }
      let layout = Layout::array::<u8>(size as usize).unwrap();
      let p = unsafe { std::alloc::alloc(layout) };
      if p.is_null() {
          // The ABI defines no failure value for alloc, and the host reads 0
          // as a valid pointer (linear-memory offset 0). Trap instead.
          std::alloc::handle_alloc_error(layout);
      }
      p as u32
  }

  #[no_mangle]
  pub extern "C" fn dealloc(ptr: u32, size: u32) {
      if ptr == 0 || size == 0 {
          return;
      }
      // The layout MUST match the one used to allocate. Freeing with a
      // different layout is undefined behaviour.
      let layout = Layout::array::<u8>(size as usize).unwrap();
      unsafe { std::alloc::dealloc(ptr as *mut u8, layout) }
  }
  ```

  The imports are qualified at the call site on purpose: `use std::alloc::{alloc,
  dealloc}` collides with the `#[no_mangle]` functions being defined and does not
  compile.
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

## 3. The 64-bit Return ABI
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

### Packing Example (Rust):
```rust
let out_ptr = alloc(output.len() as u32);
// ... copy data to out_ptr ...
return ((out_ptr as u64) << 32) | (output.len() as u64);
```

## 4. Host Functions (`env.*`)
Torana exports several functions to the plugin via the `env` module.
If you use these, you must request them in your `plugin.json` under the `permissions` array, or the host will reject the plugin.

* `env.log(level: i32, ptr: i32, len: i32)`
* `env.emit_metric(type: i32, ptr: i32, len: i32, value: f64)`

When passing strings TO the host, you don't need to pack them into a 64-bit integer. You just pass the 32-bit `ptr` and `len` as separate arguments.

## 5. Background Ticks (`run_on_tick`)

Every other hook is reactive — it runs because a request is passing through.
`run_on_tick` fires on a timer with **no request in flight**, so a plugin can act
when nothing is happening.

It has the same signature as every other hook,
`(request_id: i64, ptr: i32, len: i32) -> i64`, taking a `TickRequest` and
returning a `TickResult`. Two things differ, and both are easy to get wrong:

**There is no request, so most host calls have nothing to answer with.**
`env.original_request`, `env.original_response`, and `env.meta_*` are all
request-scoped. On a tick they return empty rather than failing. The caller's
credential does not exist either. Everything a tick needs must come from the
plugin's own durable state or from a host call that resolves its own config.

**You must set `handled = true`.** An all-defaults protobuf message encodes to
zero bytes, and the host reads zero bytes as "did nothing". A plugin that acted
but left `handled` false is indistinguishable from one that never ran. Return
`0` (a null pointer) when there genuinely was nothing to do.

Ticks are gated on the `env.background_tick` permission, and an operator must
approve it against your exact bundle digest before the hook is ever called.

```rust
// Rust: the SDK macro handles the boundary.
torana_plugin_sdk::export_tick!(my_tick_handler);

fn my_tick_handler(tick: &pb::TickRequest) -> Result<Option<pb::TickResult>, Box<dyn Error>> {
    if nothing_to_do() {
        return Ok(None);            // encodes to a 0 return: "did nothing"
    }
    Ok(Some(pb::TickResult {
        handled: true,              // REQUIRED, or the host cannot tell
        actions: 2,
        note: "refreshed 2 conversations".into(),
    }))
}
```

## 6. Summary Checklist for AI Agents
1. Did I use a real allocator (not a bump allocator)?
2. Did I implement both `alloc` and `dealloc`?
3. Did I pack the return pointer and size into a `u64`?
4. Did I return `0` for passthrough?
5. Did I parse and serialize Protobuf properly within the memory bounds?
6. If I implemented `run_on_tick`, did I set `handled = true` on any result I
   actually meant, and avoid relying on request-scoped host calls?
