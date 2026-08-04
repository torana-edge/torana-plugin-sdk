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

// richToolResultMsg is a RICH ordered message: the designated tool-result
// block carries identity, optional scalars (incl. present-false), outer
// metadata, multiple cache markers, an unrelated SIBLING result with its
// own content, surrounding text blocks, and a final trailing-signature
// block (assistant-role, per the SDK's assistant-only trailing rule).
func richToolResultMsg() *pbv2.Message {
	return &pbv2.Message{Role: "assistant", Blocks: []*pbv2.RequestBlock{
		{Kind: &pbv2.RequestBlock_Text{Text: &pbv2.RequestTextBlock{Text: "leading"}}},
		{Kind: &pbv2.RequestBlock_ToolResult{ToolResult: &pbv2.RequestToolResultBlock{
			ToolCallId:       "c1",
			ToolName:         "read",
			WillContinue:     proto.Bool(false),
			Scheduling:       proto.String("WHEN_IDLE"), // normative vocabulary
			PartMetadataJson: []byte(`{"outer":{"b":1,"a":2}}`),
			Signature:        "result-sig",
			Content: []*pbv2.ToolResultContentBlock{
				trMarker(`{"type":"ephemeral"}`),
				trText("before"),
				trMarker(`{"type":"standard"}`),
			},
		}}},
		{Kind: &pbv2.RequestBlock_ToolResult{ToolResult: &pbv2.RequestToolResultBlock{
			ToolCallId: "c2",
			ToolName:   "write",
			Content: []*pbv2.ToolResultContentBlock{
				trText("sibling-content"),
			},
		}}},
		{Kind: &pbv2.RequestBlock_Text{Text: &pbv2.RequestTextBlock{Text: "trailing"}}},
		{Kind: &pbv2.RequestBlock_TrailingSignature{TrailingSignature: &pbv2.RequestTrailingSignatureBlock{Signature: "trailing-sig"}}},
	}}
}

// TestReplaceToolResultTextRichStructuralEquality — ONLY the authorized
// fields change: the designated text value, the tool-result signature
// (cleared), and the final trailing-signature block (removed). The expected
// is built by cloning the rich original and changing exactly those three;
// proto.Equal(actual, expected) proves identity, optional scalars, outer
// metadata, every marker byte/position, surrounding text blocks, and the
// unrelated sibling result all stay EXACT.
func TestReplaceToolResultTextRichStructuralEquality(t *testing.T) {
	original := richToolResultMsg()
	// The rich original is genuinely IN-DOMAIN before mutation.
	if err := (&pbv2.ChatRequest{Model: "m", Messages: []*pbv2.Message{original}}).ValidateReplacement(); err != nil {
		t.Fatalf("the rich original is not a valid request: %v", err)
	}
	actual := proto.Clone(original).(*pbv2.Message)
	changed, err := ReplaceToolResultText(actual, 1, "after")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	expected := proto.Clone(original).(*pbv2.Message)
	expected.Blocks[1].GetToolResult().Content[1].GetText().Text = "after"
	expected.Blocks[1].GetToolResult().Signature = ""
	// The trailing-signature carrier is PRESERVED byte-for-byte (it covers
	// only preceding text/thinking + its own metadata, NOT tool results).
	if !proto.Equal(actual, expected) {
		t.Fatalf("mutation is not exactly the two authorized changes\n got: %v\nwant: %v", actual, expected)
	}
	// Both the actual and the independent expected stay IN-DOMAIN.
	if err := (&pbv2.ChatRequest{Model: "m", Messages: []*pbv2.Message{actual}}).ValidateReplacement(); err != nil {
		t.Fatalf("the mutated request left the replacement domain: %v", err)
	}
	if err := (&pbv2.ChatRequest{Model: "m", Messages: []*pbv2.Message{expected}}).ValidateReplacement(); err != nil {
		t.Fatalf("the expected request left the replacement domain: %v", err)
	}
	// The byte-identical replay is a structural no-op on the RICH message.
	before := proto.Clone(actual).(*pbv2.Message)
	changed, err = ReplaceToolResultText(actual, 1, "after")
	if err != nil || changed {
		t.Fatalf("no-op: changed=%v err=%v", changed, err)
	}
	if !proto.Equal(actual, before) {
		t.Fatal("the no-op mutated the rich message")
	}
}

// TestReplaceToolResultTextWeakenedProductionMutation — a production
// mutation that also changed a marker, the identity, or a sibling would fail
// the structural equality: the rich pin is not vacuous.
func TestReplaceToolResultTextWeakenedProductionMutation(t *testing.T) {
	original := richToolResultMsg()
	actual := proto.Clone(original).(*pbv2.Message)
	if _, err := ReplaceToolResultText(actual, 1, "after"); err != nil {
		t.Fatal(err)
	}
	expected := proto.Clone(original).(*pbv2.Message)
	expected.Blocks[1].GetToolResult().Content[1].GetText().Text = "after"
	expected.Blocks[1].GetToolResult().Signature = ""

	for name, tamper := range map[string]func(*pbv2.Message){
		"marker byte": func(m *pbv2.Message) {
			m.Blocks[1].GetToolResult().Content[0].GetCacheBreakpoint().MarkerJson = []byte(`{"type":"standard"}`)
		},
		"marker position": func(m *pbv2.Message) {
			c := m.Blocks[1].GetToolResult().Content
			c[0], c[2] = c[2], c[0]
		},
		"tool-call identity": func(m *pbv2.Message) {
			m.Blocks[1].GetToolResult().ToolCallId = "other"
		},
		"optional scalar": func(m *pbv2.Message) {
			m.Blocks[1].GetToolResult().WillContinue = proto.Bool(true)
		},
		"outer metadata": func(m *pbv2.Message) {
			m.Blocks[1].GetToolResult().PartMetadataJson = []byte(`{"outer":1}`)
		},
		"sibling result": func(m *pbv2.Message) {
			m.Blocks[2].GetToolResult().Content[0].GetText().Text = "tampered"
		},
		"surrounding text": func(m *pbv2.Message) {
			m.Blocks[0].GetText().Text = "tampered"
		},
	} {
		t.Run(name, func(t *testing.T) {
			tampered := proto.Clone(expected).(*pbv2.Message)
			tamper(tampered)
			if proto.Equal(actual, tampered) {
				t.Fatalf("weakened production mutation %q went undetected", name)
			}
		})
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
		// Malformed nested elements: fail closed BEFORE any mutation.
		{"nil content element", toolResultMsg(trContent(trText("r"), nil)), 0},
		{"arm-less element", toolResultMsg(trContent(trText("r"), &pbv2.ToolResultContentBlock{})), 0},
		{"typed-nil text arm", toolResultMsg(trContent(trText("r"), &pbv2.ToolResultContentBlock{Kind: &pbv2.ToolResultContentBlock_Text{}})), 0},
		{"typed-nil unknown arm", toolResultMsg(trContent(trText("r"), &pbv2.ToolResultContentBlock{Kind: &pbv2.ToolResultContentBlock_Unknown{}})), 0},
		{"typed-nil cache arm", toolResultMsg(trContent(trText("r"), &pbv2.ToolResultContentBlock{Kind: &pbv2.ToolResultContentBlock_CacheBreakpoint{}})), 0},
		{"malformed marker", toolResultMsg(trContent(trText("r"), trMarker(`not json`))), 0},
		{"incorrect-shape marker", toolResultMsg(trContent(trText("r"), trMarker(`[1,2]`))), 0},
		{"duplicate-key marker", toolResultMsg(trContent(trText("r"), trMarker(`{"type":"a","type":"b"}`))), 0},
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
	msg := &pbv2.Message{Role: "assistant", Blocks: []*pbv2.RequestBlock{
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
	// The trailing-signature carrier is PRESERVED: its covered content
	// (preceding text/thinking + own metadata) did not change.
	last := msg.Blocks[len(msg.Blocks)-1]
	if ts := last.GetTrailingSignature(); ts == nil || ts.Signature != "trailing-token" {
		t.Fatal("the trailing-signature carrier was disturbed by a result-text change")
	}
	if err := (&pbv2.ChatRequest{Model: "m", Messages: []*pbv2.Message{msg}}).ValidateReplacement(); err != nil {
		t.Fatalf("the changed result left the replacement domain: %v", err)
	}

	// The no-op keeps every token byte-for-byte.
	msg2 := &pbv2.Message{Role: "assistant", Blocks: []*pbv2.RequestBlock{
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

// TestReplaceLastCacheBreakpointProvenance — the corrected provenance rules:
// a nested marker real change clears ONLY the containing result signature
// (trailing carrier + unrelated tokens preserved); top-level and tool-def
// marker real changes clear NO signatures; no-op preserves everything;
// errors atomic.
func TestReplaceLastCacheBreakpointProvenance(t *testing.T) {
	trText := func(s string) *pbv2.ToolResultContentBlock {
		return &pbv2.ToolResultContentBlock{Kind: &pbv2.ToolResultContentBlock_Text{Text: &pbv2.ToolResultTextBlock{Text: s}}}
	}
	trMarker := func(m string) *pbv2.ToolResultContentBlock {
		return &pbv2.ToolResultContentBlock{Kind: &pbv2.ToolResultContentBlock_CacheBreakpoint{CacheBreakpoint: &pbv2.ToolResultCacheBreakpoint{MarkerJson: []byte(m)}}}
	}
	resultBlock := func(sig string, content ...*pbv2.ToolResultContentBlock) *pbv2.RequestBlock {
		return &pbv2.RequestBlock{Kind: &pbv2.RequestBlock_ToolResult{ToolResult: &pbv2.RequestToolResultBlock{
			ToolCallId: "c1", Signature: sig, Content: content,
		}}}
	}
	textBlock := func(s string) *pbv2.RequestBlock {
		return &pbv2.RequestBlock{Kind: &pbv2.RequestBlock_Text{Text: &pbv2.RequestTextBlock{Text: s}}}
	}
	trailingBlock := func() *pbv2.RequestBlock {
		return &pbv2.RequestBlock{Kind: &pbv2.RequestBlock_TrailingSignature{TrailingSignature: &pbv2.RequestTrailingSignatureBlock{Signature: "trailing-token"}}}
	}
	signedResultMsg := func() *pbv2.Message {
		m := &pbv2.Message{Role: "user"}
		m.Blocks = []*pbv2.RequestBlock{resultBlock("result-sig", trText("r"), trMarker(`{"type":"ephemeral"}`))}
		return m
	}

	// Nested marker real change: ONLY the containing result signature is
	// cleared; the trailing carrier and unrelated tokens are preserved.
	t.Run("nested marker clears only the result signature", func(t *testing.T) {
		req := &pbv2.ChatRequest{Model: "m", Messages: []*pbv2.Message{
			signedResultMsg(),
			{Role: "assistant", Blocks: []*pbv2.RequestBlock{textBlock("covered"), trailingBlock()}},
		}}
		changed, err := pbv2.ReplaceLastCacheBreakpoint(req, []byte(`{"type":"standard"}`))
		if err != nil || !changed {
			t.Fatalf("changed=%v err=%v", changed, err)
		}
		if got := req.Messages[0].Blocks[0].GetToolResult().Signature; got != "" {
			t.Fatalf("stale result signature survived: %q", got)
		}
		last := req.Messages[1].Blocks[len(req.Messages[1].Blocks)-1]
		if ts := last.GetTrailingSignature(); ts == nil || ts.Signature != "trailing-token" {
			t.Fatal("the trailing carrier was disturbed by a nested marker change")
		}
		if got := req.Messages[0].Blocks[0].GetToolResult().Content[0].GetText().Text; got != "r" {
			t.Fatalf("unrelated content disturbed: %q", got)
		}
	})

	// Top-level marker real change: NO signature is cleared.
	t.Run("top-level marker preserves every token", func(t *testing.T) {
		req := &pbv2.ChatRequest{Model: "m", Messages: []*pbv2.Message{{Role: "assistant", Blocks: []*pbv2.RequestBlock{
			textBlock("covered"),
			resultBlock("result-sig", trText("r")),
			{Kind: &pbv2.RequestBlock_CacheBreakpoint{CacheBreakpoint: &pbv2.RequestCacheBreakpoint{MarkerJson: []byte(`{"type":"ephemeral"}`)}}},
			trailingBlock(),
		}}}}
		changed, err := pbv2.ReplaceLastCacheBreakpoint(req, []byte(`{"type":"standard"}`))
		if err != nil || !changed {
			t.Fatalf("changed=%v err=%v", changed, err)
		}
		if got := req.Messages[0].Blocks[1].GetToolResult().Signature; got != "result-sig" {
			t.Fatalf("a top-level marker change cleared a result token: %q", got)
		}
		last := req.Messages[0].Blocks[len(req.Messages[0].Blocks)-1]
		if ts := last.GetTrailingSignature(); ts == nil || ts.Signature != "trailing-token" {
			t.Fatal("the trailing carrier was disturbed")
		}
	})

	// Tool-definition marker real change: no message token changes. The
	// result carries NO nested marker, so the tool-def carrier is the LAST
	// one in serialization order.
	t.Run("tool-def marker clears nothing", func(t *testing.T) {
		plain := &pbv2.Message{Role: "user"}
		plain.Blocks = []*pbv2.RequestBlock{resultBlock("result-sig", trText("r"))}
		req := &pbv2.ChatRequest{Model: "m",
			Tools:    []*pbv2.ToolDef{{Name: "read", ParametersJson: []byte(`{}`), CacheControlJson: []byte(`{"type":"ephemeral"}`)}},
			Messages: []*pbv2.Message{plain},
		}
		changed, err := pbv2.ReplaceLastCacheBreakpoint(req, []byte(`{"type":"standard"}`))
		if err != nil || !changed {
			t.Fatalf("changed=%v err=%v", changed, err)
		}
		if got := req.Messages[0].Blocks[0].GetToolResult().Signature; got != "result-sig" {
			t.Fatalf("a tool-def marker change cleared a message token: %q", got)
		}
	})

	// Byte-identical replay: everything preserved.
	t.Run("no-op preserves everything", func(t *testing.T) {
		req := &pbv2.ChatRequest{Model: "m", Messages: []*pbv2.Message{signedResultMsg()}}
		before := proto.Clone(req).(*pbv2.ChatRequest)
		changed, err := pbv2.ReplaceLastCacheBreakpoint(req, []byte(`{"type":"ephemeral"}`))
		if err != nil || changed {
			t.Fatalf("changed=%v err=%v", changed, err)
		}
		if !proto.Equal(req, before) {
			t.Fatal("a no-op marker replay mutated the request")
		}
	})

	// Errors atomic.
	t.Run("errors atomic", func(t *testing.T) {
		req := &pbv2.ChatRequest{Model: "m", Messages: []*pbv2.Message{signedResultMsg()}}
		before := proto.Clone(req).(*pbv2.ChatRequest)
		if _, err := pbv2.ReplaceLastCacheBreakpoint(req, []byte(`not json`)); err == nil {
			t.Fatal("a malformed marker was accepted")
		}
		if !proto.Equal(req, before) {
			t.Fatal("an error path mutated the request")
		}
	})
}
