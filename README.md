# Torana Plugin SDK

The versioned SDK for Torana WASM plugins. The supported path for the current
Torana Edge host is the Go ABI v2 SDK, templates, and conformance fixtures.

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
protobuf API as `github.com/torana-edge/torana-plugin-sdk/pb/v2`. Go guests
export the v2 surface (`run_hook`, `supported_hooks`). Declare
`"abi_version": "v2"` in `plugin.json`.

The Rust crate in `rust/torana-plugin-sdk` still speaks the ABI v1 trampoline.
The current Edge host accepts ABI v2 only, so that crate is historical and
unsupported until it is fully ported or deliberately removed. Do not advertise
or ship it as a compatible authoring path.

After changing the ABI, regenerate the checked-in Go bindings with
`./scripts/generate-go.sh`, then run the conformance suite.

## Documentation

| Guide | For |
| --- | --- |
| [Writing a plugin](docs/WRITING_A_PLUGIN.md) | Start here — scaffold, build, install, activate |
| [Plugin semantics and gotchas](docs/PLUGIN_SEMANTICS.md) | Hook behaviour, protobuf decoding, prompt-cache and tool-output safety |
| [Implementing the WASM contract](docs/WASM_PLUGIN_GUIDE.md) | **AI agents and humans** writing a plugin or an SDK from scratch — the boundary, and why every mistake there fails silently |
| [Historical ABI v1](ABI.md) | The old Rust trampoline contract. It is not accepted by the current v2-only host. |

Official plugins built on this SDK live in
[torana-plugins](https://github.com/torana-edge/torana-plugins). The proxy itself is
[torana-edge](https://github.com/torana-edge/torana-edge).
