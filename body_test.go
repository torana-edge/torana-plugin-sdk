package plugin_sdk

// Body-helper adversarial matrix: reads return copied views in wire order;
// writes mutate in place with explicit errors; signature-clearing rules are
// applied exactly where the provenance contracts demand them.

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

func textBlock(text string) *pbv2.RequestBlock {
	return &pbv2.RequestBlock{Kind: &pbv2.RequestBlock_Text{Text: &pbv2.RequestTextBlock{Text: text}}}
}

func helperMsg() *pbv2.Message {
	return &pbv2.Message{
		Role: "assistant",
		Blocks: []*pbv2.RequestBlock{
			{Kind: &pbv2.RequestBlock_Text{Text: &pbv2.RequestTextBlock{Text: "one", Signature: "S1"}}},
			{Kind: &pbv2.RequestBlock_Thinking{Thinking: &pbv2.RequestThinkingBlock{Text: "reason", Signature: "ST"}}},
			{Kind: &pbv2.RequestBlock_ToolUse{ToolUse: &pbv2.RequestToolUseBlock{
				Id: "t1", Name: "read", ArgumentsJson: []byte(`{"path":"x"}`), Signature: "SC",
			}}},
			{Kind: &pbv2.RequestBlock_Text{Text: &pbv2.RequestTextBlock{Text: "two"}}},
			{Kind: &pbv2.RequestBlock_CacheBreakpoint{CacheBreakpoint: &pbv2.RequestCacheBreakpoint{
				MarkerJson: []byte(`{"type":"ephemeral"}`),
			}}},
			{Kind: &pbv2.RequestBlock_TrailingSignature{TrailingSignature: &pbv2.RequestTrailingSignatureBlock{Signature: "STRAIL"}}},
		},
	}
}

func TestTextSegmentsAndText(t *testing.T) {
	segs := TextSegments(helperMsg())
	if len(segs) != 2 || segs[0].Text != "one" || segs[1].Text != "two" {
		t.Fatalf("segments: %+v", segs)
	}
	if Text(helperMsg()) != "onetwo" {
		t.Fatalf("Text: %q", Text(helperMsg()))
	}
	if Text(nil) != "" || TextSegments(nil) != nil {
		t.Fatal("nil message must yield empty views")
	}
}

func TestSetTextAt(t *testing.T) {
	// Wrong kind / out of range are explicit errors.
	m := helperMsg()
	if err := SetTextAt(m, 1, "x"); err == nil || !strings.Contains(err.Error(), "not a text block") {
		t.Fatalf("thinking block accepted as text target: %v", err)
	}
	if err := SetTextAt(m, 99, "x"); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("out of range accepted: %v", err)
	}
	if err := SetTextAt(nil, 0, "x"); err == nil {
		t.Fatal("nil message accepted")
	}

	// A text change clears the block's signature AND the trailing signature
	// (its TrailingStandalone scope covered the changed content).
	m = helperMsg()
	if err := SetTextAt(m, 0, "ONE"); err != nil {
		t.Fatal(err)
	}
	if m.Blocks[0].GetText().Signature != "" {
		t.Fatal("changed text kept its signature token")
	}
	if got := m.Blocks[len(m.Blocks)-1]; got.GetTrailingSignature() != nil {
		t.Fatal("trailing signature survived a covered-content change")
	}
	// Unchanged text keeps both signatures.
	m = helperMsg()
	if err := SetTextAt(m, 0, "one"); err != nil {
		t.Fatal(err)
	}
	if m.Blocks[0].GetText().Signature != "S1" {
		t.Fatal("unchanged text lost its signature")
	}
	if m.Blocks[len(m.Blocks)-1].GetTrailingSignature() == nil {
		t.Fatal("trailing signature lost on unchanged text")
	}
}

func TestReplaceAllText(t *testing.T) {
	m := helperMsg()
	if err := ReplaceAllText(m, "merged"); err != nil {
		t.Fatal(err)
	}
	if Text(m) != "merged" {
		t.Fatalf("Text after replace: %q", Text(m))
	}
	// Collapse semantics: exactly one text block, in the first text position.
	textCount := 0
	firstText := -1
	for i, b := range m.Blocks {
		if b.GetText() != nil {
			textCount++
			if firstText < 0 {
				firstText = i
			}
		}
	}
	if textCount != 1 {
		t.Fatalf("text blocks after collapse: %d", textCount)
	}
	// The first text position keeps its slot (block 0); thinking follows.
	if m.Blocks[0].GetText() == nil || firstText != 0 || m.Blocks[1].GetThinking() == nil {
		t.Fatalf("collapse moved unrelated blocks: %+v", m.Blocks)
	}

	// No text block at all: appended at the end.
	none := &pbv2.Message{Role: "user", Blocks: []*pbv2.RequestBlock{{
		Kind: &pbv2.RequestBlock_CacheBreakpoint{CacheBreakpoint: &pbv2.RequestCacheBreakpoint{MarkerJson: []byte(`{}`)}},
	}}}
	if err := ReplaceAllText(none, "added"); err != nil {
		t.Fatal(err)
	}
	last := none.Blocks[len(none.Blocks)-1]
	if last.GetText() == nil || last.GetText().Text != "added" {
		t.Fatalf("text not appended: %+v", none.Blocks)
	}
}

func TestToolCallsAndResults(t *testing.T) {
	calls := ToolCalls(helperMsg())
	if len(calls) != 1 || calls[0].Block != 2 || calls[0].Id != "t1" || calls[0].Name != "read" {
		t.Fatalf("calls: %+v", calls)
	}
	if string(calls[0].Arguments) != `{"path":"x"}` {
		t.Fatalf("arguments bytes changed: %s", calls[0].Arguments)
	}
	// Copied view: mutating the view must not touch the message.
	calls[0].Arguments[0] = 'X'
	if got := helperMsg().Blocks[2].GetToolUse().ArgumentsJson; string(got) != `{"path":"x"}` {
		t.Fatal("view shares memory with the message")
	}

	results := ToolResults(&pbv2.Message{Role: "user", Blocks: []*pbv2.RequestBlock{{
		Kind: &pbv2.RequestBlock_ToolResult{ToolResult: &pbv2.RequestToolResultBlock{
			ToolCallId: "t1",
			Content: []*pbv2.ToolResultContentBlock{
				{Kind: &pbv2.ToolResultContentBlock_Text{Text: &pbv2.ToolResultTextBlock{Text: "ok"}}},
				{Kind: &pbv2.ToolResultContentBlock_Unknown{Unknown: &pbv2.ToolResultUnknownBlock{
					Kind: "json", PayloadJson: []byte(`{"score":42}`),
				}}},
			},
		}},
	}}})
	if len(results) != 1 || results[0].ToolCallId != "t1" || len(results[0].Content) != 2 {
		t.Fatalf("results: %+v", results)
	}
	if results[0].Content[1].UnknownKind != "json" || string(results[0].Content[1].UnknownData) != `{"score":42}` {
		t.Fatalf("nested view: %+v", results[0].Content[1])
	}
}

func TestAddAndReplaceToolCall(t *testing.T) {
	m := &pbv2.Message{Role: "assistant", Blocks: []*pbv2.RequestBlock{textBlock("hi")}}
	if err := AddToolCall(m, 1, ToolCallInput{Id: "t9", Name: "write", Arguments: []byte(`{"f":"g"}`)}); err != nil {
		t.Fatal(err)
	}
	if len(m.Blocks) != 2 || m.Blocks[1].GetToolUse() == nil || m.Blocks[1].GetToolUse().Id != "t9" {
		t.Fatalf("insert: %+v", m.Blocks)
	}
	// Out of range / invalid input.
	if err := AddToolCall(m, 99, ToolCallInput{Id: "x", Name: "y", Arguments: []byte(`{}`)}); err == nil {
		t.Fatal("out-of-range insert accepted")
	}
	if err := AddToolCall(m, 0, ToolCallInput{Name: "y", Arguments: []byte(`{}`)}); err == nil {
		t.Fatal("missing id accepted")
	}
	if err := AddToolCall(m, 0, ToolCallInput{Id: "x", Name: "y", Arguments: []byte(`[]`)}); err == nil {
		t.Fatal("non-object arguments accepted")
	}

	// Replace clears the call-bound signature (covered content changed).
	m = helperMsg()
	if err := ReplaceToolCall(m, 2, ToolCallInput{Id: "t2", Name: "read", Arguments: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	tu := m.Blocks[2].GetToolUse()
	if tu.Id != "t2" || tu.Signature != "" {
		t.Fatalf("replace: %+v", tu)
	}
	if err := ReplaceToolCall(m, 0, ToolCallInput{Id: "x", Name: "y", Arguments: []byte(`{}`)}); err == nil ||
		!strings.Contains(err.Error(), "not a tool-use block") {
		t.Fatalf("wrong-kind replace: %v", err)
	}
}

func TestCacheBreakpoints(t *testing.T) {
	m := helperMsg()
	views := CacheBreakpoints(m)
	if len(views) != 1 || views[0].Block != 4 || string(views[0].Marker) != `{"type":"ephemeral"}` {
		t.Fatalf("views: %+v", views)
	}
	if cc := CacheControl(m); cc["type"] != "ephemeral" {
		t.Fatalf("CacheControl: %+v", cc)
	}
	if CacheControl(&pbv2.Message{Role: "user", Blocks: []*pbv2.RequestBlock{textBlock("x")}}) != nil {
		t.Fatal("CacheControl on a marker-free message must be nil")
	}

	// Set replaces the marker verbatim; wrong kind / invalid object refused.
	if err := SetCacheBreakpoint(m, 4, []byte(`{ "ttl" : 1 }`)); err != nil {
		t.Fatal(err)
	}
	if string(m.Blocks[4].GetCacheBreakpoint().MarkerJson) != `{ "ttl" : 1 }` {
		t.Fatalf("marker bytes not verbatim: %s", m.Blocks[4].GetCacheBreakpoint().MarkerJson)
	}
	if err := SetCacheBreakpoint(m, 0, []byte(`{}`)); err == nil {
		t.Fatal("wrong-kind set accepted")
	}
	if err := SetCacheBreakpoint(m, 4, []byte(`[]`)); err == nil {
		t.Fatal("non-object marker accepted")
	}
	if err := SetCacheBreakpoint(m, 4, nil); err == nil {
		t.Fatal("empty marker accepted (use DeleteCacheBreakpoint)")
	}

	// Add inserts at an explicit position; multiple markers are
	// representable and ordered.
	if err := AddCacheBreakpoint(m, 2, []byte(`{"type":"ephemeral"}`)); err != nil {
		t.Fatal(err)
	}
	views = CacheBreakpoints(m)
	if len(views) != 2 || views[0].Block != 2 || views[1].Block != 5 {
		t.Fatalf("after add: %+v", views)
	}
	if err := AddCacheBreakpoint(m, 99, []byte(`{}`)); err == nil {
		t.Fatal("out-of-range add accepted")
	}

	// Delete removes exactly the named position.
	if err := DeleteCacheBreakpoint(m, 2); err != nil {
		t.Fatal(err)
	}
	views = CacheBreakpoints(m)
	if len(views) != 1 || views[0].Block != 4 {
		t.Fatalf("after delete: %+v", views)
	}
	if err := DeleteCacheBreakpoint(m, 0); err == nil {
		t.Fatal("wrong-kind delete accepted")
	}
}

// TestBodyHelpersMarkerBytesVerbatim: markers/arguments ride verbatim — a
// decode/re-encode would reorder keys and change the cached prefix.
func TestBodyHelpersMarkerBytesVerbatim(t *testing.T) {
	marker := []byte(`{ "zebra" : 1 , "apple" : 2 }`)
	m := &pbv2.Message{Role: "user", Blocks: []*pbv2.RequestBlock{
		{Kind: &pbv2.RequestBlock_CacheBreakpoint{CacheBreakpoint: &pbv2.RequestCacheBreakpoint{MarkerJson: marker}}},
	}}
	views := CacheBreakpoints(m)
	if !bytes.Equal(views[0].Marker, marker) {
		t.Fatalf("marker bytes changed: %s", views[0].Marker)
	}
}

// Compileable examples: the three ordinary plugin tasks the ruling names —
// text compaction, tool-result scanning, and cache marker placement.

func ExampleReplaceAllText_textCompaction() {
	msg := &pbv2.Message{
		Role: "tool",
		Blocks: []*pbv2.RequestBlock{
			{Kind: &pbv2.RequestBlock_Text{Text: &pbv2.RequestTextBlock{Text: "long output line one\n"}}},
			{Kind: &pbv2.RequestBlock_Text{Text: &pbv2.RequestTextBlock{Text: "long output line two\n"}}},
			{Kind: &pbv2.RequestBlock_Text{Text: &pbv2.RequestTextBlock{Text: "long output line three\n"}}},
		},
	}
	// A compactor collapses the whole text to its replacement, exactly once.
	if err := ReplaceAllText(msg, "compacted result"); err != nil {
		panic(err)
	}
	// Output: compacted result
	fmt.Println(Text(msg))
}

func ExampleToolResults_toolResultScanning() {
	msg := &pbv2.Message{
		Role: "user",
		Blocks: []*pbv2.RequestBlock{
			{Kind: &pbv2.RequestBlock_Text{Text: &pbv2.RequestTextBlock{Text: "see"}}},
			{Kind: &pbv2.RequestBlock_ToolResult{ToolResult: &pbv2.RequestToolResultBlock{
				ToolCallId: "call_1",
				Content: []*pbv2.ToolResultContentBlock{
					{Kind: &pbv2.ToolResultContentBlock_Text{Text: &pbv2.ToolResultTextBlock{Text: "the answer"}}},
					{Kind: &pbv2.ToolResultContentBlock_Unknown{Unknown: &pbv2.ToolResultUnknownBlock{
						Kind: "json", PayloadJson: []byte(`{"score":42}`),
					}}},
				},
			}}},
		},
	}
	// PII / compactor scanning: enumerate tool results and their text.
	for _, r := range ToolResults(msg) {
		fmt.Printf("result for %s\n", r.ToolCallId)
		for _, c := range r.Content {
			if c.Text != "" {
				fmt.Println(c.Text)
			}
		}
	}
	// Output:
	// result for call_1
	// the answer
}

func ExampleCacheBreakpoints_cacheMarkerPlacement() {
	msg := &pbv2.Message{
		Role: "assistant",
		Blocks: []*pbv2.RequestBlock{
			{Kind: &pbv2.RequestBlock_Text{Text: &pbv2.RequestTextBlock{Text: "answer"}}},
		},
	}
	// A warmer places a breakpoint after the content it covers.
	if err := AddCacheBreakpoint(msg, 1, []byte(`{"type":"ephemeral"}`)); err != nil {
		panic(err)
	}
	for _, v := range CacheBreakpoints(msg) {
		fmt.Printf("marker at %d: %s\n", v.Block, v.Marker)
	}
	// Output:
	// marker at 1: {"type":"ephemeral"}
}
