# Historical Torana Plugin ABI v1

> **Unsupported by the current host.** Torana Edge is ABI v2-only. This file
> documents the older trampoline still implemented by the unported Rust crate;
> it is retained for provenance, not as a supported plugin-authoring contract.

The ABI is the contract between a Torana host and a WASI Preview 1 plugin.
It is versioned independently from the proxy. `abi_version: "v1"` means that
the exported functions and protobuf messages below are stable for all v1
releases.

## Guest exports

Every plugin exports `alloc(size) -> ptr` and `dealloc(ptr, size)`. Hooks use
`(request_id: u64, ptr: u32, len: u32) -> u64`; a non-zero result packs the
returned pointer in its high 32 bits and length in its low 32 bits. A zero
result is pass-through. Hosts own input buffers; guests own output buffers
until the host calls `dealloc`.

WebAssembly itself has only `i32` and `i64`, which carry no signedness — a
`.wat` signature will always read `i32`. What the notation above specifies is
how those bits must be **interpreted**: as unsigned, in both the guest source
and the host.

That is not cosmetic. A pointer above 2 GiB is reachable inside a 4 GiB wasm
memory and has its high bit set. Interpreted as signed it is negative, and
packing it into the return value sign-extends and corrupts the high half. A
guest written to the signed reading works right up until a plugin allocates
enough memory, then fails in a way that looks like memory corruption rather
than a spec misreading.

Returning zero is reserved for intentional pass-through. A handler or codec
error traps the guest call; the host discards that instance and applies the
operator-approved `failure_mode` (`pass` or `block`). The SDK helpers implement
this convention so ordinary handler failures cannot resemble success.

Supported v1 hooks are `run_before_request`, `run_after_response`,
`run_on_stream_chunk`, `run_on_http_request`, and `run_on_tick`. Their protobuf
messages are defined in
[`proto/torana/v1/torana.proto`](proto/torana/v1/torana.proto).

| Hook | Input message | Returns |
|---|---|---|
| `run_before_request` | `ChatRequest` | `ChatRequest`, or 0 for pass-through |
| `run_after_response` | **`ChatRequest`** | `ChatRequest`, or 0 for pass-through |
| `run_on_stream_chunk` | `StreamEvent` | `StreamEventResult`, or 0 |
| `run_on_http_request` | `HttpRequest` | `HttpResponse`, or 0 |
| `run_on_tick` | `TickRequest` | `TickResult`, or 0 |

**`run_after_response` takes a `ChatRequest`, and there is no `ChatResponse` in
this contract.** Torana normalises a provider's reply into the same message
shape it uses for a request: the assistant's turn arrives as `messages`, its
tool calls as `tool_calls`, and provider metadata (latency, upstream status,
token usage) under `torana_meta_json["_response"]`.

One shape is a deliberate choice. A plugin that rewrites message content works
identically in both directions, and the host's four provider adapters have one
target to normalise into rather than two. Adding a distinct `ChatResponse` would
be a v2 change and would duplicate every field.

`run_on_tick` is the only hook that fires with no request in flight, so a plugin
declaring it can act on elapsed time. It requires the `env.background_tick`
permission, and inside it there is no request: `env.original_request`,
`env.original_response`, and `env.meta_*` have nothing to read, and no caller
credential exists. Anything a tick needs must come from the plugin's own durable
state or from a host call that resolves its own configuration.

## Imports and grants

Hosts may expose `env.log`, `env.emit_metric`, and `env.host_call`. A manifest
permission is only a request: the operator must grant it to the immutable
artifact digest before the host exposes the corresponding capability.

Plugins must tolerate a missing or denied host call. They must not assume
filesystem, network, environment-variable, clock, or random access.

## Implementing this ABI

The rules above are the contract. Satisfying them correctly — the allocator, the
packed return, memory ownership across the boundary — is covered in
[docs/WASM_PLUGIN_GUIDE.md](docs/WASM_PLUGIN_GUIDE.md), which is written for AI
coding agents and humans implementing a plugin or an SDK from scratch. Every
failure mode there is silent, so it ends in a checklist worth actually running.

Current plugin authors do not need it: use the Go ABI v2 SDK and start at
[docs/WRITING_A_PLUGIN.md](docs/WRITING_A_PLUGIN.md). The Rust v1 implementation
is retained only as historical source and cannot load in the current host.

## Compatibility

Additive protobuf fields and new optional host calls are allowed in v1. An
export signature change, a changed packed-result layout, or a removed field is
an ABI-major change. Run this repository's conformance suite before publishing
an artifact.
