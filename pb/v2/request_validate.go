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
//	Message.content_parts_json        absent | JSON array
//	Message.cache_control_json        absent | JSON object
//	ToolCall (request context)        non-empty id, non-empty name,
//	                                 arguments_json REQUIRED non-empty JSON object ({} canonical)
//	ToolDef                           non-empty name,
//	                                 parameters_json REQUIRED non-empty JSON object
//	                                 ({} is a valid unconstrained schema; empty bytes are not),
//	                                 cache_control_json absent | JSON object
//
// JSON fields are validated with the shared strict JSON-text rules (valid
// UTF-8, paired surrogates, unique decoded member names, single top-level
// value — see jsontext), then checked for their documented top-level shape.
// Empty bytes are absence ONLY for absent-capable fields; a literal JSON null
// is a wrong top-level value everywhere. Unknown protobuf fields are refused
// at every level (the contract is closed; the host cannot know their
// semantics). Nil nested elements are refused without panicking.
//
// Tool-call ID rules are REQUEST-CONTEXT ONLY. Anonymous RESPONSE tool calls
// keep their separate generic ToolCall.Validate: response IDs are host-owned
// and may legitimately be absent.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"

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
	"torana.v2.ChatRequest.torana_meta_json":         {shape: "object"},
	"torana.v2.ChatRequest.provider_extensions_json": {shape: "object"},
	"torana.v2.ChatRequest.safety_settings_json":     {shape: "array"},
	"torana.v2.Message.content_parts_json":           {shape: "array"},
	"torana.v2.Message.cache_control_json":           {shape: "object"},
	"torana.v2.ToolCall.arguments_json":              {shape: "object", required: true},
	"torana.v2.ToolDef.parameters_json":              {shape: "object", required: true},
	"torana.v2.ToolDef.cache_control_json":           {shape: "object"},
}

// requestScalarRules declares a rule class for every non-JSON field of the
// four request-visible messages. The inventory test forces additive fields
// to be declared here, and each class is either ENFORCED by the validator or
// documented as inherent/unconstrained:
//
//   - enforced: "repeated-message-nonnil", "float-finite-optional",
//     "int32-positive-optional", "text-required";
//   - inherent: "text" and "repeated-text" (protobuf strings are valid UTF-8
//     by construction, so no further constraint applies), "bool", "int32"
//     (protobuf scalars are well-typed by construction);
//   - deliberately unconstrained: "text" content fields (role, content,
//     thinking, signatures, description) carry no universal constraint.
var requestScalarRules = map[string]string{
	// ChatRequest
	"torana.v2.ChatRequest.model":          "text",
	"torana.v2.ChatRequest.messages":       "repeated-message-nonnil",
	"torana.v2.ChatRequest.tools":          "repeated-message-nonnil",
	"torana.v2.ChatRequest.stream":         "bool",
	"torana.v2.ChatRequest.max_tokens":     "int32-positive-optional",
	"torana.v2.ChatRequest.temperature":    "float-finite-optional",
	"torana.v2.ChatRequest.top_p":          "float-finite-optional",
	"torana.v2.ChatRequest.stop_sequences": "repeated-text",
	// Message
	"torana.v2.Message.role":               "text",
	"torana.v2.Message.content":            "text",
	"torana.v2.Message.thinking":           "text",
	"torana.v2.Message.thinking_signature": "text",
	"torana.v2.Message.redacted_thinking":  "text",
	"torana.v2.Message.tool_calls":         "repeated-message-nonnil",
	"torana.v2.Message.tool_call_id":       "text",
	"torana.v2.Message.tool_name":          "text",
	"torana.v2.Message.trailing_signature": "text",
	"torana.v2.Message.content_signature":  "text",
	// ToolCall (request context)
	"torana.v2.ToolCall.id":        "text-required",
	"torana.v2.ToolCall.name":      "text-required",
	"torana.v2.ToolCall.signature": "text",
	// ToolDef
	"torana.v2.ToolDef.name":        "text-required",
	"torana.v2.ToolDef.description": "text",
	"torana.v2.ToolDef.strict":      "bool",
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
	for _, f := range []struct {
		field string
		raw   []byte
	}{
		{"content_parts_json", m.ContentPartsJson},
		{"cache_control_json", m.CacheControlJson},
	} {
		if err := validateJSONField(f.raw, fmt.Sprintf("chat request replacement messages[%d].%s", i, f.field),
			requestJSONFields["torana.v2.Message."+f.field]); err != nil {
			return err
		}
	}
	for j, tc := range m.ToolCalls {
		if tc == nil {
			return fmt.Errorf("chat request replacement messages[%d].tool_calls[%d] is nil", i, j)
		}
		if err := validateToolCallReplacement(tc, i, j); err != nil {
			return err
		}
	}
	return nil
}

// validateToolCallReplacement applies the REQUEST-CONTEXT tool-call rules:
// non-empty id and name, required non-empty object arguments. The generic
// ToolCall.Validate is deliberately NOT used here — anonymous response tool
// calls keep their separate host-owned-id semantics.
func validateToolCallReplacement(tc *ToolCall, mi, ti int) error {
	what := fmt.Sprintf("chat request replacement messages[%d].tool_calls[%d]", mi, ti)
	if err := checkNoUnknown(tc.ProtoReflect(), what); err != nil {
		return err
	}
	if tc.Id == "" {
		return fmt.Errorf("%s.id must be non-empty (request context)", what)
	}
	if tc.Name == "" {
		return fmt.Errorf("%s.name must be non-empty", what)
	}
	return validateJSONField(tc.ArgumentsJson, what+".arguments_json",
		requestJSONFields["torana.v2.ToolCall.arguments_json"])
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
