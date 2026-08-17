# Notes for AI coding agents

The current Torana Edge host accepts ABI v2 plugins. Use the Go SDK and
`pb/v2`; declare `"abi_version": "v2"`. The Go and Rust SDKs both implement
ABI v2 and run through the host conformance harness.

Before changing a plugin, read in order:

1. `docs/WASM_PLUGIN_GUIDE.md`
2. `docs/WRITING_A_PLUGIN.md`
3. `docs/PLUGIN_SEMANTICS.md`

## Boundaries that must stay explicit

- Use typed v2 result constructors. An empty `HookResult` is pass-through; a
  non-nil handler error traps and the host applies the approved failure mode.
- Request mutations must be deterministic. Nondeterminism destroys provider
  cache identity and costs users money.
- Use provenance-aware mutation helpers for signed content and cache markers.
- Request every host-call and narrow IR write permission actually exercised;
  the host remains authoritative even on the all-grants path.
- Treat typed refusals, malformed frames, local validation defects, and
  provider outcomes as different classes. Never branch on error text.

## Extending the SDK

- Keep `sdk.go` and the non-WASI stubs in sync.
- Regenerate checked-in protobuf code with `scripts/generate-go.sh` and verify
  a second generation is byte-identical.
- Add descriptor/reflection inventory tests when fields, permissions, signed
  surfaces, or ordered carriers change.
- Update both author guides and host integration coverage.
- Changes spanning SDK, Edge, and official plugins land in that dependency
  order with exact revision pins and a final cross-repository gate.

Only ABI v2 exists in current source. Do not add legacy compatibility layers.
