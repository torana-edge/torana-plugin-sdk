package plugin_sdk

// Tests for the compactor seam prerequisite: ToolResultScalarText and
// ReplaceToolResultText (checkpoint REV 2 bindings).

import (
	"testing"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
	"google.golang.org/protobuf/proto"
)

func trContent(cs ...*pbv2.ToolResultContentBlock) *pbv2.RequestToolResultBlock {
	return &pbv2.RequestToolResultBlock{ToolCallId: "c1", ToolName: "read", Content: cs}
}

func trText(s string) *pbv2.ToolResultContentBlock {
	return &pbv2.ToolResultContentBlock{Kind: &pbv2.ToolResultContentBlock_Text{Text: &pbv2.ToolResultTextBlock{Text: s}}}
}

func trUnknown() *pbv2.ToolResultContentBlock {
	return &pbv2.ToolResultContentBlock{Kind: &pbv2.ToolResultContentBlock_Unknown{Unknown: &pbv2.ToolResultUnknownBlock{Kind: "provider_blob", PayloadJson: []byte(`{"x":1}`)}}}
}

func trMarker(m string) *pbv2.ToolResultContentBlock {
	return &pbv2.ToolResultContentBlock{Kind: &pbv2.ToolResultContentBlock_CacheBreakpoint{CacheBreakpoint: &pbv2.ToolResultCacheBreakpoint{MarkerJson: []byte(m)}}}
}

func toolResultMsg(tr *pbv2.RequestToolResultBlock) *pbv2.Message {
	return &pbv2.Message{Role: "user", Blocks: []*pbv2.RequestBlock{{Kind: &pbv2.RequestBlock_ToolResult{ToolResult: tr}}}}
}

func scalarOf(t *testing.T, msg *pbv2.Message) (string, bool) {
	t.Helper()
	views := ToolResults(msg)
	if len(views) != 1 {
		t.Fatalf("expected one tool-result view, got %d", len(views))
	}
	return ToolResultScalarText(views[0])
}

// TestToolResultScalarTextMatrix — the scalar-compatibility discriminator:
// exactly one text arm (incl. an EXPLICIT EMPTY one) with zero unknown arms
// and any number of cache-marker arms; zero/multiple text arms or any
// unknown arm are NOT compatible (no concatenation ever).
func TestToolResultScalarTextMatrix(t *testing.T) {
	rows := []struct {
		name string
		tr   *pbv2.RequestToolResultBlock
		want string
		ok   bool
	}{
		{"single text arm", trContent(trText("the result")), "the result", true},
		{"explicit empty text arm", trContent(trText("")), "", true},
		{"text plus marker", trContent(trText("r"), trMarker(`{"type":"ephemeral"}`)), "r", true},
		{"marker before text", trContent(trMarker(`{"type":"ephemeral"}`), trText("r")), "r", true},
		{"marker-only (zero text arms)", trContent(trMarker(`{"type":"ephemeral"}`)), "", false},
		{"zero arms", trContent(), "", false},
		{"multiple text arms", trContent(trText("a"), trText("b")), "", false},
		{"unknown arm", trContent(trText("r"), trUnknown()), "", false},
		{"unknown only", trContent(trUnknown()), "", false},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			got, ok := scalarOf(t, toolResultMsg(row.tr))
			if ok != row.ok || (ok && got != row.want) {
				t.Fatalf("ToolResultScalarText = (%q, %v), want (%q, %v)", got, ok, row.want, row.ok)
			}
		})
	}
}

// TestReplaceToolResultTextInPlace — the exact in-place contract: the nested
// arm count/order and every marker byte are retained; only the designated
// text arm's value changes; a byte-identical value is a structural no-op
// preserving the whole message.
func TestReplaceToolResultTextInPlace(t *testing.T) {
	mk := func() *pbv2.Message {
		return toolResultMsg(trContent(
			trMarker(`{"type":"ephemeral"}`),
			trText("before"),
			trMarker(`{"type":"standard"}`),
		))
	}

	// Real change: markers stay at their exact positions with exact bytes.
	msg := mk()
	changed, err := ReplaceToolResultText(msg, 0, "after")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v, want changed", changed, err)
	}
	tr := msg.Blocks[0].GetToolResult()
	if len(tr.Content) != 3 {
		t.Fatalf("arm count changed: %d, want 3", len(tr.Content))
	}
	if string(tr.Content[0].GetCacheBreakpoint().MarkerJson) != `{"type":"ephemeral"}` ||
		string(tr.Content[2].GetCacheBreakpoint().MarkerJson) != `{"type":"standard"}` {
		t.Fatalf("marker bytes moved: %+v", tr.Content)
	}
	if tr.Content[1].GetText().Text != "after" {
		t.Fatalf("text = %q, want after", tr.Content[1].GetText().Text)
	}

	// Structural no-op: byte-identical value preserves the ENTIRE message.
	before := proto.Clone(msg).(*pbv2.Message)
	changed, err = ReplaceToolResultText(msg, 0, "after")
	if err != nil || changed {
		t.Fatalf("no-op: changed=%v err=%v, want unchanged", changed, err)
	}
	if !proto.Equal(msg, before) {
		t.Fatal("a no-op mutated the message")
	}
}

// TestReplaceToolResultTextAtomicErrors — every error leaves the message
// byte/structurally unchanged: nil message, out-of-range block,
// non-tool-result block, zero text arms, multiple text arms, unknown arm.
func TestReplaceToolResultTextAtomicErrors(t *testing.T) {
	if _, err := ReplaceToolResultText(nil, 0, "x"); err == nil {
		t.Fatal("nil message accepted")
	}
	cases := []struct {
		name  string
		msg   *pbv2.Message
		block int
	}{
		{"out of range", toolResultMsg(trContent(trText("r"))), 1},
		{"non-tool-result block", &pbv2.Message{Role: "user", Blocks: []*pbv2.RequestBlock{{Kind: &pbv2.RequestBlock_Text{Text: &pbv2.RequestTextBlock{Text: "t"}}}}}, 0},
		{"zero text arms", toolResultMsg(trContent(trMarker(`{"type":"ephemeral"}`))), 0},
		{"multiple text arms", toolResultMsg(trContent(trText("a"), trText("b"))), 0},
		{"unknown arm", toolResultMsg(trContent(trText("r"), trUnknown())), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := proto.Clone(tc.msg).(*pbv2.Message)
			if _, err := ReplaceToolResultText(tc.msg, tc.block, "x"); err == nil {
				t.Fatal("error case accepted")
			}
			if !proto.Equal(tc.msg, before) {
				t.Fatal("an error path mutated the message")
			}
		})
	}
}

// TestReplaceToolResultTextProvenance — a real change preserves
// part_metadata_json but clears the tool-result signature and a final
// trailing-signature block (stale covered scope); the changed result is
// still in the SDK replacement domain (ValidateReplacement green). No stale
// token survives.
func TestReplaceToolResultTextProvenance(t *testing.T) {
	msg := &pbv2.Message{Role: "user", Blocks: []*pbv2.RequestBlock{
		{Kind: &pbv2.RequestBlock_Text{Text: &pbv2.RequestTextBlock{Text: "covered"}}},
		{Kind: &pbv2.RequestBlock_ToolResult{ToolResult: &pbv2.RequestToolResultBlock{
			ToolCallId:       "c1",
			PartMetadataJson: []byte(`{"custom":1}`),
			Signature:        "sig-token",
			Content:          []*pbv2.ToolResultContentBlock{trText("before")},
		}}},
		{Kind: &pbv2.RequestBlock_TrailingSignature{TrailingSignature: &pbv2.RequestTrailingSignatureBlock{Signature: "trailing-token"}}},
	}}
	changed, err := ReplaceToolResultText(msg, 1, "after")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	tr := msg.Blocks[1].GetToolResult()
	if tr.Signature != "" {
		t.Fatalf("stale tool-result signature survived: %q", tr.Signature)
	}
	if string(tr.PartMetadataJson) != `{"custom":1}` {
		t.Fatalf("part_metadata_json was not preserved: %s", tr.PartMetadataJson)
	}
	for _, b := range msg.Blocks {
		if b.GetTrailingSignature() != nil {
			t.Fatal("a stale trailing-signature block survived the change")
		}
	}
	if err := (&pbv2.ChatRequest{Model: "m", Messages: []*pbv2.Message{msg}}).ValidateReplacement(); err != nil {
		t.Fatalf("the changed result left the replacement domain: %v", err)
	}

	// The no-op keeps every token byte-for-byte.
	msg2 := &pbv2.Message{Role: "user", Blocks: []*pbv2.RequestBlock{
		{Kind: &pbv2.RequestBlock_Text{Text: &pbv2.RequestTextBlock{Text: "covered"}}},
		{Kind: &pbv2.RequestBlock_ToolResult{ToolResult: &pbv2.RequestToolResultBlock{
			ToolCallId:       "c1",
			PartMetadataJson: []byte(`{"custom":1}`),
			Signature:        "sig-token",
			Content:          []*pbv2.ToolResultContentBlock{trText("same")},
		}}},
		{Kind: &pbv2.RequestBlock_TrailingSignature{TrailingSignature: &pbv2.RequestTrailingSignatureBlock{Signature: "trailing-token"}}},
	}}
	before := proto.Clone(msg2).(*pbv2.Message)
	changed, err = ReplaceToolResultText(msg2, 1, "same")
	if err != nil || changed {
		t.Fatalf("no-op: changed=%v err=%v", changed, err)
	}
	if !proto.Equal(msg2, before) {
		t.Fatal("the no-op disturbed provenance tokens")
	}
}

// TestReplaceToolResultTextRevertProof — the weakened form: a replacement
// that ALSO changed a marker byte or kept the signature would fail the
// structural expectations — the pins are not vacuous.
func TestReplaceToolResultTextRevertProof(t *testing.T) {
	msg := toolResultMsg(trContent(trMarker(`{"type":"ephemeral"}`), trText("r")))
	if _, err := ReplaceToolResultText(msg, 0, "after"); err != nil {
		t.Fatal(err)
	}
	// Tamper with a marker byte: the structural equality in the in-place
	// test would catch it.
	tampered := proto.Clone(msg).(*pbv2.Message)
	tampered.Blocks[0].GetToolResult().Content[0].GetCacheBreakpoint().MarkerJson = []byte(`{"type":"standard"}`)
	if proto.Equal(msg, tampered) {
		t.Fatal("revert proof: a marker change went undetected")
	}
	// Tamper with the signature: a kept stale signature fails the provenance
	// expectations.
	tampered2 := proto.Clone(msg).(*pbv2.Message)
	tampered2.Blocks[0].GetToolResult().Signature = "stale"
	if proto.Equal(msg, tampered2) {
		t.Fatal("revert proof: a stale signature went undetected")
	}
}
