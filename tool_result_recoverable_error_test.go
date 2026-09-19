package plugin_sdk

import (
	"testing"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"google.golang.org/protobuf/proto"
)

func TestReplaceToolResultWithError(t *testing.T) {
	msg := &pbv1.Message{Role: "user", Blocks: []*pbv1.RequestBlock{{
		Kind: &pbv1.RequestBlock_ToolResult{ToolResult: &pbv1.RequestToolResultBlock{
			ToolCallId: "call-1",
			ToolName:   "Read",
			Signature:  "signed-result",
			Content: []*pbv1.ToolResultContentBlock{
				{Kind: &pbv1.ToolResultContentBlock_Text{Text: &pbv1.ToolResultTextBlock{Text: "secret"}}},
				{Kind: &pbv1.ToolResultContentBlock_CacheBreakpoint{CacheBreakpoint: &pbv1.ToolResultCacheBreakpoint{MarkerJson: []byte(`{"type":"ephemeral"}`)}}},
			},
		}},
	}}}

	changed, err := ReplaceToolResultWithError(msg, 0, "Sensitive output withheld by pii_guard.")
	if err != nil || !changed {
		t.Fatalf("ReplaceToolResultWithError = %v, %v", changed, err)
	}
	result := msg.Blocks[0].GetToolResult()
	if result.IsError == nil || !*result.IsError {
		t.Fatal("tool result was not marked as an explicit error")
	}
	if result.Content[0].GetText().Text != "Sensitive output withheld by pii_guard." {
		t.Fatalf("replacement text = %q", result.Content[0].GetText().Text)
	}
	if result.Signature != "" {
		t.Fatal("changed result retained a stale signature")
	}
	if len(result.Content) != 2 || result.Content[1].GetCacheBreakpoint() == nil {
		t.Fatalf("error result did not preserve cache marker: %+v", result.Content)
	}

	want := proto.Clone(msg).(*pbv1.Message)
	changed, err = ReplaceToolResultWithError(msg, 0, "Sensitive output withheld by pii_guard.")
	if err != nil || changed || !proto.Equal(msg, want) {
		t.Fatalf("identical replay changed the message: changed=%v err=%v", changed, err)
	}
}

func TestReplaceToolResultWithErrorScrubsEveryVisibleArm(t *testing.T) {
	msg := &pbv1.Message{Role: "user", Blocks: []*pbv1.RequestBlock{{
		Kind: &pbv1.RequestBlock_ToolResult{ToolResult: &pbv1.RequestToolResultBlock{
			ToolCallId: "call-1",
			Content: []*pbv1.ToolResultContentBlock{
				{Kind: &pbv1.ToolResultContentBlock_Text{Text: &pbv1.ToolResultTextBlock{Text: "one"}}},
				{Kind: &pbv1.ToolResultContentBlock_Unknown{Unknown: &pbv1.ToolResultUnknownBlock{Kind: "media", PayloadJson: []byte(`{"data":"secret"}`)}}},
				{Kind: &pbv1.ToolResultContentBlock_CacheBreakpoint{CacheBreakpoint: &pbv1.ToolResultCacheBreakpoint{MarkerJson: []byte(`{"type":"ephemeral"}`)}}},
			},
		}},
	}}}
	if changed, err := ReplaceToolResultWithError(msg, 0, "replacement"); err != nil || !changed {
		t.Fatalf("replace multi-arm result: changed=%v err=%v", changed, err)
	}
	result := msg.Blocks[0].GetToolResult()
	if len(result.Content) != 2 || result.Content[0].GetText().Text != "replacement" || result.Content[1].GetCacheBreakpoint() == nil || result.IsError == nil || !*result.IsError {
		t.Fatalf("collapsed result = %+v", result)
	}
}

func TestReplaceToolResultWithErrorPreservesMultipleCacheSegments(t *testing.T) {
	marker := func(ttl string) *pbv1.ToolResultContentBlock {
		return &pbv1.ToolResultContentBlock{Kind: &pbv1.ToolResultContentBlock_CacheBreakpoint{CacheBreakpoint: &pbv1.ToolResultCacheBreakpoint{MarkerJson: []byte(`{"type":"ephemeral","ttl":"` + ttl + `"}`)}}}
	}
	msg := &pbv1.Message{Role: "user", Blocks: []*pbv1.RequestBlock{{
		Kind: &pbv1.RequestBlock_ToolResult{ToolResult: &pbv1.RequestToolResultBlock{
			Content: []*pbv1.ToolResultContentBlock{
				{Kind: &pbv1.ToolResultContentBlock_Text{Text: &pbv1.ToolResultTextBlock{Text: "secret one"}}}, marker("5m"),
				{Kind: &pbv1.ToolResultContentBlock_Text{Text: &pbv1.ToolResultTextBlock{Text: "secret two"}}}, marker("1h"),
			},
		}},
	}}}
	if changed, err := ReplaceToolResultWithError(msg, 0, "withheld"); err != nil || !changed {
		t.Fatalf("replace segmented result: changed=%v err=%v", changed, err)
	}
	content := msg.Blocks[0].GetToolResult().Content
	if len(content) != 4 || content[0].GetText().Text != "withheld" || content[2].GetText().Text != "withheld" {
		t.Fatalf("sanitized segments = %+v", content)
	}
	if got := string(content[1].GetCacheBreakpoint().MarkerJson); got != `{"type":"ephemeral","ttl":"5m"}` {
		t.Fatalf("first marker = %s", got)
	}
	if got := string(content[3].GetCacheBreakpoint().MarkerJson); got != `{"type":"ephemeral","ttl":"1h"}` {
		t.Fatalf("second marker = %s", got)
	}
}

func TestReplaceToolResultWithErrorIsAtomicOnMalformedShape(t *testing.T) {
	msg := &pbv1.Message{Role: "user", Blocks: []*pbv1.RequestBlock{{
		Kind: &pbv1.RequestBlock_ToolResult{ToolResult: &pbv1.RequestToolResultBlock{
			ToolCallId: "call-1",
			Content: []*pbv1.ToolResultContentBlock{
				{Kind: &pbv1.ToolResultContentBlock_Text{Text: &pbv1.ToolResultTextBlock{Text: "one"}}},
				nil,
			},
		}},
	}}}
	want := proto.Clone(msg).(*pbv1.Message)
	if changed, err := ReplaceToolResultWithError(msg, 0, "replacement"); err == nil || changed {
		t.Fatalf("malformed shape accepted: changed=%v err=%v", changed, err)
	}
	if !proto.Equal(msg, want) {
		t.Fatal("failed replacement mutated the message")
	}
}
