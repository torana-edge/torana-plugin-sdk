package plugin_sdk

import (
	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

// Verdicts are attributed host calls. Classified host refusals are
// fire-and-forget (the host logs them). Invalid arguments and protocol
// failures panic so the guest traps and failure_mode applies — a broken block
// must not look like success.
//
// Block short-circuits the pipeline. A plugin that also returns a replacement
// is not an error — block wins and the replacement is discarded.

// BlockRequest rejects the request with a provider-shaped error.
// Requires env.block_request. Status must be 400–599.
func BlockRequest(status int32, code, message string) {
	mustHostCall("env.block_request", &pbv1.BlockRequestArgs{
		Status:  status,
		Code:    code,
		Message: message,
	})
}

// RespondRequest serves content without calling upstream.
// Requires env.respond_request. If both block and respond are issued, block wins.
func RespondRequest(content string) {
	mustHostCall("env.respond_request", &pbv1.RespondRequestArgs{Content: content})
}

// RouteRequest sends the request to a different provider and/or model.
// Requires env.route_request. An empty provider with a non-empty model is a
// model-only override on the original provider.
func RouteRequest(provider, model string) {
	mustHostCall("env.route_request", &pbv1.RouteRequestArgs{
		Provider: provider,
		Model:    model,
	})
}

// SetIdentity overrides the rate-limit / identity key for this request.
// Requires env.set_identity.
func SetIdentity(identity string) {
	mustHostCall("env.set_identity", &pbv1.SetIdentityArgs{Identity: identity})
}

// MetaAppend atomically appends fragment to request-scoped metadata for
// blockIndex. Permission env.meta_set (dispatcher maps env.meta_append).
// See pb/v1.MetaAppendSuccessValue for reply semantics.
func MetaAppend(blockIndex int32, fragment []byte) ([]byte, *pbv1.HostError, error) {
	return HostCall(pbv1.MetaAppendCommand, &pbv1.MetaAppendArgs{
		BlockIndex: blockIndex,
		Fragment:   fragment,
	})
}
