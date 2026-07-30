package plugin_sdk

import (
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
// authorise rewriting opaque provider response blobs.
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
	// PolicySection maps the field to a content write grant.
	PolicySection PolicyKind = iota
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

// FieldPolicy is one field's (or action's) declarative classification.
type FieldPolicy struct {
	Kind     PolicyKind
	Section  WriteSection // PolicySection or PolicyTopology
	Delegate string       // PolicyDelegate verifier name
}

// Named delegate verifiers. Migration B implements each once.
const (
	DelegateRequest  = "request"  // capabilities_write.go
	DelegateResponse = "response" // ChatResponseFieldPolicies
	DelegateStream   = "stream"   // recursive stream field diff + topology
	DelegateHTTP     = "http"     // ServeHTTP result policy (deferred)
	DelegateTick     = "tick"     // TickOutcome observational policy
)

func SectionPolicy(s WriteSection) FieldPolicy {
	return FieldPolicy{Kind: PolicySection, Section: s}
}

func HostOwnedPolicy() FieldPolicy {
	return FieldPolicy{Kind: PolicyHostOwned}
}

func TopologyPolicy() FieldPolicy {
	return FieldPolicy{Kind: PolicyTopology, Section: SectionStreamWrite}
}

func DelegatePolicy(name string) FieldPolicy {
	return FieldPolicy{Kind: PolicyDelegate, Delegate: name}
}

// HostOwnedField is the sentinel string used in docs and older call sites.
// Prefer FieldPolicy / PolicyHostOwned in new code.
const HostOwnedField = "<host-owned>"

func (p FieldPolicy) IsHostOwned() bool { return p.Kind == PolicyHostOwned }

// ChatResponseFieldPolicies maps every ChatResponse field.
var ChatResponseFieldPolicies = map[string]FieldPolicy{
	"model":                    HostOwnedPolicy(), // model that actually answered
	"id":                       HostOwnedPolicy(),
	"message":                  SectionPolicy(SectionMessagesAssistant),
	"finish_reason":            SectionPolicy(SectionMessagesAssistant),
	"usage":                    HostOwnedPolicy(),
	"upstream_status":          HostOwnedPolicy(),
	"duration_ms":              HostOwnedPolicy(),
	"provider_extensions_json": HostOwnedPolicy(), // opaque provider output
}

// ResponseMessageFieldPolicies governs Message under ChatResponse.message.
var ResponseMessageFieldPolicies = map[string]FieldPolicy{
	"role":               HostOwnedPolicy(), // same policy as MessageStart.role
	"content":            SectionPolicy(SectionMessagesAssistant),
	"content_parts_json": SectionPolicy(SectionMessagesAssistant),
	"thinking":           SectionPolicy(SectionMessagesAssistant),
	"thinking_signature": HostOwnedPolicy(), // opaque provider binding token
	"redacted_thinking":  SectionPolicy(SectionMessagesAssistant),
	"tool_calls":         SectionPolicy(SectionMessagesAssistant),
	"tool_call_id":       SectionPolicy(SectionMessagesAssistant),
	"tool_name":          SectionPolicy(SectionMessagesAssistant),
	"cache_control_json": SectionPolicy(SectionMessagesAssistant),
}

// ResponseToolCallFieldPolicies governs ToolCall under ChatResponse.message.
var ResponseToolCallFieldPolicies = map[string]FieldPolicy{
	"id":             SectionPolicy(SectionMessagesAssistant),
	"name":           SectionPolicy(SectionMessagesAssistant),
	"arguments_json": SectionPolicy(SectionMessagesAssistant),
	"signature":      HostOwnedPolicy(), // opaque provider binding token
}

// UsageFieldPolicies: always host-owned.
var UsageFieldPolicies = map[string]FieldPolicy{
	"input_tokens":       HostOwnedPolicy(),
	"output_tokens":      HostOwnedPolicy(),
	"cache_read_tokens":  HostOwnedPolicy(),
	"cache_write_tokens": HostOwnedPolicy(),
}

// StreamEventVariantPolicies maps every StreamEvent oneof field. HostOwned on
// a variant means changing/removing/adding its payload is forbidden; an
// identical re-emit is a no-op (Migration B field diff).
var StreamEventVariantPolicies = map[string]FieldPolicy{
	"text_delta":          SectionPolicy(SectionMessagesAssistant),
	"thinking_delta":      SectionPolicy(SectionMessagesAssistant),
	"tool_call_delta":     SectionPolicy(SectionMessagesAssistant),
	"usage":               HostOwnedPolicy(),
	"error":               TopologyPolicy(),
	"signature_delta":     HostOwnedPolicy(),
	"message_start":       HostOwnedPolicy(),
	"message_stop":        SectionPolicy(SectionMessagesAssistant),
	"content_block_start": TopologyPolicy(),
	"content_block_stop":  TopologyPolicy(),
}

var ToolCallDeltaFieldPolicies = map[string]FieldPolicy{
	"index":           TopologyPolicy(),
	"arguments_delta": SectionPolicy(SectionMessagesAssistant),
}

var StreamErrorFieldPolicies = map[string]FieldPolicy{
	"code":    TopologyPolicy(),
	"message": TopologyPolicy(),
}

var MessageStartFieldPolicies = map[string]FieldPolicy{
	"role":  HostOwnedPolicy(),
	"id":    HostOwnedPolicy(),
	"model": HostOwnedPolicy(), // observed answering model, not request selection
}

var MessageStopFieldPolicies = map[string]FieldPolicy{
	"finish_reason": SectionPolicy(SectionMessagesAssistant),
}

var ContentBlockStartFieldPolicies = map[string]FieldPolicy{
	"index":     TopologyPolicy(),
	"text":      TopologyPolicy(),
	"thinking":  TopologyPolicy(),
	"tool_call": TopologyPolicy(),
	"provider":  TopologyPolicy(),
}

var ToolCallRefFieldPolicies = map[string]FieldPolicy{
	"id":        SectionPolicy(SectionMessagesAssistant),
	"name":      SectionPolicy(SectionMessagesAssistant),
	"signature": HostOwnedPolicy(),
}

var ProviderBlockFieldPolicies = map[string]FieldPolicy{
	"kind": TopologyPolicy(),
}

var ContentBlockStopFieldPolicies = map[string]FieldPolicy{
	"index": TopologyPolicy(),
}

// StreamEventsFieldPolicies: list cardinality/order is decided by the stream
// verifier (Delegate), not by labelling the repeated field as topology — a
// one-for-one TextDelta rewrite must not require ir.stream.write.
var StreamEventsFieldPolicies = map[string]FieldPolicy{
	"events": DelegatePolicy(DelegateStream),
}

// HookResultActionPolicies maps every HookResult action oneof field. Actions
// delegate to the verifier that knows the nested inventory; they are not
// single field grants and must not be labelled host-owned merely because
// enforcement lands later.
var HookResultActionPolicies = map[string]FieldPolicy{
	"replace_request":  DelegatePolicy(DelegateRequest),
	"replace_response": DelegatePolicy(DelegateResponse),
	"emit_events":      DelegatePolicy(DelegateStream),
	"serve_http":       DelegatePolicy(DelegateHTTP),
	"tick_outcome":     DelegatePolicy(DelegateTick),
	"suppress":         DelegatePolicy(DelegateStream),
}

// SignatureBinding pins content that an opaque provider signature covers.
// Migration B must either reject mutation of signed content while the
// signature is present, or have the host clear the signature when that
// content changes. A plugin cannot manufacture a valid provider signature.
//
// signature_delta is cross-event state: a per-event verifier cannot tell
// whether the surrounding block was modified. The StreamHandler/assembler or
// host stream verifier must account for it.
type SignatureBinding struct {
	Message         protoreflect.FullName
	SignatureField  string
	ContentFields   []string
	CrossEventNote  string // non-empty when binding spans events
}

// SignatureBindings is the inventory of opaque signature dependencies.
var SignatureBindings = []SignatureBinding{
	{
		Message:        "torana.v2.Message",
		SignatureField: "thinking_signature",
		ContentFields:  []string{"thinking", "redacted_thinking"},
	},
	{
		Message:        "torana.v2.ToolCall",
		SignatureField: "signature",
		ContentFields:  []string{"id", "name", "arguments_json"},
	},
	{
		Message:        "torana.v2.ToolCallRef",
		SignatureField: "signature",
		ContentFields:  []string{"id", "name"},
	},
	{
		Message:        "torana.v2.StreamEvent",
		SignatureField: "signature_delta",
		ContentFields:  nil, // surrounds the current content block
		CrossEventNote: "pair with assembler/block state; clear or reject when the signed block's governed content changes",
	},
}

// outboundDelegateTargets lists messages a Delegate policy walks when those
// inventories exist in this package. Empty means deferred or elsewhere.
var outboundDelegateTargets = map[string][]protoreflect.FullName{
	DelegateRequest:  {}, // capabilities_write.go
	DelegateResponse: {"torana.v2.ChatResponse"},
	DelegateStream:   {"torana.v2.StreamEvents", "torana.v2.StreamEvent", "torana.v2.Suppress"},
	DelegateHTTP:     {}, // deferred
	DelegateTick:     {}, // observational; deferred field inventory
}

// outboundMessageFieldPolicies registers every nested message the recursive
// inventory must classify. Empty messages are listed with an empty map.
var outboundMessageFieldPolicies = map[protoreflect.FullName]map[string]FieldPolicy{
	"torana.v2.ChatResponse":      ChatResponseFieldPolicies,
	"torana.v2.Message":           ResponseMessageFieldPolicies,
	"torana.v2.ToolCall":          ResponseToolCallFieldPolicies,
	"torana.v2.Usage":             UsageFieldPolicies,
	"torana.v2.StreamEvent":       StreamEventVariantPolicies,
	"torana.v2.ToolCallDelta":     ToolCallDeltaFieldPolicies,
	"torana.v2.StreamError":       StreamErrorFieldPolicies,
	"torana.v2.MessageStart":      MessageStartFieldPolicies,
	"torana.v2.MessageStop":       MessageStopFieldPolicies,
	"torana.v2.ContentBlockStart": ContentBlockStartFieldPolicies,
	"torana.v2.ToolCallRef":       ToolCallRefFieldPolicies,
	"torana.v2.ProviderBlock":     ProviderBlockFieldPolicies,
	"torana.v2.ContentBlockStop":  ContentBlockStopFieldPolicies,
	"torana.v2.StreamEvents":      StreamEventsFieldPolicies,
	"torana.v2.TextBlock":         {},
	"torana.v2.ThinkingBlock":     {},
	"torana.v2.Suppress":          {},
	"torana.v2.HookResult":        HookResultActionPolicies,
}

// Compatibility aliases for the previous map names (same maps, typed values).
var (
	ChatResponseFieldSections      = ChatResponseFieldPolicies
	ResponseMessageFieldSections   = ResponseMessageFieldPolicies
	ResponseToolCallFieldSections  = ResponseToolCallFieldPolicies
	UsageFieldSections             = UsageFieldPolicies
	StreamEventVariantSections     = StreamEventVariantPolicies
	ToolCallDeltaFieldSections     = ToolCallDeltaFieldPolicies
	StreamErrorFieldSections       = StreamErrorFieldPolicies
	MessageStartFieldSections      = MessageStartFieldPolicies
	MessageStopFieldSections       = MessageStopFieldPolicies
	ContentBlockStartFieldSections = ContentBlockStartFieldPolicies
	ToolCallRefFieldSections       = ToolCallRefFieldPolicies
	ProviderBlockFieldSections     = ProviderBlockFieldPolicies
	ContentBlockStopFieldSections  = ContentBlockStopFieldPolicies
	StreamEventsFieldSections      = StreamEventsFieldPolicies
	HookResultActionSections       = HookResultActionPolicies
)

// StreamTopologySection is the additive grant name for cardinality/order/
// boundary/action changes. It does not alone authorise content or host-owned
// changes. The recursive field-diff verifier that applies it lives in
// Migration B.
func StreamTopologySection() WriteSection { return SectionStreamWrite }

// OutboundPolicyFor returns the field→policy map for a registered message.
func OutboundPolicyFor(name protoreflect.FullName) (map[string]FieldPolicy, bool) {
	m, ok := outboundMessageFieldPolicies[name]
	return m, ok
}

// OutboundDelegateTargets returns messages the named delegate verifier walks
// when those inventories are registered here.
func OutboundDelegateTargets(name string) ([]protoreflect.FullName, bool) {
	t, ok := outboundDelegateTargets[name]
	return t, ok
}
