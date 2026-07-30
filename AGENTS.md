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

## Two failure modes worth naming up front

Both produce a plugin that loads, reports healthy, and is wrong.

**Returning zero means pass-through.** Any result you build must set its
`handled` flag — `StreamEventResult`, `HttpResponse`, and `TickResult` all have
one. An all-defaults protobuf message encodes to *zero bytes*, which the host
cannot distinguish from "this plugin did nothing". If your plugin acts but the
host ignores it, this is why.

**Anything you write into a request must be deterministic.** The same input must
produce byte-identical output every time. Writing a timestamp, a random value, or
the request ID into a request changes the prefix bytes, which invalidates the
provider's prompt cache and costs the operator money on *every* subsequent turn.
The proxy enforces this with a test; a plugin that fails it will not ship.

## If you are extending the SDK itself

Adding a hook or host call touches more places than it looks:

- `sdk.go` is `//go:build wasip1`. Every export there needs a matching no-op in
  `sdk_other.go`, or plugins stop compiling for host-side tests.
- Regenerate protobuf with `./scripts/generate-go.sh` and commit the result — CI
  diffs it. Use the exact `protoc-gen-go` version in the generated file header.
- **`proto/torana/v1` is held unchanged for the duration of the migration, then
  deleted.** It is not a supported surface. torana-edge and the official plugins
  still import it while they move to v2 one repo at a time, and CI runs
  `buf breaking` against `main` for this path so that migration is not disturbed
  mid-flight. That is the only reason it is protected.
  - **Do not add v1 features, fixes, or a v1→v2 compatibility layer.** Work that
    would touch v1 belongs in v2.
  - The coordinated cut deletes v1. `scripts/check-abi-breaking.sh` then protects
    **only v2** (scoped with `--path`), so comparing against a `main` that still
    contains v1 does not report the intentional deletion as a breaking change.
    After that PR merges, `main` has only v2 and the restriction can be
    simplified.
  - Torana has not launched, so there is no installed base to preserve. The
    published `v0.2.0` tag keeps working regardless — module proxy tags are
    immutable, so deleting v1 from the tree cannot reach anyone who pinned it.
- **`proto/torana/v2` is unreleased and may still be reshaped.** Nothing pins it
  and nothing consumes it, so freezing it would preserve design mistakes rather
  than prevent them — its shape has already improved several times under review.
  While `proto/torana/v1` still exists, CI protects only v1. The hard guarantee
  that no `v0.3.x` (or later) tag can ship before the cut is
  `scripts/assert-v2-cut-for-release.sh` in `release.yml`, which runs **before**
  any package or publish step. It proves behaviour, not string presence:
  `check-abi-breaking.sh --print-path` must return `proto/torana/v2`, `ci.yml`
  must actually `run:` that script, both v2 sources must exist, both v1 sources
  and `pb/torana.pb.go` must be gone. A guard that lived only on `pull_request`
  would notice too late.
- Update `docs/WASM_PLUGIN_GUIDE.md` too. It is the document that makes this SDK
  usable by weaker models, and a capability missing from its checklist silently
  makes the guide insufficient.
