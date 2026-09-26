# Add agent-facing operations to a plugin

Expose a small JSON interface through the same HTTP hook that serves your
plugin's local page. The host advertises enabled operations to terminal clients
and agent harnesses. Keep each operation's risk and schema explicit.

The [OTel plugin](https://github.com/torana-edge/torana-plugins/blob/main/plugins/otel/AGENT_OPERATIONS.md)
is a working example. For calling and administering operations, see the
[host control-plane reference](https://github.com/torana-edge/torana-edge/blob/main/docs/AGENT_CONTROL_PLANE.md).

## Contract

`agent.json` is optional. When present it has this shape:

```json
{
  "schema_version": 1,
  "description": "Machine-readable plugin operations.",
  "operations": [
    {
      "id": "status",
      "method": "GET",
      "path": "/status",
      "description": "Read plugin status.",
      "risk": "read",
      "idempotent": true,
      "output_schema": {
        "type": "object"
      }
    }
  ]
}
```

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

## Conversation-scoped operations

In an MCP dispatch, the host strips caller-supplied binding headers and injects
its own evidence. Go authors can read it with `sdk.HTTPConversation(req)`;
Rust authors use `http_conversation(&req)`. Both distinguish absent, explicitly
unbound and bound calls, rejecting malformed or ambiguous identities.
Use only the HTTP callback's request, not headers or IDs copied from tool input.
If your operation needs a conversation, decline an absent or unbound result;
never fall back to an ID supplied by the model. A bound session ID still does
not distinguish side threads inside that session.

## Packaging and approval

`agent.json` is included in the bundle digest. Changing an operation, schema,
or description therefore creates a new digest that the operator must approve,
just like changing `plugin.wasm`, `plugin.json`, or `schema.json`.

Use the host's install/build tooling or your repository's packaging process.
Ensure the descriptor is included with the exact code and manifest being
reviewed; do not copy an approval from a different digest. The official
catalogue's [contribution guide](https://github.com/torana-edge/torana-plugins/blob/main/CONTRIBUTING.md)
owns its packaging commands.
