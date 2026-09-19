# Torana plugin capabilities

Generated from `capabilities.json`; run `scripts/generate-capabilities.py` after editing the catalog.

## Hook grants

| Permission | Hook |
| --- | --- |
| `env.background_tick` | `run_on_tick` |
| `env.serve_http` | `run_on_http_request` |

## Commands

| Command | Permission | Arguments | Result | Hooks | Helpers |
| --- | --- | --- | --- | --- | --- |
| `env.block_request` | `env.block_request` | `BlockRequestArgs` | `empty` | `run_before_request` | `BlockRequest`, `MustBlockRequest` |
| `env.cache_delete` | `env.cache_set` | `CacheDeleteArgs` | `empty` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `CacheDelete` |
| `env.cache_get` | `env.cache_get` | `CacheGetArgs` | `utf8` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `CacheGet` |
| `env.cache_policy` | `env.cache_policy` | `PromptCachePolicyGetArgs` | `PromptCachePolicy` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `GetPromptCachePolicy` |
| `env.cache_set` | `env.cache_set` | `CacheSetArgs` | `empty` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `CacheSet`, `CacheSetTTL` |
| `env.credential_get` | `env.credential_get` | `CredentialGetArgs` | `bytes` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `GetCredential` |
| `env.emit_metric` | `env.emit_metric` | `import` | `empty` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `EmitMetric`, `Counter`, `Histogram`, `Gauge` |
| `env.file_append` | `env.file_append` | `FileAppendArgs` | `empty` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `AppendFile` |
| `env.file_delete` | `env.file_delete` | `FileDeleteArgs` | `empty` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `DeleteFile` |
| `env.file_list` | `env.file_list` | `FileListArgs` | `json-string-list` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `ListFiles` |
| `env.file_read` | `env.file_read` | `FileReadArgs` | `bytes` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `ReadFile` |
| `env.file_write` | `env.file_write` | `FileWriteArgs` | `empty` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `WriteFile` |
| `env.http_request` | `env.http_request` | `OutboundHTTPRequestArgs` | `OutboundHTTPResponse` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `HTTPRequest` |
| `env.log` | `env.log` | `import` | `empty` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `Log`, `Debug`, `Info` |
| `env.meta_append` | `env.meta_set` | `MetaAppendArgs` | `bytes` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `MetaAppend` |
| `env.meta_get` | `env.meta_get` | `MetaGetArgs` | `utf8` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `MetaGet` |
| `env.meta_set` | `env.meta_set` | `MetaSetArgs` | `empty` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `MetaSet` |
| `env.model_complete` | `env.model_complete` | `ModelCompleteArgs` | `ModelCompleteResult` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `ModelComplete` |
| `env.model_pricing` | `env.model_pricing` | `ModelPricingGetArgs` | `ModelPricing` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `GetModelPricing` |
| `env.now` | `env.now` | `none` | `unix-ms` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `Now` |
| `env.original_request` | `env.original_request` | `none` | `bytes` | `run_before_request`, `run_after_response`, `run_on_stream_chunk` | `OriginalRequest` |
| `env.original_response` | `env.original_response` | `none` | `bytes` | `run_after_response`, `run_on_stream_chunk` | `OriginalResponse` |
| `env.plugin_config` | `env.plugin_config` | `none` | `json-object` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `PluginConfig` |
| `env.resource_info` | `env.resource_info` | `ResourceInfoArgs` | `ResourceInfo` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `GetResourceInfo` |
| `env.respond_request` | `env.respond_request` | `RespondRequestArgs` | `empty` | `run_before_request` | `RespondRequest`, `RespondText`, `MustRespondRequest`, `MustRespondText` |
| `env.route_request` | `env.route_request` | `RouteRequestArgs` | `empty` | `run_before_request` | `RouteRequest`, `MustRouteRequest` |
| `env.set_identity` | `env.set_identity` | `SetIdentityArgs` | `empty` | `run_before_request` | `SetIdentity`, `MustSetIdentity` |
| `env.shared_cache_delete` | `env.shared_cache_set` | `CacheDeleteArgs` | `empty` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `SharedCacheDelete` |
| `env.shared_cache_get` | `env.shared_cache_get` | `CacheGetArgs` | `utf8` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `SharedCacheGet` |
| `env.shared_cache_set` | `env.shared_cache_set` | `CacheSetArgs` | `empty` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `SharedCacheSet`, `SharedCacheSetTTL` |
| `env.state_compare_and_delete` | `env.state_set` | `StateCompareAndDeleteArgs` | `StateMutationResult` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `StateCompareAndDelete` |
| `env.state_compare_and_set` | `env.state_set` | `StateCompareAndSetArgs` | `StateMutationResult` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `StateCompareAndSet` |
| `env.state_delete` | `env.state_set` | `StateDeleteArgs` | `empty` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `StateDelete` |
| `env.state_get` | `env.state_get` | `StateGetArgs` | `utf8` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `StateGet`, `StateGetJSON` |
| `env.state_get_versioned` | `env.state_get` | `StateGetArgs` | `StateValue` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `StateGetVersioned` |
| `env.state_keys` | `env.state_keys` | `none` | `json-string-list` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `StateKeys` |
| `env.state_scan` | `env.state_keys` | `StateScanArgs` | `StateScanResult` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `StateScan` |
| `env.state_set` | `env.state_set` | `StateSetArgs` | `empty` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `StateSet`, `StateSetJSON` |
| `torana_evaluate_compaction` | `env.host_call.torana_evaluate_compaction` | `compaction-json` | `compaction-json` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `EvaluateCompaction` |
| `torana_plugin_counter` | `env.host_call.torana_plugin_counter` | `counter-json` | `empty` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `RecordCounter` |
| `torana_record_savings` | `env.host_call.torana_record_savings` | `savings-json` | `empty` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `RecordSavings` |
| `torana_send_request` | `env.host_call.torana_send_request` | `egress-json` | `egress-json` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `SendRequest` |
| `verify_virtual_key` | `env.host_call.verify_virtual_key` | `virtual-key-json` | `identity-json` | `run_before_request`, `run_after_response`, `run_on_stream_chunk`, `run_on_http_request`, `run_on_tick` | `VerifyVirtualKey` |

## IR write permissions

- `ir.cache_control.write`
- `ir.messages.write.assistant`
- `ir.messages.write.developer`
- `ir.messages.write.other`
- `ir.messages.write.system`
- `ir.messages.write.tool`
- `ir.messages.write.user`
- `ir.model.write`
- `ir.params.write`
- `ir.stream.write`
- `ir.tool_result_content.write`
- `ir.tool_result_errors.write`
- `ir.tool_results.write`
- `ir.tools.write`
