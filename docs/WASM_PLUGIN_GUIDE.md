# Implementing a Torana plugin: the WASM contract

**For AI coding agents and humans.** This document exists because getting the
WASM boundary right is the part models reliably get wrong — and every mistake
here fails by returning plausible empty output rather than an error. Null bytes,
dropped mutations, a silently discarded field. Nothing crashes; the plugin just
quietly does nothing.

If you are an agent generating or modifying a Torana plugin, read this first and
check your work against the checklist at the end. If you are writing a Go
plugin, the SDK already implements all of this — start at
[WRITING_A_PLUGIN.md](WRITING_A_PLUGIN.md) and you will not need most of it.

This document is a critical reference for implementing WebAssembly (WASM) plugins in Torana Edge. **AI Coding Agents MUST read this document before generating or modifying Torana WASM plugins.**

**ABI versioning (read this before the sections below).** The current Edge host
accepts ABI v2 only. Sections 1–5 preserve the **historical v1 trampoline ABI**
still implemented by the unported Rust crate: five named hook
exports (`run_before_request`, …), a separate `request_id` argument, and v1
result messages (`StreamEventResult.handled`, `TickResult.handled`). See
[`proto/torana/v1/torana.proto`](../proto/torana/v1/torana.proto) and
[`ABI.md`](../ABI.md). That crate cannot run on the current host and is not a
supported authoring path.

**Supported Go guests use the v2 export surface:** `run_hook(ptr,size)->u64`,
`supported_hooks()->u32`, and single-action `HookResult` over
[`proto/torana/v2/torana.proto`](../proto/torana/v2/torana.proto). Declare
`"abi_version": "v2"` in `plugin.json`. Do not mix v1 exports with a v2
manifest. Section 6 is the current write-grant / host-call vocabulary enforced
by the host.
## 1. The Core Architecture (Linear Memory) — v1 trampoline
WASM plugins in Torana run inside a highly restricted sandbox (using `wazero`). 
The Go host (Torana) and the guest (the plugin) do NOT share variables, structs, or garbage collection. They only share a single, flat byte array called **Linear Memory**.

To pass a Protobuf byte array from the host to the plugin:
1. The host calls the plugin's `alloc(size)` function.
2. The plugin allocates memory and returns a 32-bit pointer.
3. The host writes the Protobuf byte array into the plugin's memory at that pointer.
4. The host calls the plugin's hook (e.g., `run_before_request(reqID, ptr, size)`).

## 2. The Golden Rule of Memory Allocation — v1 trampoline
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

## 3. The 64-bit Return ABI — v1 trampoline
Hooks like `run_before_request(reqID: u64, ptr: u32, size: u32)` must return a **64-bit integer (`u64` or `uint64`)**.
Because WASM32 only supports 32-bit pointers, we pack the pointer and the length of the response into a single 64-bit integer.

* **Format**: `(pointer << 32) | size`
* **Pass-Through**: If you don't want to modify the request, return `0`.

**Stream hook return type (v1):** `run_on_stream_chunk` returns a serialized
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
(Under v2 the stream action is `HookResult.emit_events` / `suppress` with no
`handled` flag — zero-byte return remains pass-through.)

### Packing Example (Rust):
```rust
let out_ptr = alloc(output.len() as u32);
// ... copy data to out_ptr ...
return ((out_ptr as u64) << 32) | (output.len() as u64);
```

## 4. Host Functions (`env.*`) — v1 trampoline
Torana exports several functions to the plugin via the `env` module.
If you use these, you must request them in your `plugin.json` under the `permissions` array, or the host will reject the plugin.

* `env.log(level: i32, ptr: i32, len: i32)`
* `env.emit_metric(type: i32, ptr: i32, len: i32, value: f64)`

When passing strings TO the host, you don't need to pack them into a 64-bit integer. You just pass the 32-bit `ptr` and `len` as separate arguments.

## 5. Background Ticks (`run_on_tick`) — v1 trampoline

Every other hook is reactive — it runs because a request is passing through.
`run_on_tick` fires on a timer with **no request in flight**, so a plugin can act
when nothing is happening.

**v1 export shape:** `(request_id: u64, ptr: u32, len: u32) -> u64`, taking a
`TickRequest` and returning a `TickResult` with `handled`.

**There is no caller request.** `env.original_request`, `env.original_response`,
and the caller's credential do not exist on a tick — those calls return empty
rather than failing. Everything a tick needs from durable storage must come from
the plugin's own state or from a host call that resolves its own config.

**Tick metadata (v1 and v2).** Both ABIs give the tick a synthetic execution
scope id (`request_id` / `reqID`). Plugin-private `env.meta_*` is available
inside that scope and is cleared when the tick ends (`EndRequest`). There is
still no caller request and no original request/response. The v1↔v2 difference
is envelope/export shape (`TickResult.handled` vs `HookResult.tick_outcome`),
not metadata availability. See `HookInput.request_id` in
`proto/torana/v2/torana.proto` and the host tick path in torana-edge.

**You must set `handled = true` (v1).** An all-defaults protobuf message encodes
to zero bytes, and the host reads zero bytes as "did nothing". A plugin that
acted but left `handled` false is indistinguishable from one that never ran.
Return `0` (a null pointer) when there genuinely was nothing to do. (v2 uses
`HookResult.tick_outcome` with no `handled` flag; zero bytes remain pass-through.)

Ticks are gated on the `env.background_tick` permission, and an operator must
approve it against your exact bundle digest before the hook is ever called.

```rust
// Rust: the SDK macro handles the boundary (v1 TickResult).
torana_plugin_sdk::export_tick!(my_tick_handler);

fn my_tick_handler(tick: &pb::TickRequest) -> Result<Option<pb::TickResult>, Box<dyn Error>> {
    if nothing_to_do() {
        return Ok(None);            // encodes to a 0 return: "did nothing"
    }
    Ok(Some(pb::TickResult {
        handled: true,              // REQUIRED on v1, or the host cannot tell
        actions: 2,
        note: "refreshed 2 conversations".into(),
    }))
}
```

## 6. IR write grants (v2)

Vocabulary for [`proto/torana/v2/torana.proto`](../proto/torana/v2/torana.proto).
Enforcement is current. Declare every `ir.*.write` capability you need in
`plugin.json`. Content grants reuse the same names across hooks where the
authority matches (assistant text on request and response). Topology is separate:

* `ir.cache_control.write` covers the cache breakpoint marker ONLY —
  `Message.cache_control_json` and `ToolDef.cache_control_json`. It does not
  authorise message or tool content/schema changes, and the message-role
  grants / `ir.tools.write` do not authorise those marker fields. A
  cache-economics plugin needs this one grant, not a prompt-rewriter's.
* `ir.stream.write` is **additive**. Suppress, fan-out, event-kind change, and
  block-boundary edits need it **plus** every content section you **change,
  remove, or add**. It cannot alone authorise changing or forging host-owned
  facts (usage, message_start, signature_delta, response model/id/role, opaque
  signatures, provider extension blobs). Host-owned means **immutable**: an
  identical re-emit needs no grant; Suppress/forge of those facts is forbidden.
* A one-for-one `TextDelta` rewrite needs only `ir.messages.write.assistant`.
* `ir.model.write` / `ir.params.write` cover **request** selection/params, not
  observed response facts.
* Opaque signatures (`thinking_signature`, `ToolCall.signature`,
  `ToolCallRef.signature`, `signature_delta`) bind provider tokens to content.
  Mutating signed content while leaving the signature in place is invalid; the
  host must reject that mutation or clear the signature. Streamed tool-call
  signatures bind `id`/`name` and assembled `arguments_delta` for the **same
  unique content-block index** (adapters must assign distinct indexes to
  parallel calls). `signature_delta` with open text/thinking binds that block;
  a trailing signature-only part (Code Assist) is standalone — preserve it, do
  not synthesize an empty block, and bind the preceding attributed
  text/thinking of the turn.
* Block indexes are unique across the entire streamed message and are never
  reused after close; tool deltas/stops bind their open tool block by index.
  Non-tool content (text/thinking/provider) is exclusive — at most one
  non-tool block may be open, and no tool block may be open with it — but
  MULTIPLE tool blocks may be open concurrently (OpenAI Chat streams parallel
  tool calls this way), each at its own unique index. MessageStop/end-of-stream
  with ANY block open is invalid unless the open blocks were terminally aborted
  by `StreamError`. Duplicate/open-missing sequences are invalid.
* `StreamError` is a **terminal abort**: may arrive mid-block; abandons ALL
  open blocks and incomplete tool-call buffers without synthetic
  `ContentBlockStop`s; ends the stream (no `MessageStop` required); any later
  event is invalid. It is
  also host-owned — do not forge provider-looking upstream failures; suppress
  under topology, trap under `failure_mode`, or use attributed verdicts.
* Nested message fields use `PolicyContainer` (or presence-sensitive
  Section/Topology): same presence recurses without auto-charging the parent;
  an index-only `ToolCallDelta` change needs topology only, not assistant.
  (Host enforcement vocabulary: package `outboundpolicy` — not imported by guests.)

Host-call replies use `HostCallResult` (`value` or `HostError`). An empty result
(no oneof arm) or `ERROR_CODE_UNSPECIFIED` is invalid — the Go SDK rejects an
empty host reply as a protocol error (it is not success). Unknown top-level
fields are refused; multiple known arms follow last-wins (host-produced). Guest
`HookResult` frames must go through `DecodeHookResult` before `ValidateFor` so
two known action arms cannot last-wins past the host. Verdict / `meta_append`
arguments are `BlockRequestArgs`, `RespondRequestArgs`, `RouteRequestArgs`,
`SetIdentityArgs`, and `MetaAppendArgs` in `proto/torana/v2/torana.proto`.
Command `env.meta_append` is authorised by permission `env.meta_set`; non-empty
fragments ack with an empty success value, empty fragment reads back the
complete buffer (`MetaAppendSuccessValue`).

**Two host-call paths, disjoint on purpose.** `HostCall(cmd, proto.Message)`
takes CORE `env.*` operations — verdicts, metadata, cache, state — whose shapes
are ABI surface. `HostCallExtension(cmd string, args []byte)` takes host FEATURE
commands (`torana_*`, `verify_virtual_key`) whose payloads are defined by the
feature, not the ABI; the body is opaque, the `HostCallResult` envelope is not.
Each rejects the other's namespace, so there is one route per operation. Pass
the canonical token (`torana_plugin_counter`), never the permission string
(`env.host_call.torana_plugin_counter`). The supported extension set is closed
in this SDK version and the host gates every call on the exact
`env.host_call.<command>` grant. Prefer the typed wrappers where they exist —
`SendRequest`, `GetCachePricing`.

Durable state is typed too: `StateGetArgs`, `StateSetArgs`, `StateDeleteArgs`
(`env.state_keys`, `env.now`, `env.plugin_config` and the originals take no
body — pass `nil`). **Deletion is `env.state_delete`, not a set with an empty
value**: v1 used the empty value, which made storing an empty string
impossible and contradicted the other two stores. Command
`env.state_delete` is authorised by permission `env.state_set`
(`pbv2.StateDeleteCommand` / `StateDeletePermission`) — a dispatcher
special-case exactly like `env.meta_append`; do NOT derive the permission from
the command string.

Metadata and cache reads/writes are also typed: `MetaGetArgs`, `MetaSetArgs`,
`CacheGetArgs`, `CacheSetArgs`, reached through `sdk.MetaGet` / `sdk.MetaSet` /
`sdk.CacheGet` / `sdk.CacheSet`. **A key is required; an empty value is not an
error.** Absence and emptiness are different results and the host must keep them
apart: an absent key answers `error{code: NOT_FOUND}`, a key holding `""`
answers the `value` arm with empty bytes. Guests branch with `sdk.IsNotFound`.
Meta is request-scoped and namespaced per plugin; cache is one flat namespace
shared by every plugin, so an unprefixed cache key is one another plugin can
overwrite (`sdk.ContentAddressedCacheKey`).

Go authors use typed results (`PassRequest` / `ReplaceRequest` / …), handler
`(Result, error)` signatures (errors trap), fire-and-forget verdict helpers that
panic on local/protocol failure, `HostCall(cmd, proto.Message)`,
`MetaGet`/`MetaSet`/`CacheGet`/`CacheSet` (never raw `HostCall`) and
`StreamHandler` / `StreamAssembler` for host-backed stream assembly.
`sdktest.Harness.Run(fn)` runs helper code that makes host calls outside a hook
dispatch.
## 7. Summary Checklist for AI Agents
1. Did I use a real allocator (not a bump allocator)?
2. Did I implement both `alloc` and `dealloc`?
3. Did I pack the return pointer and size into a `u64`?
4. Did I return `0` for passthrough?
5. Did I parse and serialize Protobuf properly within the memory bounds?
6. Did I use the supported v2 export shape (`run_hook` + `supported_hooks`),
   declare `abi_version: "v2"`, and request every `env.*` / `ir.*.write` I need?
7. If I Suppress or fan-out stream events (v2), did I request `ir.stream.write`
   **and** the content grants for what I change/remove/add (and avoid altering
   host-owned facts)?
8. Did I avoid forging host-owned response facts (usage, model-that-answered,
   signatures, provider extension blobs), and avoid leaving stale signatures on
   mutated signed content? (`ReplaceToolArguments` clears `ToolCallRef.signature`.)
9. If I read meta or cache, did I branch on `sdk.IsNotFound(herr)` rather than
   testing whether the value is `""`? A stored empty string is a real value,
   and a permission denial is not a cache miss.
10. If I implemented a tick, did I use `TickIdle` / `TickDid`? `env.meta_*`
   works on the synthetic tick scope, but original request/response and caller
   credentials do not.
