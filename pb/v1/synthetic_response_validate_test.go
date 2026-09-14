package v1

import "testing"

func TestSyntheticResponseRendererSubset(t *testing.T) {
	text := func() *SyntheticResponse {
		return &SyntheticResponse{Message: &ResponseMessage{Blocks: []*ResponseBlock{{Kind: &ResponseBlock_Text{Text: &ResponseTextBlock{Text: "ok"}}}}}, FinishReason: "stop"}
	}
	tool := func() *SyntheticResponse {
		return &SyntheticResponse{Message: &ResponseMessage{Blocks: []*ResponseBlock{{Kind: &ResponseBlock_ToolCall{ToolCall: &ToolCall{Name: "f", ArgumentsJson: []byte("{}")}}}}}, FinishReason: "tool_calls"}
	}
	if err := text().Validate(); err != nil {
		t.Fatalf("text: %v", err)
	}
	if err := tool().Validate(); err != nil {
		t.Fatalf("function: %v", err)
	}
	for _, tc := range []struct {
		name     string
		response *SyntheticResponse
	}{
		{"missing name", &SyntheticResponse{Message: &ResponseMessage{Blocks: []*ResponseBlock{{Kind: &ResponseBlock_ToolCall{ToolCall: &ToolCall{ArgumentsJson: []byte("{}")}}}}}, FinishReason: "tool_calls"}},
		{"id", &SyntheticResponse{Message: &ResponseMessage{Blocks: []*ResponseBlock{{Kind: &ResponseBlock_ToolCall{ToolCall: &ToolCall{Id: "x", Name: "f", ArgumentsJson: []byte("{}")}}}}}, FinishReason: "tool_calls"}},
		{"mixed finish", &SyntheticResponse{Message: &ResponseMessage{Blocks: []*ResponseBlock{{Kind: &ResponseBlock_Text{Text: &ResponseTextBlock{Text: "x"}}}, {Kind: &ResponseBlock_ToolCall{ToolCall: &ToolCall{Name: "f", ArgumentsJson: []byte("{}")}}}}}, FinishReason: "stop"}},
	} {
		if err := tc.response.Validate(); err == nil {
			t.Errorf("%s accepted", tc.name)
		}
	}
}
