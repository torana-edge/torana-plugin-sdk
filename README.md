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

See [ABI.md](ABI.md) for stability and capability rules.
