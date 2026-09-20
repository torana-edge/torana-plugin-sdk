# Torana Plugin SDK

Write a plugin once. Use it across the coding harnesses and supported model
APIs you route through [Torana](https://github.com/torana-edge/torana-edge).

The Go and Rust SDKs give your plugin a shared request/response format, typed
hooks, and helpers for safe transformations. Torana handles provider parsing,
WASM isolation and operator-approved resources.

## Build your first plugin

With Torana installed:

```bash
torana plugin new my-plugin                 # Go
torana plugin new my-rust-plugin --language rust
```

Choose one and follow [Your first plugin](docs/FIRST_PLUGIN.md): build, inspect,
approve, enable, and see it handle a request. You do not need to fork this repo
or register a plugin to share it.

## Find the right guide

| You want to… | Read |
| --- | --- |
| Get a plugin running | [First plugin](docs/FIRST_PLUGIN.md) |
| Add hooks, settings, resources or tests | [Authoring reference](docs/WRITING_A_PLUGIN.md) |
| Understand mutation, streaming and cache safety | [Plugin semantics](docs/PLUGIN_SEMANTICS.md) |
| Implement an SDK or inspect the ABI | [WASM contract](docs/WASM_PLUGIN_GUIDE.md) |
| Look up permissions | [Capability reference](capabilities-reference.md) |
| Work in Rust | [Rust SDK](rust/torana-plugin-sdk/README.md) |
| Find working plugins to learn from | [Official plugins](https://github.com/torana-edge/torana-plugins) |

Plugins have no ambient filesystem or sockets. Files, HTTP endpoints, model
services, pricing and credentials must be declared and approved for the exact
bundle. Private cache entries belong to one plugin; cross-plugin exchange
requires separate shared-cache permissions.

For optional JSON operations exposed to agents, see
[Add agent-facing operations](docs/AGENT_OPERATIONS.md).

## Compatibility

SDK **v0.5.1** implements ABI major **1**, contract revision **1**. SDK package
versions and Edge releases are separate. Use the SDK version documented by
your target host. Go uses the module tag, and Rust uses the matching crate
release.

## Contributing

```bash
go test ./...
./scripts/check-doc-examples.sh
```

The example check executes maintained Go recipes and compiles the Rust
examples; it is not a test of every prose snippet. Go WASM plugins require
reactor mode (`-buildmode=c-shared`), which `torana plugin build` supplies.

Both languages run compiled guests through Edge's conformance harness. ABI
changes need matching Go/Rust helpers, generated bindings, host enforcement
and cross-repository tests. Read [AGENTS.md](AGENTS.md) for repository
contribution boundaries and [RELEASING.md](RELEASING.md) for release procedures.
