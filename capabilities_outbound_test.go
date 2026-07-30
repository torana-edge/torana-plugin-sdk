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
	for _, d := range []DelegateKind{
		DelegateRequest, DelegateResponse, DelegateStream, DelegateHTTP, DelegateTick,
	} {
		if _, ok := OutboundDelegateTargets(d); !ok {
			t.Errorf("delegate %v missing from OutboundDelegateTargets", d)
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

	if !OutboundMessageRegistered(name) {
		t.Errorf("%s is reachable from an outbound root but has no field policy", name)
		return
	}
	fieldNames, _ := OutboundFieldNames(name)
	seenFields := map[string]bool{}
	for _, fname := range fieldNames {
		seenFields[fname] = true
	}

	fields := desc.Fields()
	protoFields := map[string]protoreflect.FieldDescriptor{}
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		protoFields[string(fd.Name())] = fd
		fname := string(fd.Name())
		p, ok := OutboundFieldPolicy(name, fname)
		if !ok {
			t.Errorf("%s.%s belongs to no policy", name, fname)
			continue
		}
		if err := p.validate(); err != nil {
			t.Errorf("%s.%s: %v", name, fname, err)
			continue
		}

		if p.Kind() == PolicyDelegate {
			d, ok := p.Delegate()
			if !ok {
				t.Errorf("%s.%s Delegate without kind", name, fname)
				continue
			}
			targets, ok := OutboundDelegateTargets(d)
			if !ok {
				t.Errorf("%s.%s delegates to unknown verifier %v", name, fname, d)
				continue
			}
			for _, target := range targets {
				if !OutboundMessageRegistered(target) {
					t.Errorf("%s.%s delegates to %s but that message has no policy",
						name, fname, target)
					continue
				}
				walkDelegateTarget(t, target, seen)
			}
			continue
		}

		if fd.Kind() != protoreflect.MessageKind {
			continue
		}
		child := fd.Message()
		if OutboundMessageRegistered(child.FullName()) {
			walkOutboundInventory(t, child, seen)
			continue
		}
		t.Errorf("%s.%s points at unregistered nested message %s", name, fname, child.FullName())
	}
	for fname := range seenFields {
		if _, ok := protoFields[fname]; !ok {
			t.Errorf("%s.%s is mapped but no longer exists in the proto", name, fname)
		}
	}
	for fname := range protoFields {
		if !seenFields[fname] {
			t.Errorf("%s.%s belongs to no policy", name, fname)
		}
	}
}

func walkDelegateTarget(t *testing.T, name protoreflect.FullName, seen map[protoreflect.FullName]bool) {
	t.Helper()
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
	want := map[string]DelegateKind{
		"replace_request":  DelegateRequest,
		"replace_response": DelegateResponse,
		"emit_events":      DelegateStream,
		"serve_http":       DelegateHTTP,
		"tick_outcome":     DelegateTick,
		"suppress":         DelegateStream,
	}
	for field, delegate := range want {
		p, ok := OutboundFieldPolicy("torana.v2.HookResult", field)
		if !ok {
			t.Fatalf("missing HookResult.%s", field)
		}
		got, ok := p.Delegate()
		if p.Kind() != PolicyDelegate || !ok || got != delegate {
			t.Errorf("HookResult.%s: want Delegate(%v), got kind=%v delegate=%v",
				field, delegate, p.Kind(), got)
		}
		if p.IsHostOwned() {
			t.Errorf("HookResult.%s must not be host-owned", field)
		}
	}
	events, ok := OutboundFieldPolicy("torana.v2.StreamEvents", "events")
	if !ok {
		t.Fatal("missing StreamEvents.events")
	}
	d, ok := events.Delegate()
	if events.Kind() != PolicyDelegate || !ok || d != DelegateStream {
		t.Fatalf("StreamEvents.events must Delegate(stream), got kind=%v %v", events.Kind(), d)
	}
}

func TestObservedResponseFactsAreHostOwned(t *testing.T) {
	for _, field := range []string{"model", "id", "usage", "upstream_status", "duration_ms", "provider_extensions_json"} {
		p, _ := OutboundFieldPolicy("torana.v2.ChatResponse", field)
		if !p.IsHostOwned() {
			t.Errorf("ChatResponse.%s must be host-owned", field)
		}
	}
	mustHost := []struct{ msg, field string }{
		{"torana.v2.Message", "role"},
		{"torana.v2.Message", "thinking_signature"},
		{"torana.v2.ToolCall", "signature"},
		{"torana.v2.MessageStart", "model"},
		{"torana.v2.ToolCallRef", "signature"},
		{"torana.v2.StreamEvent", "signature_delta"},
		{"torana.v2.StreamEvent", "usage"},
		{"torana.v2.StreamEvent", "error"},
		{"torana.v2.StreamError", "code"},
		{"torana.v2.StreamError", "message"},
	}
	for _, tc := range mustHost {
		p, ok := OutboundFieldPolicy(protoreflect.FullName(tc.msg), tc.field)
		if !ok || !p.IsHostOwned() {
			t.Errorf("%s.%s must be host-owned", tc.msg, tc.field)
		}
	}
	idx, _ := OutboundFieldPolicy("torana.v2.ToolCallDelta", "index")
	if idx.Kind() != PolicyTopology {
		t.Fatal("ToolCallDelta.index must be topology")
	}
}

func TestFieldPolicyRejectsInvalidStates(t *testing.T) {
	cases := []struct {
		name string
		p    FieldPolicy
	}{
		{"zero", FieldPolicy{}},
		{"unknown kind", FieldPolicy{kind: PolicyKind(99)}},
		{"section empty", FieldPolicy{kind: PolicySection}},
		{"section topology name", FieldPolicy{kind: PolicySection, section: SectionStreamWrite}},
		{"section with delegate", FieldPolicy{kind: PolicySection, section: SectionMessagesAssistant, delegate: DelegateRequest}},
		{"host-owned with section", FieldPolicy{kind: PolicyHostOwned, section: SectionMessagesAssistant}},
		{"host-owned with delegate", FieldPolicy{kind: PolicyHostOwned, delegate: DelegateStream}},
		{"delegate unspecified", FieldPolicy{kind: PolicyDelegate}},
		{"delegate unknown", FieldPolicy{kind: PolicyDelegate, delegate: DelegateKind(99)}},
		{"delegate with section", FieldPolicy{kind: PolicyDelegate, delegate: DelegateStream, section: SectionStreamWrite}},
		{"topology wrong section", FieldPolicy{kind: PolicyTopology, section: SectionMessagesAssistant}},
		{"topology with delegate", FieldPolicy{kind: PolicyTopology, section: SectionStreamWrite, delegate: DelegateResponse}},
	}
	for _, tc := range cases {
		if err := tc.p.validate(); err == nil {
			t.Errorf("%s: expected validation error", tc.name)
		}
	}
	if err := SectionPolicy(SectionMessagesAssistant).validate(); err != nil {
		t.Fatalf("valid SectionPolicy: %v", err)
	}
	if err := HostOwnedPolicy().validate(); err != nil {
		t.Fatalf("valid HostOwnedPolicy: %v", err)
	}
	if err := TopologyPolicy().validate(); err != nil {
		t.Fatalf("valid TopologyPolicy: %v", err)
	}
	if err := DelegatePolicy(DelegateStream).validate(); err != nil {
		t.Fatalf("valid DelegatePolicy: %v", err)
	}
}

func TestOutboundPolicyAccessorsAreCopies(t *testing.T) {
	p, ok := OutboundFieldPolicy("torana.v2.ChatResponse", "usage")
	if !ok || !p.IsHostOwned() {
		t.Fatal("usage should be host-owned")
	}
	p.kind = PolicySection
	p.section = SectionMessagesAssistant
	again, _ := OutboundFieldPolicy("torana.v2.ChatResponse", "usage")
	if !again.IsHostOwned() {
		t.Fatal("mutating returned FieldPolicy must not affect registry")
	}

	names, ok := OutboundFieldNames("torana.v2.ChatResponse")
	if !ok || len(names) == 0 {
		t.Fatal("expected field names")
	}
	names[0] = "mutated"
	names2, _ := OutboundFieldNames("torana.v2.ChatResponse")
	if names2[0] == "mutated" {
		t.Fatal("OutboundFieldNames must return a copy")
	}

	targets, ok := OutboundDelegateTargets(DelegateStream)
	if !ok || len(targets) == 0 {
		t.Fatal("expected stream targets")
	}
	targets[0] = "mutated"
	targets2, _ := OutboundDelegateTargets(DelegateStream)
	if targets2[0] == "mutated" {
		t.Fatal("OutboundDelegateTargets must return a copy")
	}

	bindings := AllSignatureBindings()
	bindings[0].SignatureField = "mutated"
	bindings[0].Content[0].Field = "mutated"
	againB := AllSignatureBindings()
	if againB[0].SignatureField == "mutated" || againB[0].Content[0].Field == "mutated" {
		t.Fatal("AllSignatureBindings must return a deep copy")
	}

	cases := StreamMutationContractCases()
	cases[0].ID = "mutated"
	againC := StreamMutationContractCases()
	if againC[0].ID == "mutated" {
		t.Fatal("StreamMutationContractCases must return a copy")
	}
}

func TestSignatureBindingsPinned(t *testing.T) {
	byMsg := map[protoreflect.FullName]SignatureBinding{}
	for _, b := range AllSignatureBindings() {
		byMsg[b.Message] = b
		if err := b.validate(); err != nil {
			t.Errorf("%v", err)
		}
	}

	msg := byMsg["torana.v2.Message"]
	if msg.SignatureField != "thinking_signature" {
		t.Fatal("Message binding missing")
	}
	assertSameContent(t, msg, "thinking", "redacted_thinking")

	tc := byMsg["torana.v2.ToolCall"]
	assertSameContent(t, tc, "id", "name", "arguments_json")

	ref := byMsg["torana.v2.ToolCallRef"]
	if ref.SignatureField != "signature" {
		t.Fatal("ToolCallRef binding missing")
	}
	assertSameContent(t, ref, "id", "name")
	foundArgs := false
	for _, c := range ref.Content {
		if c.Message == "torana.v2.ToolCallDelta" && c.Field == "arguments_delta" {
			foundArgs = true
		}
	}
	if !foundArgs {
		t.Fatal("ToolCallRef.signature must bind cross-event ToolCallDelta.arguments_delta")
	}

	sig := byMsg["torana.v2.StreamEvent"]
	if sig.SignatureField != "signature_delta" {
		t.Fatal("signature_delta binding missing")
	}
	cross := 0
	for _, c := range sig.Content {
		if c.Message != "" && c.Message != sig.Message {
			cross++
		}
	}
	if cross == 0 {
		t.Fatal("signature_delta must list cross-event content refs")
	}

	p, _ := OutboundFieldPolicy("torana.v2.ToolCallRef", "signature")
	if !p.IsHostOwned() {
		t.Fatal("ToolCallRef.signature must be host-owned")
	}
	args, _ := OutboundFieldPolicy("torana.v2.ToolCallDelta", "arguments_delta")
	if args.Kind() != PolicySection {
		t.Fatal("arguments_delta must remain a content section (binding is separate)")
	}
}

func assertSameContent(t *testing.T, b SignatureBinding, fields ...string) {
	t.Helper()
	got := map[string]bool{}
	for _, c := range b.Content {
		if c.Message == "" || c.Message == b.Message {
			got[c.Field] = true
		}
	}
	for _, f := range fields {
		if !got[f] {
			t.Errorf("%s.%s missing same-message content %s", b.Message, b.SignatureField, f)
		}
	}
}

func TestStreamMutationContractCases(t *testing.T) {
	want := map[string]StreamMutationCase{
		"change-only-tool-call-delta-index":                {ID: "change-only-tool-call-delta-index", NeedTopology: true},
		"change-content-block-start-tool-call-signature":   {ID: "change-content-block-start-tool-call-signature", Reject: true},
		"identical-usage-re-emit":                          {ID: "identical-usage-re-emit", IdenticalPassOK: true},
		"identical-message-start-re-emit":                  {ID: "identical-message-start-re-emit", IdenticalPassOK: true},
		"identical-signature-delta-re-emit":                {ID: "identical-signature-delta-re-emit", IdenticalPassOK: true},
		"identical-text-delta-re-emit":                     {ID: "identical-text-delta-re-emit", IdenticalPassOK: true},
		"unchanged-message-stop-one-for-one":               {ID: "unchanged-message-stop-one-for-one", IdenticalPassOK: true},
		"unchanged-content-block-start-one-for-one":        {ID: "unchanged-content-block-start-one-for-one", IdenticalPassOK: true},
		"suppress-usage":                                   {ID: "suppress-usage", Reject: true},
		"one-for-one-text-delta-rewrite":                   {ID: "one-for-one-text-delta-rewrite", NeedAssistant: true},
		"suppress-text-delta":                              {ID: "suppress-text-delta", NeedTopology: true, NeedAssistant: true},
		"change-tool-call-delta-args-with-start-signature": {ID: "change-tool-call-delta-args-with-start-signature", Reject: true},
		"forge-stream-error":                               {ID: "forge-stream-error", Reject: true},
	}
	got := StreamMutationContractCases()
	if len(got) != len(want) {
		t.Fatalf("got %d cases, want %d", len(got), len(want))
	}
	for _, c := range got {
		w, ok := want[c.ID]
		if !ok {
			t.Errorf("unexpected case %q", c.ID)
			continue
		}
		if c != w {
			t.Errorf("case %q: got %+v, want %+v", c.ID, c, w)
		}
	}
	for id := range want {
		found := false
		for _, c := range got {
			if c.ID == id {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing case %q", id)
		}
	}

	idx, _ := OutboundFieldPolicy("torana.v2.ToolCallDelta", "index")
	if idx.Kind() != PolicyTopology {
		t.Fatal("index change case requires topology policy on ToolCallDelta.index")
	}
	sig, _ := OutboundFieldPolicy("torana.v2.ToolCallRef", "signature")
	if !sig.IsHostOwned() {
		t.Fatal("signature change case requires host-owned ToolCallRef.signature")
	}
	errEv, _ := OutboundFieldPolicy("torana.v2.StreamEvent", "error")
	if !errEv.IsHostOwned() {
		t.Fatal("forge-stream-error case requires host-owned StreamEvent.error")
	}
}
