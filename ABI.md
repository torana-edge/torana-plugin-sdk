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

## Implementing this ABI

The rules above are the contract. Satisfying them correctly — the allocator, the
packed return, memory ownership across the boundary — is covered in
[docs/WASM_PLUGIN_GUIDE.md](docs/WASM_PLUGIN_GUIDE.md), which is written for AI
coding agents and humans implementing a plugin or an SDK from scratch. Every
failure mode there is silent, so it ends in a checklist worth actually running.

Go and Rust authors do not need it: the SDK in this repository already implements
the boundary. Start at [docs/WRITING_A_PLUGIN.md](docs/WRITING_A_PLUGIN.md).

## Compatibility

Additive protobuf fields and new optional host calls are allowed in v1. An
export signature change, a changed packed-result layout, or a removed field is
an ABI-major change. Run this repository's conformance suite before publishing
an artifact.
