package plugin_sdk

import (
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
	for name := range outboundDelegateTargets {
		if _, ok := OutboundDelegateTargets(name); !ok {
			t.Errorf("delegate %q missing from OutboundDelegateTargets", name)
		}
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
		p, ok := policy[fname]
		if !ok {
			t.Errorf("%s.%s belongs to no policy — assign Section, HostOwned, Delegate, or Topology",
				name, fname)
			continue
		}
		validateFieldPolicy(t, name, fname, p)

		if p.Kind == PolicyDelegate {
			targets, ok := OutboundDelegateTargets(p.Delegate)
			if !ok {
				t.Errorf("%s.%s delegates to unknown verifier %q", name, fname, p.Delegate)
				continue
			}
			for _, target := range targets {
				if _, ok := OutboundPolicyFor(target); !ok {
					t.Errorf("%s.%s delegates to %s but that message has no policy registry",
						name, fname, target)
					continue
				}
				// Resolve via a throwaway message of that type when possible.
				walkDelegateTarget(t, target, seen)
			}
			continue
		}

		if fd.Kind() != protoreflect.MessageKind {
			continue
		}
		child := fd.Message()
		if _, ok := OutboundPolicyFor(child.FullName()); ok {
			walkOutboundInventory(t, child, seen)
			continue
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

func validateFieldPolicy(t *testing.T, msg protoreflect.FullName, field string, p FieldPolicy) {
	t.Helper()
	switch p.Kind {
	case PolicySection:
		if p.Section == "" || !IsWritePermission(string(p.Section)) {
			t.Errorf("%s.%s Section policy maps to %q, which is not a write grant", msg, field, p.Section)
		}
		if p.Section == SectionStreamWrite {
			t.Errorf("%s.%s uses PolicySection for ir.stream.write — use PolicyTopology", msg, field)
		}
	case PolicyTopology:
		if p.Section != SectionStreamWrite {
			t.Errorf("%s.%s Topology policy must use ir.stream.write, got %q", msg, field, p.Section)
		}
	case PolicyHostOwned:
		// ok
	case PolicyDelegate:
		if p.Delegate == "" {
			t.Errorf("%s.%s Delegate policy has empty verifier name", msg, field)
		}
	default:
		t.Errorf("%s.%s has unknown PolicyKind %d", msg, field, p.Kind)
	}
}

func walkDelegateTarget(t *testing.T, name protoreflect.FullName, seen map[protoreflect.FullName]bool) {
	t.Helper()
	// Build descriptors from known generated types.
	var msg proto.Message
	switch name {
	case "torana.v2.ChatResponse":
		msg = &pbv2.ChatResponse{}
	case "torana.v2.StreamEvents":
		msg = &pbv2.StreamEvents{}
	case "torana.v2.StreamEvent":
		msg = &pbv2.StreamEvent{}
	case "torana.v2.Suppress":
		msg = &pbv2.Suppress{}
	default:
		t.Errorf("no prototype for delegate target %s", name)
		return
	}
	walkOutboundInventory(t, msg.ProtoReflect().Descriptor(), seen)
}

func TestHookResultActionsAreHonestDelegates(t *testing.T) {
	want := map[string]string{
		"replace_request":  DelegateRequest,
		"replace_response": DelegateResponse,
		"emit_events":      DelegateStream,
		"serve_http":       DelegateHTTP,
		"tick_outcome":     DelegateTick,
		"suppress":         DelegateStream,
	}
	for field, delegate := range want {
		p := HookResultActionPolicies[field]
		if p.Kind != PolicyDelegate || p.Delegate != delegate {
			t.Errorf("HookResult.%s: want Delegate(%s), got kind=%d delegate=%q",
				field, delegate, p.Kind, p.Delegate)
		}
		if p.IsHostOwned() {
			t.Errorf("HookResult.%s must not be host-owned", field)
		}
	}
	events := StreamEventsFieldPolicies["events"]
	if events.Kind != PolicyDelegate || events.Delegate != DelegateStream {
		t.Fatalf("StreamEvents.events must Delegate(stream), got kind=%d %q", events.Kind, events.Delegate)
	}
}

func TestObservedResponseFactsAreHostOwned(t *testing.T) {
	for _, field := range []string{"model", "id", "usage", "upstream_status", "duration_ms", "provider_extensions_json"} {
		if !ChatResponseFieldPolicies[field].IsHostOwned() {
			t.Errorf("ChatResponse.%s must be host-owned", field)
		}
	}
	if !ResponseMessageFieldPolicies["role"].IsHostOwned() {
		t.Fatal("response message.role must be host-owned")
	}
	if !ResponseMessageFieldPolicies["thinking_signature"].IsHostOwned() {
		t.Fatal("thinking_signature must be host-owned")
	}
	if !ResponseToolCallFieldPolicies["signature"].IsHostOwned() {
		t.Fatal("ToolCall.signature must be host-owned")
	}
	if !MessageStartFieldPolicies["model"].IsHostOwned() {
		t.Fatal("MessageStart.model must be host-owned")
	}
	if !ToolCallRefFieldPolicies["signature"].IsHostOwned() {
		t.Fatal("ToolCallRef.signature must be host-owned")
	}
	if !StreamEventVariantPolicies["signature_delta"].IsHostOwned() {
		t.Fatal("signature_delta must be host-owned")
	}
	if !StreamEventVariantPolicies["usage"].IsHostOwned() {
		t.Fatal("usage variant must be host-owned")
	}
	if ToolCallDeltaFieldPolicies["index"].Kind != PolicyTopology {
		t.Fatal("ToolCallDelta.index must be topology")
	}
}

func TestSignatureBindingsPinned(t *testing.T) {
	byMsg := map[protoreflect.FullName]SignatureBinding{}
	for _, b := range SignatureBindings {
		byMsg[b.Message] = b
		policy, ok := OutboundPolicyFor(b.Message)
		if !ok && b.Message != "torana.v2.StreamEvent" {
			t.Errorf("SignatureBinding for unknown message %s", b.Message)
			continue
		}
		if b.Message == "torana.v2.StreamEvent" {
			if b.CrossEventNote == "" {
				t.Fatal("signature_delta binding must document cross-event handling")
			}
			if !StreamEventVariantPolicies["signature_delta"].IsHostOwned() {
				t.Fatal("signature_delta must stay host-owned")
			}
			continue
		}
		sig, ok := policy[b.SignatureField]
		if !ok || !sig.IsHostOwned() {
			t.Errorf("%s.%s must be host-owned", b.Message, b.SignatureField)
		}
		for _, c := range b.ContentFields {
			cp, ok := policy[c]
			if !ok {
				t.Errorf("%s content field %s missing from policy", b.Message, c)
				continue
			}
			if cp.Kind != PolicySection {
				t.Errorf("%s.%s should be a content Section (signed content), got kind %d",
					b.Message, c, cp.Kind)
			}
		}
	}
	for _, want := range []protoreflect.FullName{
		"torana.v2.Message", "torana.v2.ToolCall", "torana.v2.ToolCallRef", "torana.v2.StreamEvent",
	} {
		if _, ok := byMsg[want]; !ok {
			t.Errorf("missing SignatureBinding for %s", want)
		}
	}
}

func TestStreamCompositionContractIsDocumented(t *testing.T) {
	// Migration B will implement the field-aware diff. These pins record the
	// cases an approximate whole-variant helper got wrong, so the real
	// verifier must cover them.
	cases := []struct {
		name string
		// Expected Migration B outcomes (documented, not computed here).
		needTopology bool
		needAssistant bool
		reject        bool // host-owned change/remove/add
		identicalOK   bool // unchanged host-owned or content re-emit
	}{
		{"change only ToolCallDelta.index", true, false, false, false},
		{"change ContentBlockStart.tool_call.signature", false, false, true, false},
		{"identical Usage re-emit", false, false, false, true},
		{"identical MessageStart re-emit", false, false, false, true},
		{"identical signature_delta re-emit", false, false, false, true},
		{"identical TextDelta re-emit", false, false, false, true},
		{"unchanged MessageStop one-for-one", false, false, false, true},
		{"unchanged ContentBlockStart one-for-one", false, false, false, true},
		{"suppress Usage", false, false, true, false},
		{"one-for-one TextDelta rewrite", false, true, false, false},
		{"suppress TextDelta", true, true, false, false},
	}
	if len(cases) < 10 {
		t.Fatal("composition contract cases missing")
	}
	// Structural pins the verifier will consult:
	if ToolCallDeltaFieldPolicies["index"].Kind != PolicyTopology {
		t.Fatal("index change must be topology in the registry")
	}
	if !ToolCallRefFieldPolicies["signature"].IsHostOwned() {
		t.Fatal("nested ToolCallRef.signature must be host-owned in the registry")
	}
	if !StreamEventVariantPolicies["usage"].IsHostOwned() {
		t.Fatal("Usage payload is host-owned; identical re-emit is still a no-op at diff time")
	}
}
