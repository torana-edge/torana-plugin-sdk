package plugin_sdk

import (
	"fmt"
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
func BlockRequest(status int32, code, message string) error {
	return checkedHostCall("env.block_request", &pbv1.BlockRequestArgs{
		Status:  status,
		Code:    code,
		Message: message,
	})
}

// RespondRequest serves content without calling upstream.
// Requires env.respond_request. If both block and respond are issued, block wins.
func RespondRequest(response *pbv1.SyntheticResponse) error {
	if response == nil {
		return fmt.Errorf("torana: respond request: response is nil")
	}
	return checkedHostCall("env.respond_request", &pbv1.RespondRequestArgs{Response: response})
}

// RespondText serves a simple text response without calling upstream.
func RespondText(content string) error {
	return RespondRequest(&pbv1.SyntheticResponse{Message: &pbv1.ResponseMessage{Blocks: []*pbv1.ResponseBlock{{Kind: &pbv1.ResponseBlock_Text{Text: &pbv1.ResponseTextBlock{Text: content}}}}}, FinishReason: "stop"})
}

// RouteRequest sends the request to a different provider and/or model.
// Requires env.route_request. An empty provider with a non-empty model is a
// model-only override on the original provider.
func RouteRequest(provider, model string) error {
	return checkedHostCall("env.route_request", &pbv1.RouteRequestArgs{
		Provider: provider,
		Model:    model,
	})
}

// SetIdentity overrides the rate-limit / identity key for this request.
// Requires env.set_identity.
func SetIdentity(identity string) error {
	return checkedHostCall("env.set_identity", &pbv1.SetIdentityArgs{Identity: identity})
}

func MustBlockRequest(status int32, code, message string) {
	mustHostCall("env.block_request", &pbv1.BlockRequestArgs{Status: status, Code: code, Message: message})
}
func MustRespondRequest(response *pbv1.SyntheticResponse) {
	mustHostCall("env.respond_request", &pbv1.RespondRequestArgs{Response: response})
}
func MustRespondText(content string) {
	mustHostCall("env.respond_request", &pbv1.RespondRequestArgs{Response: &pbv1.SyntheticResponse{Message: &pbv1.ResponseMessage{Blocks: []*pbv1.ResponseBlock{{Kind: &pbv1.ResponseBlock_Text{Text: &pbv1.ResponseTextBlock{Text: content}}}}}, FinishReason: "stop"}})
}
func MustRouteRequest(provider, model string) {
	mustHostCall("env.route_request", &pbv1.RouteRequestArgs{Provider: provider, Model: model})
}
func MustSetIdentity(identity string) {
	mustHostCall("env.set_identity", &pbv1.SetIdentityArgs{Identity: identity})
}

// MetaAppend atomically appends fragment to request-scoped metadata for
// blockIndex. Permission env.meta_set (dispatcher maps env.meta_append).
// See pb/v1.MetaAppendSuccessValue for reply semantics.
func MetaAppend(blockIndex int32, fragment []byte) ([]byte, *pbv1.HostError, error) {
	return hostCallChecked(pbv1.MetaAppendCommand, &pbv1.MetaAppendArgs{
		BlockIndex: blockIndex,
		Fragment:   fragment,
	})
}
