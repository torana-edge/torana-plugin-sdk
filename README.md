# Torana Plugin SDK

The versioned SDK for Torana WASM plugins. It includes the canonical ABI,
Go and Rust authoring paths, templates, and portable conformance fixtures.

```bash
go test ./...
GOOS=wasip1 GOARCH=wasm go build -o plugin.wasm ./examples/go-logger
```

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
| [ABI v1](ABI.md) | The normative contract, plus what you need to implement an SDK in another language |

Official plugins built on this SDK live in
[torana-plugins](https://github.com/torana-edge/torana-plugins). The proxy itself is
[torana-edge](https://github.com/torana-edge/torana-edge).
