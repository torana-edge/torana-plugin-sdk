package plugin_sdk

// The v1 capability vocabulary.
//
// Which hook names and permission strings exist is an ABI concern, not a
// host implementation detail: a plugin declares them in its manifest, and a
// manifest is a contract against the ABI rather than against one particular
// proxy build. Declaring them here gives every consumer one list to check
// against — the host that enforces them, the tooling that validates manifests
// before publishing, and plugin authors who want to verify their own manifest
// without reading someone else's source.
//
// This list already drifted once, when a copy in the official plugin
// repository's manifest validator rejected capabilities the host accepted
// perfectly well. A published list is the fix.
//
// A host is free to expose fewer than this. It must not invent names outside
// it, because a plugin requesting one has no way to know whether it will ever
// be granted.

// Hooks a plugin may declare. See docs/PLUGIN_SEMANTICS.md for what each
// one may and may not do.
var Hooks = []string{
	"run_after_response",
	"run_before_request",
	"run_on_http_request",
	"run_on_stream_chunk",
	"run_on_tick",
}

// Permissions is every capability a plugin may request, including the
// ir.*.write grants in capabilities_write.go. Requesting one is never a grant:
// an operator approves capabilities against an exact bundle digest.
//
// This is ONE list on purpose. Hosts build their allowlist from it, and an
// earlier draft kept the write grants in a separate list with a union helper —
// which meant a plugin could pass `torana plugin lint` (checking IsPermission)
// and then be refused at load (checking the env-only list). Two lists that must
// agree will eventually not.
var Permissions = append([]string{
	"env.background_tick",
	"env.block_request",
	"env.cache_get",
	"env.cache_set",
	"env.credential_get",
	"env.emit_metric",
	"env.file_append",
	"env.file_delete",
	"env.file_list",
	"env.file_read",
	"env.file_write",
	"env.host_call.torana_cache_pricing",
	"env.host_call.torana_db_query",
	"env.host_call.torana_evaluate_compaction",
	"env.host_call.torana_kms_decrypt",
	"env.host_call.torana_offload_completion",
	"env.host_call.torana_plugin_counter",
	"env.host_call.torana_record_savings",
	"env.host_call.torana_send_request",
	"env.host_call.verify_virtual_key",
	"env.http_request",
	"env.log",
	"env.meta_get",
	"env.meta_set",
	"env.now",
	"env.original_request",
	"env.original_response",
	"env.plugin_config",
	"env.request_headers",
	"env.respond_request",
	"env.route_request",
	"env.serve_http",
	"env.set_identity",
	"env.shared_cache_get",
	"env.shared_cache_set",
	"env.state_get",
	"env.state_keys",
	"env.state_set",
}, WritePermissions...)

// IsHook reports whether name is a v1 hook.
func IsHook(name string) bool { return contains(Hooks, name) }

// IsPermission reports whether name is a capability a plugin may request.
func IsPermission(name string) bool { return contains(Permissions, name) }

func contains(list []string, name string) bool {
	for _, v := range list {
		if v == name {
			return true
		}
	}
	return false
}
