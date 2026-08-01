package outboundpolicy

import (
	"strings"
	"testing"

	plugin_sdk "github.com/torana-edge/torana-plugin-sdk"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestOutboundWriteGrantsAreRequestable(t *testing.T) {
	if !plugin_sdk.IsWritePermission(string(plugin_sdk.SectionStreamWrite)) {
		t.Fatal("ir.stream.write must be in WritePermissions")
	}
	if !plugin_sdk.IsPermission(string(plugin_sdk.SectionStreamWrite)) {
		t.Fatal("ir.stream.write must be in Permissions")
	}
	if !plugin_sdk.IsPermission("env.set_identity") {
		t.Fatal("env.set_identity must be in Permissions")
	}
	if StreamTopologySection() != plugin_sdk.SectionStreamWrite {
		t.Fatal("StreamTopologySection must be ir.stream.write")
	}
}

func TestRecursiveOutboundInventory(t *testing.T) {
	// Validate is the production completeness guard hosts call at startup.
	if err := Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateFailsWhenFieldPolicyMissing(t *testing.T) {
	old := chatResponseFieldPolicies
	// Copy without finish_reason — proto still has the field.
	bad := map[string]FieldPolicy{}
	for k, v := range old {
		if k == "finish_reason" {
			continue
		}
		bad[k] = v
	}
	outboundMessageFieldPolicies["torana.v2.ChatResponse"] = bad
	defer func() { outboundMessageFieldPolicies["torana.v2.ChatResponse"] = old }()
	err := Validate()
	if err == nil {
		t.Fatal("expected Validate to fail when a proto field lacks a policy")
	}
	if !strings.Contains(err.Error(), "finish_reason") {
		t.Fatalf("error should name the missing field, got %v", err)
	}
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
	// Bound signatures are host-owned in authority — a plugin can never mint
	// one — but they are NOT unconditionally immutable: clearing is the
	// prescribed response to changing the content they cover. They carry
	// PolicyBoundSignature and are asserted separately below.
	mustBoundSignature := []struct{ msg, field string }{
		{"torana.v2.ToolCall", "signature"},
		{"torana.v2.ToolCallRef", "signature"},
		{"torana.v2.StreamEvent", "signature_delta"},
	}
	for _, tc := range mustBoundSignature {
		p, ok := OutboundFieldPolicy(protoreflect.FullName(tc.msg), tc.field)
		if !ok || !p.IsBoundSignature() {
			t.Errorf("%s.%s must be PolicyBoundSignature, got kind=%v", tc.msg, tc.field, p.Kind())
		}
		if p.IsHostOwned() {
			t.Errorf("%s.%s must not be unconditionally host-owned: a verifier built "+
				"from that would reject EmitAssembledToolCall clearing the token", tc.msg, tc.field)
		}
	}

	mustHost := []struct{ msg, field string }{
		{"torana.v2.ChatResponse", "finish_reason"},
		{"torana.v2.MessageStart", "model"},
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
}

func TestNestedContainerEvaluationPins(t *testing.T) {
	// These registry pins are the data Migration B needs so
	// change-only-tool-call-delta-index does not auto-charge assistant.
	delta, _ := OutboundFieldPolicy("torana.v2.StreamEvent", "tool_call_delta")
	if !delta.IsContainer() {
		t.Fatal("tool_call_delta must be PolicyContainer (parent must not auto-charge assistant)")
	}
	idx, _ := OutboundFieldPolicy("torana.v2.ToolCallDelta", "index")
	if idx.Kind() != PolicyTopology {
		t.Fatal("ToolCallDelta.index must be topology — index-only change → topology only")
	}
	args, _ := OutboundFieldPolicy("torana.v2.ToolCallDelta", "arguments_delta")
	sec, ok := args.Section()
	if args.Kind() != PolicySection || !ok || sec != plugin_sdk.SectionMessagesAssistant {
		t.Fatal("arguments_delta must be assistant — args-only change → assistant only")
	}

	msg, _ := OutboundFieldPolicy("torana.v2.ChatResponse", "message")
	if !msg.IsContainer() {
		t.Fatal("ChatResponse.message must be PolicyContainer")
	}
	stop, _ := OutboundFieldPolicy("torana.v2.StreamEvent", "message_stop")
	if !stop.IsContainer() {
		t.Fatal("message_stop must be PolicyContainer")
	}
	tool, _ := OutboundFieldPolicy("torana.v2.ContentBlockStart", "tool_call")
	if !tool.IsContainer() {
		t.Fatal("ContentBlockStart.tool_call must be Container so id/name changes do not charge parent topology")
	}
	id, _ := OutboundFieldPolicy("torana.v2.ToolCallRef", "id")
	if id.Kind() != PolicySection {
		t.Fatal("ToolCallRef.id must be assistant section")
	}

	// Presence/oneof change of content_block_start still carries topology on the
	// variant; same presence recurses without charging that parent (package rule).
	cbs, _ := OutboundFieldPolicy("torana.v2.StreamEvent", "content_block_start")
	if cbs.Kind() != PolicyTopology {
		t.Fatal("content_block_start variant remains Topology for presence/kind change")
	}
	textArm, _ := OutboundFieldPolicy("torana.v2.ContentBlockStart", "text")
	if textArm.Kind() != PolicyTopology {
		t.Fatal("ContentBlockStart.text oneof arm is Topology for variant switches")
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
		{"section topology name", FieldPolicy{kind: PolicySection, section: plugin_sdk.SectionStreamWrite}},
		{"section with delegate", FieldPolicy{kind: PolicySection, section: plugin_sdk.SectionMessagesAssistant, delegate: DelegateRequest}},
		{"host-owned with section", FieldPolicy{kind: PolicyHostOwned, section: plugin_sdk.SectionMessagesAssistant}},
		{"host-owned with delegate", FieldPolicy{kind: PolicyHostOwned, delegate: DelegateStream}},
		{"delegate unspecified", FieldPolicy{kind: PolicyDelegate}},
		{"delegate unknown", FieldPolicy{kind: PolicyDelegate, delegate: DelegateKind(99)}},
		{"delegate with section", FieldPolicy{kind: PolicyDelegate, delegate: DelegateStream, section: plugin_sdk.SectionStreamWrite}},
		{"topology wrong section", FieldPolicy{kind: PolicyTopology, section: plugin_sdk.SectionMessagesAssistant}},
		{"topology with delegate", FieldPolicy{kind: PolicyTopology, section: plugin_sdk.SectionStreamWrite, delegate: DelegateResponse}},
		{"container with section", FieldPolicy{kind: PolicyContainer, section: plugin_sdk.SectionMessagesAssistant}},
		{"container with delegate", FieldPolicy{kind: PolicyContainer, delegate: DelegateStream}},
		{"fixed container with section", FieldPolicy{kind: PolicyFixedContainer, section: plugin_sdk.SectionMessagesAssistant}},
		{"fixed container with delegate", FieldPolicy{kind: PolicyFixedContainer, delegate: DelegateStream}},
	}
	for _, tc := range cases {
		if err := tc.p.validate(); err == nil {
			t.Errorf("%s: expected validation error", tc.name)
		}
	}
	for _, p := range []FieldPolicy{
		sectionPolicy(plugin_sdk.SectionMessagesAssistant),
		hostOwnedPolicy(),
		topologyPolicy(),
		delegatePolicy(DelegateStream),
		containerPolicy(),
		fixedContainerPolicy(),
	} {
		if err := p.validate(); err != nil {
			t.Fatalf("valid policy rejected: %v", err)
		}
	}
}

func TestOutboundPolicyAccessorsAreCopies(t *testing.T) {
	p, ok := OutboundFieldPolicy("torana.v2.ChatResponse", "usage")
	if !ok || !p.IsHostOwned() {
		t.Fatal("usage should be host-owned")
	}
	p.kind = PolicySection
	p.section = plugin_sdk.SectionMessagesAssistant
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
	bindings[0].Content[0].Scope = SignatureScopeUnspecified
	againB := AllSignatureBindings()
	if againB[0].SignatureField == "mutated" || againB[0].Content[0].Field == "mutated" {
		t.Fatal("AllSignatureBindings must return a deep copy")
	}
}

func TestSignatureBindingsPinned(t *testing.T) {
	byMsg := map[string]SignatureBinding{}
	for _, b := range AllSignatureBindings() {
		byMsg[string(b.Message)+"/"+b.SignatureField] = b
		if err := b.validateShape(); err != nil {
			t.Errorf("shape: %v", err)
		}
	}
	if err := Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	msg := byMsg["torana.v2.Message/thinking_signature"]
	if msg.Domain != SignatureDomainRequest || msg.SignatureField != "thinking_signature" {
		t.Fatal("Message.thinking_signature must remain a request-domain binding")
	}
	var sawThinking, sawRedacted bool
	for _, c := range msg.Content {
		if c.Field == "thinking" {
			sawThinking = true
		}
		if c.Field == "redacted_thinking" {
			sawRedacted = true
		}
	}
	if !sawThinking || !sawRedacted {
		t.Fatal("thinking_signature must cover thinking and redacted_thinking")
	}
	if _, ok := OutboundFieldPolicy("torana.v2.Message", "thinking_signature"); ok {
		t.Fatal("request Message must not re-enter the outbound field registry")
	}

	trail := byMsg["torana.v2.Message/trailing_signature"]
	if trail.Domain != SignatureDomainRequest || trail.SignatureField != "trailing_signature" {
		t.Fatal("Message.trailing_signature must remain a request-domain binding")
	}
	var sawTrailThinking, sawTrailContent bool
	for _, c := range trail.Content {
		if c.Scope != SignatureScopeTrailingStandalone {
			t.Fatalf("trailing_signature unexpected scope %v", c.Scope)
		}
		if c.Message != "" {
			t.Fatal("request-domain TrailingStandalone must stay on the same message")
		}
		if c.Field == "thinking" {
			sawTrailThinking = true
		}
		if c.Field == "content" {
			sawTrailContent = true
		}
	}
	if !sawTrailThinking || !sawTrailContent {
		t.Fatal("trailing_signature must bind TrailingStandalone thinking and content")
	}
	if _, ok := OutboundFieldPolicy("torana.v2.Message", "trailing_signature"); ok {
		t.Fatal("request Message must not re-enter the outbound field registry")
	}

	cs := byMsg["torana.v2.Message/content_signature"]
	if cs.Domain != SignatureDomainRequest || cs.SignatureField != "content_signature" {
		t.Fatal("Message.content_signature must remain a request-domain binding")
	}
	if len(cs.Content) != 1 {
		t.Fatalf("content_signature must cover exactly one field, got %d", len(cs.Content))
	}
	c := cs.Content[0]
	if c.Scope != SignatureScopeSameMessage || c.Message != "" || c.Field != "content" {
		t.Fatalf("content_signature must SameMessage-bind content, got %+v", c)
	}
	if _, ok := OutboundFieldPolicy("torana.v2.Message", "content_signature"); ok {
		t.Fatal("request Message must not re-enter the outbound field registry")
	}

	ref := byMsg["torana.v2.ToolCallRef/signature"]
	var sawSameID, sawArgs bool
	for _, c := range ref.Content {
		if c.Scope == SignatureScopeSameMessage && c.Field == "id" {
			sawSameID = true
		}
		if c.Scope == SignatureScopeToolCallBlockByIndex &&
			c.Message == "torana.v2.ToolCallDelta" && c.Field == "arguments_delta" {
			sawArgs = true
		}
	}
	if !sawSameID || !sawArgs {
		t.Fatal("ToolCallRef.signature must SameMessage id/name and ToolCallBlockByIndex arguments_delta")
	}

	sig := byMsg["torana.v2.StreamEvent/signature_delta"]
	if sig.SignatureField != "signature_delta" {
		t.Fatal("signature_delta binding missing")
	}
	var sawCurrent, sawTrailing bool
	for _, c := range sig.Content {
		switch c.Scope {
		case SignatureScopeCurrentContentBlock:
			sawCurrent = true
		case SignatureScopeTrailingStandalone:
			sawTrailing = true
		default:
			t.Fatalf("signature_delta unexpected scope %v", c.Scope)
		}
		if c.Message == "torana.v2.ToolCallRef" || c.Message == "torana.v2.ToolCallDelta" {
			t.Fatal("signature_delta must not bind the tool-call path")
		}
		if c.Field != "text_delta" && c.Field != "thinking_delta" {
			t.Fatalf("unexpected signature_delta content field %s", c.Field)
		}
	}
	if !sawCurrent || !sawTrailing {
		t.Fatal("signature_delta must cover CurrentContentBlock and TrailingStandalone")
	}

	// Parallel / multi-block correlation is carried by Scope, not by flat refs.
	if SignatureScopeToolCallBlockByIndex == SignatureScopeCurrentContentBlock {
		t.Fatal("scopes must remain distinct")
	}
	if SignatureScopeTrailingStandalone == SignatureScopeCurrentContentBlock {
		t.Fatal("trailing standalone must be distinct from current block")
	}
}

func TestRequestThinkingSignatureMutationClassifies(t *testing.T) {
	var binding SignatureBinding
	found := false
	for _, b := range AllSignatureBindings() {
		if b.Message == "torana.v2.Message" && b.SignatureField == "thinking_signature" {
			binding = b
			found = true
			break
		}
	}
	if !found {
		t.Fatal("response-shape narrowing must not drop Message.thinking_signature binding")
	}
	if binding.Domain != SignatureDomainRequest {
		t.Fatalf("thinking_signature domain = %v, want request", binding.Domain)
	}

	// Request plugin rewrites assistant thinking but keeps the provider token.
	if got := ClassifySignatureMutation("tok", "tok", true); got != SignatureStale || got.Allowed() {
		t.Fatalf("intact token over mutated thinking: got %v allowed=%v", got, got.Allowed())
	}
	// Clearing when covered content changed is the prescribed response.
	if got := ClassifySignatureMutation("tok", "", true); got != SignatureCleared || !got.Allowed() {
		t.Fatalf("clear after thinking mutation: got %v allowed=%v", got, got.Allowed())
	}
	// Dropping the token without changing thinking remains forbidden.
	if got := ClassifySignatureMutation("tok", "", false); got != SignatureDropped || got.Allowed() {
		t.Fatalf("drop without content change: got %v allowed=%v", got, got.Allowed())
	}
}

// Finding-1 regression: a request plugin that rewrites Message.content while
// keeping content_signature must classify as stale. This is the binding the
// verifier computes boundContentChanged over — it iterates the binding's
// declared content refs, so the assertion that content_signature declares
// SameMessage/content is what makes the classification provably the verifier's.
func TestRequestContentSignatureMutationClassifies(t *testing.T) {
	var binding SignatureBinding
	found := false
	for _, b := range AllSignatureBindings() {
		if b.Message == "torana.v2.Message" && b.SignatureField == "content_signature" {
			binding = b
			found = true
			break
		}
	}
	if !found {
		t.Fatal("request-domain Message.content_signature binding missing")
	}
	if binding.Domain != SignatureDomainRequest {
		t.Fatalf("content_signature domain = %v, want request", binding.Domain)
	}

	// The verifier's boundContentChanged is computed over the binding's declared
	// content refs; for content_signature that must be exactly Message.content.
	if len(binding.Content) != 1 {
		t.Fatalf("content_signature must declare exactly one content ref, got %d", len(binding.Content))
	}
	ref := binding.Content[0]
	if ref.Scope != SignatureScopeSameMessage || ref.Message != "" || ref.Field != "content" {
		t.Fatalf("content_signature must SameMessage-bind content, got %+v", ref)
	}

	// Request plugin rewrites Message.content but keeps the provider token: the
	// stale case — a valid-looking provider signature over content the provider
	// never signed — and it must reject.
	if got := ClassifySignatureMutation("tok", "tok", true); got != SignatureStale || got.Allowed() {
		t.Fatalf("intact token over mutated content: got %v allowed=%v", got, got.Allowed())
	}
	// Clearing when covered content changed is the prescribed response.
	if got := ClassifySignatureMutation("tok", "", true); got != SignatureCleared || !got.Allowed() {
		t.Fatalf("clear after content mutation: got %v allowed=%v", got, got.Allowed())
	}
	// Dropping the token without changing content remains forbidden.
	if got := ClassifySignatureMutation("tok", "", false); got != SignatureDropped || got.Allowed() {
		t.Fatalf("drop without content change: got %v allowed=%v", got, got.Allowed())
	}
}

func TestSignatureBindingRejectsBadScopes(t *testing.T) {
	bad := []SignatureBinding{
		{Domain: SignatureDomainOutbound, Message: "torana.v2.ToolCall", SignatureField: "signature"},
		{
			Domain: SignatureDomainOutbound, Message: "torana.v2.ToolCall", SignatureField: "signature",
			Content: []SignatureContentRef{{Scope: SignatureScopeUnspecified, Field: "name"}},
		},
		{
			Domain: SignatureDomainOutbound, Message: "torana.v2.ToolCall", SignatureField: "signature",
			Content: []SignatureContentRef{{Scope: SignatureScopeSameMessage, Message: "torana.v2.ToolCallRef", Field: "id"}},
		},
		{
			Domain: SignatureDomainOutbound, Message: "torana.v2.ToolCall", SignatureField: "signature",
			Content: []SignatureContentRef{{Scope: SignatureScopeToolCallBlockByIndex, Message: "torana.v2.ToolCallDelta", Field: "arguments_delta"}},
		},
		{
			Domain: SignatureDomainOutbound, Message: "torana.v2.StreamEvent", SignatureField: "signature_delta",
			Content: []SignatureContentRef{{Scope: SignatureScopeCurrentContentBlock, Field: "text_delta"}},
		},
		{ // missing Domain
			Message: "torana.v2.Message", SignatureField: "thinking_signature",
			Content: []SignatureContentRef{{Scope: SignatureScopeSameMessage, Field: "thinking"}},
		},
		// Request-domain SameMessage is pinned to exactly
		// torana.v2.Message.thinking_signature and Message.content_signature: a
		// corrupted registry must not be able to relabel an ordinary string
		// field (content) as the opaque thinking token and still pass the
		// host's startup Validate() check.
		{
			Domain: SignatureDomainRequest, Message: "torana.v2.Message", SignatureField: "content",
			Content: []SignatureContentRef{{Scope: SignatureScopeSameMessage, Field: "thinking"}},
		},
		{
			Domain: SignatureDomainRequest, Message: "torana.v2.ToolCall", SignatureField: "thinking_signature",
			Content: []SignatureContentRef{{Scope: SignatureScopeSameMessage, Field: "name"}},
		},
		// The content_signature slot widens the pin, not the field set: a third
		// relabeled ordinary field (role) must still fail shape validation even
		// though its covered field (content) is the very field content_signature
		// is supposed to bind.
		{
			Domain: SignatureDomainRequest, Message: "torana.v2.Message", SignatureField: "role",
			Content: []SignatureContentRef{{Scope: SignatureScopeSameMessage, Field: "content"}},
		},
		// Request-domain TrailingStandalone is pinned to exactly
		// torana.v2.Message.trailing_signature: a corrupted registry must not
		// be able to relabel an ordinary string field (content) as the opaque
		// token and still pass the host's startup Validate() check.
		{
			Domain: SignatureDomainRequest, Message: "torana.v2.Message", SignatureField: "content",
			Content: []SignatureContentRef{{Scope: SignatureScopeTrailingStandalone, Field: "thinking"}},
		},
		{
			Domain: SignatureDomainRequest, Message: "torana.v2.ToolCall", SignatureField: "trailing_signature",
			Content: []SignatureContentRef{{Scope: SignatureScopeTrailingStandalone, Field: "name"}},
		},
		{
			Domain: SignatureDomainRequest, Message: "torana.v2.ToolCall", SignatureField: "trailing_signature",
			Content: []SignatureContentRef{{Scope: SignatureScopeTrailingStandalone, Message: "torana.v2.ToolCall", Field: "name"}},
		},
		// The complete request-domain contracts are pinned, not just the field
		// allowlist: rebinding a token to different content, swapping the
		// thinking/content pair, changing scope, dropping or duplicating a
		// covered field, or naming a different message all fail Validate()'s
		// shape check — edge startup must not report a coherent table when the
		// verifier would misclassify content changes as intact.
		{
			Domain: SignatureDomainRequest, Message: "torana.v2.Message", SignatureField: "content_signature",
			Content: []SignatureContentRef{{Scope: SignatureScopeSameMessage, Field: "thinking"}},
		},
		{ // swapped thinking/content pair: both field names individually valid
			Domain: SignatureDomainRequest, Message: "torana.v2.Message", SignatureField: "thinking_signature",
			Content: []SignatureContentRef{
				{Scope: SignatureScopeSameMessage, Field: "content"},
				{Scope: SignatureScopeSameMessage, Field: "redacted_thinking"},
			},
		},
		{ // swapped pair, other side
			Domain: SignatureDomainRequest, Message: "torana.v2.Message", SignatureField: "content_signature",
			Content: []SignatureContentRef{{Scope: SignatureScopeSameMessage, Field: "content"},
				{Scope: SignatureScopeSameMessage, Field: "thinking"}},
		},
		{ // wrong scope for the token
			Domain: SignatureDomainRequest, Message: "torana.v2.Message", SignatureField: "trailing_signature",
			Content: []SignatureContentRef{
				{Scope: SignatureScopeSameMessage, Field: "thinking"},
				{Scope: SignatureScopeSameMessage, Field: "content"},
			},
		},
		{ // partial trailing set (missing content)
			Domain: SignatureDomainRequest, Message: "torana.v2.Message", SignatureField: "trailing_signature",
			Content: []SignatureContentRef{{Scope: SignatureScopeTrailingStandalone, Field: "thinking"}},
		},
		{ // duplicated covered field
			Domain: SignatureDomainRequest, Message: "torana.v2.Message", SignatureField: "content_signature",
			Content: []SignatureContentRef{
				{Scope: SignatureScopeSameMessage, Field: "content"},
				{Scope: SignatureScopeSameMessage, Field: "content"},
			},
		},
		{ // cross-message ref on a request binding
			Domain: SignatureDomainRequest, Message: "torana.v2.Message", SignatureField: "trailing_signature",
			Content: []SignatureContentRef{
				{Scope: SignatureScopeTrailingStandalone, Field: "thinking"},
				{Scope: SignatureScopeTrailingStandalone, Message: "torana.v2.ToolCall", Field: "name"},
			},
		},
	}
	for i, b := range bad {
		if err := b.validateShape(); err == nil {
			t.Errorf("case %d: expected shape error: %+v", i, b)
		}
	}
}

// The pinned content sets compare as SETS: declaration order is irrelevant, so
// a registry that lists the same covered fields in a different order is still
// the same contract.
func TestRequestBindingShapeOrderIndependent(t *testing.T) {
	good := []SignatureBinding{
		{
			Domain: SignatureDomainRequest, Message: "torana.v2.Message", SignatureField: "thinking_signature",
			Content: []SignatureContentRef{
				{Scope: SignatureScopeSameMessage, Field: "redacted_thinking"},
				{Scope: SignatureScopeSameMessage, Field: "thinking"},
			},
		},
		{
			Domain: SignatureDomainRequest, Message: "torana.v2.Message", SignatureField: "trailing_signature",
			Content: []SignatureContentRef{
				{Scope: SignatureScopeTrailingStandalone, Field: "content"},
				{Scope: SignatureScopeTrailingStandalone, Field: "thinking"},
			},
		},
	}
	for i, b := range good {
		if err := b.validateShape(); err != nil {
			t.Errorf("case %d: reordered refs must pass: %v", i, err)
		}
	}
}

func TestValidateAgainstProto(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyContainerRejectedOnScalar(t *testing.T) {
	// Simulate a bad registry entry: PolicyContainer on a scalar would be
	// caught by Validate when wired into outboundMessageFieldPolicies.
	descs := outboundDescriptors()
	fd := descs["torana.v2.ChatResponse"].Fields().ByName("model")
	if fd == nil || fd.Kind() == protoreflect.MessageKind {
		t.Fatal("expected scalar model field")
	}
	// Direct invariant the inventory walk enforces:
	if containerPolicy().Kind() == PolicyContainer && fd.Kind() != protoreflect.MessageKind {
		// ok — this is the condition Validate checks
	}
	bad := map[string]FieldPolicy{"model": containerPolicy()}
	old := outboundMessageFieldPolicies["torana.v2.ChatResponse"]
	outboundMessageFieldPolicies["torana.v2.ChatResponse"] = bad
	defer func() { outboundMessageFieldPolicies["torana.v2.ChatResponse"] = old }()
	if err := Validate(); err == nil {
		t.Fatal("expected PolicyContainer-on-scalar to fail Validate")
	}
}

func TestPolicyFixedContainerRejectedOnSingularMessage(t *testing.T) {
	old := outboundMessageFieldPolicies["torana.v2.ChatResponse"]
	bad := map[string]FieldPolicy{}
	for k, v := range old {
		bad[k] = v
	}
	bad["message"] = fixedContainerPolicy()
	outboundMessageFieldPolicies["torana.v2.ChatResponse"] = bad
	defer func() { outboundMessageFieldPolicies["torana.v2.ChatResponse"] = old }()
	err := Validate()
	if err == nil {
		t.Fatal("expected PolicyFixedContainer on singular message to fail Validate")
	}
	if !strings.Contains(err.Error(), "repeated") {
		t.Fatalf("error should mention repeated, got %v", err)
	}
}

func TestPolicyFixedContainerRejectedOnScalar(t *testing.T) {
	old := outboundMessageFieldPolicies["torana.v2.ChatResponse"]
	bad := map[string]FieldPolicy{}
	for k, v := range old {
		bad[k] = v
	}
	bad["model"] = fixedContainerPolicy()
	outboundMessageFieldPolicies["torana.v2.ChatResponse"] = bad
	defer func() { outboundMessageFieldPolicies["torana.v2.ChatResponse"] = old }()
	if err := Validate(); err == nil {
		t.Fatal("expected PolicyFixedContainer-on-scalar to fail Validate")
	}
}

func TestPolicyDelegateRejectedOnScalar(t *testing.T) {
	old := outboundMessageFieldPolicies["torana.v2.ChatResponse"]
	bad := map[string]FieldPolicy{}
	for k, v := range old {
		bad[k] = v
	}
	bad["model"] = delegatePolicy(DelegateStream)
	outboundMessageFieldPolicies["torana.v2.ChatResponse"] = bad
	defer func() { outboundMessageFieldPolicies["torana.v2.ChatResponse"] = old }()
	if err := Validate(); err == nil {
		t.Fatal("expected PolicyDelegate-on-scalar to fail Validate")
	}
}
