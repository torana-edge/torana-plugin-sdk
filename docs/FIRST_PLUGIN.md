# Your first Torana plugin

Give a small piece of your workflow a home outside any one harness. Start with
a pass-through plugin, see it load, then add one behavior.

## 1. Choose Go or Rust

You need [Torana](https://github.com/torana-edge/torana-edge/blob/main/docs/QUICKSTART.md)
running locally. These examples use `torana` on PATH; with a source build,
use the absolute path to that binary when working outside its checkout.
Keep the same `TORANA_DATA_DIR` environment as the running host.

For Go, install Go 1.25 or newer:

```bash
torana plugin new my-plugin
cd my-plugin
```

For Rust, install Rust 1.85+, Cargo and `protoc`:

```bash
rustup target add wasm32-wasip1
torana plugin new my-plugin --language rust
cd my-plugin
```

Choose one path, not both in the same directory. The scaffold pins the SDK
used by your host and generates the source and manifest (plus a settings
schema for Go).
Keep that dependency pin. For Rust, review dependencies/build scripts before
building: Cargo build scripts execute locally outside the WASM sandbox.

## 2. Build and install

Run `torana plugin status` and note the running host's plugin directory.
Because you are now in the new project's directory, select that host directory
explicitly when installing. Replace the example path below with its absolute
path; `TORANA_DATA_DIR` alone does not select the plugin directory.

```bash
torana plugin build .
torana plugin install --dir /absolute/path/to/torana/plugins .
torana plugin inspect my-plugin
```

Keep Rust's generated `Cargo.lock`; installation uses its locked dependency
graph. Build output is local. Installation does not approve or enable it.

Inspect the digest, hooks and requested permissions. The starter is small
enough to read in full. For the Rust starter, the request hook has this shape:

```rust
use torana_plugin_sdk::{export_plugin_v1, pbv1, Plugin, RequestResult, HOOK_BEFORE_REQUEST};
struct MyPlugin;
impl Plugin for MyPlugin {
    const SUPPORTED_HOOKS: u32 = HOOK_BEFORE_REQUEST;
    fn before_request(_: pbv1::ChatRequest) -> Result<RequestResult, String> {
        Ok(RequestResult::pass())
    }
}
export_plugin_v1!(MyPlugin);
```

The equivalent Go callback returns `sdk.PassRequest(), nil`. A replacement
is explicit: `sdk.ReplaceRequest(req)` or Rust's typed replacement result.
Use SDK mutation helpers and request the specific write permissions you need.

## 3. Approve, enable, try

Create `approval.json` from the exact digest and permission set shown by
inspect. The [CLI approval guide](https://github.com/torana-edge/torana-edge/blob/main/docs/CLI.md#plugins-install-is-not-approval-and-approval-is-not-enablement)
shows the JSON shape and resource bindings. Approve the entire declared set
or leave the plugin disabled; resource budgets can be narrowed separately.

```bash
torana plugin approve my-plugin --file approval.json --yes
torana plugin enable my-plugin --yes
torana plugin status
torana feed --follow
```

You can also inspect, approve and enable in the local Web UI at
`http://127.0.0.1:8080/_torana/`.

Send the same inference request from the Torana quickstart. Status should show
the bundle loaded, and its name should appear among the invoked plugins in the
request feed. Pass-through leaves the provider request unchanged. If it does
not load, inspect the reported digest, hook, permission or configuration error;
do not grant extra capabilities to make an unexplained error disappear.

## 4. Make it yours

Try a content-free metric, a tool-definition filter or a deterministic text
rewrite. The [Go authoring reference](WRITING_A_PLUGIN.md#3-writing-plugin-logic)
includes a text-rewrite example. The maintained
[Go recipes](../examples/authoring-go) and [Rust logger](../examples/rust-logger)
provide tested building blocks.

After each rebuild, inspect and approve the new bundle digest. Test the
intended change, unchanged traffic and failure behavior. For Go,
`sdktest.CheckManifest(t, ".")` checks the manifest against registered hooks.

To stop running your plugin without losing its configuration:

```bash
torana plugin disable my-plugin --yes
```

Share the source in your own repository. Go users can install a Git URL;
Rust users clone and review the project and lockfile first, then install the
local path. If you would like others to find it, [submit it to the catalogue](https://torana.sh/plugins/submit/).
