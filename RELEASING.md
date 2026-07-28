# Releasing the SDK

Read this before creating a tag. Tags here are effectively permanent, and two
releases have already been cut wrong.

## Why a tag cannot be taken back

Go module proxies cache a tag's content **immutably**. The moment anything runs
`go get …@v0.1.2`, `proxy.golang.org` stores those bytes and serves them forever,
regardless of what the tag later points at. Moving the tag in git changes
nothing for anyone downloading through the proxy — and leaves the repository and
the proxy disagreeing about what that version *is*, which is worse than either
being wrong on its own.

So a bad tag is not fixed by re-tagging. It is fixed by cutting the next version.
Get it right the first time.

## The version lives in two places and they must agree

| Where | What |
|---|---|
| the git tag | `vX.Y.Z` — what Go consumers resolve |
| `rust/torana-plugin-sdk/Cargo.toml` | `version = "X.Y.Z"` — what Rust consumers install from crates.io |

Go has no version file, so the tag is its only source of truth. Rust does, so it
has to be bumped by hand in the same change.

The release workflow asserts they match, but **it only runs once the tag
exists** — by which point the tag is already immutable. That check is a
backstop, not a guardrail. `v0.1.1` and `v0.1.2` both tripped it: their releases
produced no artifacts and no provenance attestation, and the Rust crate still
advertised `0.1.0`.

## Cutting a release

1. **Bump `rust/torana-plugin-sdk/Cargo.toml`** to the version you are about to
   tag, without the `v`. Commit it to `main`.
2. **Verify** — `go test ./...`, `cargo test --manifest-path rust/torana-plugin-sdk/Cargo.toml`,
   and a `GOOS=wasip1 GOARCH=wasm go build ./...` (a compile check of the SDK
   library — no `-buildmode=c-shared`, because nothing here is a plugin).
3. **Tag and push.**
   ```bash
   git tag -a v0.2.1 -m "…"
   git push origin v0.2.1
   ```
   Write a real annotation: new hooks, new host calls, behaviour changes, and
   anything a plugin author has to change. It becomes the release notes.
4. **Ensure the repository has a `CARGO_REGISTRY_TOKEN` secret** authorized to
   publish `torana-plugin-sdk`.
5. **Watch the release workflow.** It runs both test suites, asserts the version
   match, publishes the Rust crate, builds the example plugins, publishes a
   GitHub Release with checksums, and attests build provenance. A failure here
   means the release train is incomplete.

## Then the downstream repos

Nothing consumes the new version until it is asked to, and CI in both repos
resolves the **published** module — so a change that spans repos is red until
this is done. A local `go.work` hides that completely, which is exactly how it
gets missed.

```bash
# torana-edge
cd torana-edge
GOWORK=off go get github.com/torana-edge/torana-plugin-sdk@v0.2.1
GOWORK=off go build ./... && GOWORK=off make testdata  # prove it without the workspace

# torana-plugins — every plugin module pins the SDK separately
cd ../torana-plugins
for d in plugins/*/; do (cd "$d" && go get github.com/torana-edge/torana-plugin-sdk@v0.2.1); done
# SDK_REF is what CI and the release job check the SDK out at — bump it too, or
# published bundles keep being built against the previous SDK.
echo v0.2.1 > SDK_REF
./scripts/test.sh
```

**Always verify with `GOWORK=off`, or with `go.work` moved aside.** The
workspace makes cross-repo development possible and makes a missing published
dependency invisible; everything builds locally and fails in CI.

If `go get` reports a `sum.golang.org … 500`, the proxy has not indexed the tag
yet. Retry — the first lookup is what triggers indexing.

## Order

```
SDK: bump Cargo.toml → tag → release green
  └── torana-edge: bump go.mod → CI green → merge
        └── torana-plugins: bump every plugin go.mod → CI green → merge
```

The plugins repo goes last: its CI builds against the host's expectations, so
merging it before the host lands means testing plugins against a proxy that does
not yet have what they need.
