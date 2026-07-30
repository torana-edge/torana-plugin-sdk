package plugin_sdk

import (
	"fmt"
	"sort"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// Cross-hook mutation policy: declarative vocabulary for what a plugin may
// CHANGE on the response and stream paths.
//
// Request-path write grants (capabilities_write.go) already cover ChatRequest.
// v2 also lets a plugin replace a ChatResponse and emit/suppress StreamEvents.
//
// Content grants reuse ir.* across hooks where the authority is the same
// (assistant text/thinking/tool arguments). Observed provider/host facts and
// opaque binding tokens are host-owned — request ir.model.write must not
// authorise lying about which model answered, and ir.params.write must not
// authorise rewriting opaque provider response blobs. StreamError is likewise
// host-owned: a plugin must not forge provider-looking upstream failures
// (suppress under topology, trap under failure_mode, or use attributed
// verdicts instead).
//
// Stream topology is an ADDITIONAL dimension, not an alternative one:
//
//	required = topology grant (when cardinality/order/boundaries/action change)
//	         ∪ every semantic section changed, removed, or added
//	any changed/removed/added host-owned fact = reject
//
// Host-owned means immutable under plugin mutation: an identical re-emit or
// pass-through needs no grant. Suppressing, forging, or altering a host-owned
// fact is forbidden. Enforcement (recursive field diff + fingerprinting) is
// Migration B and must not land on the per-event stream path without a
// stream-specific benchmark covering one-for-one TextDelta/ToolCallDelta and
// fan-out/suppress cases. This file is vocabulary and inventory only — no
// approximate public verifier helpers.
//
// Registries are package-private. Public accessors return values/clones so
// importers cannot mutate the authority table process-wide.

const (
	// SectionStreamWrite is the topology grant: Suppress, fan-out, event-kind
	// change, and content-block boundary/index changes. It is additive to
	// semantic content grants and never alone authorises altering host-owned
	// facts.
	SectionStreamWrite WriteSection = "ir.stream.write"
)

// PolicyKind classifies a field or HookResult action for the inventory.
type PolicyKind int

const (
	// PolicyUnspecified is the zero value and is always invalid.
	PolicyUnspecified PolicyKind = iota
	// PolicySection maps the field to a content write grant (never topology).
	PolicySection
	// PolicyHostOwned marks an immutable protobuf fact. Unchanged values may
	// be re-emitted; changed/removed/added values reject.
	PolicyHostOwned
	// PolicyDelegate hands the nested value to another verifier (request,
	// response, stream, HTTP, tick). Not a field grant and not host-owned.
	PolicyDelegate
	// PolicyTopology maps to ir.stream.write when this aspect actually changes
	// (cardinality, order, boundaries, indexes, event kind).
	PolicyTopology
)

// DelegateKind names the verifier a PolicyDelegate hands off to.
type DelegateKind int

const (
	DelegateUnspecified DelegateKind = iota
	DelegateRequest                  // capabilities_write.go
	DelegateResponse                 // ChatResponse field policies
	DelegateStream                   // recursive stream field diff + topology
	DelegateHTTP                     // ServeHTTP result policy (deferred)
	DelegateTick                     // TickOutcome observational policy
)

func (d DelegateKind) valid() bool {
	switch d {
	case DelegateRequest, DelegateResponse, DelegateStream, DelegateHTTP, DelegateTick:
		return true
	}
	return false
}

// FieldPolicy is one field's (or action's) declarative classification.
// Construct only via SectionPolicy / HostOwnedPolicy / TopologyPolicy /
// DelegatePolicy — zero values and mixed fields are invalid.
type FieldPolicy struct {
	kind     PolicyKind
	section  WriteSection
	delegate DelegateKind
}

// SectionPolicy maps a field to a content write grant. s must be a non-empty
// write permission other than ir.stream.write (use TopologyPolicy for that).
func SectionPolicy(s WriteSection) FieldPolicy {
	return FieldPolicy{kind: PolicySection, section: s}
}

// HostOwnedPolicy marks an immutable host/provider fact.
func HostOwnedPolicy() FieldPolicy {
	return FieldPolicy{kind: PolicyHostOwned}
}

// TopologyPolicy maps a field to the additive ir.stream.write grant.
func TopologyPolicy() FieldPolicy {
	return FieldPolicy{kind: PolicyTopology, section: SectionStreamWrite}
}

// DelegatePolicy hands nested verification to a named verifier.
func DelegatePolicy(d DelegateKind) FieldPolicy {
	return FieldPolicy{kind: PolicyDelegate, delegate: d}
}

func (p FieldPolicy) Kind() PolicyKind { return p.kind }

func (p FieldPolicy) IsHostOwned() bool { return p.kind == PolicyHostOwned }

// Section returns the content or topology section when Kind is PolicySection
// or PolicyTopology.
func (p FieldPolicy) Section() (WriteSection, bool) {
	switch p.kind {
	case PolicySection, PolicyTopology:
		return p.section, true
	}
	return "", false
}

// Delegate returns the verifier when Kind is PolicyDelegate.
func (p FieldPolicy) Delegate() (DelegateKind, bool) {
	if p.kind == PolicyDelegate {
		return p.delegate, true
	}
	return DelegateUnspecified, false
}

func (p FieldPolicy) validate() error {
	switch p.kind {
	case PolicyUnspecified:
		return fmt.Errorf("PolicyUnspecified is invalid")
	case PolicySection:
		if p.section == "" || p.section == SectionStreamWrite {
			return fmt.Errorf("PolicySection requires a non-topology write section")
		}
		if !IsWritePermission(string(p.section)) {
			return fmt.Errorf("PolicySection %q is not a write grant", p.section)
		}
		if p.delegate != DelegateUnspecified {
			return fmt.Errorf("PolicySection must not set Delegate")
		}
	case PolicyHostOwned:
		if p.section != "" || p.delegate != DelegateUnspecified {
			return fmt.Errorf("PolicyHostOwned must not set Section or Delegate")
		}
	case PolicyDelegate:
		if !p.delegate.valid() {
			return fmt.Errorf("PolicyDelegate has unknown delegate %d", p.delegate)
		}
		if p.section != "" {
			return fmt.Errorf("PolicyDelegate must not set Section")
		}
	case PolicyTopology:
		if p.section != SectionStreamWrite {
			return fmt.Errorf("PolicyTopology must use ir.stream.write")
		}
		if p.delegate != DelegateUnspecified {
			return fmt.Errorf("PolicyTopology must not set Delegate")
		}
	default:
		return fmt.Errorf("unknown PolicyKind %d", p.kind)
	}
	return nil
}

// SignatureContentRef names a field covered by an opaque signature. When
// Message is empty, the field is on the signature's own message. A non-empty
// Message is a cross-event (or cross-message) dependency Migration B must
// resolve via stream/assembler state.
type SignatureContentRef struct {
	Message protoreflect.FullName // empty = same as SignatureBinding.Message
	Field   string
}

// SignatureBinding pins content that an opaque provider signature covers.
// Migration B must either reject mutation of signed content while the
// signature is present, or have the host clear the signature when that
// content changes. A plugin cannot manufacture a valid provider signature.
type SignatureBinding struct {
	Message        protoreflect.FullName
	SignatureField string
	Content        []SignatureContentRef
}

func (b SignatureBinding) validate() error {
	if b.Message == "" || b.SignatureField == "" {
		return fmt.Errorf("SignatureBinding requires Message and SignatureField")
	}
	if len(b.Content) == 0 {
		return fmt.Errorf("%s.%s has no signed content refs", b.Message, b.SignatureField)
	}
	for _, c := range b.Content {
		if c.Field == "" {
			return fmt.Errorf("%s.%s has empty Content.Field", b.Message, b.SignatureField)
		}
	}
	return nil
}

// StreamMutationCase is a Migration B checklist row: the real field-diff
// verifier must assert these grants/rejection outcomes. This package does not
// compute them.
type StreamMutationCase struct {
	ID              string
	NeedTopology    bool
	NeedAssistant   bool
	Reject          bool // changed/removed/added host-owned fact
	IdenticalPassOK bool // unchanged re-emit needs no grant
}

var streamMutationContractCases = []StreamMutationCase{
	{ID: "change-only-tool-call-delta-index", NeedTopology: true},
	{ID: "change-content-block-start-tool-call-signature", Reject: true},
	{ID: "identical-usage-re-emit", IdenticalPassOK: true},
	{ID: "identical-message-start-re-emit", IdenticalPassOK: true},
	{ID: "identical-signature-delta-re-emit", IdenticalPassOK: true},
	{ID: "identical-text-delta-re-emit", IdenticalPassOK: true},
	{ID: "unchanged-message-stop-one-for-one", IdenticalPassOK: true},
	{ID: "unchanged-content-block-start-one-for-one", IdenticalPassOK: true},
	{ID: "suppress-usage", Reject: true},
	{ID: "one-for-one-text-delta-rewrite", NeedAssistant: true},
	{ID: "suppress-text-delta", NeedTopology: true, NeedAssistant: true},
	{ID: "change-tool-call-delta-args-with-start-signature", Reject: true},
	{ID: "forge-stream-error", Reject: true},
}

var signatureBindings = []SignatureBinding{
	{
		Message:        "torana.v2.Message",
		SignatureField: "thinking_signature",
		Content: []SignatureContentRef{
			{Field: "thinking"},
			{Field: "redacted_thinking"},
		},
	},
	{
		Message:        "torana.v2.ToolCall",
		SignatureField: "signature",
		Content: []SignatureContentRef{
			{Field: "id"},
			{Field: "name"},
			{Field: "arguments_json"},
		},
	},
	{
		// Stream shape: ContentBlockStart{ToolCallRef{id,name,signature}} then
		// ToolCallDelta{arguments_delta...}. The provider binds the signature
		// to id, name, and the assembled arguments (see Gemini stream serializer).
		Message:        "torana.v2.ToolCallRef",
		SignatureField: "signature",
		Content: []SignatureContentRef{
			{Field: "id"},
			{Field: "name"},
			{Message: "torana.v2.ToolCallDelta", Field: "arguments_delta"},
		},
	},
	{
		Message:        "torana.v2.StreamEvent",
		SignatureField: "signature_delta",
		Content: []SignatureContentRef{
			// Surrounding block content; assembler pairs signature_delta with
			// the open content block's governed fields.
			{Message: "torana.v2.StreamEvent", Field: "text_delta"},
			{Message: "torana.v2.StreamEvent", Field: "thinking_delta"},
			{Message: "torana.v2.ToolCallRef", Field: "id"},
			{Message: "torana.v2.ToolCallRef", Field: "name"},
			{Message: "torana.v2.ToolCallDelta", Field: "arguments_delta"},
		},
	},
}

var outboundDelegateTargets = map[DelegateKind][]protoreflect.FullName{
	DelegateRequest:  {}, // capabilities_write.go
	DelegateResponse: {"torana.v2.ChatResponse"},
	DelegateStream:   {"torana.v2.StreamEvents", "torana.v2.StreamEvent", "torana.v2.Suppress"},
	DelegateHTTP:     {}, // deferred
	DelegateTick:     {}, // observational; deferred field inventory
}

var chatResponseFieldPolicies = map[string]FieldPolicy{
	"model":                    HostOwnedPolicy(),
	"id":                       HostOwnedPolicy(),
	"message":                  SectionPolicy(SectionMessagesAssistant),
	"finish_reason":            SectionPolicy(SectionMessagesAssistant),
	"usage":                    HostOwnedPolicy(),
	"upstream_status":          HostOwnedPolicy(),
	"duration_ms":              HostOwnedPolicy(),
	"provider_extensions_json": HostOwnedPolicy(),
}

var responseMessageFieldPolicies = map[string]FieldPolicy{
	"role":               HostOwnedPolicy(),
	"content":            SectionPolicy(SectionMessagesAssistant),
	"content_parts_json": SectionPolicy(SectionMessagesAssistant),
	"thinking":           SectionPolicy(SectionMessagesAssistant),
	"thinking_signature": HostOwnedPolicy(),
	"redacted_thinking":  SectionPolicy(SectionMessagesAssistant),
	"tool_calls":         SectionPolicy(SectionMessagesAssistant),
	"tool_call_id":       SectionPolicy(SectionMessagesAssistant),
	"tool_name":          SectionPolicy(SectionMessagesAssistant),
	"cache_control_json": SectionPolicy(SectionMessagesAssistant),
}

var responseToolCallFieldPolicies = map[string]FieldPolicy{
	"id":             SectionPolicy(SectionMessagesAssistant),
	"name":           SectionPolicy(SectionMessagesAssistant),
	"arguments_json": SectionPolicy(SectionMessagesAssistant),
	"signature":      HostOwnedPolicy(),
}

var usageFieldPolicies = map[string]FieldPolicy{
	"input_tokens":       HostOwnedPolicy(),
	"output_tokens":      HostOwnedPolicy(),
	"cache_read_tokens":  HostOwnedPolicy(),
	"cache_write_tokens": HostOwnedPolicy(),
}

var streamEventVariantPolicies = map[string]FieldPolicy{
	"text_delta":          SectionPolicy(SectionMessagesAssistant),
	"thinking_delta":      SectionPolicy(SectionMessagesAssistant),
	"tool_call_delta":     SectionPolicy(SectionMessagesAssistant),
	"usage":               HostOwnedPolicy(),
	"error":               HostOwnedPolicy(), // observed upstream failure; do not forge
	"signature_delta":     HostOwnedPolicy(),
	"message_start":       HostOwnedPolicy(),
	"message_stop":        SectionPolicy(SectionMessagesAssistant),
	"content_block_start": TopologyPolicy(),
	"content_block_stop":  TopologyPolicy(),
}

var toolCallDeltaFieldPolicies = map[string]FieldPolicy{
	"index":           TopologyPolicy(),
	"arguments_delta": SectionPolicy(SectionMessagesAssistant),
}

var streamErrorFieldPolicies = map[string]FieldPolicy{
	"code":    HostOwnedPolicy(),
	"message": HostOwnedPolicy(),
}

var messageStartFieldPolicies = map[string]FieldPolicy{
	"role":  HostOwnedPolicy(),
	"id":    HostOwnedPolicy(),
	"model": HostOwnedPolicy(),
}

var messageStopFieldPolicies = map[string]FieldPolicy{
	"finish_reason": SectionPolicy(SectionMessagesAssistant),
}

var contentBlockStartFieldPolicies = map[string]FieldPolicy{
	"index":     TopologyPolicy(),
	"text":      TopologyPolicy(),
	"thinking":  TopologyPolicy(),
	"tool_call": TopologyPolicy(),
	"provider":  TopologyPolicy(),
}

var toolCallRefFieldPolicies = map[string]FieldPolicy{
	"id":        SectionPolicy(SectionMessagesAssistant),
	"name":      SectionPolicy(SectionMessagesAssistant),
	"signature": HostOwnedPolicy(),
}

var providerBlockFieldPolicies = map[string]FieldPolicy{
	"kind": TopologyPolicy(),
}

var contentBlockStopFieldPolicies = map[string]FieldPolicy{
	"index": TopologyPolicy(),
}

var streamEventsFieldPolicies = map[string]FieldPolicy{
	"events": DelegatePolicy(DelegateStream),
}

var hookResultActionPolicies = map[string]FieldPolicy{
	"replace_request":  DelegatePolicy(DelegateRequest),
	"replace_response": DelegatePolicy(DelegateResponse),
	"emit_events":      DelegatePolicy(DelegateStream),
	"serve_http":       DelegatePolicy(DelegateHTTP),
	"tick_outcome":     DelegatePolicy(DelegateTick),
	"suppress":         DelegatePolicy(DelegateStream),
}

var outboundMessageFieldPolicies = map[protoreflect.FullName]map[string]FieldPolicy{
	"torana.v2.ChatResponse":      chatResponseFieldPolicies,
	"torana.v2.Message":           responseMessageFieldPolicies,
	"torana.v2.ToolCall":          responseToolCallFieldPolicies,
	"torana.v2.Usage":             usageFieldPolicies,
	"torana.v2.StreamEvent":       streamEventVariantPolicies,
	"torana.v2.ToolCallDelta":     toolCallDeltaFieldPolicies,
	"torana.v2.StreamError":       streamErrorFieldPolicies,
	"torana.v2.MessageStart":      messageStartFieldPolicies,
	"torana.v2.MessageStop":       messageStopFieldPolicies,
	"torana.v2.ContentBlockStart": contentBlockStartFieldPolicies,
	"torana.v2.ToolCallRef":       toolCallRefFieldPolicies,
	"torana.v2.ProviderBlock":     providerBlockFieldPolicies,
	"torana.v2.ContentBlockStop":  contentBlockStopFieldPolicies,
	"torana.v2.StreamEvents":      streamEventsFieldPolicies,
	"torana.v2.TextBlock":         {},
	"torana.v2.ThinkingBlock":     {},
	"torana.v2.Suppress":          {},
	"torana.v2.HookResult":        hookResultActionPolicies,
}

func init() {
	for msg, fields := range outboundMessageFieldPolicies {
		for name, p := range fields {
			if err := p.validate(); err != nil {
				panic(fmt.Sprintf("outbound policy %s.%s: %v", msg, name, err))
			}
		}
	}
	for _, b := range signatureBindings {
		if err := b.validate(); err != nil {
			panic(err)
		}
	}
	seen := map[string]bool{}
	for _, c := range streamMutationContractCases {
		if c.ID == "" || seen[c.ID] {
			panic(fmt.Sprintf("invalid StreamMutationCase id %q", c.ID))
		}
		seen[c.ID] = true
	}
}

// StreamTopologySection is the additive grant name for cardinality/order/
// boundary/action changes. It does not alone authorise content or host-owned
// changes. The recursive field-diff verifier that applies it lives in
// Migration B.
func StreamTopologySection() WriteSection { return SectionStreamWrite }

// OutboundMessageRegistered reports whether msg has a field-policy inventory.
func OutboundMessageRegistered(msg protoreflect.FullName) bool {
	_, ok := outboundMessageFieldPolicies[msg]
	return ok
}

// OutboundFieldPolicy returns the policy for one field. The returned value is
// a copy; mutating it does not affect the registry.
func OutboundFieldPolicy(msg protoreflect.FullName, field string) (FieldPolicy, bool) {
	m, ok := outboundMessageFieldPolicies[msg]
	if !ok {
		return FieldPolicy{}, false
	}
	p, ok := m[field]
	return p, ok
}

// OutboundFieldNames returns a sorted copy of registered field names for msg.
func OutboundFieldNames(msg protoreflect.FullName) ([]string, bool) {
	m, ok := outboundMessageFieldPolicies[msg]
	if !ok {
		return nil, false
	}
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, true
}

// OutboundDelegateTargets returns a copy of messages the delegate walks when
// those inventories are registered here. Empty means deferred or elsewhere.
func OutboundDelegateTargets(d DelegateKind) ([]protoreflect.FullName, bool) {
	t, ok := outboundDelegateTargets[d]
	if !ok {
		return nil, false
	}
	out := make([]protoreflect.FullName, len(t))
	copy(out, t)
	return out, true
}

// AllSignatureBindings returns a deep copy of the opaque-signature inventory.
func AllSignatureBindings() []SignatureBinding {
	out := make([]SignatureBinding, len(signatureBindings))
	for i, b := range signatureBindings {
		out[i] = SignatureBinding{
			Message:        b.Message,
			SignatureField: b.SignatureField,
			Content:        append([]SignatureContentRef(nil), b.Content...),
		}
	}
	return out
}

// StreamMutationContractCases returns a copy of the Migration B checklist.
// Every row must run against the real verifier when Migration B lands.
func StreamMutationContractCases() []StreamMutationCase {
	out := make([]StreamMutationCase, len(streamMutationContractCases))
	copy(out, streamMutationContractCases)
	return out
}
