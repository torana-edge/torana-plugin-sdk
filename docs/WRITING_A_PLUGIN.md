# Plugin authoring reference

New to plugins? [Build your first plugin](FIRST_PLUGIN.md) in Go or Rust, then
use this reference for hooks, configuration, resources, testing and distribution.

A plugin is a WASI module. Torana hands it the request or response as protobuf,
it returns a modified one (or nothing, to pass through). It has no filesystem, no
network, and no environment — only the capabilities an operator has explicitly
granted to its exact build.

---

## Prerequisites

- **Go 1.25 or newer.** `-buildmode=c-shared` for `wasip1` — which the reactor
  model requires, see [PLUGIN_SEMANTICS.md](PLUGIN_SEMANTICS.md) — does not exist
  before 1.24; this SDK requires Go 1.25. Torana itself builds with 1.26.
- **Rust 1.85+** with `protoc` and the `wasm32-wasip1` target. The Rust logger
  and all-hooks guest run through the same host conformance harness as Go.
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
go get github.com/torana-edge/torana-plugin-sdk@v0.5.0
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
	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

func main() {}

func init() {
	// Register a hook to run before chat completion requests are forwarded upstream.
	sdk.OnBeforeRequest(func(ctx context.Context, req *pbv1.ChatRequest) (sdk.RequestResult, error) {
		modified := false

		for _, msg := range req.Messages {
			if msg.Role != "user" {
				continue
			}
			// A message body is an ORDERED BLOCK LIST, not a string. Walk the
			// blocks and rewrite each text block in place, so thinking, tool
			// use, images and provider-specific arms keep their positions.
			for i, block := range msg.Blocks {
				text := block.GetText()
				if text == nil || !strings.Contains(text.Text, "SECRET") {
					continue
				}
				redacted := strings.ReplaceAll(text.Text, "SECRET", "[REDACTED]")
				if err := sdk.SetTextAt(msg, i, redacted); err != nil {
					return sdk.RequestResult{}, err
				}
				modified = true
			}
		}

		if !modified {
			return sdk.PassRequest(), nil
		}
		return sdk.ReplaceRequest(req), nil
	})
}
```

### SDK Hook Signatures

- `sdk.OnBeforeRequest(fn func(ctx context.Context, req *pbv1.ChatRequest) (sdk.RequestResult, error))`
- `sdk.OnAfterResponse(fn func(ctx context.Context, resp *pbv1.ChatResponse, mutable bool) (sdk.ResponseResult, error))`
- `sdk.OnStreamChunk(fn func(ctx context.Context, chunk *pbv1.StreamEvent) (sdk.StreamResult, error))`
- `sdk.OnHTTPRequest(fn func(ctx context.Context, req *pbv1.HttpRequest) (sdk.HTTPResult, error))`
- `sdk.OnTick(fn func(ctx context.Context, tick *pbv1.TickRequest) (sdk.TickResult, error))`

Returning `Pass*` (or a zero result) means pass-through. Prefer the typed
constructors (`PassRequest`, `ReplaceRequest`, `PassEvent`, `SuppressEvent`,
`EmitEvents`, `ServeHTTP`, `TickIdle`, …). A non-nil error traps the guest so
the host applies `failure_mode`. Verdicts (`BlockRequest`, `RespondRequest`,
`RouteRequest`, `SetIdentity`) are checked attributed host calls and return an
`error`, including classified host refusals. Return that error from the hook.
Use their `Must*` variants only when failure must trap immediately.
Typed `HostCall(cmd, args)` and `HostCallExtension` return `([]byte, error)`. For stream
tool-call assembly prefer `sdk.NewStreamHandler().OnToolCall(...).Register()`.
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
    { "name": "ir.messages.write.user", "description": "Redact secrets from user message text" },
    { "name": "env.log", "description": "Emit diagnostic logs" }
  ]
}
```

Every field the plugin writes must be covered by a grant it requests here
**and** the operator approves. The hook above rewrites user text, so
`ir.messages.write.user` is not optional decoration: without it the host
refuses the replacement and the plugin's `failure_mode` decides what the
caller gets.

### Manifest Schema Reference

- **`name`**: Unique string identifier for the plugin.
- **`schema_version`**: Manifest schema version. Use `1`.
- **`id`**: Stable machine-readable plugin identifier.
- **`version`**: Semantic version string (e.g. `"0.1.0"`).
- **`description`**: Human-readable description.
- **`abi_version`**: Torana plugin ABI version. The current host accepts `"v1"`
  (`run_hook` / `supported_hooks`) for both Go and Rust guests.
- **`minimum_torana_version`**: Optional oldest compatible Torana Edge version.
- **`maximum_torana_version`**: Optional newest compatible Torana Edge version.
- **`failure_mode`**: Recommended operator policy, `"pass"` or `"block"`.
- **`repository`**: HTTPS source repository for provenance and support.
- **`hooks`**: Array of hook definitions:
  - **`name`**: Hook event type (`run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick`).
- **`requires_upstream`**: Optional stable plugin IDs that must be approved and
  earlier in the operator's configured `plugins.order`.
- **`conflicts_with`**: Optional stable plugin IDs that cannot be active in the
  same pipeline generation. A declaration by either plugin is sufficient;
  ordering cannot resolve a conflict.
- **`permissions`**: Declared host capabilities required by the plugin:
  - **`name`**: Capability permission string.
  - **`description`**: Rationale for requesting the capability.
- **`credentials`**: Named secret slots the operator must bind before the
  plugin can resolve them. Each entry has a stable `name`, a value-free
  `description`, and `required`.
- **`files`**: Plugin-private logical paths, their allowed operations
  (`append`, `read`, `write`, `list`, `delete`), `required`, and optional
  rotation limits. These are not operating-system paths. A required binding
  must be approved before the plugin can load.
- **`http_endpoints`**: Named outbound endpoint slots. Each entry declares a
  description, optional HTTPS default origin, allowed methods, and whether a
  binding is required. The operator may narrow it during approval.
- **`model_services`**: Named provider-neutral model slots. Each entry has a
  stable `name`, value-free `description`, `required`, and explicit requested
  ceilings for timeout, input bytes, output tokens, calls per minute, and
  tokens per hour. During approval the operator binds the slot to a configured
  provider and model and may only narrow those ceilings. Provider credentials
  stay host-owned; a plugin never receives or selects the provider URL or
  credential.
- **`pricing_resources`**: Named pricing slots with a stable `name`,
  value-free `description`, and `required`. The operator may bind one to the
  routed request model or to a model-service slot. Rates preserve unknown
  versus explicitly free values; plugins must decline rather than guess when
  a rate they need is absent.
- **`prompt_cache_policies`**: Named prompt-cache policy slots. Each entry has
  a stable `name`, value-free `description`, and `required`. The operator binds
  the slot to cache economics and lifetime semantics for a route or model
  service; the plugin never submits provider/model coordinates or credentials.

Manifest permissions are an all-or-nothing set under v1: every declared
permission must be approved against the exact bundle digest, or the plugin
cannot be enabled. Approvals are bound to the digest of `plugin.json`,
`plugin.wasm`, `schema.json`, and optional `agent.json`. A changed bundle must
be reviewed and approved again. The Control Plane shows the digest and
requested capabilities before enabling a plugin.

Torana Edge currently has no product release version. A development or
commit-SHA build therefore skips the optional minimum/maximum product-version
gate and relies on `abi_version`, supported hooks, requested/granted
capabilities, and exported-hook validation. Once a host reports a semantic
release version, it enforces any declared minimum and maximum. Hook execution
order comes only from the operator's `plugins.order`; manifests do not have a
hook `priority` field.

Composition declarations are evaluated against exact approved bundles before
guest code loads. `requires_upstream` constrains presence and order;
`conflicts_with` rejects incompatible pairs. Installed but unapproved or
otherwise skipped bundles do not conflict with the generation that runs.

Wazero's linear-memory isolation, execution timeout, and memory limit sandbox
untrusted guest code. Capability approvals limit which Torana host operations
the guest may invoke; they do not make an approved plugin trustworthy or review
its transformation logic. Only install artifacts you intend to run, approve the
full declared permission set (there is no per-capability subset under v1), and
prefer `failure_mode: "block"` when silent pass-through would be unsafe.

### Capability recipes

Every capability must be requested in `plugin.json` **and** approved with the
rest of the declared set against your exact bundle digest. A denied capability
does not trap: Go helpers return an error wrapping a classified
`HostCallRefusalError`, so a plugin should degrade rather than assume.

The tables below explain common authoring choices; they are not a second
capability catalog. The checked-in [`capabilities.json`](../capabilities.json)
is authoritative, and the Go `sdk.Commands`, `sdk.CommandPermission`, and
`sdk.HelperPermissions` APIs expose that same generated inventory to tooling.
**Verdicts — change what happens to the request**

| Capability | SDK | Description |
| --- | --- | --- |
| `env.set_identity` | (v1 host call; `SetIdentityArgs`) | Override the rate-limit / identity key for this request. |
| `env.block_request` | `sdk.BlockRequest` → v1 `BlockRequestArgs` | Reject the request with a provider-shaped error. |
| `env.respond_request` | `sdk.RespondText` / `sdk.RespondRequest` → v1 synthetic response | Answer directly with a canonical response; tool IDs and signatures are host-owned. |
| `env.route_request` | `sdk.RouteRequest` → v1 `RouteRequestArgs` | Send the request to a different provider. |

**Credentials, private files, and scoped HTTP**

| Capability | SDK | Description |
| --- | --- | --- |
| `env.credential_get` | `sdk.GetCredential` | Resolve one declared, operator-bound credential slot. The plugin receives the raw bytes and must treat them as secret. |
| `env.file_append` | `sdk.AppendFile` | Atomically append to one declared plugin-private file. |
| `env.file_read` | `sdk.ReadFile` | Read one declared private file within the approved response limit. |
| `env.file_write` | `sdk.WriteFile` | Atomically replace one declared private file. |
| `env.file_list` | `sdk.ListFiles` | List declared files under a logical prefix. |
| `env.file_delete` | `sdk.DeleteFile` | Delete one declared private file. |
| `env.http_request` | `sdk.HTTPRequest` | Call an operator-bound endpoint slot using a relative path and the approved method and resource budgets. |

**Bound model and pricing resources**

| Capability | SDK | Description |
| --- | --- | --- |
| `env.model_complete` | `sdk.ModelComplete` | Run a provider-neutral prompt through one declared model-service slot. The operator binding owns provider, URL, model, credential, timeout, and hard budgets. |
| `env.model_pricing` | `sdk.GetModelPricing` | Resolve one declared pricing slot without letting the plugin select arbitrary provider/model coordinates. Missing rates remain unknown rather than being guessed. |
| `env.resource_info` | `sdk.GetResourceInfo` | Inspect the effective kind, name, operations, and hard limits of one declared resource without receiving its URL, credential, or secret configuration. |

Model-service calls are attributed plugin egress and do not recursively run
the caller-facing plugin pipeline. This prevents a scanner or compactor from
calling itself, while retaining the same host-side provider adapters,
credential isolation, limits, and observability as other model traffic.

Build model requests from canonical `Message` blocks, optional canonical
`ToolDef` values, and `OutputFormat`; read `ModelCompleteResult.Message`, a
canonical `ResponseMessage`, rather than a legacy flat content string. For a
direct reply, `RespondText` is the shortest safe helper. A structured
`SyntheticResponse` may contain response text and tool-call blocks, but leaves
tool-call IDs and signatures empty: the host generates those identities after
it accepts the response. Never copy an observed provider tool ID into a
synthetic response.

Credential and HTTP approval are deliberately separate. A plugin granted both
can transmit that credential, which is sometimes the point and always part of
the operator's security decision. HTTP calls never accept an absolute URL:
the manifest names an endpoint slot, approval binds its exact origin, and the
plugin supplies only the method, relative path, headers, and body.

**IR write grants — what a plugin may CHANGE**

These apply across hooks for the same semantic area where the authority is
actually the same (assistant text/thinking/tool arguments). Observed response
facts (answering model, response id, usage, role, provider extension blobs,
opaque signatures) are **host-owned** — immutable under plugin mutation
(identical re-emit is a no-op; change/remove/forge rejects). Request
`ir.model.write` / `ir.params.write` do not authorise forging them.

| Capability | Covers |
| --- | --- |
| `ir.tool_results.write` | The tool-result TEXT VALUE ONLY: position-preserving changes to `ToolResultTextBlock.text` at the exact (message, block, content) position, independent of the enclosing message role (the official compactors request ONLY this grant for result text). It does NOT authorize topology, cache markers, roles, metadata, identities, unknown arms, `will_continue`/`scheduling`, or ordinary prompt text — those stay under their own grants. |
| `ir.cache_control.write` | The cache breakpoint marker ONLY, on its three ordered carriers: `ToolDef.cache_control_json` (tools-first section), `RequestBlock.cache_breakpoint` (outer block), and the nested `ToolResultContentBlock.cache_breakpoint` inside a tool result. It does NOT authorise message or tool content/schema changes, and the message-role grants / `ir.tools.write` do NOT authorise these marker fields — a plugin that changes a breakpoint marker must hold this grant, and nothing else it changes is covered by it. |
| `ir.messages.write.{user,assistant,system,tool,developer,other}` | Message **content** of that role (request and response). Not response role or signatures. |
| `ir.tools.write` | Tool definitions on the request. |
| `ir.model.write` | **Request** model selection only. |
| `ir.params.write` | Request sampling params and request provider extension blobs. |
| `ir.stream.write` | **Additive** topology grant: Suppress, fan-out, kind change, block boundaries/indexes. Required **in addition to** content grants for what was changed/removed/added. Cannot alone alter host-owned facts. |

Stream composition is verified by field diff: `required = topology
(when cardinality/order/boundaries/kind change) ∪ every semantic section
changed/removed/added`. A one-for-one `TextDelta` rewrite needs only
`ir.messages.write.assistant`. When `AfterResponse.mutable` is false, a
`ReplaceResponse` is discarded by the host.

## The observable request prefix and cache-breakpoint carriers

The host's prompt-cache identity is the **observable request prefix**: the
bytes the provider will cache, closed at the LAST cache-breakpoint marker in
serialization order. One SDK-owned projection defines it for every consumer
(the Edge host keys its conversation cache on it; cache-aware plugins key
their decisions on it), so no plugin can drift into a second definition.

### `RequestObservablePrefix(req) ([]byte, bool, error)`

Returns the deterministic protobuf serialization of the provider-visible
cached prefix and whether the request carries a cache-breakpoint carrier:

- **Marker presence oracle**: `hasBreakpoint` is true when a carrier exists
  in the tools-first/outer/nested order (the prefix closes at it), false for
  a no-marker request (the whole request is the automatic-cache prefix). It
  is a PURE read — the same traversal the projection uses, with no mutation
  — so a plugin can decline a no-marker request BEFORE any state access
  without re-implementing the carrier model.
- **Owned validation**: the request must pass `ValidateReplacement`; an
  out-of-domain request is an ERROR — never a partial projection. Decline
  without state access or mutation when it errors.
- **Marker model**: the prefix closes at the LAST carrier in
  tools-first/outer/nested order. A tool-section marker ends the prefix in
  the tools (no message is part of it); an outer or nested marker truncates
  the messages inclusive at the exact block/nested position. NO marker means
  the WHOLE request is the prefix (automatic prefix caching).
- **Field set**: model, tools, messages through the boundary,
  `provider_extensions_json`, `safety_settings_json`, and the generation
  params (`max_tokens` / `temperature` / `top_p` / `stop_sequences`,
  presence-aware). ONLY `stream` and `torana_meta_json` are excluded — every
  other top-level field folds, so an additive field is a deliberate,
inventory-tested decision.

### Function and free-form tools

`ToolCalls` and `ToolResults` expose both ordinary JSON function calls and
provider-native free-form tools in the same wire-ordered views. Inspect
`InvocationKind` before reading the input: `FUNCTION` uses `Arguments`, while
`FREEFORM` uses the presence-sensitive `InputText` (an empty string is still a
real input). Free-form results retain their ordered text/unknown content, so
policy and compaction plugins do not need provider-specific JSON parsing.

Definitions use the same `ToolDef.invocation_kind`. Function definitions carry
`parameters_json`; free-form definitions carry `input_format_json` and may
carry an outermost-first `namespace_path` for harness tool namespaces. The path
is observable provider topology, not part of `ir.tools.write`: plugins may edit
the definition inside its namespace but cannot move it to another namespace.
- **Purity**: the projection is a fresh clone; the input is never mutated
  and the returned bytes never alias it. Raw JSON folds verbatim (member
  order and lexemes are identity-relevant), optional scalars fold
  presence-aware, and stop sequences fold ordered.

### `ToolResultScalarText(v ToolResultView) (text string, ok bool)`

Scalar compatibility for a tool-result view (from `ToolResults`): a result
with EXACTLY ONE text arm (an explicit EMPTY text arm is compatible), ZERO
unknown/provider arms, and any number of cache-marker arms. Zero text arms
(marker-only), multiple text arms, or any unknown arm ⇒ `ok=false` — decline
the result UNCHANGED. Text arms are never concatenated (concatenation is not
injective and the flat scalar had no such shape).

Follow the [tool-result safety rules](PLUGIN_SEMANTICS.md#explicit-tool-failures)
before any compaction side effects. Resolve the name from the result itself or
the matching call, and decline unsupported content shapes:

```go
names := sdk.ToolNamesByCallID(req.Messages)
for _, msg := range req.Messages {
    for _, result := range sdk.ToolResults(msg) {
        text, ok := sdk.ToolResultScalarText(result)
        if !ok || result.MustStayExact(names[result.ToolCallId], text) {
            continue
        }
        // The scalar result is eligible for further policy/economic checks.
    }
}
```

### `ReplaceToolResultText(msg, block, text) (changed bool, err error)`

The total, self-validating, ATOMIC in-place replacement of a tool-result
block's single text arm, by message block index:

- every error (nil message, out-of-range block, non-tool-result block, zero/
  multiple text arms, unknown arm) leaves the message unchanged;
- the exact nested arm count/order and every marker byte are retained — only
  the designated text value changes, so the provider cached-prefix boundary
  does not move;
- a byte-identical value is a structural no-op (`changed=false`) preserving
  every provenance token;
- a real change preserves `part_metadata_json` and the ENTIRE final
  trailing-signature carrier (which covers only preceding text/thinking and
  its own metadata — NOT tool results), and clears only the containing
  tool-result signature — no stale token survives.

### Cache-marker provenance (ReplaceLastCacheBreakpoint)

- a tool-definition marker real change clears NO message token;
- a top-level message marker real change preserves EVERY signature
  token/carrier (markers are outside the trailing coverage);
- a NESTED tool-result marker real change clears ONLY the containing
  tool-result signature; the trailing carrier and unrelated tokens are
  preserved;
- a byte-identical replay preserves everything; every error is atomic.

### `ReplaceLastCacheBreakpoint(req, marker) (changed bool, err error)`

Applies a cache-breakpoint marker **exactly**:

- Replaces the marker on the LAST EXISTING carrier (tool section / outer
  block / nested tool-result content) — it NEVER inserts a marker and never
  guesses a position.
- No carrier ⇒ `ErrNoCacheBreakpoint` (treat as pass: decline without state
  or mutation).
- ATOMIC: the request and the marker (a strict JSON object) are validated
  and the carrier is located BEFORE any mutation; on every error the request
  is unchanged. On success exactly one carrier is replaced with a defensive
  byte copy.
- `changed=false` when the new marker bytes are byte-identical to the
  existing marker. A lexically different but semantically identical marker
  IS a change — conservative: the new bytes are identity-relevant, even
  though an adapter may compact raw JSON at its wire boundary.
- The marker mutation requires the `ir.cache_control.write` grant (see the
  capability table).

Opaque signatures (`thinking_signature`, `ToolCall.signature`,
`ToolCallRef.signature`) bind provider tokens to content. Mutating signed
content while leaving the signature in place is invalid — the host must reject
that mutation or clear the signature. On the stream path, `ToolCallRef.signature`
binds `id`/`name`/invocation kind and the matching
`ToolCallDelta.arguments_delta` or presence-sensitive `input_text_delta` for the **same unique
content-block index** (never reused after close; adapters must assign distinct
indexes to parallel tool calls, which may be open concurrently — non-tool
content stays exclusive). `StreamError` is a terminal abort: it may arrive
mid-block, abandons ALL open blocks and incomplete tool-call buffers without a
synthetic stop, ends the
stream, and makes any later event invalid. Streamed `signature_delta` with open
text/thinking binds that block; a trailing signature-only part (Code Assist) is
standalone — preserve it without synthesizing an empty block. `StreamError` is
also host-owned (do not forge upstream failures). Host enforcement tables live
in package `outboundpolicy` (not for WASM guests).

**Reading the request**

| Capability | SDK | Description |
| --- | --- | --- |
| `env.original_request` | `sdk.OriginalRequest` | The caller's pristine request, before any plugin ran. Plugins are chained, so this is the only way to see what was actually sent. |
| `env.original_response` | `sdk.OriginalResponse` | The raw upstream body. Non-streaming only — streams are never buffered. |
| `env.request_headers` | via `ToranaMeta` **and** `HttpRequest.headers_json` | Allowlisted request headers (see below). Applies **per target**: the grant is checked against the exact plugin that will execute, for both the chat metadata surface (`ToranaMeta` `_request_headers`) and the HTTP hook surface (`HttpRequest.headers_json`). |

**HTTP hook headers — three classes, enforced at dispatch**

`run_on_http_request` handlers receive `HttpRequest.headers_json`, a
JSON-encoded `map[string][]string`. The host forwards ONLY:

- **safe operational headers, always visible:** `Accept`, `Content-Type`,
  `User-Agent`;
- **credential/identity headers, only when your exact plugin holds the
  approved `env.request_headers` grant:** `Authorization`, `X-Api-Key`,
  `X-Torana-User`, `X-Torana-Team`, `X-Torana-Tenant`;
- **everything else is never forwarded** — including `Cookie`,
  `Proxy-Authorization`, arbitrary custom secret headers, and
  `X-Torana-Agent` (which is caller-controlled, not host-injected; never
  treat it as trusted).

Allowed multi-values are preserved under their canonical names. The filter
runs at the plugin dispatch boundary against the target plugin's approved
grants: a caller-supplied `headers_json` is never authoritative, and plugin B
does not inherit sensitive-header access merely because plugin A holds the
grant.

**State — pick the right one; the wrong choice fails silently**

| Capability | SDK | Scope |
| --- | --- | --- |
| `env.meta_get` / `env.meta_set` | `sdk.MetaGet`, `sdk.MetaSet` | One request, private to your plugin (the host namespaces your keys). Gone when the request ends. |
| `env.meta_append` (permission `env.meta_set`) | v1 `MetaAppendArgs` | Atomic append by block index. Non-empty fragment → empty success value (ack). Empty fragment → complete buffer read (absent → empty bytes). Dispatcher maps the command onto `env.meta_set` — there is no separate grant. |
| `env.cache_get` / `env.cache_set` | `sdk.CacheGet`; `sdk.CacheSet`, `sdk.CacheSetTTL`, `sdk.CacheDelete` | Across requests, optionally TTL'd, and private to the exact executing plugin. Delete uses the `env.cache_set` grant. Empty is a stored value. |
| `env.shared_cache_get` / `env.shared_cache_set` | `sdk.SharedCacheGet`; `sdk.SharedCacheSet`, `sdk.SharedCacheSetTTL`, `sdk.SharedCacheDelete` | Explicit cross-plugin exchange. Delete uses the set grant. Private cache grants never imply these capabilities. |
| `env.state_get` / `env.state_set` / `env.state_keys` | `sdk.StateGet`, `sdk.StateGetVersioned`; `sdk.StateSet`, `sdk.StateDelete`, `sdk.StateCompareAndSet`, `sdk.StateCompareAndDelete`; `sdk.StateKeys`, `sdk.StateScan` | Across requests and restarts, private, never expires. Commands share their read, write, or keys grant as grouped here. Empty is a stored value. Versions are opaque and non-reusable; scans are ordered cursor pages limited to 256 entries. |

**Reading meta and cache: distinguish missing from empty**

Reads return `(value, found, error)`; writes return `error`. `MetaAppend`
returns `([]byte, error)` and an empty fragment reads the assembled value.
The same read shape applies to `StateGet`; writes return `error`. The same
absence-vs-emptiness rule applies to all three stores. The read pattern is where
the three channels matter — branch on the middle one:

```go
v, found, err := sdk.MetaGet("draft")
if err != nil { return sdk.PassRequest(), err }
if !found { v = defaultDraft }
// found=true and v=="" means an explicitly stored empty value.
// v is the stored value, which may legitimately be "".
```

A plugin that genuinely wants to continue without a capability should say so
explicitly at that call site, as a deliberate choice with a comment — it is not
the default.

**Absence is not emptiness.** A key that was never written returns a
`NOT_FOUND` refusal; a metadata, state, or cache key holding `""` returns
success with an empty value. `MetaSet(k, "")` therefore stores a present-empty
value. Use `StateDelete` and `CacheDelete` for the durable stores; request
metadata disappears with its request scope.
Do not test `v == ""` to decide whether something was stored.

Do **not** reach these through `sdk.HostCall` directly. The typed helpers
validate the key before crossing the boundary, so a mistake fails at the call
instead of silently storing nothing.

**Host feature calls (`env.host_call.*`)**

`torana_plugin_counter` and
the rest are *host features*, not ABI operations. Their payloads are defined by
the feature, so they take an opaque body:

```go
payload, _ := json.Marshal(map[string]any{"counter": "decisions", "delta": 1})
v, err := sdk.HostCallExtension("torana_plugin_counter", payload)
```

Pass the **canonical command token** (`torana_plugin_counter`), not the
permission string (`env.host_call.torana_plugin_counter`). `HostCallExtension`
refuses `env.`-prefixed commands: core operations have typed arguments and go
through `HostCall`, and routing them here would bypass the typed contract.

The result envelope is *not* opaque. A framed `HostError` becomes a Go error
wrapping `*sdk.HostCallRefusalError` with a stable code
(`PERMISSION_DENIED`, `NOT_CONFIGURED`, `UNAVAILABLE`, `INVALID_ARGUMENT`).
Protocol and transport failures also return errors. A `status` field only
appears where status is real data, such as a pricing decision.

Where the SDK already has a typed helper, use it instead of constructing a raw
extension call. Platform resource helpers such as `sdk.ModelComplete`,
`sdk.GetModelPricing`, and `sdk.GetPromptCachePolicy` use typed ABI commands;
extension helpers such as `sdk.SendRequest` own their extension framing.

**Acting outside a request**

| Capability | SDK | Description |
| --- | --- | --- |
| `env.background_tick` | `sdk.OnTick` | Run on a timer with no request in flight. See [PLUGIN_SEMANTICS §5](PLUGIN_SEMANTICS.md) for what is unavailable inside a tick. |
| `env.host_call.torana_send_request` | `sdk.SendRequest` | Send your own provider request. **Spends the operator's money** — requires a per-plugin budget in `plugins.runtime.egress` or it is refused. |
| `env.now` | `sdk.Now` | Read the host clock through a permission-gated host call, so a test can control it (the `sdktest` harness has `SetNow`). **Never write this into a request** — see the determinism warning below. |

**Economics**

| Capability | SDK | Description |
| --- | --- | --- |
| `env.cache_policy` | `sdk.GetPromptCachePolicy` | Resolve one operator-bound prompt-cache policy without exposing or accepting provider/model coordinates. |
| `env.model_pricing` | `sdk.GetModelPricing` | Resolve one operator-bound model-pricing resource. |
| `env.host_call.torana_evaluate_compaction` | `sdk.HostCallExtension` | Ask whether a proposed compaction pays for itself. |
| `env.host_call.torana_record_savings` | `sdk.HostCallExtension` | Report bytes saved, attributed to your plugin. |

**Observability**

| Capability | SDK | Description |
| --- | --- | --- |
| `env.log` | `sdk.Debug`, `sdk.Info` (or typed `sdk.Log`) | Best-effort diagnostics; the void import cannot acknowledge delivery. |
| `env.emit_metric` | `sdk.Counter`, `sdk.Histogram`, `sdk.Gauge` (or typed `sdk.EmitMetric`) | Best-effort OTel metrics; use low-cardinality labels. |
| `env.host_call.torana_plugin_counter` | `sdk.HostCallExtension` | Named counters that appear in `/stats`. |
| `env.serve_http` | `sdk.OnHTTPRequest` | Serve pages and JSON under `/_torana/plugin/<name>/`. |
| `env.plugin_config` | `sdk.PluginConfig` | Read your own strict JSON object. Empty configuration becomes `{}`; malformed JSON, duplicate keys, scalar/array values, and host refusals return errors. |

### What the host tells you about a request

Every callback context carries the typed invocation snapshot returned by
`sdk.Execution(ctx)`: effective provider/model, optional conversation identity
and deadline, synthetic-response status, and the host's memory, host-response,
and stream-buffer limits. `sdk.RequestID(ctx)` returns the invocation ID. These
are observations, not authority to select a destination or exceed a limit.

Beyond the request itself, Torana publishes routing context in
`ChatRequest.ToranaMetaJson`. It never reaches the wire and is excluded from the
determinism check, so it is safe for the host to vary per request.

| Key | Meaning |
| --- | --- |
| `_provider` | The selected route's provider name. For prices, use an operator-bound pricing resource rather than constructing provider/model coordinates. |
| `_conversation_id` | A stable label for the conversation, derived from the canonical IR — not from any harness header, so it works identically across every wire format. |
| `_path` | The provider-stripped caller path. Native routing preserves it; an explicit protocol bridge constructs the destination endpoint. Do not treat it as a universal upstream replay URL. |
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


**Classified refusals through the convenience helpers**

`StateGetJSON`, `StateSetJSON`, and `sdk.Now()` return plain `error`s, but the
classification is preserved: any framed host refusal unwraps with `errors.As`
into `*sdk.HostCallRefusalError` (Code, Reason, Message), so you branch
advisory-vs-contract without string matching. State `NOT_CONFIGURED`
simultaneously satisfies `errors.Is(err, sdk.ErrStateUnavailable)` — both
contracts hold for the same error:

```go
var refusal *sdk.HostCallRefusalError
if errors.As(err, &refusal) {
    switch refusal.Code {
    case pbv1.ErrorCode_ERROR_CODE_NOT_CONFIGURED, pbv1.ErrorCode_ERROR_CODE_UNAVAILABLE:
        // Advisory: decline and continue — retrying now cannot help.
    default:
        // Contract/protocol defect (PERMISSION_DENIED, INVALID_ARGUMENT,
        // INTERNAL, ...): return the error so the host applies failure_mode.
    }
}
if errors.Is(err, sdk.ErrStateUnavailable) {
    // State-specific advisory: durable state is not configured.
}
```

`StateGetJSON` keeps its special absence contract: a `NOT_FOUND` refusal means
`found == false` with a nil error. The error classes are precise:

- malformed `HostCallResult` frames are protocol errors, never refusals;
- `StateGetJSON` on a present-empty value is a local JSON decode error (and
  is not absence);
- `Now` on an empty or non-numeric successful value is a local/protocol
  reading error;
- local JSON marshal/decode errors are not refusals;
- `StateSetJSON` may validly succeed with an empty result value — setters
  have no result payload, so an empty value is a successful ack, not an
  error.

### Decoding JSON you did not write: `strictjson`

A plugin that reads JSON off the wire and re-emits it must not change it on the
way through. `encoding/json` is built to be forgiving, and every one of those
conveniences is a silent rewrite of someone else's bytes:

| `encoding/json` | What that costs a plugin |
|---|---|
| replaces invalid UTF-8 with U+FFFD | two different inputs decode to the same text |
| keeps the **last** of duplicate members | the request you forward is not the one you were given |
| decodes numbers as `float64` | `9007199254740993` re-emits as `9007199254740992` |
| ignores data after the top-level value | a second, unnoticed document rides along |

`github.com/torana-edge/torana-plugin-sdk/strictjson` refuses each of these
instead:

```go
import "github.com/torana-edge/torana-plugin-sdk/strictjson"

// A lossless object: numbers keep their exact lexeme through json.Number,
// duplicates are rejected at every nesting level, and trailing data is an
// error. "null" decodes to a nil map with no error, so you decide whether
// absence is tolerable.
args, err := strictjson.DecodeObject([]byte(argsJSON))

// A closed object: the same refusals, plus unknown and null members.
// Presence is preserved — a member written as "" or false is reported
// present, one that was never written is not.
raw, err := strictjson.DecodeObjectStrict(body, "version", "tools")
```

Use `DecodeObject` for data whose shape you do not own (tool arguments, a
model's JSON output) and `DecodeObjectStrict` for a document you define (your
own config envelope, a response from a service you specified).


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

The host validates the whole configuration against JSON Schema before saving:
nested objects and arrays, required fields, enums and `additionalProperties`
are enforced, not just the fields rendered as controls. With
`additionalProperties: false`, undeclared keys are rejected. The legacy
`fields` format validates declared scalar fields but permits unlisted keys;
it is not equivalent to a closed JSON Schema. The host retains a compatibility
exception for a top-level `_comment` entry in older configurations.

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

Use `torana-plugin-sdk` and compile a `cdylib` for `wasm32-wasip1`. A plugin
implements the typed `Plugin` trait and declares its exact hook bitmap with
`export_plugin_v1!`; the macro exports `abi_version`, `supported_hooks`, and
`run_hook`.

Pin the source revision used by your host so the build does not depend on
registry publication:

```toml
[dependencies]
torana-plugin-sdk = { git = "https://github.com/torana-edge/torana-plugin-sdk", rev = "ad98c6d3467f628dd2f630c054715f8b347daa29", version = "=0.5.0" }
```

Once version 0.5.0 is available on crates.io, a registry-only dependency may
use `torana-plugin-sdk = "=0.5.0"`.

```rust
use torana_plugin_sdk::{export_plugin_v1, pbv1, Plugin, RequestResult, HOOK_BEFORE_REQUEST};
struct Logger;
impl Plugin for Logger {
    const SUPPORTED_HOOKS: u32 = HOOK_BEFORE_REQUEST;
    fn before_request(_: pbv1::ChatRequest) -> Result<RequestResult, String> {
        Ok(RequestResult::pass())
    }
}
export_plugin_v1!(Logger);
```

The five Rust trait methods are:

```rust
fn before_request(pbv1::ChatRequest) -> Result<RequestResult, String>;
fn after_response(pbv1::ChatResponse) -> Result<ResponseResult, String>;
fn on_stream(pbv1::StreamEvent) -> Result<StreamResult, String>;
fn on_http(pbv1::HttpRequest) -> Result<HttpResult, String>;
fn on_tick(pbv1::TickRequest) -> Result<TickResult, String>;
```

Combine multiple hooks by OR-ing the exported hook constants and implementing
the corresponding typed trait methods. Each family has its own result type;
`RequestResult` cannot be returned from an after-response callback. Host calls
use protobuf arguments and typed refusals preserve stable `ErrorCode` values.

The repository keeps a complete Go example with all five hook signatures,
checked verdict errors, execution context, versioned state CAS, and a canonical
structured model call in [`examples/authoring-go`](../examples/authoring-go).
The maintained Rust guest is [`examples/rust-logger`](../examples/rust-logger),
and the compiled [all-hooks guest](../conformance/guests/rust-allhooks) pins all
five default method signatures.
Run `scripts/check-doc-examples.sh` after changing the example code or SDK surface.

---

## 7. Testing your plugin

Use the `sdktest` package. It runs your hooks in-process, so an ordinary
`go test ./...` exercises the code the host actually calls — no proxy, no WASM
toolchain, no sibling checkout.

Native tests and the compiled Go guest share the same `DispatchHook`
implementation and config helpers. `PluginConfig` returns raw JSON and an
error; decode it only after checking the error. WASI memory transfer and
error-to-trap conversion remain covered by the compiled conformance suite.

```go
package main

import (
	"testing"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"github.com/torana-edge/torana-plugin-sdk/sdktest"
)

func TestBlocksOnDetectedPII(t *testing.T) {
	h := sdktest.New(t).
		WithPermissions([]string{"env.plugin_config", "env.model_complete", "env.block_request"}).
		WithHooks([]string{"run_before_request"})
	h.SetConfig(`{"on_error":"block"}`)
	h.StubModelComplete(func(args *pbv1.ModelCompleteArgs) (*pbv1.ModelCompleteResult, *pbv1.HostError, error) {
		return &pbv1.ModelCompleteResult{Message: &pbv1.ResponseMessage{Blocks: []*pbv1.ResponseBlock{{Kind: &pbv1.ResponseBlock_Text{Text: &pbv1.ResponseTextBlock{Text: `{"pii":true,"findings":[{"type":"email","line":1}]}`}}}}}}, nil, nil
	})

	res := h.BeforeRequest(&pbv1.ChatRequest{Messages: []*pbv1.Message{{
		Role: "user",
		// The body is the ordered block list. There is no flat Content
		// field to set — one text block is the shortest valid body.
		Blocks: []*pbv1.RequestBlock{{
			Kind: &pbv1.RequestBlock_Text{
				Text: &pbv1.RequestTextBlock{Text: "contact: someone@example.com"},
			},
		}},
	}}})

	if len(h.BlockCalls()) == 0 {
		t.Fatal("expected the request to be blocked")
	}
	_ = res
}
```
Your plugin's `init()` has already registered its handlers by the time a test
runs, so there is nothing to wire up.

### What the harness gives you

| | |
|---|---|
| `BeforeRequest` / `AfterResponse` | dispatch a request/response hook; results report `PassedThrough` only on success with a zero-byte result. `AfterResponse` exposes `Replacement` (guest proposal) and `Applied` (only when `mutable`) |
| `StreamChunk` | dispatch one stream event; prefer `StreamHandler` in the plugin under test for tool assembly |
| `HTTPRequest` / `Tick` | dispatch the remaining hooks |
| `NewRequest` | create an isolated invocation lifecycle and retain its attempted, accepted, and effective calls across hooks |
| `Calls` / `AcceptedCalls` / `EffectiveBlockCalls` (and related verdict accessors) | distinguish what the plugin attempted, what the host accepted, and which accepted verdict finally controlled the request |
| `SetConfig` | what `sdk.PluginConfig()` returns |
| `WithPermissions` / `WithHooks` | enforce the exact manifest surface in tests; undeclared commands and cross-hook operations are refused before a stub runs |
| `StubHostCall` / `DenyPermission` | override one command, or make it answer with the host's permission-denied envelope |
| `HostResultValue` / `HostResultError` | frame a stub's reply. Typed and extension commands speak `HostCallResult`, so a stub returning a bare payload produces a decode error rather than the value it meant |
| `Run(fn)` | run helper code that makes host calls outside a hook. Do not nest a dispatch method inside it |
| `SeedCache` / `SeedState` / `Cache` / `State` | start from a warm store, and assert what the plugin wrote |
| `Logs` / `Metrics` / `Calls` | everything the plugin emitted or asked for, in order |
| `SetNow` | fix the clock, so time-dependent logic is deterministic |
| `CheckManifest` | cross-check `plugin.json` against the hooks you actually registered |

Cache, state, meta, and the clock are emulated in memory. Everything else —
model services, egress, and pricing — answer exactly as an unconfigured host
would, so stub the ones your plugin needs.

### Test effective outcomes, not just attempted calls

A typed pass result keeps the payload unchanged; it does not undo an accepted
`BlockRequest`. An accepted block also survives a later callback failure.
Transient respond/route verdicts from a failed invocation are discarded.
Check accepted and effective calls as well as attempted calls, and return
verdict-helper errors so the operator's failure policy can apply. The
[block/pass test](../sdktest/sdktest_test.go) and
[failure-outcome tests](../sdktest/outcome_scope_test.go) exercise these cases.

### Parallel tests

`t.Parallel()` is safe. The installed host is process-global — your plugin calls
`sdk.HostCall()` with no host argument, because on `wasip1` the host *is* the
runtime — so `sdktest` installs a harness only for the duration of each hook
dispatch and serializes those dispatches. Parallel tests keep their own config,
stubs, logs, metrics and captured calls; only the dispatches themselves take
turns.

### Cross-check your manifest

The host rejects a bundle when its declared hooks and exported hook bitmap do
not agree. Catch that mismatch in the source checkout, before packaging or
installation, with one line:

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

See [Add agent-facing operations](AGENT_OPERATIONS.md) for the descriptor
schema and authoring contract. The [host control-plane reference](https://github.com/torana-edge/torana-edge/blob/main/docs/AGENT_CONTROL_PLANE.md)
covers discovery, dispatch and caller protections.
