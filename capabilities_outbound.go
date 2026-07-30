package plugin_sdk

// Cross-hook mutation policy: what a plugin may CHANGE on the response and
// stream paths.
//
// Request-path write grants (capabilities_write.go) already cover ChatRequest.
// v2 also lets a plugin replace a ChatResponse and emit/suppress StreamEvents,
// and those surfaces had no vocabulary at all — a plugin could rewrite the
// assistant turn, forge usage, or inject stream errors with no grant.
//
// Principle: IR-scoped grants apply across hooks for the same semantic area.
// Rewriting assistant content on the response or stream path needs
// ir.messages.write.assistant, the same grant that covers assistant messages
// on the request path. Splitting into a parallel or.* namespace would double
// the approval surface without a product reason.
//
// One new grant covers structural stream operations that are not "edit
// assistant text": Suppress, fan-out, block boundary changes, and emitting a
// StreamError. That is ir.stream.write (listed in WritePermissions).
//
// Some fields are host-owned: changing them is a protocol violation, not a
// grantable edit. The inventory maps below mark those with HostOwnedField so
// a descriptor walk fails the moment a new field lands without a decision.
//
// Enforcement (section fingerprinting, reject-and-failure_mode) is Migration B.
// This file is the vocabulary and the inventory the verifier will use.

const (
	// SectionStreamWrite covers structural stream mutations: Suppress, Emit
	// fan-out, content-block boundaries, and emitting StreamError. Editing
	// assistant text/thinking/tool-call arguments still needs
	// ir.messages.write.assistant (or ir.model.write for MessageStart.model).
	SectionStreamWrite WriteSection = "ir.stream.write"
)

// HostOwnedField marks a protobuf field no write grant covers. A plugin that
// changes one has its whole output rejected — the same rule as
// ChatRequest.torana_meta_json on the request path.
const HostOwnedField = "<host-owned>"

// ChatResponseFieldSections maps every ChatResponse protobuf field name to the
// grant that governs changing it, or HostOwnedField.
var ChatResponseFieldSections = map[string]string{
	"model":                    string(SectionModel),
	"id":                       HostOwnedField, // provider identity
	"message":                  string(SectionMessagesAssistant),
	"finish_reason":            string(SectionMessagesAssistant),
	"usage":                    HostOwnedField, // billing / observability
	"upstream_status":          HostOwnedField, // host-measured
	"duration_ms":              HostOwnedField, // host-measured
	"provider_extensions_json": string(SectionParams),
}

// ResponseMessageFieldSections governs nested Message fields under
// ChatResponse.message. Roles other than assistant are unexpected on a
// response turn; the assistant grant still covers the tree.
var ResponseMessageFieldSections = map[string]string{
	"role":               string(SectionMessagesAssistant),
	"content":            string(SectionMessagesAssistant),
	"content_parts_json": string(SectionMessagesAssistant),
	"thinking":           string(SectionMessagesAssistant),
	"thinking_signature": string(SectionMessagesAssistant),
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
	"signature":      string(SectionMessagesAssistant),
}

// UsageFieldSections: Usage appears on ChatResponse and as a StreamEvent
// variant. Always host-owned — forging tokens is forging the bill.
var UsageFieldSections = map[string]string{
	"input_tokens":       HostOwnedField,
	"output_tokens":      HostOwnedField,
	"cache_read_tokens":  HostOwnedField,
	"cache_write_tokens": HostOwnedField,
}

// StreamEventVariantSections maps every StreamEvent oneof field name to the
// grant governing emitting or replacing that variant.
var StreamEventVariantSections = map[string]string{
	"text_delta":          string(SectionMessagesAssistant),
	"thinking_delta":      string(SectionMessagesAssistant),
	"tool_call_delta":     string(SectionMessagesAssistant),
	"usage":               HostOwnedField,
	"error":               string(SectionStreamWrite),
	"signature_delta":     string(SectionMessagesAssistant),
	"message_start":       string(SectionStreamWrite),
	"message_stop":        string(SectionMessagesAssistant),
	"content_block_start": string(SectionStreamWrite),
	"content_block_stop":  string(SectionStreamWrite),
}

// ToolCallDeltaFieldSections governs nested ToolCallDelta fields.
var ToolCallDeltaFieldSections = map[string]string{
	"index":           string(SectionStreamWrite),
	"arguments_delta": string(SectionMessagesAssistant),
}

// StreamErrorFieldSections governs nested StreamError fields when emitted.
var StreamErrorFieldSections = map[string]string{
	"code":    string(SectionStreamWrite),
	"message": string(SectionStreamWrite),
}

// MessageStartFieldSections governs nested MessageStart fields.
var MessageStartFieldSections = map[string]string{
	"role":  HostOwnedField, // provider/session fact
	"id":    HostOwnedField,
	"model": string(SectionModel),
}

// MessageStopFieldSections governs nested MessageStop fields.
var MessageStopFieldSections = map[string]string{
	"finish_reason": string(SectionMessagesAssistant),
}

// ContentBlockStartFieldSections governs ContentBlockStart (index + block oneof).
var ContentBlockStartFieldSections = map[string]string{
	"index":     string(SectionStreamWrite),
	"text":      string(SectionStreamWrite),
	"thinking":  string(SectionStreamWrite),
	"tool_call": string(SectionStreamWrite),
	"provider":  string(SectionStreamWrite),
}

// ToolCallRefFieldSections governs ToolCallRef under ContentBlockStart.
var ToolCallRefFieldSections = map[string]string{
	"id":        string(SectionMessagesAssistant),
	"name":      string(SectionMessagesAssistant),
	"signature": string(SectionMessagesAssistant),
}

// ProviderBlockFieldSections governs ProviderBlock under ContentBlockStart.
var ProviderBlockFieldSections = map[string]string{
	"kind": string(SectionStreamWrite),
}

// ContentBlockStopFieldSections governs ContentBlockStop.
var ContentBlockStopFieldSections = map[string]string{
	"index": string(SectionStreamWrite),
}

// StreamSuppressSection is the grant required to return Suppress on a stream
// hook (drop the input event and emit nothing).
func StreamSuppressSection() WriteSection { return SectionStreamWrite }
