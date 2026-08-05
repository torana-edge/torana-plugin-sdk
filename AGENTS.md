# Notes for AI coding agents

The current Torana Edge host accepts ABI v2 plugins. Use the Go SDK and
`pb/v2`; declare `"abi_version": "v2"`. The Rust crate in this repository is
still ABI v1 and is not compatible with the current host.

Before changing a plugin, read in order:

1. `docs/WASM_PLUGIN_GUIDE.md`
2. `docs/WRITING_A_PLUGIN.md`
3. `docs/PLUGIN_SEMANTICS.md`

`ABI.md` is a historical v1 reference, not the active contract.

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

ABI v1 remains only because the unported Rust crate consumes it. Do not add v1
features or compatibility layers. Before a public release that promises Rust,
port it completely to v2; otherwise remove/archive the v1 surface and describe
Rust as unsupported.
