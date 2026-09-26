# Add agent-facing operations to a plugin

Expose a small JSON interface through the same HTTP hook that serves your
plugin's local page. The host advertises enabled operations to terminal clients
and agent harnesses. Keep each operation's risk and schema explicit.

The [OTel plugin](https://github.com/torana-edge/torana-plugins/blob/main/plugins/otel/AGENT_OPERATIONS.md)
is a working example. For calling and administering operations, see the
[host control-plane reference](https://github.com/torana-edge/torana-edge/blob/main/docs/AGENT_CONTROL_PLANE.md).

## Contract

`agent.json` is optional. For a plugin that wants its operations in Torana's
MCP server and `torana>` commands, use schema version 2:

```json
{
  "schema_version": 2,
  "namespace": {
    "title": "My plugin",
    "summary": "Explains and controls this plugin.",
    "alias": "mine",
    "categories": ["workflow"]
  },
  "operations": [
    {
      "id": "session.status",
      "method": "GET",
      "path": "/status",
      "description": "Read this conversation's plugin status.",
      "risk": "read",
      "idempotent": true,
      "model_access": "read",
      "conversation_binding": "required",
      "output_schema": {
        "type": "object"
      }
    }
  ]
}
```

The plugin's manifest name is its canonical namespace. The optional short
`alias` is for typing directives; MCP always uses the canonical name. Namespace
title (up to 60 characters), summary (up to 300), and categories help people
and agents find the plugin's operations. Torana also adds standard operations
to every namespace, such as `_info`, `_status`, `_enable`, and `_disable`;
plugins cannot declare IDs beginning with `_`.

Each operation can add:

- `model_access`: `read`, `confirm`, or `never`. The default follows `risk`
  (`read` → `read`, `write` → `confirm`, `destructive` → `never`). A plugin may
  make access stricter, and the operator may tighten it further. The host's
  protected-operation floor cannot be relaxed.
- `conversation_binding`: `none`, `preferred`, or `required`. Use `required`
  whenever the result or change belongs to the current conversation. A model
  cannot name a different conversation through an operation.

For MCP calls, Torana supplies `X-Torana-MCP-Binding` as `bound` or `unbound`.
Only a verified `bound` call also receives `X-Torana-Conversation-Id` and
`X-Torana-Tool-Use-Id`. A missing binding header means this is **not an MCP
call** (for example, a UI, CLI, or agent API request), rather than an unbound
MCP call. An operation requiring conversation binding must refuse both a
missing header and `unbound`; never infer identity from operation input or
caller-supplied headers. Torana strips those headers and supplies its own
verified evidence to the plugin.

Each operation can also add:

- `directive`: for a user-typed `torana> <namespace> <command> ...` form, with
  `command`, ordered property names in `args` (`?` marks an optional argument),
  and optional `user_direct`. Set `user_direct` only for low-risk reversible
  writes; the host disallows it on protected plugins and protected operations.
- `examples`: short phrases to help operation search and `torana> ask`.
- `deprecated` and `replaced_by`: keep an older operation callable while
  directing people to its replacement; deprecated operations leave search.

For example, a route pin may declare `"directive": {"command": "pin",
"args": ["step", "user_turns?"], "user_direct": true}`. The host still checks
the input schema and access policy before dispatch. The plugin itself must
implement the operation's `/agent/...` HTTP path.

Version-1 descriptors remain valid for existing integrations, but have no
declared namespace title, alias, directives, or explicit model access. New
plugins should use version 2.

Each operation must use JSON input and output. `input_schema` is optional;
`output_schema` is required. Supported methods are `GET`, `POST`, `PUT`,
`PATCH`, and `DELETE`. Risk must be `read`, `write`, or `destructive`, and it
must agree with the method. Schemas describe the contract to callers; plugin
bundles are rejected if they use schema keywords the host cannot enforce, and
Torana validates both request and response values at dispatch.

The supported subset is `type`, `properties`, `required`,
`additionalProperties` (boolean), `items`, `const`, `enum`, `$schema`, `title`,
and `description`. Without `input_schema`, an operation accepts no body.

The public operation path is:

```text
/_torana/api/v1/agent/plugins/<plugin-name><operation-path>
```

Torana rewrites it before dispatch, so the plugin receives:

```text
/agent<operation-path>
```

Mutating calls made without a browser `Origin` must include:

```text
X-Torana-Local-Request: 1
```

The existing control-plane loopback, host, and request-origin protections still
apply. Plugin HTTP handling also requires the `run_on_http_request` hook and an
approved `env.serve_http` grant.

## Packaging and approval

`agent.json` is included in the bundle digest. Changing an operation, schema,
or description therefore creates a new digest that the operator must approve,
just like changing `plugin.wasm`, `plugin.json`, or `schema.json`.

Use the host's install/build tooling or your repository's packaging process.
Ensure the descriptor is included with the exact code and manifest being
reviewed; do not copy an approval from a different digest. The official
catalogue's [contribution guide](https://github.com/torana-edge/torana-plugins/blob/main/CONTRIBUTING.md)
owns its packaging commands.
