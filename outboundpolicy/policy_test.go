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
	err := validateChatResponsePolicies(bad)
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

	think := byMsg["torana.v2.RequestThinkingBlock/signature"]
	if think.Domain != SignatureDomainRequest || think.Message != "torana.v2.RequestThinkingBlock" {
		t.Fatal("RequestThinkingBlock.signature must remain a request-domain binding")
	}
	var sawThinking bool
	for _, c := range think.Content {
		if c.Field == "text" {
			sawThinking = true
		}
	}
	if !sawThinking {
		t.Fatal("thinking signature must cover its own text")
	}
	if _, ok := OutboundFieldPolicy("torana.v2.RequestThinkingBlock", "signature"); ok {
		t.Fatal("request block must not re-enter the outbound field registry")
	}

	trail := byMsg["torana.v2.RequestTrailingSignatureBlock/signature"]
	if trail.Domain != SignatureDomainRequest || trail.Message != "torana.v2.RequestTrailingSignatureBlock" {
		t.Fatal("RequestTrailingSignatureBlock.signature must remain a request-domain binding")
	}
	var sawTrailText, sawTrailThinking, sawTrailMeta bool
	for _, c := range trail.Content {
		switch {
		case c.Scope == SignatureScopeSameMessage && c.Field == "part_metadata_json":
			sawTrailMeta = true
		case c.Scope != SignatureScopeTrailingStandalone:
			t.Fatalf("trailing_signature unexpected scope %v for %+v", c.Scope, c)
		case c.Message == "torana.v2.RequestTextBlock" && c.Field == "text":
			sawTrailText = true
		case c.Message == "torana.v2.RequestThinkingBlock" && c.Field == "text":
			sawTrailThinking = true
		}
	}
	if !sawTrailMeta {
		t.Fatal("trailing signature must SameMessage-bind its own part_metadata_json")
	}
	if !sawTrailText || !sawTrailThinking {
		t.Fatal("trailing_signature must bind the preceding closed text and thinking blocks")
	}
	if _, ok := OutboundFieldPolicy("torana.v2.RequestTrailingSignatureBlock", "signature"); ok {
		t.Fatal("request block must not re-enter the outbound field registry")
	}

	cs := byMsg["torana.v2.RequestTextBlock/signature"]
	if cs.Domain != SignatureDomainRequest || cs.Message != "torana.v2.RequestTextBlock" {
		t.Fatal("RequestTextBlock.signature must remain a request-domain binding")
	}
	if len(cs.Content) != 2 {
		t.Fatalf("content signature must cover exactly two fields, got %d", len(cs.Content))
	}
	var sawText, sawMeta bool
	for _, c := range cs.Content {
		if c.Scope != SignatureScopeSameMessage || c.Message != "" {
			t.Fatalf("content signature must SameMessage-bind its own fields, got %+v", c)
		}
		switch c.Field {
		case "text":
			sawText = true
		case "part_metadata_json":
			sawMeta = true
		}
	}
	if !sawText || !sawMeta {
		t.Fatalf("content signature must bind text + part_metadata_json, got %+v", cs.Content)
	}
	if _, ok := OutboundFieldPolicy("torana.v2.RequestTextBlock", "signature"); ok {
		t.Fatal("request block must not re-enter the outbound field registry")
	}

	tu := byMsg["torana.v2.RequestToolUseBlock/signature"]
	if tu.Domain != SignatureDomainRequest || tu.Message != "torana.v2.RequestToolUseBlock" {
		t.Fatal("RequestToolUseBlock.signature must be a request-domain binding")
	}
	want := map[string]bool{"id": true, "name": true, "arguments_json": true, "part_metadata_json": true}
	if len(tu.Content) != len(want) {
		t.Fatalf("tool-use signature must cover exactly id/name/arguments_json/part_metadata_json, got %d refs", len(tu.Content))
	}
	for _, r := range tu.Content {
		if r.Scope != SignatureScopeSameMessage || r.Message != "" || !want[r.Field] {
			t.Fatalf("tool-use signature ref %+v outside the pinned set", r)
		}
	}
	if _, ok := OutboundFieldPolicy("torana.v2.RequestToolUseBlock", "signature"); ok {
		t.Fatal("request block must not re-enter the outbound field registry")
	}

	// Reflection-backed completeness: the request-visible signature tokens
	// and the declared set are exactly equal — the six block tokens, and
	// nothing else.
	descs := outboundDescriptors()
	tokens := map[string]bool{}
	for _, f := range requestSignatureTokenFields(descs) {
		tokens[f] = true
	}
	for _, key := range []string{
		"torana.v2.RequestThinkingBlock/signature",
		"torana.v2.RequestTextBlock/signature",
		"torana.v2.RequestToolUseBlock/signature",
		"torana.v2.RequestUnknownBlock/signature",
		"torana.v2.RequestToolResultBlock/signature",
		"torana.v2.RequestTrailingSignatureBlock/signature",
	} {
		if !tokens[key] {
			t.Fatalf("reflection inventory missing request token %s", key)
		}
		delete(tokens, key)
	}
	if len(tokens) != 0 {
		t.Fatalf("unexpected request-visible signature tokens: %v", keysOf(tokens))
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
		if b.Message == "torana.v2.RequestThinkingBlock" && b.SignatureField == "signature" {
			binding = b
			found = true
			break
		}
	}
	if !found {
		t.Fatal("the ABI redesign must not drop the thinking-block signature binding")
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
		if b.Message == "torana.v2.RequestTextBlock" && b.SignatureField == "signature" {
			binding = b
			found = true
			break
		}
	}
	if !found {
		t.Fatal("request-domain RequestTextBlock.signature binding missing")
	}
	if binding.Domain != SignatureDomainRequest {
		t.Fatalf("content signature domain = %v, want request", binding.Domain)
	}

	// The verifier's boundContentChanged is computed over the binding's declared
	// content refs; for the text-block signature that must be the block's own
	// text AND its provider part metadata (the complete signed Part).
	if len(binding.Content) != 2 {
		t.Fatalf("content signature must declare exactly two content refs, got %d", len(binding.Content))
	}
	var sawText, sawMeta bool
	for _, ref := range binding.Content {
		if ref.Scope != SignatureScopeSameMessage || ref.Message != "" {
			t.Fatalf("content signature must SameMessage-bind its own fields, got %+v", ref)
		}
		switch ref.Field {
		case "text":
			sawText = true
		case "part_metadata_json":
			sawMeta = true
		}
	}
	if !sawText || !sawMeta {
		t.Fatalf("content signature must bind text + part_metadata_json, got %+v", binding.Content)
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

func keysOf(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
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
			Message: "torana.v2.RequestThinkingBlock", SignatureField: "signature",
			Content: []SignatureContentRef{{Scope: SignatureScopeSameMessage, Field: "text"}},
		},
		// Request-domain SameMessage is pinned to exactly the thinking-block
		// and text-block signature contracts: a corrupted registry must not
		// be able to relabel an ordinary string field (text) as the opaque
		// thinking token and still pass the host's startup Validate() check.
		{
			Domain: SignatureDomainRequest, Message: "torana.v2.RequestThinkingBlock", SignatureField: "text",
			Content: []SignatureContentRef{{Scope: SignatureScopeSameMessage, Field: "text"}},
		},
		{
			Domain: SignatureDomainRequest, Message: "torana.v2.ToolCall", SignatureField: "signature",
			Content: []SignatureContentRef{{Scope: SignatureScopeSameMessage, Field: "name"}},
		},
		// A relabeled ordinary field (role on a different block message) must
		// still fail shape validation.
		{
			Domain: SignatureDomainRequest, Message: "torana.v2.RequestTextBlock", SignatureField: "role",
			Content: []SignatureContentRef{{Scope: SignatureScopeSameMessage, Field: "text"}},
		},
		// Request-domain TrailingStandalone is pinned to exactly
		// RequestTrailingSignatureBlock.signature with the two covered block
		// kinds: a corrupted registry must not be able to relabel an ordinary
		// string field (text) as the opaque token and still pass the host's
		// startup Validate() check.
		{
			Domain: SignatureDomainRequest, Message: "torana.v2.RequestTextBlock", SignatureField: "text",
			Content: []SignatureContentRef{{Scope: SignatureScopeTrailingStandalone, Field: "text"}},
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
		// thinking/text pair, changing scope, dropping or duplicating a
		// covered field, or naming a different message all fail Validate()'s
		// shape check — edge startup must not report a coherent table when the
		// verifier would misclassify content changes as intact.
		{ // rebind: the thinking token covering a different field
			Domain: SignatureDomainRequest, Message: "torana.v2.RequestThinkingBlock", SignatureField: "signature",
			Content: []SignatureContentRef{{Scope: SignatureScopeSameMessage, Field: "data"}},
		},
		{ // swapped thinking/text pair: both field names individually valid
			Domain: SignatureDomainRequest, Message: "torana.v2.RequestThinkingBlock", SignatureField: "signature",
			Content: []SignatureContentRef{
				{Scope: SignatureScopeSameMessage, Field: "data"},
				{Scope: SignatureScopeSameMessage, Field: "text"},
			},
		},
		{ // swapped pair, other side
			Domain: SignatureDomainRequest, Message: "torana.v2.RequestTextBlock", SignatureField: "signature",
			Content: []SignatureContentRef{{Scope: SignatureScopeSameMessage, Field: "text"},
				{Scope: SignatureScopeSameMessage, Field: "text"}},
		},
		{ // wrong scope for the token
			Domain: SignatureDomainRequest, Message: "torana.v2.RequestTrailingSignatureBlock", SignatureField: "signature",
			Content: []SignatureContentRef{
				{Scope: SignatureScopeSameMessage, Field: "text"},
				{Scope: SignatureScopeSameMessage, Field: "text"},
			},
		},
		{ // partial trailing set (missing the thinking block)
			Domain: SignatureDomainRequest, Message: "torana.v2.RequestTrailingSignatureBlock", SignatureField: "signature",
			Content: []SignatureContentRef{{Scope: SignatureScopeTrailingStandalone, Message: "torana.v2.RequestTextBlock", Field: "text"}},
		},
		{ // duplicated covered field
			Domain: SignatureDomainRequest, Message: "torana.v2.RequestThinkingBlock", SignatureField: "signature",
			Content: []SignatureContentRef{
				{Scope: SignatureScopeSameMessage, Field: "text"},
				{Scope: SignatureScopeSameMessage, Field: "text"},
			},
		},
		{ // cross-message ref on a SameMessage request binding
			Domain: SignatureDomainRequest, Message: "torana.v2.RequestThinkingBlock", SignatureField: "signature",
			Content: []SignatureContentRef{
				{Scope: SignatureScopeSameMessage, Field: "text"},
				{Scope: SignatureScopeSameMessage, Message: "torana.v2.ToolCall", Field: "name"},
			},
		},
		// The tool-use token's exact covered set is id + name +
		// arguments_json: each missing member, an extra/duplicated member,
		// a wrong scope, and a cross-message ref must all fail the shape
		// check — proving the contract, not just the happy path.
		{ // tool-use partial: missing id
			Domain: SignatureDomainRequest, Message: "torana.v2.RequestToolUseBlock", SignatureField: "signature",
			Content: []SignatureContentRef{
				{Scope: SignatureScopeSameMessage, Field: "name"},
				{Scope: SignatureScopeSameMessage, Field: "arguments_json"},
			},
		},
		{ // tool-use partial: missing name
			Domain: SignatureDomainRequest, Message: "torana.v2.RequestToolUseBlock", SignatureField: "signature",
			Content: []SignatureContentRef{
				{Scope: SignatureScopeSameMessage, Field: "id"},
				{Scope: SignatureScopeSameMessage, Field: "arguments_json"},
			},
		},
		{ // tool-use partial: missing arguments_json
			Domain: SignatureDomainRequest, Message: "torana.v2.RequestToolUseBlock", SignatureField: "signature",
			Content: []SignatureContentRef{
				{Scope: SignatureScopeSameMessage, Field: "id"},
				{Scope: SignatureScopeSameMessage, Field: "name"},
			},
		},
		{ // tool-use extra member
			Domain: SignatureDomainRequest, Message: "torana.v2.RequestToolUseBlock", SignatureField: "signature",
			Content: []SignatureContentRef{
				{Scope: SignatureScopeSameMessage, Field: "id"},
				{Scope: SignatureScopeSameMessage, Field: "name"},
				{Scope: SignatureScopeSameMessage, Field: "arguments_json"},
				{Scope: SignatureScopeSameMessage, Field: "tool_name"},
			},
		},
		{ // tool-use duplicated member
			Domain: SignatureDomainRequest, Message: "torana.v2.RequestToolUseBlock", SignatureField: "signature",
			Content: []SignatureContentRef{
				{Scope: SignatureScopeSameMessage, Field: "id"},
				{Scope: SignatureScopeSameMessage, Field: "id"},
				{Scope: SignatureScopeSameMessage, Field: "name"},
				{Scope: SignatureScopeSameMessage, Field: "arguments_json"},
			},
		},
		{ // tool-use wrong scope
			Domain: SignatureDomainRequest, Message: "torana.v2.RequestToolUseBlock", SignatureField: "signature",
			Content: []SignatureContentRef{
				{Scope: SignatureScopeTrailingStandalone, Message: "torana.v2.RequestToolUseBlock", Field: "id"},
				{Scope: SignatureScopeTrailingStandalone, Message: "torana.v2.RequestToolUseBlock", Field: "name"},
				{Scope: SignatureScopeTrailingStandalone, Message: "torana.v2.RequestToolUseBlock", Field: "arguments_json"},
			},
		},
		{ // tool-use cross-message ref
			Domain: SignatureDomainRequest, Message: "torana.v2.RequestToolUseBlock", SignatureField: "signature",
			Content: []SignatureContentRef{
				{Scope: SignatureScopeSameMessage, Field: "id"},
				{Scope: SignatureScopeSameMessage, Field: "name"},
				{Scope: SignatureScopeSameMessage, Message: "torana.v2.ToolCall", Field: "arguments_json"},
			},
		},
	}
	for i, b := range bad {
		if err := b.validateShape(); err == nil {
			t.Errorf("case %d: expected shape error: %+v", i, b)
		}
	}
}

// Completeness: removing ANY required request-domain binding must fail the
// startup proof — Validate() must prove every pinned token has a declaration,
// not just that the declarations that exist are well-formed. The token list
// is DERIVED from the reflection inventory (every request-visible "signature"
// field), so a token added to the descriptors cannot silently skip this check
// by being missing from a hard-coded list here.
func TestRequestBindingCompletenessRequiresEveryToken(t *testing.T) {
	descs := outboundDescriptors()
	tokens := requestSignatureTokenFields(descs)
	if len(tokens) != 6 {
		t.Fatalf("expected exactly six request-visible signature tokens, got %v", tokens)
	}
	for _, token := range tokens {
		t.Run(token+" removed", func(t *testing.T) {
			bindings := make([]SignatureBinding, 0, len(signatureBindings)-1)
			for _, b := range signatureBindings {
				if b.Domain == SignatureDomainRequest && string(b.Message)+"/"+b.SignatureField == token {
					continue
				}
				bindings = append(bindings, b)
			}
			if err := validateRequestBindingCompleteness(bindings); err == nil {
				t.Fatalf("removing the %s binding must fail the completeness pass", token)
			}
		})
	}
	// An extra, unpinned request token is equally a broken registry.
	bindings := append([]SignatureBinding{}, signatureBindings...)
	bindings = append(bindings, SignatureBinding{
		Domain: SignatureDomainRequest, Message: "torana.v2.RequestTextBlock", SignatureField: "invented_signature",
		Content: []SignatureContentRef{{Scope: SignatureScopeSameMessage, Field: "text"}},
	})
	if err := validateRequestBindingCompleteness(bindings); err == nil {
		t.Fatal("an unpinned request token must fail the completeness pass")
	}
	// The real registry passes.
	if err := validateRequestBindingCompleteness(signatureBindings); err != nil {
		t.Fatalf("the real registry must pass: %v", err)
	}
}

// The pinned content sets compare as SETS: declaration order is irrelevant, so
// a registry that lists the same covered fields in a different order is still
// the same contract.
func TestRequestBindingShapeOrderIndependent(t *testing.T) {
	good := []SignatureBinding{
		{
			Domain: SignatureDomainRequest, Message: "torana.v2.RequestTextBlock", SignatureField: "signature",
			Content: []SignatureContentRef{
				{Scope: SignatureScopeSameMessage, Field: "part_metadata_json"},
				{Scope: SignatureScopeSameMessage, Field: "text"},
			},
		},
		{
			Domain: SignatureDomainRequest, Message: "torana.v2.RequestTrailingSignatureBlock", SignatureField: "signature",
			Content: []SignatureContentRef{
				{Scope: SignatureScopeSameMessage, Field: "part_metadata_json"},
				{Scope: SignatureScopeTrailingStandalone, Message: "torana.v2.RequestThinkingBlock", Field: "text"},
				{Scope: SignatureScopeTrailingStandalone, Message: "torana.v2.RequestTextBlock", Field: "text"},
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
	if containerPolicy().Kind() != PolicyContainer {
		t.Fatal("container policy must report PolicyContainer")
	}
	old := outboundMessageFieldPolicies["torana.v2.ChatResponse"]
	bad := map[string]FieldPolicy{}
	for k, v := range old {
		bad[k] = v
	}
	bad["model"] = containerPolicy()
	if err := validateChatResponsePolicies(bad); err == nil {
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
	err := validateChatResponsePolicies(bad)
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
	if err := validateChatResponsePolicies(bad); err == nil {
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
	if err := validateChatResponsePolicies(bad); err == nil {
		t.Fatal("expected PolicyDelegate-on-scalar to fail Validate")
	}
}

func validateChatResponsePolicies(fields map[string]FieldPolicy) error {
	policies := make(map[protoreflect.FullName]map[string]FieldPolicy, len(outboundMessageFieldPolicies))
	for message, original := range outboundMessageFieldPolicies {
		policies[message] = original
	}
	policies["torana.v2.ChatResponse"] = fields
	descs := outboundDescriptors()
	return validateMessageCompleteness(
		descs["torana.v2.ChatResponse"],
		map[protoreflect.FullName]bool{},
		descs,
		policies,
	)
}
