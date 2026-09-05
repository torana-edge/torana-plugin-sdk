# Torana Plugin SDK

The versioned Go and Rust SDKs for Torana WASM Plugin ABI v1.

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
protobuf API as `github.com/torana-edge/torana-plugin-sdk/pb/v1`. Go guests
export the v1 surface (`run_hook`, `supported_hooks`). Declare
`"abi_version": "v1"` in `plugin.json`.

The Rust crate in `rust/torana-plugin-sdk` exports the same ABI surface. Its
logger and all-hooks guest run through the shared host conformance harness in
CI, so Rust support is exercised as an executable contract rather than only a
compile example.

After changing the ABI, regenerate the checked-in Go bindings with
`./scripts/generate-go.sh`, then run the conformance suite.

Plugins have no ambient filesystem or sockets. Capability-scoped helpers expose
operator-bound credentials, plugin-private logical files, exact-origin HTTP
endpoints, model services, and pricing resources in both Go and Rust. A
manifest must declare each resource and the operator must approve the exact
bundle before the host call succeeds. Model-service bindings—not guest code—own
provider URLs, models, credentials, and hard budgets.

## Documentation

| Guide | For |
| --- | --- |
| [Writing a plugin](docs/WRITING_A_PLUGIN.md) | Start here — scaffold, build, install, activate |
| [Plugin semantics and gotchas](docs/PLUGIN_SEMANTICS.md) | Hook behaviour, protobuf decoding, prompt-cache and tool-output safety |
| [Implementing the WASM contract](docs/WASM_PLUGIN_GUIDE.md) | **AI agents and humans** writing a plugin or an SDK from scratch — the boundary, and why every mistake there fails silently |

Official plugins built on this SDK live in
[torana-plugins](https://github.com/torana-edge/torana-plugins). The proxy itself is
[torana-edge](https://github.com/torana-edge/torana-edge).

## Cache namespaces

`env.cache_get` and `env.cache_set` are private to the executing plugin. Two
plugins using the same key cannot observe or overwrite each other's values.

Intentional cross-plugin exchange uses the separately approved
`env.shared_cache_get` / `env.shared_cache_set` capabilities through
`sdk.SharedCacheGet` / `sdk.SharedCacheSet`. Private cache grants never imply
shared-cache access.
