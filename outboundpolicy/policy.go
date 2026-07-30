package outboundpolicy

import (
	"fmt"
	"sort"

	plugin_sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
	"google.golang.org/protobuf/proto"
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
// Nested message evaluation (Migration B must implement exactly this):
//
//   - PolicyContainer: never charge the parent; always recurse. Presence or
//     oneof-selection change of the container is expressed by treating nested
//     fields as added/removed, plus topology when the enclosing stream
//     kind/cardinality/boundaries change.
//   - PolicySection / PolicyTopology on a message-valued field: if the same
//     message/oneof variant is present on both sides, recurse and do not
//     charge the parent; if presence or oneof selection changes, charge the
//     parent and recursively account for added/removed nested fields.
//   - PolicyHostOwned on a message: the whole subtree is immutable; any
//     nested change/remove/add rejects.
//   - Scalar fields apply their own policy directly.
//
// Registries are package-private. Constructors are package-private. Public
// accessors return values/clones so importers cannot mutate the authority
// table or mint invalid policies.
//
// Host/linter only: WASM guests must not import this package. Guest-relevant
// capability names (ir.stream.write, …) live in the root plugin_sdk module.
// Descriptor reflection runs in Validate(), not package init, so importing
// hosts pay once explicitly rather than on every guest module instance.

// PolicyKind classifies a field or HookResult action for the inventory.
type PolicyKind int

const (
	// PolicyUnspecified is the zero value and is always invalid.
	PolicyUnspecified PolicyKind = iota
	// PolicySection maps the field to a content write grant (never topology).
	PolicySection
	// PolicyHostOwned marks an immutable protobuf fact. Unchanged values may
	// be re-emitted; changed/removed/added values reject. On a message field
	// this covers the whole subtree.
	PolicyHostOwned
	// PolicyDelegate hands the nested value to another verifier (request,
	// response, stream, HTTP, tick). Not a field grant and not host-owned.
	PolicyDelegate
	// PolicyTopology maps to ir.stream.write when this aspect actually changes
	// (cardinality, order, boundaries, indexes, event kind).
	PolicyTopology
	// PolicyContainer marks a message-valued field that never contributes a
	// grant itself. Migration B always recurses into nested field policies
	// (see package comment nesting rules).
	PolicyContainer
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

// SignatureScope says how a signed content ref correlates to stream/assembler
// state. (Message, Field) alone is not enough when multiple blocks or parallel
// tool calls are in flight.
type SignatureScope int

const (
	SignatureScopeUnspecified SignatureScope = iota
	// SignatureScopeSameMessage: field on the signature's own message.
	SignatureScopeSameMessage
	// SignatureScopeCurrentContentBlock: signature_delta emitted together with
	// an open text/thinking block (same provider part carried text/thinking and
	// a thoughtSignature). Not the tool-call path.
	SignatureScopeCurrentContentBlock
	// SignatureScopeTrailingStandalone: signature_delta with no open content
	// block — Code Assist's final {"thoughtSignature","text":""} part after
	// earlier text. Host normalization: preserve as a standalone SignatureDelta
	// after prior ContentBlockStops; do not synthesize an empty text/thinking
	// block. Binds the preceding closed text/thinking content the assembler
	// attributed to this turn (reject-or-clear if that content mutates while
	// the token remains). Does not bind tool-call blocks. Non-streaming
	// adapters must not drop signature-only empty-text parts (today's
	// appendModel disagreement is a host Migration B fix).
	SignatureScopeTrailingStandalone
	// SignatureScopeToolCallBlockByIndex: ToolCallRef.signature binds id/name
	// on the start event and ToolCallDelta.arguments_delta events that share
	// the same ContentBlockStart.index. Requires the ABI invariant that block
	// indexes are unique within one streamed message (adapters must assign
	// distinct indexes to parallel Gemini parts — today's Index:0 for every
	// call is invalid under this contract).
	SignatureScopeToolCallBlockByIndex
)

func (s SignatureScope) valid() bool {
	switch s {
	case SignatureScopeSameMessage, SignatureScopeCurrentContentBlock,
		SignatureScopeTrailingStandalone, SignatureScopeToolCallBlockByIndex:
		return true
	}
	return false
}

// FieldPolicy is one field's (or action's) declarative classification.
// Values come only from the package-private registry constructors.
type FieldPolicy struct {
	kind     PolicyKind
	section  plugin_sdk.WriteSection
	delegate DelegateKind
}

func sectionPolicy(s plugin_sdk.WriteSection) FieldPolicy {
	return FieldPolicy{kind: PolicySection, section: s}
}

func hostOwnedPolicy() FieldPolicy {
	return FieldPolicy{kind: PolicyHostOwned}
}

func topologyPolicy() FieldPolicy {
	return FieldPolicy{kind: PolicyTopology, section: plugin_sdk.SectionStreamWrite}
}

func delegatePolicy(d DelegateKind) FieldPolicy {
	return FieldPolicy{kind: PolicyDelegate, delegate: d}
}

func containerPolicy() FieldPolicy {
	return FieldPolicy{kind: PolicyContainer}
}

func (p FieldPolicy) Kind() PolicyKind { return p.kind }

func (p FieldPolicy) IsHostOwned() bool { return p.kind == PolicyHostOwned }

func (p FieldPolicy) IsContainer() bool { return p.kind == PolicyContainer }

// Section returns the content or topology section when Kind is PolicySection
// or PolicyTopology.
func (p FieldPolicy) Section() (plugin_sdk.WriteSection, bool) {
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
		if p.section == "" || p.section == plugin_sdk.SectionStreamWrite {
			return fmt.Errorf("PolicySection requires a non-topology write section")
		}
		if !plugin_sdk.IsWritePermission(string(p.section)) {
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
		if p.section != plugin_sdk.SectionStreamWrite {
			return fmt.Errorf("PolicyTopology must use ir.stream.write")
		}
		if p.delegate != DelegateUnspecified {
			return fmt.Errorf("PolicyTopology must not set Delegate")
		}
	case PolicyContainer:
		if p.section != "" || p.delegate != DelegateUnspecified {
			return fmt.Errorf("PolicyContainer must not set Section or Delegate")
		}
	default:
		return fmt.Errorf("unknown PolicyKind %d", p.kind)
	}
	return nil
}

// SignatureContentRef names a field covered by an opaque signature and how
// Migration B correlates it to stream/assembler state.
type SignatureContentRef struct {
	Scope   SignatureScope
	Message protoreflect.FullName // required except SameMessage may omit (= binding message)
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

func (b SignatureBinding) validateShape() error {
	if b.Message == "" || b.SignatureField == "" {
		return fmt.Errorf("SignatureBinding requires Message and SignatureField")
	}
	if len(b.Content) == 0 {
		return fmt.Errorf("%s.%s has no signed content refs", b.Message, b.SignatureField)
	}
	for i, c := range b.Content {
		if !c.Scope.valid() {
			return fmt.Errorf("%s.%s content[%d]: invalid scope", b.Message, b.SignatureField, i)
		}
		if c.Field == "" {
			return fmt.Errorf("%s.%s content[%d]: empty Field", b.Message, b.SignatureField, i)
		}
		switch c.Scope {
		case SignatureScopeSameMessage:
			if c.Message != "" && c.Message != b.Message {
				return fmt.Errorf("%s.%s content[%d]: SameMessage must not name a different message",
					b.Message, b.SignatureField, i)
			}
		case SignatureScopeToolCallBlockByIndex:
			if b.Message != "torana.v2.ToolCallRef" {
				return fmt.Errorf("%s.%s: ToolCallBlockByIndex only valid on ToolCallRef.signature",
					b.Message, b.SignatureField)
			}
			if c.Message == "" {
				return fmt.Errorf("%s.%s content[%d]: ToolCallBlockByIndex requires Message",
					b.Message, b.SignatureField, i)
			}
		case SignatureScopeCurrentContentBlock, SignatureScopeTrailingStandalone:
			if b.SignatureField != "signature_delta" {
				return fmt.Errorf("%s.%s: %v only valid on signature_delta",
					b.Message, b.SignatureField, c.Scope)
			}
			if c.Message == "" {
				return fmt.Errorf("%s.%s content[%d]: scope %v requires Message",
					b.Message, b.SignatureField, i, c.Scope)
			}
		}
	}
	return nil
}

var signatureBindings = []SignatureBinding{
	{
		Message:        "torana.v2.Message",
		SignatureField: "thinking_signature",
		Content: []SignatureContentRef{
			{Scope: SignatureScopeSameMessage, Field: "thinking"},
			{Scope: SignatureScopeSameMessage, Field: "redacted_thinking"},
		},
	},
	{
		Message:        "torana.v2.ToolCall",
		SignatureField: "signature",
		Content: []SignatureContentRef{
			{Scope: SignatureScopeSameMessage, Field: "id"},
			{Scope: SignatureScopeSameMessage, Field: "name"},
			{Scope: SignatureScopeSameMessage, Field: "arguments_json"},
		},
	},
	{
		// ContentBlockStart{ToolCallRef{id,name,signature}} then
		// ToolCallDelta{index, arguments_delta...} sharing that block index.
		Message:        "torana.v2.ToolCallRef",
		SignatureField: "signature",
		Content: []SignatureContentRef{
			{Scope: SignatureScopeSameMessage, Field: "id"},
			{Scope: SignatureScopeSameMessage, Field: "name"},
			{Scope: SignatureScopeToolCallBlockByIndex, Message: "torana.v2.ToolCallDelta", Field: "arguments_delta"},
		},
	},
	{
		// Text/thinking signatures — not the function-call path.
		// CurrentContentBlock: signature on the same provider part as text/thinking.
		// TrailingStandalone: Code Assist final thoughtSignature-only part
		// (see torana-edge gemini stream standalone branch + codeassist-stream-text.sse).
		Message:        "torana.v2.StreamEvent",
		SignatureField: "signature_delta",
		Content: []SignatureContentRef{
			{Scope: SignatureScopeCurrentContentBlock, Message: "torana.v2.StreamEvent", Field: "text_delta"},
			{Scope: SignatureScopeCurrentContentBlock, Message: "torana.v2.StreamEvent", Field: "thinking_delta"},
			{Scope: SignatureScopeTrailingStandalone, Message: "torana.v2.StreamEvent", Field: "text_delta"},
			{Scope: SignatureScopeTrailingStandalone, Message: "torana.v2.StreamEvent", Field: "thinking_delta"},
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
	"model":                    hostOwnedPolicy(),
	"id":                       hostOwnedPolicy(),
	"message":                  containerPolicy(), // recurse; do not auto-charge assistant
	"finish_reason":            sectionPolicy(plugin_sdk.SectionMessagesAssistant),
	"usage":                    hostOwnedPolicy(),
	"upstream_status":          hostOwnedPolicy(),
	"duration_ms":              hostOwnedPolicy(),
	"provider_extensions_json": hostOwnedPolicy(),
}

var responseMessageFieldPolicies = map[string]FieldPolicy{
	"role":               hostOwnedPolicy(),
	"content":            sectionPolicy(plugin_sdk.SectionMessagesAssistant),
	"content_parts_json": sectionPolicy(plugin_sdk.SectionMessagesAssistant),
	"thinking":           sectionPolicy(plugin_sdk.SectionMessagesAssistant),
	"thinking_signature": hostOwnedPolicy(),
	"redacted_thinking":  sectionPolicy(plugin_sdk.SectionMessagesAssistant),
	"tool_calls":         sectionPolicy(plugin_sdk.SectionMessagesAssistant),
	"tool_call_id":       sectionPolicy(plugin_sdk.SectionMessagesAssistant),
	"tool_name":          sectionPolicy(plugin_sdk.SectionMessagesAssistant),
	"cache_control_json": sectionPolicy(plugin_sdk.SectionMessagesAssistant),
}

var responseToolCallFieldPolicies = map[string]FieldPolicy{
	"id":             sectionPolicy(plugin_sdk.SectionMessagesAssistant),
	"name":           sectionPolicy(plugin_sdk.SectionMessagesAssistant),
	"arguments_json": sectionPolicy(plugin_sdk.SectionMessagesAssistant),
	"signature":      hostOwnedPolicy(),
}

var usageFieldPolicies = map[string]FieldPolicy{
	"input_tokens":       hostOwnedPolicy(),
	"output_tokens":      hostOwnedPolicy(),
	"cache_read_tokens":  hostOwnedPolicy(),
	"cache_write_tokens": hostOwnedPolicy(),
}

var streamEventVariantPolicies = map[string]FieldPolicy{
	"text_delta":          sectionPolicy(plugin_sdk.SectionMessagesAssistant),
	"thinking_delta":      sectionPolicy(plugin_sdk.SectionMessagesAssistant),
	"tool_call_delta":     containerPolicy(), // index vs args decided by nested fields
	"usage":               hostOwnedPolicy(),
	"error":               hostOwnedPolicy(),
	"signature_delta":     hostOwnedPolicy(),
	"message_start":       hostOwnedPolicy(),
	"message_stop":        containerPolicy(),
	"content_block_start": topologyPolicy(), // presence/kind; same presence → recurse only
	"content_block_stop":  topologyPolicy(),
}

var toolCallDeltaFieldPolicies = map[string]FieldPolicy{
	"index":           topologyPolicy(),
	"arguments_delta": sectionPolicy(plugin_sdk.SectionMessagesAssistant),
}

var streamErrorFieldPolicies = map[string]FieldPolicy{
	"code":    hostOwnedPolicy(),
	"message": hostOwnedPolicy(),
}

var messageStartFieldPolicies = map[string]FieldPolicy{
	"role":  hostOwnedPolicy(),
	"id":    hostOwnedPolicy(),
	"model": hostOwnedPolicy(),
}

var messageStopFieldPolicies = map[string]FieldPolicy{
	"finish_reason": sectionPolicy(plugin_sdk.SectionMessagesAssistant),
}

var contentBlockStartFieldPolicies = map[string]FieldPolicy{
	"index":     topologyPolicy(),
	"text":      topologyPolicy(), // oneof arm presence; same presence → recurse
	"thinking":  topologyPolicy(),
	"tool_call": containerPolicy(), // id/name/signature via ToolCallRef
	"provider":  topologyPolicy(),
}

var toolCallRefFieldPolicies = map[string]FieldPolicy{
	"id":        sectionPolicy(plugin_sdk.SectionMessagesAssistant),
	"name":      sectionPolicy(plugin_sdk.SectionMessagesAssistant),
	"signature": hostOwnedPolicy(),
}

var providerBlockFieldPolicies = map[string]FieldPolicy{
	"kind": topologyPolicy(),
}

var contentBlockStopFieldPolicies = map[string]FieldPolicy{
	"index": topologyPolicy(),
}

var streamEventsFieldPolicies = map[string]FieldPolicy{
	"events": delegatePolicy(DelegateStream),
}

var hookResultActionPolicies = map[string]FieldPolicy{
	"replace_request":  delegatePolicy(DelegateRequest),
	"replace_response": delegatePolicy(DelegateResponse),
	"emit_events":      delegatePolicy(DelegateStream),
	"serve_http":       delegatePolicy(DelegateHTTP),
	"tick_outcome":     delegatePolicy(DelegateTick),
	"suppress":         delegatePolicy(DelegateStream),
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

// Migration B human checklist (not a public API — build executable fixtures
// with concrete before/after events when the verifier lands):
//
// Nesting / grants:
//   - change only ToolCallDelta.index → topology only
//   - change only ToolCallDelta.arguments_delta → assistant only
//   - add/remove a tool_call_delta → topology + assistant
//   - change only ToolCallRef.id/name inside unchanged ContentBlockStart → assistant
//   - switch ContentBlockStart oneof variant (text↔tool_call) → topology (+ nested)
//
// Host-owned / signatures:
//   - identical Usage/MessageStart/signature_delta/TextDelta re-emit → no grant
//   - suppress Usage / forge StreamError → reject
//   - change args deltas for signed tool-call block index → reject or clear signature
//   - parallel tool calls: mutating block B args must not be gated by block A's signature
//   - multi-block: signature_delta binds current text/thinking block only
//
// Topology / indexes:
//   - one-for-one TextDelta rewrite → assistant only
//   - suppress TextDelta → topology + assistant
//   - block indexes unique within one streamed message; delta/stop match open start
//   - parallel tool calls: two indexes, only first signed → second args unbound by first sig
//   - Gemini adapters must stop emitting Index:0 for every function-call part
//
// Trailing standalone signature (Code Assist):
//   - SignatureDelta after closed text blocks, no open block → TrailingStandalone
//   - do not synthesize empty text block; align non-stream appendModel drop

func init() {
	// Shape-only: no protobuf reflection (hosts call Validate once explicitly).
	for msg, fields := range outboundMessageFieldPolicies {
		for name, p := range fields {
			if err := p.validate(); err != nil {
				panic(fmt.Sprintf("outbound policy %s.%s: %v", msg, name, err))
			}
		}
	}
	for _, b := range signatureBindings {
		if err := b.validateShape(); err != nil {
			panic(err)
		}
	}
}

// Validate checks the registry against protobuf descriptors and kind/field
// invariants (PolicyContainer only on message fields, PolicyDelegate only on
// message/wrapper fields). Hosts and linters should call this once at process
// start. Guests must not import this package.
func Validate() error {
	descs := outboundDescriptors()
	for msg, fields := range outboundMessageFieldPolicies {
		desc, ok := descs[msg]
		if !ok {
			// Empty fieldless registries (TextBlock, Suppress, …) still need a descriptor.
			return fmt.Errorf("outbound policy message %s has no protobuf descriptor", msg)
		}
		for name, p := range fields {
			fd := desc.Fields().ByName(protoreflect.Name(name))
			if fd == nil {
				return fmt.Errorf("%s.%s missing from protobuf", msg, name)
			}
			switch p.Kind() {
			case PolicyContainer:
				if fd.Kind() != protoreflect.MessageKind {
					return fmt.Errorf("%s.%s: PolicyContainer requires a message field, got %v", msg, name, fd.Kind())
				}
			case PolicyDelegate:
				if fd.Kind() != protoreflect.MessageKind {
					return fmt.Errorf("%s.%s: PolicyDelegate requires a message field, got %v", msg, name, fd.Kind())
				}
			}
		}
	}
	for _, b := range signatureBindings {
		if err := b.validateShape(); err != nil {
			return err
		}
		if err := validateSignatureBindingAgainstProto(b, descs); err != nil {
			return err
		}
	}
	return nil
}

func outboundDescriptors() map[protoreflect.FullName]protoreflect.MessageDescriptor {
	roots := []proto.Message{
		&pbv2.ChatResponse{},
		&pbv2.StreamEvent{},
		&pbv2.StreamEvents{},
		&pbv2.HookResult{},
		&pbv2.Suppress{},
		&pbv2.Message{},
		&pbv2.ToolCall{},
		&pbv2.ToolCallDelta{},
		&pbv2.ToolCallRef{},
		&pbv2.ContentBlockStart{},
		&pbv2.ContentBlockStop{},
		&pbv2.MessageStart{},
		&pbv2.MessageStop{},
		&pbv2.Usage{},
		&pbv2.StreamError{},
		&pbv2.ProviderBlock{},
		&pbv2.TextBlock{},
		&pbv2.ThinkingBlock{},
	}
	out := map[protoreflect.FullName]protoreflect.MessageDescriptor{}
	var walk func(protoreflect.MessageDescriptor)
	walk = func(d protoreflect.MessageDescriptor) {
		if _, ok := out[d.FullName()]; ok {
			return
		}
		out[d.FullName()] = d
		fields := d.Fields()
		for i := 0; i < fields.Len(); i++ {
			fd := fields.Get(i)
			if fd.Kind() == protoreflect.MessageKind {
				walk(fd.Message())
			}
		}
	}
	for _, m := range roots {
		walk(m.ProtoReflect().Descriptor())
	}
	return out
}

func validateSignatureBindingAgainstProto(b SignatureBinding, descs map[protoreflect.FullName]protoreflect.MessageDescriptor) error {
	sigDesc, ok := descs[b.Message]
	if !ok {
		return fmt.Errorf("signature message %s unknown to proto inventory", b.Message)
	}
	sigFD := sigDesc.Fields().ByName(protoreflect.Name(b.SignatureField))
	if sigFD == nil {
		return fmt.Errorf("%s has no field %s", b.Message, b.SignatureField)
	}
	// signature_delta is a StreamEvent oneof arm named signature_delta.
	sigPolicy, ok := outboundMessageFieldPolicies[b.Message][b.SignatureField]
	if !ok || !sigPolicy.IsHostOwned() {
		return fmt.Errorf("%s.%s must be host-owned in the field policy registry", b.Message, b.SignatureField)
	}
	for i, c := range b.Content {
		msgName := c.Message
		if msgName == "" || c.Scope == SignatureScopeSameMessage {
			msgName = b.Message
		}
		desc, ok := descs[msgName]
		if !ok {
			return fmt.Errorf("%s.%s content[%d]: message %s unknown", b.Message, b.SignatureField, i, msgName)
		}
		if desc.Fields().ByName(protoreflect.Name(c.Field)) == nil {
			return fmt.Errorf("%s.%s content[%d]: %s has no field %s", b.Message, b.SignatureField, i, msgName, c.Field)
		}
		if _, ok := outboundMessageFieldPolicies[msgName][c.Field]; !ok {
			return fmt.Errorf("%s.%s content[%d]: %s.%s missing from field policy registry",
				b.Message, b.SignatureField, i, msgName, c.Field)
		}
	}
	return nil
}

// StreamTopologySection is the additive grant name for cardinality/order/
// boundary/action changes. It does not alone authorise content or host-owned
// changes. The recursive field-diff verifier that applies it lives in
// Migration B.
func StreamTopologySection() plugin_sdk.WriteSection { return plugin_sdk.SectionStreamWrite }

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
