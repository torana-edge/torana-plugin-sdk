# Plugin semantics and gotchas

The parts of writing a Torana plugin that are not obvious from the ABI: how the
protobuf payload must be decoded, how each hook actually behaves, and two classes
of bug that are silent by construction.

Start with [WRITING_A_PLUGIN.md](WRITING_A_PLUGIN.md) if you have not written one
yet.

This guide is designed to help engineers and AI agents implement Torana WASM plugins robustly, avoiding common pitfalls related to the Torana plugin architecture, Go's WASI integration, and JSON serialization.

## 1. WASI Build Mode

Torana uses `wazero` for executing WASM plugins. For standard Go (not TinyGo), compiling to `wasip1/wasm` with standard commands yields a command-oriented execution model, which means the runtime shuts down after `main()` completes. This breaks `Torana`'s hook-based reactor execution model where Torana calls exported functions multiple times.

**CRITICAL:** Always compile your standard Go plugins as a C-shared library to enable the reactor model.
```bash
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .
```

**Plugin binaries are build artifacts and are NEVER committed to git** (`*.wasm`
is gitignored). Torana logs a warning at load time if `plugin.wasm` is older
than the plugin's Go sources, and tells you to reinstall that plugin.

Where you rebuild depends on which repository you are in: `make testdata` builds
torana-edge's test fixtures, `./scripts/build.sh <plugin>` builds a bundle in
torana-plugins, and `torana plugin install` builds from source and prints the
digest for anything installed. There is no longer a target that builds "all
plugins" from one place — they live in their own repositories.

## 2. Protobuf Structure and Torana's Payload

Torana uses a strict Protobuf contract for all WASM boundaries to prevent schema corruption.
When Torana invokes `run_before_request`, it passes serialized bytes of
`pb/v2.ChatRequest` inside a `HookInput` frame.

The Go plugin SDK handles all the underlying memory allocation, pointer packing, and Protobuf marshaling for you.

**CRITICAL:** Do NOT attempt to read raw JSON or use `map[string]any`. You will lose the benefits of Protobuf unknown field preservation.

### The Correct Unmarshaling Pattern

Use the generated `pb/v2` types and the `sdk` handlers. The SDK automatically unmarshals the request and marshals the response, fully preserving unknown fields under the hood.

```go
package main

import (
	"context"
	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

func main() {}

func init() {
	sdk.OnBeforeRequest(func(ctx context.Context, req *pbv2.ChatRequest) (sdk.RequestResult, error) {
		modified := false

		// Extract and modify the fields you care about
		if len(req.Tools) > 0 {
			// modify req.Tools...
			modified = true
		}

		if !modified {
			return sdk.PassRequest(), nil
		}
		return sdk.ReplaceRequest(req), nil
	})
}
```

## 3. Stream Hooks: Suppress, Replace, Fan-Out

`run_on_stream_chunk` handlers return a typed `sdk.StreamResult`. Use the helpers:

| Helper | Meaning |
|---|---|
| `sdk.PassEvent()` | forward the event unchanged |
| `sdk.SuppressEvent()` | drop the event from the stream |
| `sdk.EmitEvents(ev…)` | substitute one or more events |

Prefer `sdk.NewStreamHandler()` for tool-call assembly and text rewrites: it
buffers via host metadata (`meta_append` / `meta_set`), never Go-object state,
and re-emits the exact assembled original on callback errors (fail-open).

```go
sdk.NewStreamHandler().
	OnToolCall(func(ctx context.Context, call sdk.ToolCall) (sdk.ToolCallAction, error) {
		// call.Arguments is fully assembled JSON
		return sdk.ReplaceToolArguments(`{"rewritten":true}`), nil
	}).
	OnTextDelta(func(ctx context.Context, text string) (sdk.TextAction, error) {
		return sdk.PassText(), nil
	}).
	Register()
```

Raw `OnStreamChunk` handlers that return a non-nil error trap so `failure_mode`
applies. Once buffering begins, never forward only the current fragment — fail
closed or re-emit the assembled original.

**State scoping rules:**
- `env.meta_set` / `env.meta_get` — plugin-private AND request-scoped. Other
  plugins and other requests can never see these keys. Setting an empty
  value deletes the key.
- `env.meta_append` (permission `env.meta_set`) — append/read tool-call argument
  fragments by block index.
- `env.cache_set` / `env.cache_get` — shared across plugins and requests
  (with a TTL). Use for cross-request handoff, e.g. the compactor caches
  intents by `tool_call_id` that the keyword_compactor reads next turn.

## 4. Response Hooks: `run_after_response` semantics differ by path

`run_after_response` fires on **both** response paths, but with **asymmetric**
mutation semantics — know which one you're on before relying on it:

| Response path | Mutations (assistant content, tool-call name/args) |
|---|---|
| Non-streaming JSON | **Applied** — written back into the response body before the client sees it (`internal/proxy/jsonresponse.go`). |
| Streaming SSE | **Observational only** — the hook runs *after* the stream has already been serialized to the client, so any mutation is discarded (`internal/proxy/server.go`). |

**Why:** buffering an entire SSE stream to allow post-hoc rewrites would defeat
streaming latency, so the streaming path invokes the hook purely for observation.
This is the right channel for **metrics / audit / usage** plugins (e.g. `otel`),
which only read the `_response` signal (latency, upstream status, token usage).

**If your plugin needs to *rewrite* the final response**, do it on the streaming
path via `run_on_stream_chunk` (mutate events as they flow — see §3), not
`run_after_response`. Torana logs a heads-up at load time for any plugin that
declares `run_after_response`, reminding you the streaming mutations are dropped.

## 5. Background Hooks: `run_on_tick` has no request

`run_on_tick` is the only hook that fires when nothing is passing through the
proxy. That makes it the only place a plugin can act on elapsed time — and it
means most of what a plugin normally relies on is simply absent.

| Host call | Inside a request | Inside a tick |
|---|---|---|
| `env.meta_get` / `env.meta_set` | per-request scratch space | **empty** — there is no request to scope to |
| `env.original_request` | the caller's pristine request | **empty** |
| `env.original_response` | the raw upstream body | **empty** |
| `env.plugin_config` | your config | your config |
| `env.cache_get` / `env.cache_set` | shared, cross-request | shared, cross-request |

There is also **no caller credential**. Host calls that would normally fall back
to the caller's own API key have nothing to fall back to, so anything a tick
sends must be authenticated by provider-level configuration. A tick that assumes
otherwise fails as a silent 401, not as an error you can see.

The practical consequence: **anything a tick needs must already be in the
plugin's own durable state**, written there during an earlier request hook, or
obtainable from a host call that resolves its own configuration.

Two more things:

- **Return `TickIdle()`** (or a zero `TickResult`) when there is nothing to do.
  Intentional work uses `TickDid(...)`. A zero-byte `HookResult` means
  pass-through — there is no v1 `handled` flag on the Go v2 path.
- **`failure_mode` and ticks — current host behaviour, changing in Migration B.**
  Today the host ignores `failure_mode` for ticks: a trapping tick is logged and
  the other plugins' ticks continue. The reasoning was that `failure_mode`
  selects whether a failing plugin blocks or passes *the request*, and a tick
  has no request.

  This is **pre-Migration-B behaviour, not the v2 contract.** v1's tick handling
  was one of the inconsistencies v2 set out to remove — `ABI.md` claimed
  `failure_mode` was universal while `run_on_tick` ignored it and
  `run_on_http_request` mapped every error to 503. Migration B makes failure
  handling uniform across hooks. Do not write a plugin that relies on a trapping
  tick being silently tolerated.
Ticks require the `env.background_tick` permission and are off unless the
operator also sets `plugins.runtime.tick_interval_seconds`. Both are deliberate:
code running outside any request is work an operator cannot see in a trace, and
it may spend their money.

## 6. Prompt-Cache Compliance

Provider prompt caching bills cached input tokens at ~10% of full price — it is
the single biggest cost lever an agent session has, and a plugin can silently
destroy it. Two rules keep a plugin compliant:

**1. Never strip cache breakpoints.** `Message.cache_control_json` and
`ToolDef.cache_control_json` carry the client's cache markers (Anthropic
`cache_control`, Bedrock `cachePoint`) through the plugin boundary. They
survive automatically — a plugin that returns a request keeps them without
doing anything. But a plugin that *restructures* messages (splits, merges,
reorders, drops) must carry the marker to the equivalent position in its
output. The SDK helpers do this:

```go
cc := sdk.CacheControl(msg)          // read a message's breakpoint (nil if none)
sdk.SetCacheBreakpoint(msg, cc)      // attach / clear one
sdk.MoveCacheBreakpoint(from, to)    // transfer when merging messages
```

**2. Be deterministic over the cacheable prefix.** Everything before the last
breakpoint — tools, system prompt, conversation history — must serialize to
the *same bytes* on every request that replays the same history. OpenAI
caching is an exact-prefix match (one changed token busts it); Anthropic
hashes the rendered prompt up to each breakpoint. Concretely:

- No wall-clock time, randomness, request IDs, or counters in anything you
  inject before a breakpoint.
- Any value derived for a *historical* message must be a pure function of
  that message (the intent plugin's heuristic fill once mixed in a snippet of
  the latest user message — every turn re-serialized the same history to
  different bytes, busting the cache from that point on; it now derives only
  from the call's own name+args).
- One-time changes are fine: the first compaction of a tool result re-caches
  once and then stays stable (keyed by content, tool arguments, intent, and
  policy version) — a potential net win after rewrite cost. What's
  fatal is *per-turn* variance.
- Per-request state belongs in `ToranaMetaJson` (never serialized to the
  wire), not in messages or tool schemas.

The guardrail test `internal/plugin/cache_compliance_test.go` runs every
in-repo plugin twice over an identical request and asserts byte-identical
output, and asserts markers survive the round-trip. New plugins are picked up
by adding their name to the list — do so.

## 7. Tool-output safety

The trailing tool-result batch is evidence the model requested but has not yet
consumed. Keep it exact unless an explicit rule opts a recoverable tool into
deterministic `first_pass` reduction. Model-based compaction must always wait
for at least one exact exposure.

Unknown tools, mutation outputs, and failures default to exact. Historical
source reads need a recent exact window and a deterministic recovery marker;
economically gated transformations must be assessed as one batch from the
earliest changed item. Reuse the policy and cache-key helpers in `plugin-sdk`
rather than inventing plugin-specific matching or call-ID-only keys. See
[COMPACTION.md](https://github.com/torana-edge/torana-edge/blob/main/docs/COMPACTION.md) for the public contract.

## 8. Request-replacement validation (normative output contract)

A `ReplaceRequest` returned from `run_before_request` is validated atomically
by `HookResult.ValidateFor(HOOK_BEFORE_REQUEST)` BEFORE the host chains or
applies it. These are REPLACEMENT-OUTPUT rules: the plugin and the provider
must never be able to inspect different logical requests. Accepted host input
is normalized into this closed domain before plugin dispatch, so a plugin
only ever sees canonical shapes.

Executable field table (see `pb/v2/request_validate.go` for the normative
implementation):

| Field | Rule |
|---|---|
| `torana_meta_json` | absent or JSON object (host-owned, immutable) |
| `provider_extensions_json` | absent or JSON object |
| `safety_settings_json` | absent or JSON **array** (Gemini shape) |
| `Message.content_parts_json` | absent or JSON array |
| `Message.cache_control_json` | absent or JSON object |
| `ToolCall.arguments_json` | REQUIRED non-empty JSON object per present call (`{}` is the canonical no-arguments shape) |
| `ToolDef.parameters_json` | REQUIRED non-empty JSON object per present definition (`{}` is a valid unconstrained schema; empty bytes are not) |
| `ToolDef.cache_control_json` | absent or JSON object |

Universal rules:

- no nil nested messages, tool calls, or tool definitions (an adversarial
  guest must not be able to crash the verifier);
- unknown protobuf fields are refused at the request, message, tool-call,
  and tool-definition levels (the contract is closed);
- `ToolCall.id` and `ToolCall.name` are non-empty in the REQUEST context;
  `ToolDef.name` is non-empty. Anonymous RESPONSE tool calls are exempt from
  the id rule: response ids are host-owned and may legitimately be absent
  (their own `ToolCall.Validate` governs them);
- `temperature` and `top_p` are finite when present; no provider-specific
  ranges are invented;
- JSON fields must be valid UTF-8, surrogate-safe, and duplicate-free, with
  exactly one top-level value (`pb/v2/jsontext`). Empty bytes are absence
  only for absent-capable fields; a literal JSON `null` is a wrong top-level
  value everywhere.

A replacement that fails this contract is refused; whether the refusal
follows `failure_mode` (pass keeps the accepted input, block produces the
plugin-failure refusal) or becomes a host-level terminal error is a host
policy decision documented in the host's own checkpoint — the SDK unit is
the single normative statement of the domain, shared by Go guests, handmade
guests, and the host's unconditional verifier.
