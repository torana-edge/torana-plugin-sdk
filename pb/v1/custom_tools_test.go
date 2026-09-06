package v1

import (
	"strings"
	"testing"
)

func stringPointer(v string) *string { return &v }

func validFreeformRequest() *ChatRequest {
	return &ChatRequest{
		Model: "gpt",
		Tools: []*ToolDef{{
			Name: "exec", InvocationKind: ToolInvocationKind_TOOL_INVOCATION_KIND_FREEFORM,
			InputFormatJson: []byte(`{"type":"grammar","syntax":"lark"}`),
			NamespacePath:   []string{"functions"},
		}},
		Messages: []*Message{
			{Role: "assistant", Blocks: []*RequestBlock{{Kind: &RequestBlock_ToolUse{ToolUse: &RequestToolUseBlock{
				Id: "call_1", Name: "exec", InvocationKind: ToolInvocationKind_TOOL_INVOCATION_KIND_FREEFORM,
				InputText: stringPointer(""),
			}}}}},
			{Role: "tool", Blocks: []*RequestBlock{{Kind: &RequestBlock_ToolResult{ToolResult: &RequestToolResultBlock{
				ToolCallId: "call_1", ToolName: "exec", InvocationKind: ToolInvocationKind_TOOL_INVOCATION_KIND_FREEFORM,
				Content: []*ToolResultContentBlock{{Kind: &ToolResultContentBlock_Text{Text: &ToolResultTextBlock{Text: "ok"}}}},
			}}}}},
		},
	}
}

func TestFreeformReplacementDomain(t *testing.T) {
	if err := validFreeformRequest().ValidateReplacement(); err != nil {
		t.Fatalf("valid free-form request rejected: %v", err)
	}

	tests := []struct {
		name string
		edit func(*ChatRequest)
		want string
	}{
		{"missing input presence", func(r *ChatRequest) { r.Messages[0].Blocks[0].GetToolUse().InputText = nil }, "input_text is required"},
		{"freeform arguments", func(r *ChatRequest) { r.Messages[0].Blocks[0].GetToolUse().ArgumentsJson = []byte(`{}`) }, "arguments_json is forbidden"},
		{"unknown call kind", func(r *ChatRequest) { r.Messages[0].Blocks[0].GetToolUse().InvocationKind = 99 }, "invocation_kind is unknown"},
		{"unknown result kind", func(r *ChatRequest) { r.Messages[1].Blocks[0].GetToolResult().InvocationKind = 99 }, "invocation_kind is unknown"},
		{"missing format", func(r *ChatRequest) { r.Tools[0].InputFormatJson = nil }, "input_format_json is required"},
		{"freeform schema", func(r *ChatRequest) { r.Tools[0].ParametersJson = []byte(`{}`) }, "parameters_json is forbidden"},
		{"freeform strict", func(r *ChatRequest) { r.Tools[0].Strict = true }, "strict is forbidden"},
		{"unknown definition kind", func(r *ChatRequest) { r.Tools[0].InvocationKind = 99 }, "invocation_kind is unknown"},
		{"empty namespace segment", func(r *ChatRequest) { r.Tools[0].NamespacePath = []string{"functions", ""} }, "namespace_path[1] must be non-empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := validFreeformRequest()
			tc.edit(r)
			err := r.ValidateReplacement()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestFunctionAndFreeformDefinitionArmsAreExclusive(t *testing.T) {
	function := &ChatRequest{Tools: []*ToolDef{{Name: "read", ParametersJson: []byte(`{}`)}}}
	if err := function.ValidateReplacement(); err != nil {
		t.Fatalf("ordinary function definition rejected: %v", err)
	}
	function.Tools[0].InputFormatJson = []byte(`{}`)
	if err := function.ValidateReplacement(); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("mixed function definition accepted: %v", err)
	}
}
