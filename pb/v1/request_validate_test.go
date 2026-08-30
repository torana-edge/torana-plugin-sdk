package v1_test

// Normative request-replacement validation matrix (the executable ABI
// invariant/adversarial spec for the ordered message body). Every rule class
// is pinned here: universal structural rules (nil nesting, unknown protobuf
// fields, finite floats, block-grammar identity and placement) and the JSON
// field table (absent / valid / malformed / wrong top-level / duplicate keys
// / lone surrogate per field, with the documented top-level shape and
// absence capability).
//
// These are ABSOLUTE STRUCTURAL rules: they validate what a plugin RETURNS
// as a ReplaceRequest with no accepted request in sight. Relational
// provenance (signature binding, grants, host-owned facts) is the host
// verifier's layer, not this validator's.

import (
	"math"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	v1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

func textBlock(text string) *v1.RequestBlock {
	return &v1.RequestBlock{Kind: &v1.RequestBlock_Text{Text: &v1.RequestTextBlock{Text: text}}}
}

func baseRequest() *v1.ChatRequest {
	return &v1.ChatRequest{
		Model: "m",
		Messages: []*v1.Message{
			{Role: "user", Blocks: []*v1.RequestBlock{textBlock("hi")}},
		},
	}
}

// injectUnknown appends an unknown field (tag 99, varint) to the wire bytes
// of m and returns a fresh copy carrying it.
func injectUnknown[T proto.Message](t *testing.T, m T, newOne func() T) T {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	b = protowire.AppendTag(b, 99, protowire.VarintType)
	b = protowire.AppendVarint(b, 1)
	out := newOne()
	if err := proto.Unmarshal(b, out); err != nil {
		t.Fatalf("unmarshal with unknown field: %v", err)
	}
	return out
}

func mustRejectReplacement(t *testing.T, name string, req *v1.ChatRequest) {
	t.Helper()
	if err := req.ValidateReplacement(); err == nil {
		t.Fatalf("%s: replacement accepted", name)
	}
}

func mustAcceptReplacement(t *testing.T, name string, req *v1.ChatRequest) {
	t.Helper()
	if err := req.ValidateReplacement(); err != nil {
		t.Fatalf("%s: replacement rejected: %v", name, err)
	}
}

// --- universal rules -------------------------------------------------------

func TestReplacementNilAndNestedNil(t *testing.T) {
	mustRejectReplacement(t, "nil chat", nil)
	mustRejectReplacement(t, "nil message", &v1.ChatRequest{Messages: []*v1.Message{nil}})
	mustRejectReplacement(t, "nil block", &v1.ChatRequest{Messages: []*v1.Message{{
		Role: "user", Blocks: []*v1.RequestBlock{nil},
	}}})
	mustRejectReplacement(t, "block without arm", &v1.ChatRequest{Messages: []*v1.Message{{
		Role: "user", Blocks: []*v1.RequestBlock{{}},
	}}})
	mustRejectReplacement(t, "typed-nil text arm", &v1.ChatRequest{Messages: []*v1.Message{{
		Role: "user", Blocks: []*v1.RequestBlock{{Kind: &v1.RequestBlock_Text{}}},
	}}})
	mustRejectReplacement(t, "typed-nil tool_use arm", &v1.ChatRequest{Messages: []*v1.Message{{
		Role: "assistant", Blocks: []*v1.RequestBlock{{Kind: &v1.RequestBlock_ToolUse{}}},
	}}})
	mustRejectReplacement(t, "nil nested content", &v1.ChatRequest{Messages: []*v1.Message{{
		Role: "user", Blocks: []*v1.RequestBlock{{
			Kind: &v1.RequestBlock_ToolResult{ToolResult: &v1.RequestToolResultBlock{
				ToolCallId: "t1",
				Content:    []*v1.ToolResultContentBlock{nil},
			}},
		}},
	}}})
	mustRejectReplacement(t, "nested content without arm", &v1.ChatRequest{Messages: []*v1.Message{{
		Role: "user", Blocks: []*v1.RequestBlock{{
			Kind: &v1.RequestBlock_ToolResult{ToolResult: &v1.RequestToolResultBlock{
				ToolCallId: "t1",
				Content:    []*v1.ToolResultContentBlock{{}},
			}},
		}},
	}}})
	mustRejectReplacement(t, "nil tool def", &v1.ChatRequest{Tools: []*v1.ToolDef{nil}})
}

func TestReplacementEmptyBlocks(t *testing.T) {
	// An explicit empty provider message is ONE text block with text == "";
	// an empty blocks list is not a second spelling.
	mustRejectReplacement(t, "no blocks", &v1.ChatRequest{Messages: []*v1.Message{{Role: "user"}}})
	mustAcceptReplacement(t, "explicit empty text block", &v1.ChatRequest{
		Messages: []*v1.Message{{Role: "user", Blocks: []*v1.RequestBlock{textBlock("")}}},
	})
}

func TestReplacementUnknownFields(t *testing.T) {
	req := injectUnknown(t, baseRequest(), func() *v1.ChatRequest { return &v1.ChatRequest{} })
	mustRejectReplacement(t, "request level", req)

	msg := injectUnknown(t, &v1.Message{Role: "user", Blocks: []*v1.RequestBlock{textBlock("hi")}},
		func() *v1.Message { return &v1.Message{} })
	mustRejectReplacement(t, "message level", &v1.ChatRequest{Messages: []*v1.Message{msg}})

	blk := injectUnknown(t, textBlock("hi"), func() *v1.RequestBlock { return &v1.RequestBlock{} })
	mustRejectReplacement(t, "block level", &v1.ChatRequest{Messages: []*v1.Message{{
		Role: "user", Blocks: []*v1.RequestBlock{blk},
	}}})

	leaf := injectUnknown(t, &v1.RequestTextBlock{Text: "hi"}, func() *v1.RequestTextBlock { return &v1.RequestTextBlock{} })
	mustRejectReplacement(t, "leaf level", &v1.ChatRequest{Messages: []*v1.Message{{
		Role: "user", Blocks: []*v1.RequestBlock{{Kind: &v1.RequestBlock_Text{Text: leaf}}},
	}}})

	nested := injectUnknown(t, &v1.ToolResultTextBlock{Text: "ok"}, func() *v1.ToolResultTextBlock { return &v1.ToolResultTextBlock{} })
	mustRejectReplacement(t, "nested leaf level", &v1.ChatRequest{Messages: []*v1.Message{{
		Role: "user", Blocks: []*v1.RequestBlock{{
			Kind: &v1.RequestBlock_ToolResult{ToolResult: &v1.RequestToolResultBlock{
				ToolCallId: "t1",
				Content:    []*v1.ToolResultContentBlock{{Kind: &v1.ToolResultContentBlock_Text{Text: nested}}},
			}},
		}},
	}}})

	td := injectUnknown(t, &v1.ToolDef{Name: "read", ParametersJson: []byte(`{}`)}, func() *v1.ToolDef { return &v1.ToolDef{} })
	mustRejectReplacement(t, "tool-def level", &v1.ChatRequest{Tools: []*v1.ToolDef{td}})
}

// TestReplacementMaxTokens: when present, max_tokens must be strictly
// positive (zero and negatives are invalid on every provider surface; absent
// is fine).
func TestReplacementMaxTokens(t *testing.T) {
	zero := int32(0)
	neg := int32(-1)
	min := int32(math.MinInt32)
	for _, tc := range []struct {
		name string
		v    int32
	}{
		{"zero", zero},
		{"negative", neg},
		{"MinInt32", min},
	} {
		req := baseRequest()
		req.MaxTokens = &tc.v
		mustRejectReplacement(t, tc.name, req)
	}
	pos := int32(1)
	req := baseRequest()
	req.MaxTokens = &pos
	mustAcceptReplacement(t, "positive", req)
	mustAcceptReplacement(t, "absent", baseRequest())
}

func TestReplacementNonFiniteFloats(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value float64
	}{
		{"temperature NaN", math.NaN()},
		{"temperature +Inf", math.Inf(1)},
		{"temperature -Inf", math.Inf(-1)},
	} {
		req := baseRequest()
		req.Temperature = &tc.value
		mustRejectReplacement(t, tc.name, req)
	}
	for _, tc := range []struct {
		name  string
		value float64
	}{
		{"top_p NaN", math.NaN()},
		{"top_p +Inf", math.Inf(1)},
		{"top_p -Inf", math.Inf(-1)},
	} {
		req := baseRequest()
		req.TopP = &tc.value
		mustRejectReplacement(t, tc.name, req)
	}
	// Finite values are accepted; no provider-specific ranges are invented.
	req := baseRequest()
	v := -3.5
	req.Temperature = &v
	w := 2000.25
	req.TopP = &w
	mustAcceptReplacement(t, "finite out-of-natural-range values accepted", req)
}

// --- block identity and placement rules ------------------------------------

// fullBlocks returns a valid assistant message exercising every block kind.
func fullBlocks() *v1.Message {
	return &v1.Message{Role: "assistant", Blocks: []*v1.RequestBlock{
		{Kind: &v1.RequestBlock_Thinking{Thinking: &v1.RequestThinkingBlock{Text: "reasoning", Signature: "SIG"}}},
		textBlock("hello"),
		{Kind: &v1.RequestBlock_RedactedThinking{RedactedThinking: &v1.RequestRedactedThinkingBlock{Data: "..."}}},
		{Kind: &v1.RequestBlock_ToolUse{ToolUse: &v1.RequestToolUseBlock{
			Id: "t1", Name: "read", ArgumentsJson: []byte(`{"path":"x"}`), Signature: "CALLSIG",
		}}},
		{Kind: &v1.RequestBlock_CacheBreakpoint{CacheBreakpoint: &v1.RequestCacheBreakpoint{
			MarkerJson: []byte(`{"type":"ephemeral"}`),
		}}},
		{Kind: &v1.RequestBlock_Unknown{Unknown: &v1.RequestUnknownBlock{
			Kind: "custom", PayloadJson: []byte(`{"v":1e999}`),
		}}},
	}}
}

func TestReplacementBlockIdentityAndPlacement(t *testing.T) {
	// Tool-use identity is required.
	req := baseRequest()
	req.Messages[0] = &v1.Message{Role: "assistant", Blocks: []*v1.RequestBlock{{
		Kind: &v1.RequestBlock_ToolUse{ToolUse: &v1.RequestToolUseBlock{Name: "read", ArgumentsJson: []byte(`{}`)}},
	}}}
	mustRejectReplacement(t, "tool_use missing id", req)

	req = baseRequest()
	req.Messages[0] = &v1.Message{Role: "assistant", Blocks: []*v1.RequestBlock{{
		Kind: &v1.RequestBlock_ToolUse{ToolUse: &v1.RequestToolUseBlock{Id: "t1", ArgumentsJson: []byte(`{}`)}},
	}}}
	mustRejectReplacement(t, "tool_use missing name", req)

	req = baseRequest()
	req.Messages[0] = &v1.Message{Role: "assistant", Blocks: []*v1.RequestBlock{{
		Kind: &v1.RequestBlock_ToolUse{ToolUse: &v1.RequestToolUseBlock{Id: "t1", Name: "read"}},
	}}}
	mustRejectReplacement(t, "tool_use missing arguments", req)

	// Tool-result identity is required, and the nested content list is
	// NON-EMPTY: the canonical spelling of a present-but-empty provider
	// result is ONE explicit empty text element.
	req = baseRequest()
	req.Messages[0] = &v1.Message{Role: "user", Blocks: []*v1.RequestBlock{{
		Kind: &v1.RequestBlock_ToolResult{ToolResult: &v1.RequestToolResultBlock{}},
	}}}
	mustRejectReplacement(t, "tool_result missing tool_call_id", req)

	req = baseRequest()
	req.Messages[0] = &v1.Message{Role: "user", Blocks: []*v1.RequestBlock{{
		Kind: &v1.RequestBlock_ToolResult{ToolResult: &v1.RequestToolResultBlock{ToolCallId: "t1"}},
	}}}
	mustRejectReplacement(t, "tool_result empty content list", req)

	req = baseRequest()
	req.Messages[0] = &v1.Message{Role: "user", Blocks: []*v1.RequestBlock{{
		Kind: &v1.RequestBlock_ToolResult{ToolResult: &v1.RequestToolResultBlock{
			ToolCallId: "t1",
			Content:    []*v1.ToolResultContentBlock{{Kind: &v1.ToolResultContentBlock_Text{Text: &v1.ToolResultTextBlock{Text: ""}}}},
		}},
	}}}
	mustAcceptReplacement(t, "tool_result explicit empty text element", req)

	// Unknown kind is required; payload must be an object.
	req = baseRequest()
	req.Messages[0] = &v1.Message{Role: "user", Blocks: []*v1.RequestBlock{{
		Kind: &v1.RequestBlock_Unknown{Unknown: &v1.RequestUnknownBlock{PayloadJson: []byte(`{}`)}},
	}}}
	mustRejectReplacement(t, "unknown missing kind", req)

	req = baseRequest()
	req.Messages[0] = &v1.Message{Role: "user", Blocks: []*v1.RequestBlock{{
		Kind: &v1.RequestBlock_Unknown{Unknown: &v1.RequestUnknownBlock{Kind: "custom"}},
	}}}
	mustRejectReplacement(t, "unknown missing payload", req)

	// Trailing signature: non-empty, assistant-only, final.
	req = baseRequest()
	req.Messages[0] = &v1.Message{Role: "assistant", Blocks: []*v1.RequestBlock{
		textBlock("hi"),
		{Kind: &v1.RequestBlock_TrailingSignature{TrailingSignature: &v1.RequestTrailingSignatureBlock{Signature: ""}}},
	}}
	mustRejectReplacement(t, "trailing signature empty", req)

	req = baseRequest()
	req.Messages[0] = &v1.Message{Role: "user", Blocks: []*v1.RequestBlock{
		textBlock("hi"),
		{Kind: &v1.RequestBlock_TrailingSignature{TrailingSignature: &v1.RequestTrailingSignatureBlock{Signature: "SIG"}}},
	}}
	mustRejectReplacement(t, "trailing signature non-assistant", req)

	req = baseRequest()
	req.Messages[0] = &v1.Message{Role: "assistant", Blocks: []*v1.RequestBlock{
		{Kind: &v1.RequestBlock_TrailingSignature{TrailingSignature: &v1.RequestTrailingSignatureBlock{Signature: "SIG"}}},
		textBlock("hi"),
	}}
	mustRejectReplacement(t, "trailing signature not final", req)

	req = baseRequest()
	req.Messages[0] = &v1.Message{Role: "assistant", Blocks: []*v1.RequestBlock{
		textBlock("hi"),
		{Kind: &v1.RequestBlock_TrailingSignature{TrailingSignature: &v1.RequestTrailingSignatureBlock{Signature: "SIG"}}},
	}}
	mustAcceptReplacement(t, "trailing signature final assistant", req)

	// The trailing token binds preceding CLOSED text/thinking content:
	// standalone forms are unrepresentable and refused absolutely.
	standalone := []struct {
		name   string
		blocks []*v1.RequestBlock
	}{
		{"trailing alone", []*v1.RequestBlock{{
			Kind: &v1.RequestBlock_TrailingSignature{TrailingSignature: &v1.RequestTrailingSignatureBlock{Signature: "SIG"}},
		}}},
		{"tool-use only", []*v1.RequestBlock{
			{Kind: &v1.RequestBlock_ToolUse{ToolUse: &v1.RequestToolUseBlock{
				Id: "t1", Name: "read", ArgumentsJson: []byte(`{}`),
			}}},
			{Kind: &v1.RequestBlock_TrailingSignature{TrailingSignature: &v1.RequestTrailingSignatureBlock{Signature: "SIG"}}},
		}},
		{"redacted only", []*v1.RequestBlock{
			{Kind: &v1.RequestBlock_RedactedThinking{RedactedThinking: &v1.RequestRedactedThinkingBlock{Data: "..."}}},
			{Kind: &v1.RequestBlock_TrailingSignature{TrailingSignature: &v1.RequestTrailingSignatureBlock{Signature: "SIG"}}},
		}},
	}
	for _, tc := range standalone {
		req = baseRequest()
		req.Messages[0] = &v1.Message{Role: "assistant", Blocks: tc.blocks}
		mustRejectReplacement(t, "trailing "+tc.name, req)
	}

	// Explicit EMPTY text/thinking blocks are still real covered blocks.
	req = baseRequest()
	req.Messages[0] = &v1.Message{Role: "assistant", Blocks: []*v1.RequestBlock{
		textBlock(""),
		{Kind: &v1.RequestBlock_TrailingSignature{TrailingSignature: &v1.RequestTrailingSignatureBlock{Signature: "SIG"}}},
	}}
	mustAcceptReplacement(t, "trailing over explicit empty text", req)

	req = baseRequest()
	req.Messages[0] = &v1.Message{Role: "assistant", Blocks: []*v1.RequestBlock{
		{Kind: &v1.RequestBlock_Thinking{Thinking: &v1.RequestThinkingBlock{Text: ""}}},
		{Kind: &v1.RequestBlock_TrailingSignature{TrailingSignature: &v1.RequestTrailingSignatureBlock{Signature: "SIG"}}},
	}}
	mustAcceptReplacement(t, "trailing over explicit empty thinking", req)

	// Every block kind in one message is valid (provider-independent
	// grammar: no closed global role/kind matrix).
	mustAcceptReplacement(t, "full block set", &v1.ChatRequest{Messages: []*v1.Message{fullBlocks()}})

	// Tool results nest text/unknown/cache only — nested tool use/results/
	// thinking/signatures are impossible at the type level (dedicated oneof).
	req = baseRequest()
	req.Messages[0] = &v1.Message{Role: "user", Blocks: []*v1.RequestBlock{{
		Kind: &v1.RequestBlock_ToolResult{ToolResult: &v1.RequestToolResultBlock{
			ToolCallId: "t1",
			Content: []*v1.ToolResultContentBlock{
				{Kind: &v1.ToolResultContentBlock_Text{Text: &v1.ToolResultTextBlock{Text: "ok"}}},
				{Kind: &v1.ToolResultContentBlock_Unknown{Unknown: &v1.ToolResultUnknownBlock{
					Kind: "json", PayloadJson: []byte(`{"score":42}`),
				}}},
				{Kind: &v1.ToolResultContentBlock_CacheBreakpoint{CacheBreakpoint: &v1.ToolResultCacheBreakpoint{
					MarkerJson: []byte(`{"type":"ephemeral"}`),
				}}},
			},
		}},
	}}}
	mustAcceptReplacement(t, "nested result content kinds", req)

	req = baseRequest()
	req.Messages[0] = &v1.Message{Role: "user", Blocks: []*v1.RequestBlock{{
		Kind: &v1.RequestBlock_ToolResult{ToolResult: &v1.RequestToolResultBlock{
			ToolCallId: "t1",
			Content: []*v1.ToolResultContentBlock{{
				Kind: &v1.ToolResultContentBlock_Unknown{Unknown: &v1.ToolResultUnknownBlock{PayloadJson: []byte(`{}`)}},
			}},
		}},
	}}}
	mustRejectReplacement(t, "nested unknown missing kind", req)

	// Unmodelled non-empty roles with text blocks stay representable (the
	// ir.messages.write.other catch-all).
	req = baseRequest()
	req.Messages[0] = &v1.Message{Role: "clippy", Blocks: []*v1.RequestBlock{textBlock("hi")}}
	mustAcceptReplacement(t, "unmodelled role with text block", req)
}

// --- JSON field table ------------------------------------------------------

// jsonFieldCase drives the per-field matrix: the field is set on a copy of
// the request via a mutator; absent/valid must pass, malformed/wrong-top-
// level/duplicate/surrogate must fail.
func TestReplacementJSONFieldMatrix(t *testing.T) {
	dup := `{"a":1,"a":2}`
	surrogate := `{"a":"\ud800"}`
	badUTF8 := "{\"a\":\"\xff\"}"
	malformed := `{"a":`

	type badRow struct {
		name   string
		sample string
		family string // expected substring of the rejection
	}
	cases := []struct {
		name   string
		mutate func(*v1.ChatRequest, []byte)
		absent func(*v1.ChatRequest)
		// absentOK marks absent-capable fields; required fields must reject
		// empty bytes (the canonical empty shape is {} or []).
		absentOK bool
		valid    []string
		bad      []badRow
	}{
		{
			name: "tool_use arguments_json object required",
			absent: func(r *v1.ChatRequest) {
				r.Messages[0].Blocks[0].GetToolUse().ArgumentsJson = nil
			},
			mutate: func(r *v1.ChatRequest, b []byte) { r.Messages[0].Blocks[0].GetToolUse().ArgumentsJson = b },
			valid:  []string{`{}`, `{"path":"server.go"}`, `{"big":1e999,"neg":-1e999}`},
			bad: []badRow{
				{"malformed", malformed, "jsontext:"},
				{"wrong-shape-array", `[]`, "must be a JSON object"},
				{"wrong-shape-number", `1`, "must be a JSON object"},
				{"duplicate", dup, "duplicate"},
				{"surrogate", surrogate, "surrogate"},
				{"utf8", badUTF8, "UTF-8"},
				{"null", `null`, "must be a JSON object"},
			},
		},
		{
			name: "cache_breakpoint marker_json object required",
			absent: func(r *v1.ChatRequest) {
				r.Messages[0].Blocks[1].GetCacheBreakpoint().MarkerJson = nil
			},
			mutate: func(r *v1.ChatRequest, b []byte) { r.Messages[0].Blocks[1].GetCacheBreakpoint().MarkerJson = b },
			valid:  []string{`{}`, `{"type":"ephemeral","ttl":9007199254740993}`},
			bad: []badRow{
				{"malformed", malformed, "jsontext:"},
				{"wrong-shape-array", `[]`, "must be a JSON object"},
				{"wrong-shape-string", `"str"`, "must be a JSON object"},
				{"duplicate", dup, "duplicate"},
				{"surrogate", surrogate, "surrogate"},
				{"utf8", badUTF8, "UTF-8"},
				{"null", `null`, "must be a JSON object"},
			},
		},
		{
			name: "unknown payload_json object required",
			absent: func(r *v1.ChatRequest) {
				r.Messages[0].Blocks[2].GetUnknown().PayloadJson = nil
			},
			mutate: func(r *v1.ChatRequest, b []byte) { r.Messages[0].Blocks[2].GetUnknown().PayloadJson = b },
			valid:  []string{`{}`, `{"vendor":1e999}`},
			bad: []badRow{
				{"malformed", malformed, "jsontext:"},
				{"wrong-shape-array", `[]`, "must be a JSON object"},
				{"wrong-shape-string", `"str"`, "must be a JSON object"},
				{"duplicate", dup, "duplicate"},
				{"surrogate", surrogate, "surrogate"},
				{"utf8", badUTF8, "UTF-8"},
				{"null", `null`, "must be a JSON object"},
			},
		},
		{
			name: "nested unknown payload_json object required",
			absent: func(r *v1.ChatRequest) {
				r.Messages[0].Blocks[3].GetToolResult().Content[0].GetUnknown().PayloadJson = nil
			},
			mutate: func(r *v1.ChatRequest, b []byte) {
				r.Messages[0].Blocks[3].GetToolResult().Content[0].GetUnknown().PayloadJson = b
			},
			valid: []string{`{}`, `{"json":{"score":42}}`},
			bad: []badRow{
				{"malformed", malformed, "jsontext:"},
				{"wrong-shape-array", `[]`, "must be a JSON object"},
				{"null", `null`, "must be a JSON object"},
			},
		},
		{
			name: "nested cache marker_json object required",
			absent: func(r *v1.ChatRequest) {
				r.Messages[0].Blocks[3].GetToolResult().Content[1].GetCacheBreakpoint().MarkerJson = nil
			},
			mutate: func(r *v1.ChatRequest, b []byte) {
				r.Messages[0].Blocks[3].GetToolResult().Content[1].GetCacheBreakpoint().MarkerJson = b
			},
			valid: []string{`{}`, `{"type":"ephemeral"}`},
			bad: []badRow{
				{"malformed", malformed, "jsontext:"},
				{"wrong-shape-array", `[]`, "must be a JSON object"},
				{"null", `null`, "must be a JSON object"},
			},
		},
		{
			name: "tool def parameters_json object required",
			absent: func(r *v1.ChatRequest) {
				r.Tools[0].ParametersJson = nil
			},
			mutate: func(r *v1.ChatRequest, b []byte) { r.Tools[0].ParametersJson = b },
			valid:  []string{`{}`, `{"type":"object","properties":{}}`},
			bad: []badRow{
				{"malformed", malformed, "jsontext:"},
				{"wrong-shape-array", `[]`, "must be a JSON object"},
				{"wrong-shape-string", `"str"`, "must be a JSON object"},
				{"duplicate", dup, "duplicate"},
				{"surrogate", surrogate, "surrogate"},
				{"utf8", badUTF8, "UTF-8"},
				{"null", `null`, "must be a JSON object"},
			},
		},
		{
			name:     "tool def cache_control_json object",
			absentOK: true,
			absent: func(r *v1.ChatRequest) {
				r.Tools[0].CacheControlJson = nil
			},
			mutate: func(r *v1.ChatRequest, b []byte) { r.Tools[0].CacheControlJson = b },
			valid:  []string{`{}`, `{"type":"ephemeral"}`},
			bad: []badRow{
				{"malformed", malformed, "jsontext:"},
				{"wrong-shape-array", `[]`, "must be a JSON object"},
				{"wrong-shape-string", `"str"`, "must be a JSON object"},
				{"duplicate", dup, "duplicate"},
				{"surrogate", surrogate, "surrogate"},
				{"utf8", badUTF8, "UTF-8"},
				{"null", `null`, "must be a JSON object"},
			},
		},
		{
			name:     "provider_extensions_json object",
			absentOK: true,
			absent: func(r *v1.ChatRequest) {
				r.ProviderExtensionsJson = nil
			},
			mutate: func(r *v1.ChatRequest, b []byte) { r.ProviderExtensionsJson = b },
			valid:  []string{`{}`, `{"stream_options":{"include_usage":true}}`},
			bad: []badRow{
				{"malformed", malformed, "jsontext:"},
				{"wrong-shape-array", `[]`, "must be a JSON object"},
				{"wrong-shape-string", `"str"`, "must be a JSON object"},
				{"duplicate", dup, "duplicate"},
				{"surrogate", surrogate, "surrogate"},
				{"utf8", badUTF8, "UTF-8"},
				{"null", `null`, "must be a JSON object"},
			},
		},
		{
			name:     "safety_settings_json array",
			absentOK: true,
			absent: func(r *v1.ChatRequest) {
				r.SafetySettingsJson = nil
			},
			mutate: func(r *v1.ChatRequest, b []byte) { r.SafetySettingsJson = b },
			valid:  []string{`[]`, `[{"category":"HARM_CATEGORY_HATE","threshold":"BLOCK"}]`},
			bad: []badRow{
				{"malformed", malformed, "jsontext:"},
				{"wrong-shape-object", `{}`, "must be a JSON array"},
				{"wrong-shape-string", `"str"`, "must be a JSON array"},
				{"duplicate", `[{"a":1,"a":2}]`, "duplicate"},
				{"surrogate", `[{"a":"\ud800"}]`, "surrogate"},
				{"utf8", "[\"" + "\xff" + "\"]", "UTF-8"},
				{"null", `null`, "must be a JSON array"},
			},
		},
		{
			name:     "torana_meta_json object host-owned",
			absentOK: true,
			absent: func(r *v1.ChatRequest) {
				r.ToranaMetaJson = nil
			},
			mutate: func(r *v1.ChatRequest, b []byte) { r.ToranaMetaJson = b },
			valid:  []string{`{}`, `{"_provider":"oai"}`},
			bad: []badRow{
				{"malformed", malformed, "jsontext:"},
				{"wrong-shape-array", `[]`, "must be a JSON object"},
				{"wrong-shape-string", `"str"`, "must be a JSON object"},
				{"duplicate", dup, "duplicate"},
				{"surrogate", surrogate, "surrogate"},
				{"utf8", badUTF8, "UTF-8"},
				{"null", `null`, "must be a JSON object"},
			},
		},
	}

	// A base request with the block fields the table mutates.
	validBase := func() *v1.ChatRequest {
		r := baseRequest()
		r.Messages[0] = &v1.Message{Role: "assistant", Blocks: []*v1.RequestBlock{
			{Kind: &v1.RequestBlock_ToolUse{ToolUse: &v1.RequestToolUseBlock{
				Id: "t1", Name: "read", ArgumentsJson: []byte(`{}`),
			}}},
			{Kind: &v1.RequestBlock_CacheBreakpoint{CacheBreakpoint: &v1.RequestCacheBreakpoint{
				MarkerJson: []byte(`{}`),
			}}},
			{Kind: &v1.RequestBlock_Unknown{Unknown: &v1.RequestUnknownBlock{
				Kind: "custom", PayloadJson: []byte(`{}`),
			}}},
			{Kind: &v1.RequestBlock_ToolResult{ToolResult: &v1.RequestToolResultBlock{
				ToolCallId: "t1",
				Content: []*v1.ToolResultContentBlock{
					{Kind: &v1.ToolResultContentBlock_Unknown{Unknown: &v1.ToolResultUnknownBlock{
						Kind: "json", PayloadJson: []byte(`{}`),
					}}},
					{Kind: &v1.ToolResultContentBlock_CacheBreakpoint{CacheBreakpoint: &v1.ToolResultCacheBreakpoint{
						MarkerJson: []byte(`{}`),
					}}},
				},
			}}},
		}}
		r.Tools = []*v1.ToolDef{{Name: "read", ParametersJson: []byte(`{}`)}}
		return r
	}

	for _, c := range cases {
		t.Run(c.name+"/absent", func(t *testing.T) {
			req := validBase()
			c.absent(req)
			if c.absentOK {
				mustAcceptReplacement(t, "absent field", req)
			} else {
				mustRejectReplacement(t, "required field absent", req)
			}
		})
		for _, v := range c.valid {
			t.Run(c.name+"/valid:"+v, func(t *testing.T) {
				req := validBase()
				c.mutate(req, []byte(v))
				mustAcceptReplacement(t, "valid field", req)
			})
		}
		for _, b := range c.bad {
			t.Run(c.name+"/bad:"+b.name, func(t *testing.T) {
				req := validBase()
				c.mutate(req, []byte(b.sample))
				err := req.ValidateReplacement()
				if err == nil {
					t.Fatalf("%s (%s): invalid field accepted", b.name, b.family)
				}
				if !strings.Contains(err.Error(), b.family) {
					t.Fatalf("%s: rejection %q does not name family %q", b.name, err, b.family)
				}
			})
		}
	}
}

// TestReplacementJSONNullIsNotAbsence: the canonical empty shapes are `{}`
// / `[]`; a literal JSON null is a wrong top-level value everywhere, and
// empty bytes are absence only for absent-capable fields.
func TestReplacementJSONNullIsNotAbsence(t *testing.T) {
	req := baseRequest()
	req.Messages[0] = &v1.Message{Role: "assistant", Blocks: []*v1.RequestBlock{{
		Kind: &v1.RequestBlock_ToolUse{ToolUse: &v1.RequestToolUseBlock{
			Id: "t1", Name: "read", ArgumentsJson: []byte(`null`),
		}},
	}}}
	mustRejectReplacement(t, "arguments null", req)

	req = baseRequest()
	req.Messages[0] = &v1.Message{Role: "user", Blocks: []*v1.RequestBlock{{
		Kind: &v1.RequestBlock_Unknown{Unknown: &v1.RequestUnknownBlock{Kind: "c", PayloadJson: []byte(`null`)}},
	}}}
	mustRejectReplacement(t, "unknown payload null", req)
}

// TestHookResultValidateForReplaceRequest: HookResult.ValidateFor(BEFORE_REQUEST)
// invokes the normative replacement validator on the nested ReplaceRequest;
// other actions are unaffected.
func TestHookResultValidateForReplaceRequest(t *testing.T) {
	valid := &v1.ChatRequest{Model: "m", Messages: []*v1.Message{{Role: "user", Blocks: []*v1.RequestBlock{textBlock("hi")}}}}
	hr := &v1.HookResult{Action: &v1.HookResult_ReplaceRequest{ReplaceRequest: valid}}
	if err := hr.ValidateFor(v1.Hook_HOOK_BEFORE_REQUEST); err != nil {
		t.Fatalf("valid replacement rejected: %v", err)
	}

	bad := &v1.ChatRequest{Model: "m", Messages: []*v1.Message{{Role: "assistant", Blocks: []*v1.RequestBlock{{
		Kind: &v1.RequestBlock_ToolUse{ToolUse: &v1.RequestToolUseBlock{Name: "read", ArgumentsJson: []byte(`{}`)}},
	}}}}}
	hr = &v1.HookResult{Action: &v1.HookResult_ReplaceRequest{ReplaceRequest: bad}}
	err := hr.ValidateFor(v1.Hook_HOOK_BEFORE_REQUEST)
	if err == nil || !strings.Contains(err.Error(), "tool_use.id") {
		t.Fatalf("invalid replacement accepted or wrong error: %v", err)
	}

	// A wrong-hook dispatch still fails before content validation.
	hr = &v1.HookResult{Action: &v1.HookResult_ReplaceRequest{ReplaceRequest: valid}}
	if err := hr.ValidateFor(v1.Hook_HOOK_AFTER_RESPONSE); err == nil {
		t.Fatal("replacement dispatched to the wrong hook accepted")
	}

	// Invalid UTF-8 in a Go-constructed string is rejected by ValidateFor
	// BEFORE proto.Marshal would refuse it — the Go-guest and handwritten-
	// wire-guest domains are the same.
	badModel := &v1.ChatRequest{Model: string([]byte{0xff})}
	hr = &v1.HookResult{Action: &v1.HookResult_ReplaceRequest{ReplaceRequest: badModel}}
	if err := hr.ValidateFor(v1.Hook_HOOK_BEFORE_REQUEST); err == nil ||
		!strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("invalid Go-side UTF-8 accepted or wrong error: %v", err)
	}

	// A valid Unicode replacement passes the same gate.
	uni := &v1.ChatRequest{Model: "héllo 日本語", Messages: []*v1.Message{{Role: "user", Blocks: []*v1.RequestBlock{textBlock("雪")}}}}
	hr = &v1.HookResult{Action: &v1.HookResult_ReplaceRequest{ReplaceRequest: uni}}
	if err := hr.ValidateFor(v1.Hook_HOOK_BEFORE_REQUEST); err != nil {
		t.Fatalf("valid unicode replacement rejected: %v", err)
	}
}

// TestReplacementMessageRoleNonEmpty: the request-domain Message.role is
// non-empty UTF-8 — a nil Message element survives protobuf transport as a
// zero-length message that decodes to an empty non-nil Message{}, so the
// decoded form must be rejected. The catch-all role decision stays open:
// any NON-EMPTY role (known or unmodelled) is accepted.
func TestReplacementMessageRoleNonEmpty(t *testing.T) {
	// Decisive wire round trip: nil element -> zero-length wire -> empty
	// non-nil Message{} on decode -> rejected by the shared contract.
	req := &v1.ChatRequest{Messages: []*v1.Message{nil}}
	raw, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var decoded v1.ChatRequest
	if err := proto.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	mustRejectReplacement(t, "wire-decoded empty message", &decoded)

	// Explicitly empty and known roles are accepted; any NON-EMPTY
	// unmodelled role is accepted too (the catch-all stays open).
	empty := &v1.ChatRequest{Messages: []*v1.Message{{Role: ""}}}
	mustRejectReplacement(t, "empty role", empty)

	for _, role := range []string{"user", "assistant", "system", "tool", "developer", "clippy-2000"} {
		m := &v1.ChatRequest{Messages: []*v1.Message{{Role: role, Blocks: []*v1.RequestBlock{textBlock("x")}}}}
		mustAcceptReplacement(t, "role "+role, m)
	}

	// A nil Message element is refused on the wire-visible decoding path
	// exactly like a zero-length message: the role rule catches both.
	wire := &v1.ChatRequest{Messages: []*v1.Message{{Role: "user", Blocks: []*v1.RequestBlock{textBlock("hi")}}}}
	raw, err = proto.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	var back v1.ChatRequest
	if err := proto.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if err := back.ValidateReplacement(); err != nil {
		t.Fatalf("valid wire round trip rejected: %v", err)
	}
}
