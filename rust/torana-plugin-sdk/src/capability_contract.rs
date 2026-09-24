// Generated from ../../capabilities.json by scripts/generate-capabilities.py.
// The runtime uses this inventory to reject commands absent from its ABI build.
#[derive(Clone, Copy)]
pub(crate) struct CommandContract {
    pub command: &'static str,
    pub arguments: &'static str,
    pub result: &'static str,
}

pub(crate) const COMMANDS: &[CommandContract] = &[
    CommandContract {
        command: "env.block_request",
        arguments: "BlockRequestArgs",
        result: "empty",
    },
    CommandContract {
        command: "env.cache_delete",
        arguments: "CacheDeleteArgs",
        result: "empty",
    },
    CommandContract {
        command: "env.cache_get",
        arguments: "CacheGetArgs",
        result: "utf8",
    },
    CommandContract {
        command: "env.cache_policy",
        arguments: "PromptCachePolicyGetArgs",
        result: "PromptCachePolicy",
    },
    CommandContract {
        command: "env.cache_set",
        arguments: "CacheSetArgs",
        result: "empty",
    },
    CommandContract {
        command: "env.credential_get",
        arguments: "CredentialGetArgs",
        result: "bytes",
    },
    CommandContract {
        command: "env.emit_metric",
        arguments: "import",
        result: "empty",
    },
    CommandContract {
        command: "env.file_append",
        arguments: "FileAppendArgs",
        result: "empty",
    },
    CommandContract {
        command: "env.file_delete",
        arguments: "FileDeleteArgs",
        result: "empty",
    },
    CommandContract {
        command: "env.file_list",
        arguments: "FileListArgs",
        result: "json-string-list",
    },
    CommandContract {
        command: "env.file_read",
        arguments: "FileReadArgs",
        result: "bytes",
    },
    CommandContract {
        command: "env.file_write",
        arguments: "FileWriteArgs",
        result: "empty",
    },
    CommandContract {
        command: "env.http_request",
        arguments: "OutboundHTTPRequestArgs",
        result: "OutboundHTTPResponse",
    },
    CommandContract {
        command: "env.log",
        arguments: "import",
        result: "empty",
    },
    CommandContract {
        command: "env.meta_append",
        arguments: "MetaAppendArgs",
        result: "bytes",
    },
    CommandContract {
        command: "env.meta_get",
        arguments: "MetaGetArgs",
        result: "utf8",
    },
    CommandContract {
        command: "env.meta_set",
        arguments: "MetaSetArgs",
        result: "empty",
    },
    CommandContract {
        command: "env.model_complete",
        arguments: "ModelCompleteArgs",
        result: "ModelCompleteResult",
    },
    CommandContract {
        command: "env.model_capabilities",
        arguments: "ModelCapabilitiesArgs",
        result: "ModelCapabilities",
    },
    CommandContract {
        command: "env.model_pricing",
        arguments: "ModelPricingGetArgs",
        result: "ModelPricing",
    },
    CommandContract {
        command: "env.now",
        arguments: "none",
        result: "unix-ms",
    },
    CommandContract {
        command: "env.original_request",
        arguments: "none",
        result: "bytes",
    },
    CommandContract {
        command: "env.original_response",
        arguments: "none",
        result: "bytes",
    },
    CommandContract {
        command: "env.plugin_config",
        arguments: "none",
        result: "json-object",
    },
    CommandContract {
        command: "env.resource_info",
        arguments: "ResourceInfoArgs",
        result: "ResourceInfo",
    },
    CommandContract {
        command: "env.respond_request",
        arguments: "RespondRequestArgs",
        result: "empty",
    },
    CommandContract {
        command: "env.route_request",
        arguments: "RouteRequestArgs",
        result: "empty",
    },
    CommandContract {
        command: "env.suggest",
        arguments: "SuggestArgs",
        result: "SuggestResult",
    },
    CommandContract {
        command: "env.set_identity",
        arguments: "SetIdentityArgs",
        result: "empty",
    },
    CommandContract {
        command: "env.shared_cache_delete",
        arguments: "CacheDeleteArgs",
        result: "empty",
    },
    CommandContract {
        command: "env.shared_cache_get",
        arguments: "CacheGetArgs",
        result: "utf8",
    },
    CommandContract {
        command: "env.shared_cache_set",
        arguments: "CacheSetArgs",
        result: "empty",
    },
    CommandContract {
        command: "env.state_compare_and_delete",
        arguments: "StateCompareAndDeleteArgs",
        result: "StateMutationResult",
    },
    CommandContract {
        command: "env.state_compare_and_set",
        arguments: "StateCompareAndSetArgs",
        result: "StateMutationResult",
    },
    CommandContract {
        command: "env.state_delete",
        arguments: "StateDeleteArgs",
        result: "empty",
    },
    CommandContract {
        command: "env.state_get",
        arguments: "StateGetArgs",
        result: "utf8",
    },
    CommandContract {
        command: "env.state_get_versioned",
        arguments: "StateGetArgs",
        result: "StateValue",
    },
    CommandContract {
        command: "env.state_keys",
        arguments: "none",
        result: "json-string-list",
    },
    CommandContract {
        command: "env.state_scan",
        arguments: "StateScanArgs",
        result: "StateScanResult",
    },
    CommandContract {
        command: "env.state_set",
        arguments: "StateSetArgs",
        result: "empty",
    },
    CommandContract {
        command: "torana_evaluate_compaction",
        arguments: "compaction-json",
        result: "compaction-json",
    },
    CommandContract {
        command: "torana_plugin_counter",
        arguments: "counter-json",
        result: "empty",
    },
    CommandContract {
        command: "torana_record_savings",
        arguments: "savings-json",
        result: "empty",
    },
    CommandContract {
        command: "torana_send_request",
        arguments: "egress-json",
        result: "egress-json",
    },
    CommandContract {
        command: "verify_virtual_key",
        arguments: "virtual-key-json",
        result: "identity-json",
    },
];

pub(crate) fn command(name: &str) -> Option<CommandContract> {
    COMMANDS.iter().copied().find(|entry| entry.command == name)
}
