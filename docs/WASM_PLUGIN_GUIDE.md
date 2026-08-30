# Implementing a Torana plugin: the WASM contract

This is the low-level ABI-v1 reference for humans or language-SDK authors. Go
and Rust plugin authors should normally start with
[`WRITING_A_PLUGIN.md`](WRITING_A_PLUGIN.md) and use the maintained SDK rather
than implementing this boundary themselves.

Torana accepts **ABI v1 only**. A plugin declares `"abi_version": "v1"` and
uses the protobuf contract in
[`proto/torana/v1/torana.proto`](../proto/torana/v1/torana.proto). Go and Rust
are the supported authoring paths because both have maintained examples and
compiled guests exercised through the real host conformance harness. Merely
being able to target WASI does not make another language supported.

## 1. Linear memory and ownership

The host and guest exchange protobuf bytes through WASM32 linear memory. A
guest exports:

```text
alloc(size: u32) -> u32
dealloc(ptr: u32, size: u32)
```

The host allocates guest input memory, writes one serialized `HookInput`, calls
the hook, and frees the input. A non-empty hook result is guest-owned memory;
the host reads it and calls `dealloc` with the same pointer and size.

Use a real allocator. A bump allocator leaks on every request. Allocation
failure must trap: returning a null pointer is indistinguishable from offset
zero or pass-through and can silently fail open. Deallocation must use exactly
the layout used for allocation.

Standard Go plugins must use the reactor build mode:

```bash
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .
```

Without `-buildmode=c-shared`, `main` exits at instantiation and every later
hook call sees a closed module. The Go SDK owns the allocator and exports; do
not copy them into normal plugins.

The Rust crate similarly owns the allocator and v1 exports. Its
`export_plugin_v1!` macro is the supported entry point.

## 2. Exports, hook bitmap, and result framing

ABI v1 has one dispatcher and one declaration bitmap:

```text
supported_hooks() -> u32
run_hook(ptr: u32, size: u32) -> u64
```

`supported_hooks` returns the OR of the `Hook` bits the plugin implements.
`run_hook` receives one serialized `HookInput`. The hook named by its oneof arm
must be present in the bitmap and must match the payload.

The return value packs the output pointer in the high 32 bits and output length
in the low 32 bits:

```text
(u64(ptr) << 32) | u64(length)
```

Returning zero means exact pass-through. Any non-zero output is one serialized
`HookResult` with exactly one action valid for the invoked hook. Never encode an
empty result as a successful mutation, and never let protobuf oneof last-wins
semantics hide multiple action arms: validate the wire frame before accepting
it.

Errors in guest decoding, local validation, or handler execution should trap so
the operator-approved `failure_mode` applies. They must not become an empty
result, because empty output means “continue unchanged.”

Minimal Rust shape:

```rust
use torana_plugin_sdk::{export_plugin_v1, pbv1, HOOK_BEFORE_REQUEST};

fn dispatch(input: pbv1::HookInput) -> Result<Option<pbv1::HookResult>, String> {
    let Some(pbv1::hook_input::Payload::ChatRequest(_request)) = input.payload else {
        return Err("received an undeclared hook".into());
    };
    Ok(None) // pass through
}

export_plugin_v1!(HOOK_BEFORE_REQUEST, dispatch);
```

## 3. Host imports and refusal framing

The stable imports live in module `env`:

```text
log(level: i32, ptr: u32, len: u32)
emit_metric(kind: i32, ptr: u32, len: u32,
            value: f64, labels_ptr: u32, labels_len: u32)
host_call(command_ptr: u32, command_len: u32,
          args_ptr: u32, args_len: u32) -> u64
```

Every import requires its exact approved manifest permission. A manifest asks;
the operator's approval of the exact bundle digest grants.

`host_call` returns packed host-owned bytes containing `HostCallResult`. The
envelope must contain exactly one `value` or classified `HostError` arm. An
empty envelope, malformed frame, unknown field, unspecified error code, or
unknown error code is a protocol error—not success and not an advisory refusal.
Branch on error codes, never diagnostic strings.

Core `env.*` operations use protobuf arguments through the typed SDK helpers.
Feature calls such as `torana_send_request` use the extension path and their
closed command vocabulary. Do not pass permission strings as command names.

Absence and an empty stored value are different. Cache, metadata, and state
lookups report absence as `NOT_FOUND`; a present empty value remains a success.
State deletion uses the typed delete command, authorized by `env.state_set`.

## 4. Mutation authority and provenance

Every accepted request, response, or stream mutation is verified by the host.
Declare the narrow `ir.*.write` permissions for the sections that actually
change:

- role grants cover ordinary message content for that role;
- `ir.tool_results.write` covers only position-preserving tool-result text
  value changes, independent of enclosing role;
- `ir.cache_control.write` covers only cache-breakpoint marker fields;
- `ir.tools.write` covers tool definitions and request-side tool changes;
- `ir.model.write` and `ir.params.write` cover request selection and parameters;
- `ir.stream.write` covers stream topology and is additive to content grants.

Topology changes, arm insertion/removal, and content changes may require a
union of grants. The SDK's provenance-aware helpers implement the allowed
signature clearing rules; use them instead of directly rewriting signed
blocks.

Opaque provider signatures bind their documented content. A real covered
content change requires the applicable signature to be cleared. A byte-identical
operation preserves it. Stale, forged, gratuitously dropped, or independently
altered signatures are rejected. Host-owned response facts and provider
identity fields cannot be forged even with every write grant.

Cache-marker changes and tool-result text changes have deliberately narrow
signature effects. Use `ReplaceLastCacheBreakpoint`,
`ReplaceToolResultText`, `ReplaceToolCall`, `SetTextAt`, and the other typed
helpers so the verified provenance contract remains synchronized with guest
behavior.

## 5. Streams and background ticks

Stream events use globally unique block indexes within a message. Text,
thinking, and provider blocks are exclusive; multiple tool-call blocks may be
open concurrently when each has a distinct index. Stops must match an open
block. `MessageStop` with an open block is invalid.

`StreamError` is a terminal, host-owned abort. It abandons every open block and
incomplete assembly without synthetic stops, requires no later `MessageStop`,
and forbids later events. A plugin may not forge a provider-looking stream
error.

Use `StreamHandler` or `StreamAssembler` for host-backed tool-call assembly.
Suppressing, reindexing, splitting, joining, or fanning out events requires
`ir.stream.write` plus every changed content grant. A one-for-one assistant text
delta rewrite needs only `ir.messages.write.assistant`.

`run_on_tick` has no caller request or credential. Original request/response
calls are unavailable; durable work must come from plugin state, cache, config,
or explicitly budgeted provider egress. Return pass-through when idle and a
`TickOutcome` only for completed work. Background execution requires both the
`env.background_tick` permission and an operator-configured cadence.

## 6. Checklist

1. Is the manifest ABI exactly `v1`?
2. Does the guest use a real allocator and matching deallocator?
3. Does it export `supported_hooks` and `run_hook` with the exact signatures?
4. Does zero output mean only intentional pass-through?
5. Are `HookInput`, `HookResult`, and `HostCallResult` decoded and validated
   before use?
6. Does every local/protocol failure trap or return an explicit typed error
   rather than silently pass?
7. Are all host calls made through the correct typed SDK path and classified by
   error code?
8. Does the manifest request every exercised host-call and narrow IR write
   grant, and no stale grant?
9. Are signed mutations performed through provenance-aware SDK helpers?
10. Do stream topology mutations request `ir.stream.write` plus the affected
    content grants?
11. Are absence and present-empty values kept distinct?
12. Does a compiled guest run through the real host conformance harness, not
    merely compile?
