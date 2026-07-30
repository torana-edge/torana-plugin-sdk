package plugin_sdk

import (
	"fmt"
	"sort"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Cross-hook mutation policy: what a plugin may CHANGE on the response and
// stream paths.
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
// So Suppress of a TextDelta needs ir.stream.write AND
// ir.messages.write.assistant; Suppress of Usage is forbidden; a one-for-one
// TextDelta rewrite needs only the assistant grant.
//
// Enforcement (fingerprinting) is Migration B and must not land on the
// per-event stream path without a stream-specific benchmark. This file is the
// vocabulary and inventory the verifier will use.

const (
	// SectionStreamWrite is the topology grant: Suppress, fan-out, event-kind
	// change, and content-block boundary changes. It is additive to semantic
	// content grants and never alone authorises altering host-owned facts.
	SectionStreamWrite WriteSection = "ir.stream.write"
)

// HostOwnedField marks a protobuf field no write grant covers. A plugin that
// changes, removes, or forges one has its whole output rejected.
const HostOwnedField = "<host-owned>"

// ChatResponseFieldSections maps every ChatResponse field to a grant or
// HostOwnedField. Observed answer facts are host-owned.
var ChatResponseFieldSections = map[string]string{
	"model":                    HostOwnedField, // model that actually answered
	"id":                       HostOwnedField,
	"message":                  string(SectionMessagesAssistant),
	"finish_reason":            string(SectionMessagesAssistant),
	"usage":                    HostOwnedField,
	"upstream_status":          HostOwnedField,
	"duration_ms":              HostOwnedField,
	"provider_extensions_json": HostOwnedField, // opaque provider output, not request params
}

// ResponseMessageFieldSections governs Message under ChatResponse.message.
var ResponseMessageFieldSections = map[string]string{
	"role":               HostOwnedField, // same policy as MessageStart.role
	"content":            string(SectionMessagesAssistant),
	"content_parts_json": string(SectionMessagesAssistant),
	"thinking":           string(SectionMessagesAssistant),
	"thinking_signature": HostOwnedField, // opaque provider binding token
	"redacted_thinking":  string(SectionMessagesAssistant),
	"tool_calls":         string(SectionMessagesAssistant),
	"tool_call_id":       string(SectionMessagesAssistant),
	"tool_name":          string(SectionMessagesAssistant),
	"cache_control_json": string(SectionMessagesAssistant),
}

// ResponseToolCallFieldSections governs ToolCall under ChatResponse.message.
var ResponseToolCallFieldSections = map[string]string{
	"id":             string(SectionMessagesAssistant),
	"name":           string(SectionMessagesAssistant),
	"arguments_json": string(SectionMessagesAssistant),
	"signature":      HostOwnedField, // opaque provider binding token
}

// UsageFieldSections: always host-owned.
var UsageFieldSections = map[string]string{
	"input_tokens":       HostOwnedField,
	"output_tokens":      HostOwnedField,
	"cache_read_tokens":  HostOwnedField,
	"cache_write_tokens": HostOwnedField,
}

// StreamEventVariantSections maps every StreamEvent oneof field. HostOwnedField
// means Suppress and Emit of that variant are forbidden.
var StreamEventVariantSections = map[string]string{
	"text_delta":          string(SectionMessagesAssistant),
	"thinking_delta":      string(SectionMessagesAssistant),
	"tool_call_delta":     string(SectionMessagesAssistant),
	"usage":               HostOwnedField,
	"error":               string(SectionStreamWrite),
	"signature_delta":     HostOwnedField,
	"message_start":       HostOwnedField, // nested role/id/model are host-owned
	"message_stop":        string(SectionMessagesAssistant),
	"content_block_start": string(SectionStreamWrite),
	"content_block_stop":  string(SectionStreamWrite),
}

var ToolCallDeltaFieldSections = map[string]string{
	"index":           string(SectionStreamWrite),
	"arguments_delta": string(SectionMessagesAssistant),
}

var StreamErrorFieldSections = map[string]string{
	"code":    string(SectionStreamWrite),
	"message": string(SectionStreamWrite),
}

var MessageStartFieldSections = map[string]string{
	"role":  HostOwnedField,
	"id":    HostOwnedField,
	"model": HostOwnedField, // observed answering model, not request selection
}

var MessageStopFieldSections = map[string]string{
	"finish_reason": string(SectionMessagesAssistant),
}

var ContentBlockStartFieldSections = map[string]string{
	"index":     string(SectionStreamWrite),
	"text":      string(SectionStreamWrite),
	"thinking":  string(SectionStreamWrite),
	"tool_call": string(SectionStreamWrite),
	"provider":  string(SectionStreamWrite),
}

var ToolCallRefFieldSections = map[string]string{
	"id":        string(SectionMessagesAssistant),
	"name":      string(SectionMessagesAssistant),
	"signature": HostOwnedField,
}

var ProviderBlockFieldSections = map[string]string{
	"kind": string(SectionStreamWrite),
}

var ContentBlockStopFieldSections = map[string]string{
	"index": string(SectionStreamWrite),
}

// StreamEventsFieldSections covers the emit_events wrapper. List cardinality is
// topology; each event has its own policy via RequiredStreamMutation.
var StreamEventsFieldSections = map[string]string{
	"events": string(SectionStreamWrite),
}

// HookResultActionSections maps every HookResult action oneof field.
// emit_events / suppress are topology actions that compose with per-event
// policies; replace_response uses ChatResponseFieldSections (host-owned
// fields still forbidden).
var HookResultActionSections = map[string]string{
	"replace_request":  string(SectionMessagesAssistant), // request inventory is capabilities_write.go
	"replace_response": string(SectionMessagesAssistant),
	"emit_events":      string(SectionStreamWrite),
	"serve_http":       HostOwnedField, // HTTP path has its own future vocabulary
	"tick_outcome":     HostOwnedField, // observational report
	"suppress":         string(SectionStreamWrite),
}

// outboundMessageFieldPolicies registers every nested message the recursive
// inventory must classify. Empty messages are listed with an empty map.
var outboundMessageFieldPolicies = map[protoreflect.FullName]map[string]string{
	"torana.v2.ChatResponse":      ChatResponseFieldSections,
	"torana.v2.Message":           ResponseMessageFieldSections,
	"torana.v2.ToolCall":          ResponseToolCallFieldSections,
	"torana.v2.Usage":             UsageFieldSections,
	"torana.v2.StreamEvent":       StreamEventVariantSections,
	"torana.v2.ToolCallDelta":     ToolCallDeltaFieldSections,
	"torana.v2.StreamError":       StreamErrorFieldSections,
	"torana.v2.MessageStart":      MessageStartFieldSections,
	"torana.v2.MessageStop":       MessageStopFieldSections,
	"torana.v2.ContentBlockStart": ContentBlockStartFieldSections,
	"torana.v2.ToolCallRef":       ToolCallRefFieldSections,
	"torana.v2.ProviderBlock":     ProviderBlockFieldSections,
	"torana.v2.ContentBlockStop":  ContentBlockStopFieldSections,
	"torana.v2.StreamEvents":      StreamEventsFieldSections,
	"torana.v2.TextBlock":         {},
	"torana.v2.ThinkingBlock":     {},
	"torana.v2.Suppress":          {},
	"torana.v2.HookResult":        HookResultActionSections,
}

// StreamTopologySection is the additive grant for cardinality/order/boundary/
// action changes. It does not alone authorise content or host-owned changes.
func StreamTopologySection() WriteSection { return SectionStreamWrite }

// EventCarriesHostOwned reports whether suppressing or forging ev would touch
// a host-owned fact.
func EventCarriesHostOwned(ev *pbv2.StreamEvent) bool {
	if ev == nil {
		return false
	}
	name := streamVariantName(ev)
	if name == "" {
		return false
	}
	return StreamEventVariantSections[name] == HostOwnedField
}

// SectionsForStreamEvent returns the semantic write sections represented by ev
// (excluding HostOwnedField). Callers add the topology grant separately when
// cardinality/kind/boundaries change.
func SectionsForStreamEvent(ev *pbv2.StreamEvent) []WriteSection {
	if ev == nil {
		return nil
	}
	name := streamVariantName(ev)
	section := StreamEventVariantSections[name]
	if section == "" || section == HostOwnedField {
		return nil
	}
	out := []WriteSection{WriteSection(section)}
	switch e := ev.Event.(type) {
	case *pbv2.StreamEvent_ToolCallDelta:
		if e.ToolCallDelta != nil && e.ToolCallDelta.ArgumentsDelta != "" {
			out = appendUniqueSection(out, SectionMessagesAssistant)
		}
	case *pbv2.StreamEvent_ContentBlockStart:
		if e.ContentBlockStart.GetToolCall() != nil {
			out = appendUniqueSection(out, SectionMessagesAssistant)
		}
	}
	return out
}

// RequiredStreamMutation reports the grants needed to turn input into outputs.
// An empty outputs slice is Suppress. Returns an error when the mutation would
// change, remove, or add a host-owned fact.
func RequiredStreamMutation(input *pbv2.StreamEvent, outputs []*pbv2.StreamEvent) ([]WriteSection, error) {
	inName := streamVariantName(input)

	if input != nil && EventCarriesHostOwned(input) {
		if len(outputs) == 0 {
			return nil, fmt.Errorf("suppressing a host-owned stream event (%s) is forbidden", inName)
		}
		return nil, fmt.Errorf("rewriting or fanning out a host-owned stream event (%s) is forbidden", inName)
	}
	for _, out := range outputs {
		if EventCarriesHostOwned(out) {
			return nil, fmt.Errorf("emitting a host-owned stream event (%s) is forbidden", streamVariantName(out))
		}
	}

	need := map[WriteSection]struct{}{}
	topology := false
	switch {
	case len(outputs) != 1:
		topology = true // Suppress or fan-out
	case inName != streamVariantName(outputs[0]):
		topology = true // kind change
	case isBoundaryVariant(inName):
		topology = true // boundary/index changes are topology
	}
	if topology {
		need[SectionStreamWrite] = struct{}{}
	}

	for _, s := range SectionsForStreamEvent(input) {
		need[s] = struct{}{}
	}
	for _, out := range outputs {
		for _, s := range SectionsForStreamEvent(out) {
			need[s] = struct{}{}
		}
	}

	result := make([]WriteSection, 0, len(need))
	for s := range need {
		result = append(result, s)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

func streamVariantName(ev *pbv2.StreamEvent) string {
	if ev == nil || ev.Event == nil {
		return ""
	}
	od := ev.ProtoReflect().Descriptor().Oneofs().ByName("event")
	fd := ev.ProtoReflect().WhichOneof(od)
	if fd == nil {
		return ""
	}
	return string(fd.Name())
}

func isBoundaryVariant(name string) bool {
	switch name {
	case "content_block_start", "content_block_stop", "message_stop", "error":
		return true
	}
	return false
}

func appendUniqueSection(s []WriteSection, add WriteSection) []WriteSection {
	for _, x := range s {
		if x == add {
			return s
		}
	}
	return append(s, add)
}

// OutboundPolicyFor returns the field→section map for a registered message.
func OutboundPolicyFor(name protoreflect.FullName) (map[string]string, bool) {
	m, ok := outboundMessageFieldPolicies[name]
	return m, ok
}
