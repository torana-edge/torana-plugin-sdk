package plugin_sdk

import (
	"strings"
	"testing"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestOutboundWriteGrantsAreRequestable(t *testing.T) {
	if !IsWritePermission(string(SectionStreamWrite)) {
		t.Fatal("ir.stream.write must be in WritePermissions")
	}
	if !IsPermission(string(SectionStreamWrite)) {
		t.Fatal("ir.stream.write must be in Permissions")
	}
	if !IsPermission("env.set_identity") {
		t.Fatal("env.set_identity must be in Permissions")
	}
	if StreamTopologySection() != SectionStreamWrite {
		t.Fatal("StreamTopologySection must be ir.stream.write")
	}
}

func TestRecursiveOutboundInventory(t *testing.T) {
	roots := []proto.Message{
		&pbv2.ChatResponse{},
		&pbv2.StreamEvent{},
		&pbv2.StreamEvents{},
		&pbv2.HookResult{},
		&pbv2.Suppress{},
	}
	seen := map[protoreflect.FullName]bool{}
	for _, root := range roots {
		walkOutboundInventory(t, root.ProtoReflect().Descriptor(), seen)
	}
}

func walkOutboundInventory(t *testing.T, desc protoreflect.MessageDescriptor, seen map[protoreflect.FullName]bool) {
	t.Helper()
	name := desc.FullName()
	if seen[name] {
		return
	}
	seen[name] = true

	policy, ok := OutboundPolicyFor(name)
	if !ok {
		t.Errorf("%s is reachable from an outbound root but has no field policy — "+
			"register it in outboundMessageFieldPolicies (empty map if fieldless)", name)
		return
	}

	fields := desc.Fields()
	seenFields := map[string]bool{}
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		fname := string(fd.Name())
		seenFields[fname] = true
		section, ok := policy[fname]
		if !ok {
			t.Errorf("%s.%s belongs to no grant section — assign it or mark it %s",
				name, fname, HostOwnedField)
			continue
		}
		if section != HostOwnedField && !IsWritePermission(section) {
			t.Errorf("%s.%s maps to %q, which is not a write grant", name, fname, section)
		}
		if fd.Kind() != protoreflect.MessageKind {
			continue
		}
		child := fd.Message()
		if _, ok := OutboundPolicyFor(child.FullName()); ok {
			walkOutboundInventory(t, child, seen)
			continue
		}
		// HookResult actions whose nested inventories live elsewhere (request
		// write grants) or are deferred (HTTP) must still be named above, but
		// are not walked here.
		if name == "torana.v2.HookResult" {
			switch fname {
			case "replace_request", "serve_http", "tick_outcome":
				continue
			}
		}
		t.Errorf("%s.%s points at unregistered nested message %s — "+
			"add it to outboundMessageFieldPolicies", name, fname, child.FullName())
	}
	for fname := range policy {
		if !seenFields[fname] {
			t.Errorf("%s.%s is mapped but no longer exists in the proto", name, fname)
		}
	}
}

func TestRequiredStreamMutationComposition(t *testing.T) {
	text := func(s string) *pbv2.StreamEvent {
		return &pbv2.StreamEvent{Event: &pbv2.StreamEvent_TextDelta{TextDelta: s}}
	}
	usage := &pbv2.StreamEvent{Event: &pbv2.StreamEvent_Usage{Usage: &pbv2.Usage{InputTokens: 1}}}
	msgStart := &pbv2.StreamEvent{Event: &pbv2.StreamEvent_MessageStart{
		MessageStart: &pbv2.MessageStart{Role: "assistant", Id: "x", Model: "m"},
	}}
	sig := &pbv2.StreamEvent{Event: &pbv2.StreamEvent_SignatureDelta{SignatureDelta: "sig"}}
	toolDelta := &pbv2.StreamEvent{Event: &pbv2.StreamEvent_ToolCallDelta{
		ToolCallDelta: &pbv2.ToolCallDelta{Index: 0, ArgumentsDelta: `{"a":1}`},
	}}

	t.Run("one-for-one text rewrite needs assistant only", func(t *testing.T) {
		need, err := RequiredStreamMutation(text("a"), []*pbv2.StreamEvent{text("b")})
		if err != nil {
			t.Fatal(err)
		}
		if !sectionsEqual(need, []WriteSection{SectionMessagesAssistant}) {
			t.Fatalf("got %v, want only assistant", need)
		}
	})

	t.Run("suppress text needs topology and assistant", func(t *testing.T) {
		need, err := RequiredStreamMutation(text("a"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if !sectionsEqual(need, []WriteSection{SectionMessagesAssistant, SectionStreamWrite}) {
			t.Fatalf("got %v, want assistant+topology", need)
		}
	})

	t.Run("fan-out text needs topology and assistant", func(t *testing.T) {
		need, err := RequiredStreamMutation(text("a"), []*pbv2.StreamEvent{text("b"), text("c")})
		if err != nil {
			t.Fatal(err)
		}
		if !sectionsEqual(need, []WriteSection{SectionMessagesAssistant, SectionStreamWrite}) {
			t.Fatalf("got %v, want assistant+topology", need)
		}
	})

	t.Run("suppress usage forbidden", func(t *testing.T) {
		_, err := RequiredStreamMutation(usage, nil)
		if err == nil || !strings.Contains(err.Error(), "host-owned") {
			t.Fatalf("want host-owned suppress error, got %v", err)
		}
	})

	t.Run("suppress message_start forbidden", func(t *testing.T) {
		_, err := RequiredStreamMutation(msgStart, nil)
		if err == nil || !strings.Contains(err.Error(), "host-owned") {
			t.Fatalf("want host-owned suppress error, got %v", err)
		}
	})

	t.Run("suppress signature_delta forbidden", func(t *testing.T) {
		_, err := RequiredStreamMutation(sig, nil)
		if err == nil || !strings.Contains(err.Error(), "host-owned") {
			t.Fatalf("want host-owned suppress error, got %v", err)
		}
	})

	t.Run("emit usage forbidden", func(t *testing.T) {
		_, err := RequiredStreamMutation(text("a"), []*pbv2.StreamEvent{usage})
		if err == nil || !strings.Contains(err.Error(), "host-owned") {
			t.Fatalf("want host-owned emit error, got %v", err)
		}
	})

	t.Run("suppress tool_call_delta needs topology and assistant", func(t *testing.T) {
		need, err := RequiredStreamMutation(toolDelta, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !sectionsEqual(need, []WriteSection{SectionMessagesAssistant, SectionStreamWrite}) {
			t.Fatalf("got %v, want assistant+topology", need)
		}
	})

	t.Run("kind change needs topology and both sections", func(t *testing.T) {
		need, err := RequiredStreamMutation(text("a"), []*pbv2.StreamEvent{
			{Event: &pbv2.StreamEvent_ThinkingDelta{ThinkingDelta: "t"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !sectionsEqual(need, []WriteSection{SectionMessagesAssistant, SectionStreamWrite}) {
			t.Fatalf("got %v, want assistant+topology", need)
		}
	})
}

func TestObservedResponseFactsAreHostOwned(t *testing.T) {
	for _, field := range []string{"model", "id", "usage", "upstream_status", "duration_ms", "provider_extensions_json"} {
		if ChatResponseFieldSections[field] != HostOwnedField {
			t.Errorf("ChatResponse.%s must be host-owned, got %q", field, ChatResponseFieldSections[field])
		}
	}
	if ResponseMessageFieldSections["role"] != HostOwnedField {
		t.Fatal("response message.role must be host-owned")
	}
	if ResponseMessageFieldSections["thinking_signature"] != HostOwnedField {
		t.Fatal("thinking_signature must be host-owned")
	}
	if ResponseToolCallFieldSections["signature"] != HostOwnedField {
		t.Fatal("ToolCall.signature must be host-owned")
	}
	if MessageStartFieldSections["model"] != HostOwnedField {
		t.Fatal("MessageStart.model must be host-owned")
	}
	if ToolCallRefFieldSections["signature"] != HostOwnedField {
		t.Fatal("ToolCallRef.signature must be host-owned")
	}
	if StreamEventVariantSections["signature_delta"] != HostOwnedField {
		t.Fatal("signature_delta must be host-owned")
	}
}

func sectionsEqual(got, want []WriteSection) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
