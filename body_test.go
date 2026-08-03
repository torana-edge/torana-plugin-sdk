package plugin_sdk

// Body-helper adversarial matrix: reads return copied views in wire order;
// writes mutate in place with explicit errors; signature-clearing rules are
// applied exactly where the provenance contracts demand them.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

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

// TestHelperPropertyMatrix is the compact reference/property matrix for the
// body helpers: starting from a valid message, a successful helper call must
// produce an absolutely valid message; a semantic no-op must preserve every
// provenance token byte-for-byte; an actual covered-content change must clear
// exactly the covered token(s); an error must leave the input byte-for-byte
// unchanged; typed-nil block adversaries must error, not panic; and no
// helper may break trailing-signature finality.
func TestHelperPropertyMatrix(t *testing.T) {
	valid := func(m *pbv2.Message) bool {
		return (&pbv2.ChatRequest{Messages: []*pbv2.Message{m}}).ValidateReplacement() == nil
	}
	snapshot := func(m *pbv2.Message) []byte {
		b, err := proto.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	expectValid := func(t *testing.T, what string, m *pbv2.Message) {
		t.Helper()
		if !valid(m) {
			t.Fatalf("%s: helper output is not absolutely valid: %v",
				what, (&pbv2.ChatRequest{Messages: []*pbv2.Message{m}}).ValidateReplacement())
		}
	}
	expectNoopPreserves := func(t *testing.T, what string, m *pbv2.Message, before []byte) {
		t.Helper()
		if !bytes.Equal(before, snapshot(m)) {
			t.Fatalf("%s: semantic no-op changed the message", what)
		}
	}

	// 1. No-op SetTextAt preserves the block token AND the trailing token.
	m := helperMsg()
	before := snapshot(m)
	if err := SetTextAt(m, 0, "one"); err != nil {
		t.Fatal(err)
	}
	expectNoopPreserves(t, "SetTextAt unchanged", m, before)

	// 2. Changed SetTextAt clears exactly the covered token + trailing.
	m = helperMsg()
	if err := SetTextAt(m, 0, "ONE"); err != nil {
		t.Fatal(err)
	}
	if m.Blocks[0].GetText().Signature != "" {
		t.Fatal("covered token not cleared on change")
	}
	if m.Blocks[1].GetThinking().Signature != "ST" {
		t.Fatal("uncovered thinking token cleared")
	}
	if m.Blocks[2].GetToolUse().Signature != "SC" {
		t.Fatal("uncovered tool token cleared")
	}
	if last := m.Blocks[len(m.Blocks)-1]; last.GetTrailingSignature() != nil {
		t.Fatal("trailing token survived a covered-content change")
	}
	expectValid(t, "SetTextAt changed", m)

	// 3. No-op ReplaceAllText preserves every token (already-collapsed
	// message with one text block equal to the requested value).
	m = &pbv2.Message{Role: "assistant", Blocks: []*pbv2.RequestBlock{
		{Kind: &pbv2.RequestBlock_Text{Text: &pbv2.RequestTextBlock{Text: "onetwo", Signature: "S1"}}},
		{Kind: &pbv2.RequestBlock_Thinking{Thinking: &pbv2.RequestThinkingBlock{Text: "reason", Signature: "ST"}}},
		{Kind: &pbv2.RequestBlock_TrailingSignature{TrailingSignature: &pbv2.RequestTrailingSignatureBlock{Signature: "STRAIL"}}},
	}}
	before = snapshot(m)
	if err := ReplaceAllText(m, "onetwo"); err != nil {
		t.Fatal(err)
	}
	expectNoopPreserves(t, "ReplaceAllText no-op", m, before)
	expectValid(t, "ReplaceAllText no-op", m)

	// 4. No-op ReplaceToolCall preserves the call token (exact bytes).
	m = helperMsg()
	before = snapshot(m)
	if err := ReplaceToolCall(m, 2, ToolCallInput{Id: "t1", Name: "read", Arguments: []byte(`{"path":"x"}`)}); err != nil {
		t.Fatal(err)
	}
	expectNoopPreserves(t, "ReplaceToolCall identical", m, before)

	// 5. Changed ReplaceToolCall clears exactly the call token.
	m = helperMsg()
	if err := ReplaceToolCall(m, 2, ToolCallInput{Id: "t1", Name: "read", Arguments: []byte(`{"path":"y"}`)}); err != nil {
		t.Fatal(err)
	}
	if m.Blocks[2].GetToolUse().Signature != "" {
		t.Fatal("call token not cleared on argument change")
	}
	if m.Blocks[0].GetText().Signature != "S1" || m.Blocks[1].GetThinking().Signature != "ST" {
		t.Fatal("uncovered tokens cleared")
	}
	expectValid(t, "ReplaceToolCall changed", m)

	// 6. Trailing-final safety: insertions after the final trailing block
	// are refused; insertions AT its position are fine.
	m = helperMsg() // trailing at index 5
	if err := AddToolCall(m, 6, ToolCallInput{Id: "t9", Name: "w", Arguments: []byte(`{}`)}); err == nil {
		t.Fatal("AddToolCall after trailing accepted")
	}
	if err := AddCacheBreakpoint(m, 6, []byte(`{}`)); err == nil {
		t.Fatal("AddCacheBreakpoint after trailing accepted")
	}
	before = snapshot(m)
	if err := AddToolCall(m, 5, ToolCallInput{Id: "t9", Name: "w", Arguments: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if last := m.Blocks[len(m.Blocks)-1]; last.GetTrailingSignature() == nil {
		t.Fatal("trailing signature lost its finality after a legal insert")
	}
	expectValid(t, "AddToolCall before trailing", m)
	_ = before

	// 7. ReplaceAllText with no text block: an EXPLICIT EMPTY text block is
	// created for the empty string (absence != empty), and a stale trailing
	// token is removed before appending.
	toolOnly := &pbv2.Message{Role: "assistant", Blocks: []*pbv2.RequestBlock{{
		Kind: &pbv2.RequestBlock_ToolUse{ToolUse: &pbv2.RequestToolUseBlock{
			Id: "t1", Name: "read", ArgumentsJson: []byte(`{}`),
		}},
	}}}
	if err := ReplaceAllText(toolOnly, ""); err != nil {
		t.Fatal(err)
	}
	if len(toolOnly.Blocks) != 2 || toolOnly.Blocks[1].GetText() == nil || toolOnly.Blocks[1].GetText().Text != "" {
		t.Fatalf("empty replacement must append one explicit empty text block: %+v", toolOnly.Blocks)
	}
	expectValid(t, "ReplaceAllText empty append", toolOnly)

	thinkingOnly := &pbv2.Message{Role: "assistant", Blocks: []*pbv2.RequestBlock{
		{Kind: &pbv2.RequestBlock_Thinking{Thinking: &pbv2.RequestThinkingBlock{Text: "r"}}},
	}}
	if err := ReplaceAllText(thinkingOnly, ""); err != nil {
		t.Fatal(err)
	}
	if len(thinkingOnly.Blocks) != 2 || thinkingOnly.Blocks[1].GetText() == nil {
		t.Fatalf("thinking + explicit empty text expected: %+v", thinkingOnly.Blocks)
	}
	expectValid(t, "ReplaceAllText thinking + empty", thinkingOnly)

	thinkingTrailing := &pbv2.Message{Role: "assistant", Blocks: []*pbv2.RequestBlock{
		{Kind: &pbv2.RequestBlock_Thinking{Thinking: &pbv2.RequestThinkingBlock{Text: "r", Signature: "ST"}}},
		{Kind: &pbv2.RequestBlock_TrailingSignature{TrailingSignature: &pbv2.RequestTrailingSignatureBlock{Signature: "S"}}},
	}}
	if err := ReplaceAllText(thinkingTrailing, ""); err != nil {
		t.Fatal(err)
	}
	if len(thinkingTrailing.Blocks) != 2 || thinkingTrailing.Blocks[1].GetText() == nil {
		t.Fatalf("thinking + explicit empty text expected: %+v", thinkingTrailing.Blocks)
	}
	if last := thinkingTrailing.Blocks[len(thinkingTrailing.Blocks)-1]; last.GetTrailingSignature() != nil {
		t.Fatal("stale trailing token not removed on append")
	}
	expectValid(t, "ReplaceAllText thinking + trailing + empty", thinkingTrailing)

	// An already-explicit-empty single text block with the empty request is
	// a byte-identical no-op preserving its signature and a valid trailing
	// token.
	already := &pbv2.Message{Role: "assistant", Blocks: []*pbv2.RequestBlock{
		{Kind: &pbv2.RequestBlock_Text{Text: &pbv2.RequestTextBlock{Text: "", Signature: "S1"}}},
		{Kind: &pbv2.RequestBlock_Thinking{Thinking: &pbv2.RequestThinkingBlock{Text: "r", Signature: "ST"}}},
		{Kind: &pbv2.RequestBlock_TrailingSignature{TrailingSignature: &pbv2.RequestTrailingSignatureBlock{Signature: "S"}}},
	}}
	before = snapshot(already)
	if err := ReplaceAllText(already, ""); err != nil {
		t.Fatal(err)
	}
	expectNoopPreserves(t, "ReplaceAllText empty no-op", already, before)
	expectValid(t, "ReplaceAllText empty no-op", already)

	// 8. ReplaceAllText with no text block and a trailing signature: the
	// stale token is removed, the text is appended, and the result is valid.
	none := &pbv2.Message{Role: "assistant", Blocks: []*pbv2.RequestBlock{
		{Kind: &pbv2.RequestBlock_CacheBreakpoint{CacheBreakpoint: &pbv2.RequestCacheBreakpoint{MarkerJson: []byte(`{}`)}}},
		{Kind: &pbv2.RequestBlock_TrailingSignature{TrailingSignature: &pbv2.RequestTrailingSignatureBlock{Signature: "S"}}},
	}}
	if err := ReplaceAllText(none, "added"); err != nil {
		t.Fatal(err)
	}
	expectValid(t, "ReplaceAllText append with trailing", none)
	last := none.Blocks[len(none.Blocks)-1]
	if last.GetText() == nil || last.GetText().Text != "added" {
		t.Fatalf("text not appended: %+v", none.Blocks)
	}

	// 9. Errors leave the input byte-for-byte unchanged.
	for _, tc := range []struct {
		name string
		call func(*pbv2.Message) error
	}{
		{"SetTextAt wrong kind", func(m *pbv2.Message) error { return SetTextAt(m, 1, "x") }},
		{"SetTextAt nil block", func(m *pbv2.Message) error { return SetTextAt(m, 2, "x") }},
		{"ReplaceToolCall nil block", func(m *pbv2.Message) error {
			return ReplaceToolCall(m, 2, ToolCallInput{Id: "a", Name: "b", Arguments: []byte(`{}`)})
		}},
		{"SetCacheBreakpoint nil block", func(m *pbv2.Message) error { return SetCacheBreakpoint(m, 2, []byte(`{}`)) }},
		{"DeleteCacheBreakpoint nil block", func(m *pbv2.Message) error { return DeleteCacheBreakpoint(m, 2) }},
		{"AddToolCall after trailing", func(m *pbv2.Message) error {
			return AddToolCall(m, 6, ToolCallInput{Id: "a", Name: "b", Arguments: []byte(`{}`)})
		}},
	} {
		m = helperMsg()
		// Inject a nil block at index 2 for the nil adversaries BEFORE the
		// snapshot, so the invariance check covers the helper call itself.
		if tc.name == "SetTextAt nil block" || tc.name == "ReplaceToolCall nil block" ||
			tc.name == "SetCacheBreakpoint nil block" || tc.name == "DeleteCacheBreakpoint nil block" {
			m.Blocks[2] = nil
		}
		before := snapshot(m)
		if err := tc.call(m); err == nil {
			t.Fatalf("%s: expected an error", tc.name)
		}
		if !bytes.Equal(before, snapshot(m)) {
			t.Fatalf("%s: error mutated the input", tc.name)
		}
	}

	// 10. Typed-nil arm adversaries: wrong-kind errors, never panics.
	m = helperMsg()
	m.Blocks = append(m.Blocks, &pbv2.RequestBlock{Kind: &pbv2.RequestBlock_Text{}})
	before = snapshot(m)
	if err := SetTextAt(m, 6, "x"); err == nil {
		t.Fatal("typed-nil text arm accepted")
	}
	if !bytes.Equal(before, snapshot(m)) {
		t.Fatal("typed-nil error mutated the input")
	}
}

// TestCacheControlUseNumber: contract-valid numeric markers must not vanish
// through the convenience decode.
func TestCacheControlUseNumber(t *testing.T) {
	m := &pbv2.Message{Role: "user", Blocks: []*pbv2.RequestBlock{{
		Kind: &pbv2.RequestBlock_CacheBreakpoint{CacheBreakpoint: &pbv2.RequestCacheBreakpoint{
			MarkerJson: []byte(`{"n":1e999,"big":18446744073709551615}`),
		}},
	}}}
	cc := CacheControl(m)
	if cc == nil {
		t.Fatal("marker vanished through the decode")
	}
	if _, ok := cc["n"].(json.Number); !ok {
		t.Fatalf("1e999 must decode as json.Number, got %T", cc["n"])
	}
	if _, ok := cc["big"].(json.Number); !ok {
		t.Fatalf("uint64-sized member must decode as json.Number, got %T", cc["big"])
	}
}
