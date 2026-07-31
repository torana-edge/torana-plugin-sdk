package outboundpolicy

import (
	"testing"

	plugin_sdk "github.com/torana-edge/torana-plugin-sdk"
)

func TestChatResponseNarrowedPolicyContract(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatal(err)
	}

	content, ok := OutboundFieldPolicy("torana.v2.ResponseMessage", "content")
	if !ok {
		t.Fatal("ResponseMessage.content missing from inventory")
	}
	sec, ok := content.Section()
	if content.Kind() != PolicySection || !ok || sec != plugin_sdk.SectionMessagesAssistant {
		t.Fatal("ResponseMessage.content must be assistant section (value writable when present)")
	}

	name, _ := OutboundFieldPolicy("torana.v2.ToolCall", "name")
	nsec, ok := name.Section()
	if name.Kind() != PolicySection || !ok || nsec != plugin_sdk.SectionMessagesAssistant {
		t.Fatal("ToolCall.name must map to SectionMessagesAssistant")
	}
	args, _ := OutboundFieldPolicy("torana.v2.ToolCall", "arguments_json")
	asec, ok := args.Section()
	if args.Kind() != PolicySection || !ok || asec != plugin_sdk.SectionMessagesAssistant {
		t.Fatal("ToolCall.arguments_json must map to SectionMessagesAssistant")
	}

	toolCalls, ok := OutboundFieldPolicy("torana.v2.ResponseMessage", "tool_calls")
	if !ok || !toolCalls.IsFixedContainer() {
		t.Fatal("ResponseMessage.tool_calls must be PolicyFixedContainer")
	}
	if toolCalls.IsContainer() {
		t.Fatal("PolicyFixedContainer must not report as ordinary PolicyContainer")
	}

	finish, _ := OutboundFieldPolicy("torana.v2.ChatResponse", "finish_reason")
	if !finish.IsHostOwned() {
		t.Fatal("ChatResponse.finish_reason must be host-owned")
	}
	id, _ := OutboundFieldPolicy("torana.v2.ToolCall", "id")
	if !id.IsHostOwned() {
		t.Fatal("ToolCall.id must be host-owned")
	}

	// Request-shaped Message fields must not appear on the response inventory.
	for _, field := range []string{
		"role", "content_parts_json", "thinking", "thinking_signature",
		"redacted_thinking", "tool_call_id", "tool_name", "cache_control_json",
	} {
		if _, ok := OutboundFieldPolicy("torana.v2.ResponseMessage", field); ok {
			t.Errorf("ResponseMessage must not expose dead field %q", field)
		}
		if _, ok := OutboundFieldPolicy("torana.v2.Message", field); ok {
			t.Errorf("request Message must not remain in outbound inventory (found %q)", field)
		}
	}

	toolSig, _ := OutboundFieldPolicy("torana.v2.ToolCall", "signature")
	if !toolSig.IsBoundSignature() {
		t.Fatal("ToolCall.signature must remain PolicyBoundSignature")
	}
}
