package v2

// Normative request-replacement validation (the approved marshal-failure
// prerequisite, SDK unit).
//
// These are REPLACEMENT-OUTPUT rules: they validate what a plugin RETURNS as
// a ReplaceRequest, so the plugin and the provider can never be made to
// inspect different logical requests. Accepted host input is normalized into
// this closed domain before plugin dispatch (the Edge side performs the
// nil-to-{} normalization and jsontext-clean parsing); this validator is the
// single normative statement of the domain.
//
// The rules, per the approved executable field contract:
//
//	ChatRequest.torana_meta_json      SDK ABSOLUTE: absent | strict JSON object
//	                                 HOST RELATIONAL: bytes must equal the accepted
//	                                 input; no grant authorizes changes
//	ChatRequest.provider_extensions_json  absent | JSON object
//	ChatRequest.safety_settings_json  absent | JSON array    (Gemini shape)
//	ChatRequest.temperature / top_p   finite when present    (no invented ranges)
//	Message.role                      non-empty UTF-8; blocks REQUIRED (>= 1)
//	Message.blocks                    the SOLE body authority: ordered
//	                                 RequestBlock sequence, provider wire
//	                                 order; no competing flat content fields
//	RequestTextBlock                  text (explicit empty first-class),
//	                                 optional provenance-governed signature
//	RequestThinkingBlock              text + current-block signature
//	RequestRedactedThinkingBlock      data
//	RequestToolUseBlock               non-empty id, non-empty name,
//	                                 arguments_json REQUIRED non-empty JSON object ({} canonical),
//	                                 optional provenance-governed signature
//	RequestToolResultBlock            non-empty tool_call_id, optional tool_name,
//	                                 NON-EMPTY ordered NESTED
//	                                 ToolResultContentBlock content (one
//	                                 explicit empty text element is the
//	                                 canonical empty-result spelling; an empty
//	                                 list is refused — text/unknown/cache kinds
//	                                 only, nested tool use/result/thinking/
//	                                 signature is unrepresentable at the type
//	                                 level)
//	RequestCacheBreakpoint            marker_json REQUIRED JSON object;
//	                                 positional: closes the cached prefix
//	RequestUnknownBlock               non-empty provider kind,
//	                                 payload_json REQUIRED strict JSON object.
//	                                 The kind-specific projection invariant
//	                                 (no discriminant / canonical cache member
//	                                 inside the payload) is the PROVIDER
//	                                 ADAPTER's marshal validator — an
//	                                 executable edge obligation; the SDK has
//	                                 no provider vocabulary to prove it
//	RequestTrailingSignatureBlock     non-empty token, ASSISTANT-ONLY, FINAL
//	ToolResultContentBlock            oneof text | unknown | cache_breakpoint
//	ToolDef                           non-empty name,
//	                                 parameters_json REQUIRED non-empty JSON object
//	                                 ({} is a valid unconstrained schema; empty bytes are not),
//	                                 cache_control_json absent | JSON object
//
// These rules are ABSOLUTE STRUCTURAL validity: they validate what a plugin
// RETURNS as a ReplaceRequest with no accepted request in sight, so the
// plugin and the provider can never be made to inspect different logical
// requests. Relational provenance (signature binding, grants, host-owned
// facts, cache-coverage union) is the HOST verifier's job, layered on top.
//
// JSON fields are validated with the shared strict JSON-text rules (valid
// UTF-8, paired surrogates, unique decoded member names, single top-level
// value — see jsontext), then checked for their documented top-level shape.
// Empty bytes are absence ONLY for absent-capable fields; a literal JSON null
// is a wrong top-level value everywhere. Unknown protobuf fields are refused
// at every level (the contract is closed; the host cannot know their
// semantics). Nil nested elements and typed-nil oneof wrappers are refused
// without panicking. Every protobuf string is valid UTF-8 (descriptor-driven
// walk — Go strings are NOT valid UTF-8 by construction, only wire-decoded
// protobuf strings are).
//
// Generic ToolCall is RESPONSE-side only (ResponseMessage.tool_calls and the
// stream's ToolCallRef); request tool calls are RequestToolUseBlock blocks.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"unicode/utf8"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/torana-edge/torana-plugin-sdk/pb/v2/jsontext"
)

// jsonFieldRule is one entry of the executable field table.
type jsonFieldRule struct {
	// shape is the documented top-level JSON type: "object" or "array".
	shape string
	// required means empty bytes are invalid (the field must carry the
	// canonical empty shape: {} or []).
	required bool
}

// requestJSONFields is the executable JSON-field table: every JSON byte
// field of a request replacement mapped to its shape and absence capability.
// TestReplacementFieldInventory walks the descriptors against the same
// names, so an additive v2 field fails until a rule is decided here.
var requestJSONFields = map[string]jsonFieldRule{
	"torana.v2.ChatRequest.torana_meta_json":          {shape: "object"},
	"torana.v2.ChatRequest.provider_extensions_json":  {shape: "object"},
	"torana.v2.ChatRequest.safety_settings_json":      {shape: "array"},
	"torana.v2.RequestToolUseBlock.arguments_json":    {shape: "object", required: true},
	"torana.v2.RequestUnknownBlock.payload_json":      {shape: "object", required: true},
	"torana.v2.RequestCacheBreakpoint.marker_json":    {shape: "object", required: true},
	"torana.v2.ToolResultUnknownBlock.payload_json":   {shape: "object", required: true},
	"torana.v2.ToolResultCacheBreakpoint.marker_json": {shape: "object", required: true},
	// Response-side ToolCall (ResponseMessage.tool_calls): declared for
	// inventory totality; the request validator never walks it — the
	// response validator governs its shape.
	"torana.v2.ToolCall.arguments_json":    {shape: "object", required: true},
	"torana.v2.ToolDef.parameters_json":    {shape: "object", required: true},
	"torana.v2.ToolDef.cache_control_json": {shape: "object"},
}

// requestScalarRules declares a rule class for every non-JSON field of the
// request-visible messages (the ChatRequest tree: messages, blocks, leaves,
// nested tool-result content, tools). The inventory test forces additive fields
// to be declared here, and each class is either ENFORCED by the validator or
// documented as inherent/unconstrained:
//
//   - enforced: "repeated-message-nonnil", "float-finite-optional",
//     "int32-positive-optional", "text-required-utf8";
//   - "text-utf8" and "repeated-text-utf8": UTF-8 validity is ENFORCED by
//     the descriptor-driven string walk (checkStringsUTF8) — Go strings are
//     NOT valid UTF-8 by construction, only wire-decoded protobuf strings
//     are; emptiness is allowed;
//   - "bool": protobuf scalars are well-typed by construction;
//   - deliberately unconstrained beyond UTF-8: content fields (role,
//     content, thinking, signatures, description) carry no universal
//     constraint.
var requestScalarRules = map[string]string{
	// ChatRequest
	"torana.v2.ChatRequest.model":          "text-utf8",
	"torana.v2.ChatRequest.messages":       "repeated-message-nonnil",
	"torana.v2.ChatRequest.tools":          "repeated-message-nonnil",
	"torana.v2.ChatRequest.stream":         "bool",
	"torana.v2.ChatRequest.max_tokens":     "int32-positive-optional",
	"torana.v2.ChatRequest.temperature":    "float-finite-optional",
	"torana.v2.ChatRequest.top_p":          "float-finite-optional",
	"torana.v2.ChatRequest.stop_sequences": "repeated-text-utf8",
	// Message
	// role is non-empty UTF-8 in the request domain: an empty role is not
	// a meaningful request message, and a nil Message element survives
	// protobuf transport as a zero-length message that would otherwise
	// decode to exactly this empty state. The catch-all role decision
	// stays open — there is deliberately no closed enum. blocks must be
	// non-empty (an explicit empty provider message is ONE text block with
	// text == ""; an empty list is not a second spelling).
	"torana.v2.Message.role":   "text-required-utf8",
	"torana.v2.Message.blocks": "repeated-message-nonnil",
	// RequestBlock oneof member fields (kind arms; typed-nil refused by the
	// structural block walk).
	"torana.v2.RequestBlock.text":               "oneof-message-member",
	"torana.v2.RequestBlock.thinking":           "oneof-message-member",
	"torana.v2.RequestBlock.redacted_thinking":  "oneof-message-member",
	"torana.v2.RequestBlock.tool_use":           "oneof-message-member",
	"torana.v2.RequestBlock.tool_result":        "oneof-message-member",
	"torana.v2.RequestBlock.cache_breakpoint":   "oneof-message-member",
	"torana.v2.RequestBlock.unknown":            "oneof-message-member",
	"torana.v2.RequestBlock.trailing_signature": "oneof-message-member",
	// RequestTextBlock
	"torana.v2.RequestTextBlock.text":      "text-utf8",
	"torana.v2.RequestTextBlock.signature": "text-utf8",
	// RequestThinkingBlock
	"torana.v2.RequestThinkingBlock.text":      "text-utf8",
	"torana.v2.RequestThinkingBlock.signature": "text-utf8",
	// RequestRedactedThinkingBlock
	"torana.v2.RequestRedactedThinkingBlock.data": "text-utf8",
	// RequestToolUseBlock
	"torana.v2.RequestToolUseBlock.id":        "text-required-utf8",
	"torana.v2.RequestToolUseBlock.name":      "text-required-utf8",
	"torana.v2.RequestToolUseBlock.signature": "text-utf8",
	// RequestToolResultBlock
	"torana.v2.RequestToolResultBlock.tool_call_id": "text-required-utf8",
	"torana.v2.RequestToolResultBlock.tool_name":    "text-utf8",
	"torana.v2.RequestToolResultBlock.content":      "repeated-message-nonnil",
	// ToolResultContentBlock oneof member fields
	"torana.v2.ToolResultContentBlock.text":             "oneof-message-member",
	"torana.v2.ToolResultContentBlock.unknown":          "oneof-message-member",
	"torana.v2.ToolResultContentBlock.cache_breakpoint": "oneof-message-member",
	// ToolResultTextBlock
	"torana.v2.ToolResultTextBlock.text": "text-utf8",
	// ToolResultUnknownBlock
	"torana.v2.ToolResultUnknownBlock.kind": "text-required-utf8",
	// RequestUnknownBlock
	"torana.v2.RequestUnknownBlock.kind": "text-required-utf8",
	// RequestTrailingSignatureBlock
	"torana.v2.RequestTrailingSignatureBlock.signature": "text-required-utf8",
	// ToolCall — RESPONSE-side only now (ResponseMessage.tool_calls). ID is
	// host-owned and may legitimately be absent for anonymous response
	// calls; requiredness is the response validator's job.
	"torana.v2.ToolCall.id":        "text-utf8",
	"torana.v2.ToolCall.name":      "text-required-utf8",
	"torana.v2.ToolCall.signature": "text-utf8",
	// ToolDef
	"torana.v2.ToolDef.name":        "text-required-utf8",
	"torana.v2.ToolDef.description": "text-utf8",
	"torana.v2.ToolDef.strict":      "bool",
}

// checkStringsUTF8 enforces UTF-8 validity on every protobuf string field
// reachable from m, recursively through message fields with list indexes in
// the error path. Go guests construct these values IN MEMORY, where Go
// strings may carry arbitrary bytes — protobuf's UTF-8 guarantee holds only
// after successful wire decoding, so a contract that skipped this check
// would accept a replacement the trampoline's own proto.Marshal rejects, and
// the advertised domain would differ between Go SDK guests and handwritten
// wire guests. Descriptor-driven by construction: there is no partial
// hand-maintained list, so an additive string field is covered automatically
// (and still forces a deliberate inventory decision).
func checkStringsUTF8(m protoreflect.Message, path string) error {
	var firstErr error
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		full := path + "." + string(fd.Name())
		switch {
		case fd.Kind() == protoreflect.StringKind && !fd.IsList():
			if !utf8.ValidString(v.String()) {
				firstErr = fmt.Errorf("%s: invalid UTF-8", full)
				return false
			}
		case fd.Kind() == protoreflect.StringKind && fd.IsList():
			list := v.List()
			for i := 0; i < list.Len(); i++ {
				if !utf8.ValidString(list.Get(i).String()) {
					firstErr = fmt.Errorf("%s[%d]: invalid UTF-8", full, i)
					return false
				}
			}
		case fd.Kind() == protoreflect.MessageKind && !fd.IsList():
			msg := v.Message()
			if !msg.IsValid() {
				return true
			}
			if err := checkStringsUTF8(msg, full); err != nil {
				firstErr = err
				return false
			}
		case fd.Kind() == protoreflect.MessageKind && fd.IsList():
			list := v.List()
			for i := 0; i < list.Len(); i++ {
				elem := list.Get(i).Message()
				if !elem.IsValid() {
					continue
				}
				if err := checkStringsUTF8(elem, fmt.Sprintf("%s[%d]", full, i)); err != nil {
					firstErr = err
					return false
				}
			}
		}
		return true
	})
	return firstErr
}

// checkNoUnknown refuses a message carrying unknown protobuf fields.
func checkNoUnknown(m protoreflect.Message, what string) error {
	if len(m.GetUnknown()) != 0 {
		return fmt.Errorf("%s carries unknown protobuf fields", what)
	}
	return nil
}

// validateJSONField applies the jsontext rules plus the documented top-level
// shape to one byte field. Empty bytes are absence for absent-capable fields
// and invalid for required ones; a literal JSON null is a wrong top-level
// value everywhere.
func validateJSONField(raw []byte, field string, rule jsonFieldRule) error {
	if len(raw) == 0 {
		if rule.required {
			return fmt.Errorf("%s must be a non-empty JSON %s (use the canonical empty shape)", field, rule.shape)
		}
		return nil
	}
	if err := jsontext.Validate(raw); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	// Shape discrimination only, via a UseNumber decode: jsontext already
	// guarantees structural strictness, and UseNumber keeps large numerics
	// (e.g. 1e999) from failing the shape check — the bytes travel verbatim
	// to the provider, so the validator must never reject a document a
	// provider-side parser would accept. A shape check that overflowed on
	// numbers would be a NEW differential.
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("%s is not valid JSON: %w", field, err)
	}
	switch rule.shape {
	case "object":
		if _, ok := v.(map[string]any); !ok {
			return fmt.Errorf("%s must be a JSON object", field)
		}
	case "array":
		if _, ok := v.([]any); !ok {
			return fmt.Errorf("%s must be a JSON array", field)
		}
	}
	return nil
}

// ValidateReplacement reports whether x is a well-formed request
// replacement: the executable field table, the universal structural rules,
// and the request-context identity rules all hold. It is the normative
// contract consumed by HookResult.ValidateFor and by the host's
// handwritten-guest/unconditional verifier.
func (x *ChatRequest) ValidateReplacement() error {
	if x == nil {
		return fmt.Errorf("chat request replacement is nil")
	}
	if err := checkNoUnknown(x.ProtoReflect(), "chat request replacement"); err != nil {
		return err
	}
	if err := checkStringsUTF8(x.ProtoReflect(), "chat request replacement"); err != nil {
		return err
	}
	if x.MaxTokens != nil && *x.MaxTokens <= 0 {
		// Universal validity: every provider surface rejects zero and
		// negative max_tokens (the adapters emit the value verbatim once
		// present). Absence is the canonical "provider default" state.
		return fmt.Errorf("max_tokens must be strictly positive when present")
	}
	if x.Temperature != nil && !finiteFloat(*x.Temperature) {
		return fmt.Errorf("temperature must be finite")
	}
	if x.TopP != nil && !finiteFloat(*x.TopP) {
		return fmt.Errorf("top_p must be finite")
	}
	for _, f := range []struct {
		field string
		raw   []byte
	}{
		{"provider_extensions_json", x.ProviderExtensionsJson},
		{"safety_settings_json", x.SafetySettingsJson},
		{"torana_meta_json", x.ToranaMetaJson},
	} {
		if err := validateJSONField(f.raw, f.field, requestJSONFields["torana.v2.ChatRequest."+f.field]); err != nil {
			return err
		}
	}
	for i, m := range x.Messages {
		if m == nil {
			return fmt.Errorf("chat request replacement messages[%d] is nil", i)
		}
		if err := validateMessageReplacement(m, i); err != nil {
			return err
		}
	}
	for i, td := range x.Tools {
		if td == nil {
			return fmt.Errorf("chat request replacement tools[%d] is nil", i)
		}
		if err := validateToolDefReplacement(td, i); err != nil {
			return err
		}
	}
	return nil
}

func validateMessageReplacement(m *Message, i int) error {
	if err := checkNoUnknown(m.ProtoReflect(), fmt.Sprintf("chat request replacement messages[%d]", i)); err != nil {
		return err
	}
	if m.Role == "" {
		return fmt.Errorf("chat request replacement messages[%d].role must be non-empty", i)
	}
	if len(m.Blocks) == 0 {
		// An explicit empty provider message is ONE text block with
		// text == ""; an empty blocks list is not a second spelling.
		return fmt.Errorf("chat request replacement messages[%d].blocks must contain at least one block", i)
	}
	// hasCoveredBlock: at least one text or thinking block exists in this
	// message — the trailing-signature token binds preceding CLOSED
	// text/thinking content, so a standalone trailing token (no covered
	// block) is meaningless and refused absolutely.
	hasCoveredBlock := false
	for _, b := range m.Blocks {
		if b == nil {
			continue
		}
		if b.GetText() != nil || b.GetThinking() != nil {
			hasCoveredBlock = true
			break
		}
	}
	for j, b := range m.Blocks {
		if b == nil {
			return fmt.Errorf("chat request replacement messages[%d].blocks[%d] is nil", i, j)
		}
		if err := validateRequestBlock(b, fmt.Sprintf("%d", i), j, len(m.Blocks), m.Role, hasCoveredBlock); err != nil {
			return err
		}
	}
	return nil
}

// validateRequestBlock applies the absolute block-grammar rules: a kind arm
// must be present and non-typed-nil, leaf contracts hold, and the
// provider-independent placement rules (trailing signature assistant-only
// and final) hold. Provider-specific role/block combinations are the
// ADAPTER marshal validator's job — the SDK contract is provider-independent
// (the ir.messages.write.other catch-all and unmodelled provider roles stay
// representable).
func validateRequestBlock(b *RequestBlock, mi string, bi, blockCount int, role string, hasCoveredBlock bool) error {
	what := fmt.Sprintf("chat request replacement messages[%s].blocks[%d]", mi, bi)
	if err := checkNoUnknown(b.ProtoReflect(), what); err != nil {
		return err
	}
	switch k := b.Kind.(type) {
	case *RequestBlock_Text:
		if k.Text == nil {
			return fmt.Errorf("%s text arm is a typed nil", what)
		}
		return checkNoUnknown(k.Text.ProtoReflect(), what+".text")
	case *RequestBlock_Thinking:
		if k.Thinking == nil {
			return fmt.Errorf("%s thinking arm is a typed nil", what)
		}
		return checkNoUnknown(k.Thinking.ProtoReflect(), what+".thinking")
	case *RequestBlock_RedactedThinking:
		if k.RedactedThinking == nil {
			return fmt.Errorf("%s redacted_thinking arm is a typed nil", what)
		}
		return checkNoUnknown(k.RedactedThinking.ProtoReflect(), what+".redacted_thinking")
	case *RequestBlock_ToolUse:
		if k.ToolUse == nil {
			return fmt.Errorf("%s tool_use arm is a typed nil", what)
		}
		tu := k.ToolUse
		if err := checkNoUnknown(tu.ProtoReflect(), what+".tool_use"); err != nil {
			return err
		}
		if tu.Id == "" {
			return fmt.Errorf("%s.tool_use.id must be non-empty", what)
		}
		if tu.Name == "" {
			return fmt.Errorf("%s.tool_use.name must be non-empty", what)
		}
		return validateJSONField(tu.ArgumentsJson, what+".tool_use.arguments_json",
			requestJSONFields["torana.v2.RequestToolUseBlock.arguments_json"])
	case *RequestBlock_ToolResult:
		if k.ToolResult == nil {
			return fmt.Errorf("%s tool_result arm is a typed nil", what)
		}
		tr := k.ToolResult
		if err := checkNoUnknown(tr.ProtoReflect(), what+".tool_result"); err != nil {
			return err
		}
		if tr.ToolCallId == "" {
			return fmt.Errorf("%s.tool_result.tool_call_id must be non-empty", what)
		}
		if len(tr.Content) == 0 {
			// The canonical spelling of a present-but-empty provider result
			// is ONE explicit empty ToolResultTextBlock; an empty list is
			// the second spelling the ABI exists to remove.
			return fmt.Errorf("%s.tool_result.content must contain at least one element "+
				"(use one explicit empty text element for an empty result)", what)
		}
		for c, cb := range tr.Content {
			if cb == nil {
				return fmt.Errorf("%s.tool_result.content[%d] is nil", what, c)
			}
			if err := validateToolResultContentBlock(cb, fmt.Sprintf("%s.tool_result.content[%d]", what, c)); err != nil {
				return err
			}
		}
		return nil
	case *RequestBlock_CacheBreakpoint:
		if k.CacheBreakpoint == nil {
			return fmt.Errorf("%s cache_breakpoint arm is a typed nil", what)
		}
		cb := k.CacheBreakpoint
		if err := checkNoUnknown(cb.ProtoReflect(), what+".cache_breakpoint"); err != nil {
			return err
		}
		return validateJSONField(cb.MarkerJson, what+".cache_breakpoint.marker_json",
			requestJSONFields["torana.v2.RequestCacheBreakpoint.marker_json"])
	case *RequestBlock_Unknown:
		if k.Unknown == nil {
			return fmt.Errorf("%s unknown arm is a typed nil", what)
		}
		u := k.Unknown
		if err := checkNoUnknown(u.ProtoReflect(), what+".unknown"); err != nil {
			return err
		}
		if u.Kind == "" {
			return fmt.Errorf("%s.unknown.kind must be non-empty", what)
		}
		return validateJSONField(u.PayloadJson, what+".unknown.payload_json",
			requestJSONFields["torana.v2.RequestUnknownBlock.payload_json"])
	case *RequestBlock_TrailingSignature:
		if k.TrailingSignature == nil {
			return fmt.Errorf("%s trailing_signature arm is a typed nil", what)
		}
		ts := k.TrailingSignature
		if err := checkNoUnknown(ts.ProtoReflect(), what+".trailing_signature"); err != nil {
			return err
		}
		if ts.Signature == "" {
			return fmt.Errorf("%s.trailing_signature.signature must be non-empty", what)
		}
		if role != "assistant" {
			return fmt.Errorf("%s.trailing_signature is only valid on assistant messages", what)
		}
		if bi != blockCount-1 {
			return fmt.Errorf("%s.trailing_signature must be the final block of the message", what)
		}
		if !hasCoveredBlock {
			// The token binds preceding CLOSED text/thinking content; a
			// standalone trailing signature (no covered block — e.g. a
			// tool-call-only or redacted-only message) is unrepresentable.
			// An EXPLICIT EMPTY text/thinking block is still a real covered
			// block.
			return fmt.Errorf("%s.trailing_signature requires at least one preceding text or thinking block", what)
		}
		return nil
	default:
		return fmt.Errorf("%s has no kind arm", what)
	}
}

// validateToolResultContentBlock applies the nested tool-result grammar:
// text, unknown/provider content, or a cache breakpoint — nothing else is
// representable at the type level.
func validateToolResultContentBlock(cb *ToolResultContentBlock, what string) error {
	if err := checkNoUnknown(cb.ProtoReflect(), what); err != nil {
		return err
	}
	switch k := cb.Kind.(type) {
	case *ToolResultContentBlock_Text:
		if k.Text == nil {
			return fmt.Errorf("%s text arm is a typed nil", what)
		}
		return checkNoUnknown(k.Text.ProtoReflect(), what+".text")
	case *ToolResultContentBlock_Unknown:
		if k.Unknown == nil {
			return fmt.Errorf("%s unknown arm is a typed nil", what)
		}
		u := k.Unknown
		if err := checkNoUnknown(u.ProtoReflect(), what+".unknown"); err != nil {
			return err
		}
		if u.Kind == "" {
			return fmt.Errorf("%s.unknown.kind must be non-empty", what)
		}
		return validateJSONField(u.PayloadJson, what+".unknown.payload_json",
			requestJSONFields["torana.v2.ToolResultUnknownBlock.payload_json"])
	case *ToolResultContentBlock_CacheBreakpoint:
		if k.CacheBreakpoint == nil {
			return fmt.Errorf("%s cache_breakpoint arm is a typed nil", what)
		}
		cbp := k.CacheBreakpoint
		if err := checkNoUnknown(cbp.ProtoReflect(), what+".cache_breakpoint"); err != nil {
			return err
		}
		return validateJSONField(cbp.MarkerJson, what+".cache_breakpoint.marker_json",
			requestJSONFields["torana.v2.ToolResultCacheBreakpoint.marker_json"])
	default:
		return fmt.Errorf("%s has no kind arm", what)
	}
}

func validateToolDefReplacement(td *ToolDef, i int) error {
	what := fmt.Sprintf("chat request replacement tools[%d]", i)
	if err := checkNoUnknown(td.ProtoReflect(), what); err != nil {
		return err
	}
	if td.Name == "" {
		return fmt.Errorf("%s.name must be non-empty", what)
	}
	if err := validateJSONField(td.ParametersJson, what+".parameters_json",
		requestJSONFields["torana.v2.ToolDef.parameters_json"]); err != nil {
		return err
	}
	return validateJSONField(td.CacheControlJson, what+".cache_control_json",
		requestJSONFields["torana.v2.ToolDef.cache_control_json"])
}

func finiteFloat(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
