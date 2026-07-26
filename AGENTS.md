# Notes for AI coding agents

If you are generating or modifying a **Torana plugin**, read
[`docs/WASM_PLUGIN_GUIDE.md`](docs/WASM_PLUGIN_GUIDE.md) before writing code, and
check your output against the checklist at the end of it.

That guide exists for a specific reason. The WASM boundary is the part models get
wrong most reliably, and every mistake fails *silently* — a wrong allocator, an
unpacked return value, or a mishandled pointer produces plausible empty output
rather than an error. Nothing crashes. The plugin loads, reports healthy, and does
nothing.

Order to read in:

1. [`docs/WASM_PLUGIN_GUIDE.md`](docs/WASM_PLUGIN_GUIDE.md) — the boundary, and why it fails quietly
2. [`docs/WRITING_A_PLUGIN.md`](docs/WRITING_A_PLUGIN.md) — scaffold, manifest, build, install
3. [`docs/PLUGIN_SEMANTICS.md`](docs/PLUGIN_SEMANTICS.md) — hook behaviour, protobuf decoding, prompt-cache and tool-output safety
4. [`ABI.md`](ABI.md) — the normative contract

If you are writing in **Go or Rust**, use the SDK in this repository rather than
reimplementing the boundary. It already handles allocation, the packed return,
and protobuf round-tripping.
