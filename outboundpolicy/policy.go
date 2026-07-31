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
// fact is forbidden.
//
// Bound signatures are the ONE exception, and PolicyBoundSignature marks them
// rather than prose. An opaque provider token is host-owned — a plugin can
// never mint one — but CLEARING it is the prescribed response to legitimately
// changing the content it covers, so an unconditional host-owned rule would
// reject the SDK's own EmitAssembledToolCall output. ClassifySignatureMutation
// is the normative rule; do not restate it anywhere else.
//
// Signature comparison is TRANSACTIONAL over the binding's scope, never
// per-event. A tool block is compared as a whole — start id/name plus every
// arguments_delta sharing the index — between the accepted stream and the
// plugin's output. Judging at the initial suppression would reject every
// buffering assembler, which is precisely what StreamHandler does on the pass
// path: suppress fragments, then re-emit them byte-identically.
//
// Enforcement (recursive field diff + fingerprinting) is
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
//     kind/cardinality/boundaries change. Element count of a repeated
//     container is NOT constrained — use PolicyFixedContainer when it is.
//   - PolicyFixedContainer: like PolicyContainer (never charge the parent;
//     always recurse into children) AND requires equal element count with
//     positional correspondence: output element N is a mutation of accepted
//     element N. Adding, removing, or reordering elements rejects. Valid only
//     on repeated message fields. For elements with no stable provider ID,
//     positional identity is all the wire can prove.
//   - PolicySection / PolicyTopology on a message-valued field: if the same
//     message/oneof variant is present on both sides, recurse and do not
//     charge the parent; if presence or oneof selection changes, charge the
//     parent and recursively account for added/removed nested fields.
//   - PolicyHostOwned on a message: the whole subtree is immutable; any
//     nested change/remove/add rejects.
//   - Scalar fields apply their own policy directly. Optional scalars with
//     PolicySection (e.g. ResponseMessage.content): presence is host-owned /
//     fixed; the value is section-writable only when present on both sides.
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
	// PolicyBoundSignature marks an opaque provider signature that is host
	// owned but NOT unconditionally immutable: clearing it is the prescribed
	// response to legitimately mutating the content it covers.
	//
	// A plain PolicyHostOwned here would forbid the one mutation the SDK must
	// perform. EmitAssembledToolCall clears ToolCallRef.signature when a
	// plugin replaces the arguments, because the token no longer describes
	// what is being sent; a verifier built from an unconditional host-owned
	// rule would reject the SDK's own output.
	//
	// ClassifySignatureMutation is the single normative rule. Every field
	// carrying this kind must appear as a SignatureBinding.SignatureField and
	// vice versa — Validate enforces the pairing, so the two tables cannot
	// drift.
	PolicyBoundSignature
	// PolicyDelegate hands the nested value to another verifier (request,
	// response, stream, HTTP, tick). Not a field grant and not host-owned.
	PolicyDelegate
	// PolicyTopology maps to ir.stream.write when this aspect actually changes
	// (cardinality, order, boundaries, indexes, event kind).
	PolicyTopology
	// PolicyContainer marks a message-valued field that never contributes a
	// grant itself. Migration B always recurses into nested field policies
	// (see package comment nesting rules). Repeated containers under this
	// kind may change cardinality; use PolicyFixedContainer when they must not.
	PolicyContainer
	// PolicyFixedContainer is PolicyContainer plus fixed repeated cardinality
	// and positional order. See package comment nesting rules.
	PolicyFixedContainer
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

func boundSignaturePolicy() FieldPolicy {
	return FieldPolicy{kind: PolicyBoundSignature}
}

// SignatureMutation classifies what happened to a bound signature between the
// accepted value and a plugin's output. It is the ONE rule: the package
// comment, the registry, the SDK's emit path and the tests all state it
// through this function rather than restating it in prose.
type SignatureMutation int

const (
	// SignatureMutationInvalid is the zero value and never returned.
	SignatureMutationInvalid SignatureMutation = iota
	// SignatureIntact: bound content identical and token identical. Allowed —
	// this is pass-through and temporary suppress-then-re-emit. Buffering a
	// tool block and replaying it byte-identically is not deletion.
	SignatureIntact
	// SignatureCleared: bound content changed and the token was emptied.
	// Allowed, and required: the token no longer describes what ships.
	SignatureCleared
	// SignatureDropped: the token was emptied while the content it covers was
	// left identical. Rejected.
	//
	// The exception that lets a signature be cleared is narrowly "cleared
	// BECAUSE the covered content changed". Removing provenance from content
	// the provider did sign is just removing a host-owned fact, which the
	// package rule forbids, and it is indistinguishable from laundering.
	//
	// Nor is it merely lossy: providers can require the token on a later turn
	// (Gemini/Code Assist thoughtSignature is the live case), so dropping it
	// breaks replay rather than degrading gracefully.
	SignatureDropped
	// SignatureStale: bound content changed but the token was kept. Rejected —
	// this is the dangerous case, a valid-looking provider signature over
	// content the provider never signed.
	SignatureStale
	// SignatureForged: the token was replaced with a different non-empty
	// value. Always rejected, whether or not bound content changed. A plugin
	// cannot manufacture a provider signature.
	SignatureForged
	// SignatureAdded: a token appeared where the accepted value had none.
	// Always rejected, for the same reason as forgery.
	SignatureAdded
)

func (m SignatureMutation) Allowed() bool {
	return m == SignatureIntact || m == SignatureCleared
}

func (m SignatureMutation) String() string {
	switch m {
	case SignatureIntact:
		return "intact"
	case SignatureCleared:
		return "cleared"
	case SignatureDropped:
		return "dropped"
	case SignatureStale:
		return "stale"
	case SignatureForged:
		return "forged"
	case SignatureAdded:
		return "added"
	}
	return "invalid"
}

// ClassifySignatureMutation applies the settled bound-signature rule.
//
// boundContentChanged is decided over the WHOLE signature scope, not one
// event: for SignatureScopeToolCallBlockByIndex that means the assembled tool
// block (start id/name plus every arguments_delta sharing the index), compared
// between the accepted stream and the plugin's output. Classifying at the
// initial suppression would reject every buffering assembler, which is exactly
// what StreamHandler does on the pass path.
func ClassifySignatureMutation(accepted, returned string, boundContentChanged bool) SignatureMutation {
	switch {
	case accepted == returned:
		if accepted == "" {
			return SignatureIntact
		}
		if boundContentChanged {
			return SignatureStale
		}
		return SignatureIntact
	case returned == "":
		if !boundContentChanged {
			// Provenance stripped from content the provider actually signed.
			return SignatureDropped
		}
		return SignatureCleared
	case accepted == "":
		return SignatureAdded
	default:
		return SignatureForged
	}
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

func fixedContainerPolicy() FieldPolicy {
	return FieldPolicy{kind: PolicyFixedContainer}
}

func (p FieldPolicy) Kind() PolicyKind { return p.kind }

func (p FieldPolicy) IsHostOwned() bool { return p.kind == PolicyHostOwned }

// IsBoundSignature reports whether the field is an opaque provider token whose
// mutation is governed by ClassifySignatureMutation rather than by the
// unconditional host-owned rule.
func (p FieldPolicy) IsBoundSignature() bool { return p.kind == PolicyBoundSignature }

func (p FieldPolicy) IsContainer() bool { return p.kind == PolicyContainer }

// IsFixedContainer reports whether the field recurses into children under a
// fixed repeated cardinality and positional-order contract.
func (p FieldPolicy) IsFixedContainer() bool { return p.kind == PolicyFixedContainer }

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
	case PolicyBoundSignature:
		if p.section != "" || p.delegate != DelegateUnspecified {
			return fmt.Errorf("PolicyBoundSignature must not set Section or Delegate")
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
	case PolicyContainer, PolicyFixedContainer:
		if p.section != "" || p.delegate != DelegateUnspecified {
			return fmt.Errorf("%v must not set Section or Delegate", p.kind)
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
//
// Domain separates binding reachability from response-field reachability:
// request-side Message.thinking_signature remains normative after
// ResponseMessage dropped thinking from the response shape.
type SignatureBinding struct {
	Domain         SignatureDomain
	Message        protoreflect.FullName
	SignatureField string
	Content        []SignatureContentRef
}

// SignatureDomain says which path a binding governs and which inventory
// Validate uses to check it.
type SignatureDomain int

const (
	SignatureDomainUnspecified SignatureDomain = iota
	// SignatureDomainOutbound pairs with PolicyBoundSignature entries in the
	// response/stream field registries in this package.
	SignatureDomainOutbound
	// SignatureDomainRequest covers ChatRequest Message fields that are not
	// reachable from ChatResponse after ResponseMessage. Validated against
	// the request proto, not the outbound field registry.
	SignatureDomainRequest
)

func (d SignatureDomain) valid() bool {
	switch d {
	case SignatureDomainOutbound, SignatureDomainRequest:
		return true
	}
	return false
}

func (b SignatureBinding) validateShape() error {
	if !b.Domain.valid() {
		return fmt.Errorf("SignatureBinding requires Domain (request or outbound)")
	}
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
		// Request-side only: ChatRequest.messages still use Message. An
		// assistant-writing plugin may rewrite historical thinking; the
		// provider token must clear or the mutation must reject. Not on
		// ResponseMessage — that shape deliberately omits thinking.
		Domain:         SignatureDomainRequest,
		Message:        "torana.v2.Message",
		SignatureField: "thinking_signature",
		Content: []SignatureContentRef{
			{Scope: SignatureScopeSameMessage, Field: "thinking"},
			{Scope: SignatureScopeSameMessage, Field: "redacted_thinking"},
		},
	},
	{
		Domain:         SignatureDomainOutbound,
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
		Domain:         SignatureDomainOutbound,
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
		Domain:         SignatureDomainOutbound,
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
	"finish_reason":            hostOwnedPolicy(),
	"usage":                    hostOwnedPolicy(),
	"upstream_status":          hostOwnedPolicy(),
	"duration_ms":              hostOwnedPolicy(),
	"provider_extensions_json": hostOwnedPolicy(),
}

var responseMessageFieldPolicies = map[string]FieldPolicy{
	// Presence is host-owned/fixed; value is assistant-writable when present
	// on both sides (see package comment optional-scalar rule).
	"content": sectionPolicy(plugin_sdk.SectionMessagesAssistant),
	// Fixed cardinality/order: recurse into ToolCall children for in-place
	// name/arguments edits. Positional identity for anonymous (empty-ID) calls.
	"tool_calls": fixedContainerPolicy(),
}

var responseToolCallFieldPolicies = map[string]FieldPolicy{
	"id":             hostOwnedPolicy(),
	"name":           sectionPolicy(plugin_sdk.SectionMessagesAssistant),
	"arguments_json": sectionPolicy(plugin_sdk.SectionMessagesAssistant),
	"signature":      boundSignaturePolicy(),
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
	"signature_delta":     boundSignaturePolicy(),
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
	"signature": boundSignaturePolicy(),
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
	"torana.v2.ResponseMessage":   responseMessageFieldPolicies,
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
//   - StreamHandler ReplaceToolArguments clears ToolCallRef.signature in the
//     re-emitted ContentBlockStart (SDK encodes this; host may also clear)
//   - transactional tool-call buffering: suppress start+deltas then re-emit an
//     identical assembled block (pass / fail-open) is NOT suppress/forge of the
//     host-owned signature — verify across the whole tool block, not per event
//   - parallel tool calls: mutating block B args must not be gated by block A's signature
//   - multi-block: signature_delta binds current text/thinking block only
//
// Topology / indexes / stream state:
//   - one-for-one TextDelta rewrite → assistant only
//   - suppress TextDelta → topology + assistant
//   - indexes unique across the entire streamed message; never reused after close
//   - at most one content block open; second start before stop → reject
//   - text/thinking/CurrentContentBlock signature_delta with no open block → reject
//   - duplicate index after prior block closed → reject
//   - MessageStop/end-of-stream with open block → reject (unless already
//     terminally aborted by StreamError)
//   - start(tool), partial args, StreamError → valid terminal abort (discard buffer)
//   - start(text), text delta, StreamError → valid terminal abort
//   - StreamError then any later event → reject
//   - start(...), ordinary EOF without stop/error → reject
//   - sequential parallel tool-call blocks with distinct indexes → accept
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

// Validate checks that the outbound enforcement inventory is complete and
// consistent with protobuf descriptors. Hosts and linters should call this
// once at process start. Guests must not import this package.
//
// Checks:
//   - every protobuf field in every reachable registered message has exactly
//     one policy; no policy names a missing field;
//   - every nested message reachable without an explicit empty Delegate is
//     registered; every DelegateKind has a target entry and every named target
//     has a registry;
//   - PolicyContainer / PolicyDelegate only on message-valued fields;
//   - PolicyFixedContainer only on repeated message-valued fields;
//   - signature bindings validate against proto + host-owned signature fields.
func Validate() error {
	descs := outboundDescriptors()

	for _, d := range []DelegateKind{
		DelegateRequest, DelegateResponse, DelegateStream, DelegateHTTP, DelegateTick,
	} {
		targets, ok := outboundDelegateTargets[d]
		if !ok {
			return fmt.Errorf("delegate %v has no target entry", d)
		}
		for _, target := range targets {
			if _, ok := outboundMessageFieldPolicies[target]; !ok {
				return fmt.Errorf("delegate %v target %s has no field-policy registry", d, target)
			}
			if _, ok := descs[target]; !ok {
				return fmt.Errorf("delegate %v target %s has no protobuf descriptor", d, target)
			}
		}
	}

	roots := []proto.Message{
		&pbv2.ChatResponse{},
		&pbv2.StreamEvent{},
		&pbv2.StreamEvents{},
		&pbv2.HookResult{},
		&pbv2.Suppress{},
	}
	seen := map[protoreflect.FullName]bool{}
	for _, root := range roots {
		if err := validateMessageCompleteness(root.ProtoReflect().Descriptor(), seen, descs); err != nil {
			return err
		}
	}

	for _, b := range signatureBindings {
		if err := b.validateShape(); err != nil {
			return err
		}
		switch b.Domain {
		case SignatureDomainOutbound:
			if err := validateOutboundSignatureBindingAgainstProto(b, descs); err != nil {
				return err
			}
		case SignatureDomainRequest:
			if err := validateRequestSignatureBindingAgainstProto(b, descs); err != nil {
				return err
			}
		}
	}
	if err := validateBoundSignaturePairing(); err != nil {
		return err
	}
	return nil
}

// validateBoundSignaturePairing checks that the field registry and the
// signature-binding table describe the same set of tokens.
//
// The two are separate structures saying related things: the registry says
// "this field is a bound signature", the binding says "this is the content it
// covers". Either alone is silently wrong. A bound signature with no binding
// is a token nothing defines the scope of, so a verifier cannot compute
// boundContentChanged and has no basis to allow clearing. A binding over a
// field the registry still calls unconditionally host-owned is precisely the
// contradiction this pairing exists to prevent — the SDK clears the token and
// a faithful verifier rejects it.
//
// Enforcing the correspondence here means the drift cannot survive a host
// start, rather than being caught by whoever notices the behaviour.
func validateBoundSignaturePairing() error {
	bound := map[protoreflect.FullName]map[string]bool{}
	for msg, fields := range outboundMessageFieldPolicies {
		for name, p := range fields {
			if p.kind == PolicyBoundSignature {
				if bound[msg] == nil {
					bound[msg] = map[string]bool{}
				}
				bound[msg][name] = true
			}
		}
	}
	type bindingKey struct {
		msg   protoreflect.FullName
		field string
	}
	claimed := map[bindingKey]bool{}

	for _, b := range signatureBindings {
		if b.Domain != SignatureDomainOutbound {
			continue
		}
		fields, ok := outboundMessageFieldPolicies[b.Message]
		if !ok {
			return fmt.Errorf("signature binding %s.%s: message has no field registry",
				b.Message, b.SignatureField)
		}
		p, ok := fields[b.SignatureField]
		if !ok {
			return fmt.Errorf("signature binding %s.%s: field has no policy",
				b.Message, b.SignatureField)
		}
		if p.kind != PolicyBoundSignature {
			return fmt.Errorf("signature binding %s.%s: policy is %v, want PolicyBoundSignature — "+
				"a binding says the token may be cleared when its content changes, so an "+
				"unconditionally host-owned policy would reject the SDK's own output",
				b.Message, b.SignatureField, p.kind)
		}
		// One token, one scope. Deleting from the tracking map is idempotent,
		// so a second binding for the same field used to pass silently and give
		// that token two different definitions of what it covers — a verifier
		// iterating the exported bindings would have no unique contract to
		// implement.
		key := bindingKey{b.Message, b.SignatureField}
		if claimed[key] {
			return fmt.Errorf("signature binding %s.%s is declared more than once: "+
				"a token must have exactly one definition of the content it covers",
				b.Message, b.SignatureField)
		}
		claimed[key] = true

		// Duplicate content refs within one binding are redundant at best and
		// contradictory at worst, and make a verifier's scope ambiguous.
		seenRef := map[SignatureContentRef]bool{}
		for _, c := range b.Content {
			if seenRef[c] {
				return fmt.Errorf("signature binding %s.%s lists content ref %s.%s (scope %v) twice",
					b.Message, b.SignatureField, c.Message, c.Field, c.Scope)
			}
			seenRef[c] = true
		}

		delete(bound[b.Message], b.SignatureField)
	}
	for msg, fields := range bound {
		for name := range fields {
			return fmt.Errorf("%s.%s is PolicyBoundSignature with no SignatureBinding: "+
				"nothing defines what content the token covers, so a verifier cannot tell "+
				"a legitimate clear from a discarded fact", msg, name)
		}
	}
	return nil
}

func validateMessageCompleteness(
	desc protoreflect.MessageDescriptor,
	seen map[protoreflect.FullName]bool,
	descs map[protoreflect.FullName]protoreflect.MessageDescriptor,
) error {
	name := desc.FullName()
	if seen[name] {
		return nil
	}
	seen[name] = true

	fields, ok := outboundMessageFieldPolicies[name]
	if !ok {
		return fmt.Errorf("%s is reachable from an outbound root but has no field-policy registry", name)
	}
	if _, ok := descs[name]; !ok {
		return fmt.Errorf("%s has a registry but no protobuf descriptor", name)
	}

	protoFields := map[string]protoreflect.FieldDescriptor{}
	fds := desc.Fields()
	for i := 0; i < fds.Len(); i++ {
		fd := fds.Get(i)
		fname := string(fd.Name())
		protoFields[fname] = fd
		p, ok := fields[fname]
		if !ok {
			return fmt.Errorf("%s.%s belongs to no grant/policy — inventory incomplete", name, fname)
		}
		if err := p.validate(); err != nil {
			return fmt.Errorf("%s.%s: %w", name, fname, err)
		}
		switch p.Kind() {
		case PolicyContainer, PolicyDelegate:
			if fd.Kind() != protoreflect.MessageKind {
				return fmt.Errorf("%s.%s: %v requires a message field, got %v", name, fname, p.Kind(), fd.Kind())
			}
		case PolicyFixedContainer:
			if fd.Kind() != protoreflect.MessageKind {
				return fmt.Errorf("%s.%s: PolicyFixedContainer requires a message field, got %v", name, fname, fd.Kind())
			}
			if !fd.IsList() {
				return fmt.Errorf("%s.%s: PolicyFixedContainer requires a repeated message field", name, fname)
			}
		}

		if p.Kind() == PolicyDelegate {
			d, ok := p.Delegate()
			if !ok {
				return fmt.Errorf("%s.%s: PolicyDelegate without kind", name, fname)
			}
			targets, ok := outboundDelegateTargets[d]
			if !ok {
				return fmt.Errorf("%s.%s delegates to unknown verifier %v", name, fname, d)
			}
			for _, target := range targets {
				td, ok := descs[target]
				if !ok {
					return fmt.Errorf("%s.%s delegate target %s has no descriptor", name, fname, target)
				}
				if err := validateMessageCompleteness(td, seen, descs); err != nil {
					return err
				}
			}
			// Empty targets (request/http/tick deferred) intentionally skip the
			// field's protobuf message type — inventory lives elsewhere.
			continue
		}

		if fd.Kind() != protoreflect.MessageKind {
			continue
		}
		child := fd.Message()
		if _, ok := outboundMessageFieldPolicies[child.FullName()]; !ok {
			return fmt.Errorf("%s.%s points at unregistered nested message %s", name, fname, child.FullName())
		}
		if err := validateMessageCompleteness(child, seen, descs); err != nil {
			return err
		}
	}
	for fname := range fields {
		if _, ok := protoFields[fname]; !ok {
			return fmt.Errorf("%s.%s is mapped but no longer exists in the proto", name, fname)
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
		&pbv2.ChatRequest{},
		&pbv2.Message{},
		&pbv2.ResponseMessage{},
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

func validateOutboundSignatureBindingAgainstProto(b SignatureBinding, descs map[protoreflect.FullName]protoreflect.MessageDescriptor) error {
	sigDesc, ok := descs[b.Message]
	if !ok {
		return fmt.Errorf("signature message %s unknown to proto inventory", b.Message)
	}
	sigFD := sigDesc.Fields().ByName(protoreflect.Name(b.SignatureField))
	if sigFD == nil {
		return fmt.Errorf("%s has no field %s", b.Message, b.SignatureField)
	}
	// The registry pairing (PolicyBoundSignature ⇔ SignatureBinding) is checked
	// once, in validateBoundSignaturePairing. Repeating a weaker version of it
	// here is how the two tables drifted in the first place.
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

// validateRequestSignatureBindingAgainstProto checks request-domain bindings
// against ChatRequest Message descriptors only. They must not require an
// outbound field registry — ResponseMessage deliberately dropped those fields.
func validateRequestSignatureBindingAgainstProto(b SignatureBinding, descs map[protoreflect.FullName]protoreflect.MessageDescriptor) error {
	if b.Domain != SignatureDomainRequest {
		return fmt.Errorf("validateRequestSignatureBindingAgainstProto called for domain %v", b.Domain)
	}
	sigDesc, ok := descs[b.Message]
	if !ok {
		return fmt.Errorf("request signature message %s unknown to proto inventory", b.Message)
	}
	if sigDesc.Fields().ByName(protoreflect.Name(b.SignatureField)) == nil {
		return fmt.Errorf("%s has no field %s", b.Message, b.SignatureField)
	}
	if _, ok := outboundMessageFieldPolicies[b.Message]; ok {
		return fmt.Errorf("request signature binding %s.%s: message must not be in the outbound "+
			"field registry (response shape must stay free of request-only fields)",
			b.Message, b.SignatureField)
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

// AllSignatureBindings returns a deep copy of the opaque-signature inventory
// across request and outbound domains. Hosts must consume this complete set —
// response-shape narrowing must not drop request-side bindings.
func AllSignatureBindings() []SignatureBinding {
	out := make([]SignatureBinding, len(signatureBindings))
	for i, b := range signatureBindings {
		out[i] = SignatureBinding{
			Domain:         b.Domain,
			Message:        b.Message,
			SignatureField: b.SignatureField,
			Content:        append([]SignatureContentRef(nil), b.Content...),
		}
	}
	return out
}
