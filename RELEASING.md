# Releasing the SDK

Read this before creating a tag. Tags here are effectively permanent.

## Why a tag cannot be taken back

Go module proxies cache a tag's content **immutably**. The moment anything runs
`go get …@vX.Y.Z`, `proxy.golang.org` stores those bytes and serves them forever,
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
backstop, not a guardrail. Before tagging, the pull-request gate must prove the
crate version is not behind the repository's latest tag.

## Cutting a release

Go, Rust, and the Edge host implement ABI v1. Keep both compiled conformance
guests in the host test: the language-support claim is an executable contract,
not merely two crates that happen to compile.

1. **Bump `rust/torana-plugin-sdk/Cargo.toml`** to the version you are about to
   tag, without the `v`, and update all three Cargo lockfiles:
   `rust/torana-plugin-sdk/Cargo.lock`, `examples/rust-logger/Cargo.lock`, and
   `conformance/guests/rust-allhooks/Cargo.lock`. Choose
   an unused version newer than the latest tag. ABI and package versions are
   independent: ABI v1 does not imply SDK v0.1. Commit the version to `main`.
2. **Verify** — `go test ./...`,
   `cargo test --locked --manifest-path rust/torana-plugin-sdk/Cargo.toml`,
   `cargo build --locked --target wasm32-wasip1 --manifest-path examples/rust-logger/Cargo.toml`,
   `cargo build --locked --target wasm32-wasip1 --manifest-path conformance/guests/rust-allhooks/Cargo.toml`,
   and `GOOS=wasip1 GOARCH=wasm go build ./...` (a compile check of the SDK
   library — no `-buildmode=c-shared`, because nothing here is a plugin).
3. **Check publishing access before tagging.** The repository needs a
   `CARGO_REGISTRY_TOKEN` Actions secret restricted to the crate name
   `torana-plugin-sdk`. For the first publication, grant `publish-new` and
   `publish-update`; after that, an update-only replacement is sufficient.
   Do not grant owner-management, yank, or unrestricted legacy access. Use an
   expiration date and rotate the secret before it expires. Never put a token
   in a command argument, release note, issue, or pull request.
4. **Tag and push a commit already on the default branch.** Both publishing
   workflows reject source commits that are not ancestors of its current tip.
   ```bash
   git tag -a vX.Y.Z -m "…"
   git push origin vX.Y.Z
   ```
   Write a real annotation: new hooks, new host calls, behaviour changes, and
   anything a plugin author has to change. It becomes the release notes.
5. **Watch the release workflow.** It runs both test suites, asserts the version
   match, packages the Rust crate, builds the example plugins, publishes a
   GitHub Release with checksums, and attests build provenance. Rust publishing
   runs last: a crates.io failure stays visible but leaves the GitHub release
   and Go module available. Resolve Rust publication separately without moving
   the tag; verify the failing step before proceeding with Go consumers.

## Recover Rust publication without replacing the GitHub release

If a GitHub release succeeds but the Rust upload fails, recover that
distribution channel without moving the tag or regenerating release assets.
Published Rust setup instructions use crates.io, so restore that channel before
announcing the release or updating downstream scaffolds.

1. Confirm the registry does not already contain the intended version. A
   publish timeout can occur after the upload succeeds; do not assume failure
   means nothing was published. See [Cargo's publish behavior](https://doc.rust-lang.org/cargo/commands/cargo-publish.html).
2. Add or repair the repository Actions secret described above. Ensure the
   crates.io account can publish this crate and has completed email verification.
3. Dispatch `publish-rust.yml` from the default branch for the existing tag:

   ```bash
   gh workflow run publish-rust.yml --repo torana-edge/torana-plugin-sdk -f tag=vX.Y.Z
   ```

   The recovery workflow requires an existing published `vX.Y.Z` GitHub
   release, resolves its tag to an immutable commit, and checks that the commit
   is reachable from the repository's default branch before running any of its
   code. It then checks the crate version, runs Rust tests and the independent
   packaged-consumer check, and publishes only the Rust crate. It does not
   modify a tag, GitHub release, or its assets.
4. Verify the version on crates.io and its docs.rs build. A green GitHub release
   job alone is not evidence that every distribution channel is available.

Token scope details: [crates.io scope definitions](https://rust-lang.github.io/rfcs/2947-crates-io-token-scopes.html).
Manual dispatch requires the workflow on the default branch:
[GitHub workflow documentation](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/manually-run-a-workflow).

The registry token is available only to the final `cargo publish` step. Cargo
rebuilds the packaged crate for verification in that step, so its build scripts
and dependencies inherit the token then; this verification remains enabled.
The ancestry check relies on the default branch's review protections. It does
not add a separate human approval gate for each publication.

## Then the downstream repos

Nothing consumes the new version until it is asked to, and CI in both repos
resolves the **published** module — so a change that spans repos is red until
this is done. A local `go.work` hides that completely, which is exactly how it
gets missed.

```bash
# torana-edge
cd torana-edge
GOWORK=off go get github.com/torana-edge/torana-plugin-sdk@vX.Y.Z
GOWORK=off go build ./... && GOWORK=off make testdata  # prove it without the workspace

# torana-plugins — every plugin module pins the SDK separately
cd ../torana-plugins
for d in plugins/*/; do (cd "$d" && go get github.com/torana-edge/torana-plugin-sdk@vX.Y.Z); done
# SDK_REF is what CI and the release job check the SDK out at — bump it too, or
# published bundles keep being built against the previous SDK.
echo vX.Y.Z > SDK_REF
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
