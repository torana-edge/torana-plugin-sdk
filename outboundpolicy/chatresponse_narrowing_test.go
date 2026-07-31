package outboundpolicy

import (
	"testing"

	plugin_sdk "github.com/torana-edge/torana-plugin-sdk"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestChatResponseNarrowedPolicyContract(t *testing.T) {
	content, _ := OutboundFieldPolicy("torana.v2.Message", "content")
	sec, ok := content.Section()
	if content.Kind() != PolicySection || !ok || sec != plugin_sdk.SectionMessagesAssistant {
		t.Fatal("Message.content must remain assistant-writable")
	}
	name, _ := OutboundFieldPolicy("torana.v2.ToolCall", "name")
	if name.Kind() != PolicySection {
		t.Fatal("ToolCall.name must remain assistant-writable")
	}
	args, _ := OutboundFieldPolicy("torana.v2.ToolCall", "arguments_json")
	if args.Kind() != PolicySection {
		t.Fatal("ToolCall.arguments_json must remain assistant-writable")
	}

	toolCalls, _ := OutboundFieldPolicy("torana.v2.Message", "tool_calls")
	if !toolCalls.IsContainer() {
		t.Fatal("Message.tool_calls must be PolicyContainer for fixed cardinality/order")
	}

	hostOwned := []struct{ msg, field string }{
		{"torana.v2.ChatResponse", "finish_reason"},
		{"torana.v2.Message", "content_parts_json"},
		{"torana.v2.Message", "thinking"},
		{"torana.v2.Message", "redacted_thinking"},
		{"torana.v2.Message", "tool_call_id"},
		{"torana.v2.Message", "tool_name"},
		{"torana.v2.Message", "cache_control_json"},
		{"torana.v2.ToolCall", "id"},
	}
	for _, tc := range hostOwned {
		p, ok := OutboundFieldPolicy(protoreflect.FullName(tc.msg), tc.field)
		if !ok || !p.IsHostOwned() {
			t.Errorf("%s.%s must be host-owned under narrowed contract", tc.msg, tc.field)
		}
	}

	thinkingSig, _ := OutboundFieldPolicy("torana.v2.Message", "thinking_signature")
	if !thinkingSig.IsBoundSignature() {
		t.Fatal("Message.thinking_signature must remain PolicyBoundSignature")
	}
	toolSig, _ := OutboundFieldPolicy("torana.v2.ToolCall", "signature")
	if !toolSig.IsBoundSignature() {
		t.Fatal("ToolCall.signature must remain PolicyBoundSignature")
	}
}
