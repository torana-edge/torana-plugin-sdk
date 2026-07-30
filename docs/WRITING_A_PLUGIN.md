# Writing a Torana plugin

End to end: scaffold a plugin, write its logic, declare what it needs, build it,
and get it running in a Torana instance.

A plugin is a WASI module. Torana hands it the request or response as protobuf,
it returns a modified one (or nothing, to pass through). It has no filesystem, no
network, and no environment — only the capabilities an operator has explicitly
granted to its exact build.

---

## Prerequisites

- **Go 1.24 or newer.** `-buildmode=c-shared` for `wasip1` — which the reactor
  model requires, see [PLUGIN_SEMANTICS.md](PLUGIN_SEMANTICS.md) — does not exist
  before 1.24. Torana itself builds with 1.26.
- **Rust stable plus `wasm32-wasip1`** for the supported Rust authoring path.
- **The `torana` binary**, which is both the proxy and the plugin CLI.

---

## 1. Quickstart with `torana plugin`

You can scaffold a new external plugin directory using Torana:

```bash
torana plugin init my-custom-plugin
cd my-custom-plugin
```

This creates `go.mod`, `plugin.wasm.go`, and `plugin.json`.

---

## 2. Manual Project Setup

To create a new plugin project manually in an external repository:

1. Initialize a new Go module:

```bash
mkdir my-custom-plugin
cd my-custom-plugin
go mod init github.com/your-org/my-custom-plugin
```

2. Fetch the standalone Torana plugin SDK:

```bash
go get github.com/torana-edge/torana-plugin-sdk@latest
```

> **Note**: The SDK repository contains the ABI, helpers, templates, and
> conformance tests without pulling the proxy into your plugin module.

---

## 3. Writing Plugin Logic

Create `plugin.wasm.go`:

```go
package main

import (
	"context"
	"strings"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	"github.com/torana-edge/torana-plugin-sdk/pb"
)

func main() {}

func init() {
	// Register a hook to run before chat completion requests are forwarded upstream.
	sdk.OnBeforeRequest(func(ctx context.Context, req *pb.ChatRequest) (*pb.ChatRequest, error) {
		modified := false

		for _, msg := range req.Messages {
			if msg.Role == "user" && strings.Contains(msg.Content, "SECRET") {
				msg.Content = strings.ReplaceAll(msg.Content, "SECRET", "[REDACTED]")
				modified = true
			}
		}

		if !modified {
			return nil, nil // Return nil, nil if request was not modified
		}
		return req, nil
	})
}
```

### SDK Hook Signatures

- `sdk.OnBeforeRequest(fn func(ctx context.Context, req *pb.ChatRequest) (*pb.ChatRequest, error))`
- `sdk.OnAfterResponse(fn func(ctx context.Context, resp *pb.ChatRequest) (*pb.ChatRequest, error))`
- `sdk.OnStreamChunk(fn func(ctx context.Context, chunk *pb.StreamEvent) (*pb.StreamEventResult, error))`
- `sdk.OnHTTPRequest(fn func(ctx context.Context, req *pb.HttpRequest) (*pb.HttpResponse, error))`
- `sdk.OnTick(fn func(ctx context.Context, tick *pb.TickRequest) (*pb.TickResult, error))`

Returning `nil` means pass-through in every case. For hooks whose result type
has a `Handled` field (`StreamEventResult`, `HttpResponse`, `TickResult`) you
must set it on any result you mean — an all-defaults protobuf message encodes to
zero bytes, which the host reads as "did nothing".

---

## 4. Writing the Manifest (`plugin.json`)

Every plugin directory must contain a `plugin.json` file describing its metadata, hooks, and required host permissions.

```json
{
  "schema_version": 1,
  "id": "my-custom-plugin",
  "name": "my-custom-plugin",
  "version": "0.1.0",
  "description": "Redacts sensitive terms from user prompts",
  "abi_version": "v1",
  "minimum_torana_version": "0.1.0",
  "failure_mode": "block",
  "repository": "https://github.com/your-org/my-custom-plugin",
  "hooks": [
    { "name": "run_before_request" }
  ],
  "permissions": [
    { "name": "env.log", "description": "Emit diagnostic logs" }
  ]
}
```

### Manifest Schema Reference

- **`name`**: Unique string identifier for the plugin.
- **`schema_version`**: Manifest schema version. Use `1`.
- **`id`**: Stable machine-readable plugin identifier.
- **`version`**: Semantic version string (e.g. `"0.1.0"`).
- **`description`**: Human-readable description.
- **`abi_version`**: Torana plugin ABI version. Use `"v1"`.
- **`minimum_torana_version`**: Optional oldest compatible Torana Edge version.
- **`maximum_torana_version`**: Optional newest compatible Torana Edge version.
- **`failure_mode`**: Recommended operator policy, `"pass"` or `"block"`.
- **`repository`**: HTTPS source repository for provenance and support.
- **`hooks`**: Array of hook definitions:
  - **`name`**: Hook event type (`run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick`).
- **`requires_upstream`**: Optional stable plugin IDs that must be approved and
  earlier in the operator's configured `plugins.order`.
- **`permissions`**: Declared host capabilities required by the plugin:
  - **`name`**: Capability permission string.
  - **`description`**: Rationale for requesting the capability.

Manifest permissions are requests, not grants. In production, Torana only
exposes capabilities present in an operator-owned approval, and that approval
is bound to the digest of the exact `plugin.json`, `plugin.wasm`,
`schema.json`, and optional `agent.json` bundle. A changed bundle must be
reviewed and approved again.
The Control Plane shows the digest and requested capabilities before enabling a
plugin.

Torana Edge currently has no product release version. A development or
commit-SHA build therefore skips the optional minimum/maximum product-version
gate and relies on `abi_version`, supported hooks, requested/granted
capabilities, and exported-hook validation. Once a host reports a semantic
release version, it enforces any declared minimum and maximum. Hook execution
order comes only from the operator's `plugins.order`; manifests do not have a
hook `priority` field.

Wazero's linear-memory isolation, execution timeout, and memory limit sandbox
untrusted guest code. Capability approvals separately limit which Torana host
operations the guest may invoke; they do not make an approved plugin trustworthy
or review its request/response transformation logic. Only install artifacts you
intend to run, grant the minimum requested subset, and prefer `failure_mode:
"block"` when silent pass-through would be unsafe.

### Available Capability Strings

Every capability must be requested in `plugin.json` **and** granted by the
operator against your exact bundle digest. A denied capability does not trap —
host calls return `{"status":"error","message":"permission denied"}` and SDK
helpers surface it as an error, so a plugin should degrade rather than assume.

**Verdicts — change what happens to the request**

| Capability | SDK | Description |
| --- | --- | --- |
| `env.block_request` | `sdk.BlockRequest` | Reject the request with a provider-shaped error. |
| `env.respond_request` | `sdk.RespondRequest` | Answer directly without going upstream. |
| `env.route_request` | `sdk.RouteRequest` | Send the request to a different provider. |
| `env.set_identity` | (v2 host call) | Override the rate-limit / identity key for this request. |

**IR write grants — what a plugin may CHANGE**

These apply across hooks for the same semantic area: rewriting assistant
content on the response or stream path needs the same
`ir.messages.write.assistant` grant as on the request path.

| Capability | Covers |
| --- | --- |
| `ir.messages.write.{user,assistant,system,tool,developer,other}` | Message bodies of that role (request and response). |
| `ir.tools.write` | Tool definitions on the request. |
| `ir.model.write` | Model name (request, response, `MessageStart.model`). |
| `ir.params.write` | Sampling params and provider extension blobs. |
| `ir.stream.write` | Structural stream ops: Suppress, fan-out, block boundaries, emitting `StreamError`. |

Some fields are host-owned (usage, upstream status, duration, response id) —
changing them is a protocol violation, not a grantable edit. When
`AfterResponse.mutable` is false, a `ReplaceResponse` is discarded by the host.

**Reading the request**

| Capability | SDK | Description |
| --- | --- | --- |
| `env.original_request` | `sdk.OriginalRequest` | The caller's pristine request, before any plugin ran. Plugins are chained, so this is the only way to see what was actually sent. |
| `env.original_response` | `sdk.OriginalResponse` | The raw upstream body. Non-streaming only — streams are never buffered. |
| `env.request_headers` | via `ToranaMeta` | Allowlisted request headers. |

**State — pick the right one; the wrong choice fails silently**

| Capability | SDK | Scope |
| --- | --- | --- |
| `env.meta_get` / `env.meta_set` | `sdk.HostCall` | One request, private to your plugin. Gone when the request ends. |
| `env.cache_get` / `env.cache_set` | `sdk.HostCall` | Across requests, TTL'd, **shared with every other plugin**. Prefix your keys. |
| `env.state_get` / `env.state_set` / `env.state_keys` | `sdk.StateGet`, `sdk.StateSet`, `sdk.StateKeys` | Across requests **and restarts**, private, never expires. You must delete your own keys. |

**Acting outside a request**

| Capability | SDK | Description |
| --- | --- | --- |
| `env.background_tick` | `sdk.OnTick` | Run on a timer with no request in flight. See [PLUGIN_SEMANTICS §5](PLUGIN_SEMANTICS.md) for what is unavailable inside a tick. |
| `env.host_call.torana_send_request` | `sdk.SendRequest` | Send your own provider request. **Spends the operator's money** — requires a per-plugin budget in `plugins.runtime.egress` or it is refused. |
| `env.now` | `sdk.Now` | Read the host clock. WASI gives a guest none. **Never write this into a request** — see the determinism warning below. |

**Economics**

| Capability | SDK | Description |
| --- | --- | --- |
| `env.host_call.torana_cache_pricing` | `sdk.GetCachePricing` | Cache prices, lifetimes, and the break-even refresh count for a provider/model. |
| `env.host_call.torana_evaluate_compaction` | `sdk.HostCall` | Ask whether a proposed compaction pays for itself. |
| `env.host_call.torana_record_savings` | `sdk.HostCall` | Report bytes saved, attributed to your plugin. |
| `env.host_call.torana_offload_completion` | `sdk.HostCall` | Summarize via the configured cheap model. |

**Observability**

| Capability | SDK | Description |
| --- | --- | --- |
| `env.log` | `sdk.Log` | Diagnostic logging. |
| `env.emit_metric` | `sdk.EmitMetric` | OTel metrics. |
| `env.host_call.torana_plugin_counter` | `sdk.HostCall` | Named counters that appear in `/stats`. |
| `env.serve_http` | `sdk.OnHTTPRequest` | Serve pages and JSON under `/_torana/plugin/<name>/`. |
| `env.plugin_config` | `sdk.PluginConfig` | Read your own `plugins.config.<name>` blob. |

### What the host tells you about a request

Beyond the request itself, Torana publishes routing context in
`ChatRequest.ToranaMetaJson`. It never reaches the wire and is excluded from the
determinism check, so it is safe for the host to vary per request.

| Key | Meaning |
| --- | --- |
| `_provider` | The provider this request was routed to. You need this to ask about pricing, which is keyed by provider name. |
| `_conversation_id` | A stable label for the conversation, derived from the canonical IR — not from any harness header, so it works identically across every wire format. |
| `_path` | The provider-stripped request path. Torana forwards whatever the caller sent rather than synthesizing one, so anything replaying a conversation needs this. |
| `_response` | On `run_after_response` only: latency, upstream status, and token usage including cache reads and writes. |

```go
var meta struct {
    Provider       string `json:"_provider"`
    ConversationID string `json:"_conversation_id"`
    Path           string `json:"_path"`
}
_ = json.Unmarshal(req.ToranaMetaJson, &meta)
```

Treat every field as optional. An empty value means the host did not supply it,
and a plugin that would spend money on the strength of it should decline instead.

---

## 5. Describing your configuration (`schema.json`)

Optional. Without it, an operator edits your plugin's settings as raw JSON. With
it, the control plane renders a form.

It is **JSON Schema** (draft 2020-12), which is what `torana plugin init`
scaffolds:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "on_error": {
      "type": "string",
      "enum": ["block", "allow"],
      "title": "On Error",
      "default": "block",
      "description": "Fail closed or open when the scanner cannot decide."
    },
    "max_scan_chars": {
      "type": "integer",
      "minimum": 0,
      "title": "Max Scan Chars",
      "default": 0,
      "description": "Character cap for scan input. 0 is unbounded."
    }
  }
}
```

| JSON Schema | Becomes |
| --- | --- |
| `"type": "string"` | text input |
| `"type": "number"` / `"integer"` | number input |
| `"type": "boolean"` | checkbox |
| `"enum": [...]` of strings | select |
| `title` | the field label (defaults to the key) |
| `description` | help text under the field |
| `default` | the value shown when unset |

**Anything else falls through to the raw JSON editor rather than being
approximated.** An array or a nested object has no scalar control that could
hold it, and a form that rendered one would corrupt the value on save. Declaring
such a setting is fine — `keyword_compactor` does — it simply is not rendered.

Values are type-checked against what you declare before they reach your plugin,
so a string where you said number is rejected at save time rather than
misbehaving inside the guest. Keys you do *not* declare are passed through
untouched: `schema.json` describes the form, not the whole accepted config, and
several official plugins read settings they never declare.

### Live pickers

A string field can offer values from a live host resource:

```json
"conversations": {
  "type": "string",
  "x-torana-source": "conversations",
  "title": "Conversation IDs"
}
```

The control plane fetches the named resource and offers each value beside the
input. `conversations` is currently the only source. JSON Schema permits unknown
keywords, so this travels without making the document invalid.

### A note on the older format

Torana also accepts `{"fields": [{"key": …, "type": …}]}`, an earlier
Torana-specific shape. It still works and it is the only way to control field
*order* — JSON Schema properties are an object, so derived fields are ordered
alphabetically. Prefer JSON Schema for anything new: it is standard, it carries
constraints (`minimum`, `enum`, `required`) that a UI manifest cannot express,
and it is what the official plugin repository validates.

---

## 6. Building the Plugin WASM

Build the WebAssembly binary targeting WASI (`wasip1`):

```bash
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .
```

Or using Torana:

```bash
torana plugin build . -o plugin.wasm
```

### Rust

Create a `cdylib` crate:

```toml
[package]
name = "my-torana-plugin"
version = "0.1.0"
edition = "2021"

[lib]
crate-type = ["cdylib"]

[dependencies]
torana-plugin-sdk = "0.3"
```

Register and export a hook:

```rust
use torana_plugin_sdk::{export_before_request, pb};

fn before(request: &mut pb::ChatRequest) -> bool {
    // Return true only when request was changed.
    false
}

export_before_request!(before);
```

Build the bundle:

```bash
rustup target add wasm32-wasip1
cargo build --release --target wasm32-wasip1
cp target/wasm32-wasip1/release/my_torana_plugin.wasm plugin.wasm
```

---

## 7. Testing your plugin

Use the `sdktest` package. It runs your hooks in-process, so an ordinary
`go test ./...` exercises the code the host actually calls — no proxy, no WASM
toolchain, no sibling checkout.

```go
package main

import (
	"testing"

	"github.com/torana-edge/torana-plugin-sdk/pb"
	"github.com/torana-edge/torana-plugin-sdk/sdktest"
)

func TestBlocksOnDetectedPII(t *testing.T) {
	h := sdktest.New(t)
	h.SetConfig(`{"on_error":"block"}`)
	h.StubHostCall("torana_offload_completion", func(args string) (string, error) {
		return `{"completion":"EMAIL"}`, nil
	})

	res := h.BeforeRequest(&pb.ChatRequest{Messages: []*pb.Message{
		{Role: "tool", Content: "contact: someone@example.com"},
	}})

	if res.Block == nil {
		t.Fatal("expected the request to be blocked")
	}
}
```

Your plugin's `init()` has already registered its handlers by the time a test
runs, so there is nothing to wire up.

### What the harness gives you

| | |
|---|---|
| `BeforeRequest` / `AfterResponse` | dispatch a request hook; the result reports `PassedThrough` and any `Block`/`Respond`/`Route` verdict |
| `StreamChunk` / `StreamChunks` | dispatch one event, or a whole sequence, returning what the host would forward |
| `HTTPRequest` / `Tick` | dispatch the remaining hooks |
| `SetConfig` | what `sdk.PluginConfig()` returns |
| `StubHostCall` / `DenyPermission` | override one command, or make it answer with the host's permission-denied envelope |
| `SeedCache` / `SeedState` / `Cache` / `State` | start from a warm store, and assert what the plugin wrote |
| `Logs` / `Metrics` / `Calls` | everything the plugin emitted or asked for, in order |
| `SetNow` | fix the clock, so time-dependent logic is deterministic |
| `CheckManifest` | cross-check `plugin.json` against the hooks you actually registered |

Cache, state, meta, and the clock are emulated in memory. Everything else —
offload completions, egress, pricing — answers exactly as an unconfigured host
would, so stub the ones your plugin needs.

### Why the harness mirrors the host's rough edges

`sdktest` reproduces the host's reply shapes byte for byte, including its
inconsistent ones. A tidier fake would let tests pass against responses no
plugin will ever see in production.

The same applies to footguns. If you set a verdict and then return `nil` from
your handler, `sdktest` reports no verdict — because that is what the host
does, and it is a mistake worth catching in a test rather than in a demo.

### Parallel tests

`t.Parallel()` is safe. The installed host is process-global — your plugin calls
`sdk.HostCall()` with no host argument, because on `wasip1` the host *is* the
runtime — so `sdktest` installs a harness only for the duration of each hook
dispatch and serializes those dispatches. Parallel tests keep their own config,
stubs, logs, metrics and captured calls; only the dispatches themselves take
turns.

### Cross-check your manifest

A declared hook with no registered handler loads healthy and never acts. A
registered handler for an undeclared hook is never dispatched. Both fail
silently in production; one line catches either:

```go
func TestManifestMatchesRegistrations(t *testing.T) {
	sdktest.CheckManifest(t, ".")
}
```

---

## 8. Installing and activating

Publish the plugin by pushing it to any git repository — there is no index to
register with and nothing to publish. Users install it by path:

```bash
torana plugin install github.com/you/your-plugins/plugins/foo
torana plugin install github.com/you/your-plugins/plugins/foo@v1.2.0
torana plugin install https://gitlab.example.com/group/repo.git//plugins/foo@v1.2.0
torana plugin install ./foo          # local directory
```

`install` fetches the directory, builds it locally with the wasip1 toolchain,
copies the bundle into `plugins.dir`, and prints the SHA-256 digest of what it
built. Nothing is downloaded prebuilt, so a user can read the source they are
about to run.

Installing does **not** enable anything. Torana loads no plugin the operator has
not approved:

1. Open the control plane at `/_torana/`.
2. Inspect the bundle: its digest, the capabilities it requests, and why.
3. Approve that digest and choose a failure policy.
4. Enable it and set its position in the pipeline.

The approval binds to the digest. Rebuild the plugin, change a permission, or add
an `agent.json` and it needs approving again — which is the point.

## 9. Optional agent-facing operations

Plugins that already vend a page through `run_on_http_request` can also expose
machine-readable operations. Add a language-neutral `agent.json` descriptor and
handle the advertised path beneath `/agent` in the same HTTP hook. Torana
aggregates enabled operations in `GET /_torana/api/v1/`, enforces JSON
responses, and includes the descriptor in the digest-bound approval.

See [AGENT_CONTROL_PLANE.md](https://github.com/torana-edge/torana-edge/blob/main/docs/AGENT_CONTROL_PLANE.md) for the descriptor schema,
dispatch contract, validation rules, and a complete curl example.
