# Torana Plugin SDK

The versioned SDK for Torana WASM plugins. It includes the canonical ABI,
Go and Rust authoring paths, templates, and portable conformance fixtures.

```bash
go test ./...
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm ./examples/go-logger
```

**`-buildmode=c-shared` is not optional.** Without it Go produces a *command*
module: the host instantiates it, `main()` runs to completion, and the module
exits before a single hook is called. The hooks are still exported and
instantiation still reports success — wazero treats a clean exit as one — so the
plugin loads and is pooled. Every call against it then fails with
`module closed with exit_code(0)`, and the operator's `failure_mode` decides
what happens to the request.

So it is not silent, but it fails on every request with an error that names
nothing you did wrong. Torana needs a *reactor* module that stays resident and
waits to be called. See
[docs/PLUGIN_SEMANTICS.md](docs/PLUGIN_SEMANTICS.md).

Use the Go package as `github.com/torana-edge/torana-plugin-sdk` and the
protobuf API as `github.com/torana-edge/torana-plugin-sdk/pb`.

The Rust crate lives in `rust/torana-plugin-sdk`; its build script generates
bindings from the checked-in v1 protobuf contract. Rust builds require
`protoc` and the WASI target configured by the caller.

After changing the ABI, regenerate the checked-in Go bindings with
`./scripts/generate-go.sh`, then run the conformance suite.

## Documentation

| Guide | For |
| --- | --- |
| [Writing a plugin](docs/WRITING_A_PLUGIN.md) | Start here — scaffold, build, install, activate |
| [Plugin semantics and gotchas](docs/PLUGIN_SEMANTICS.md) | Hook behaviour, protobuf decoding, prompt-cache and tool-output safety |
| [Implementing the WASM contract](docs/WASM_PLUGIN_GUIDE.md) | **AI agents and humans** writing a plugin or an SDK from scratch — the boundary, and why every mistake there fails silently |
| [ABI v1](ABI.md) | The normative contract |

Official plugins built on this SDK live in
[torana-plugins](https://github.com/torana-edge/torana-plugins). The proxy itself is
[torana-edge](https://github.com/torana-edge/torana-edge).
