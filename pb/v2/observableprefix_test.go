package v2

// Tests for the shared observable request-prefix projection and the
// exact-carrier cache-breakpoint replacement (cache-tier reconciliation
// REV 2). The bidirectional top-level descriptor inventory owns the complete
// include/exclude decision: an additive top-level field is picked up and the
// test FAILS CLOSED until a deliberate ruling row exists. Nested block
// completeness remains owned by the RequestBlocksFingerprint / validation
// inventories.

import (
	"bytes"
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"
)

// baseObservableRequest is a VALID request carrying one top-level marker on
// messages[1] (so prefix computations exercise the truncation path).
func baseObservableRequest() *ChatRequest {
	return &ChatRequest{
		Model:         "m",
		MaxTokens:     proto.Int32(64),
		Temperature:   proto.Float64(0.5),
		TopP:          proto.Float64(0.9),
		StopSequences: []string{"END"},
		Tools: []*ToolDef{{
			Name:             "read",
			Description:      "d",
			ParametersJson:   []byte(`{"type":"object"}`),
			CacheControlJson: nil,
		}},
		ProviderExtensionsJson: []byte(`{"custom":{"b":1,"a":2}}`),
		SafetySettingsJson:     []byte(`[{"category":"HARM_CATEGORY_X"}]`),
		Messages: []*Message{
			{Role: "user", Blocks: []*RequestBlock{{Kind: &RequestBlock_Text{Text: &RequestTextBlock{Text: "hi"}}}}},
			{
				Role: "user",
				Blocks: []*RequestBlock{
					{Kind: &RequestBlock_Text{Text: &RequestTextBlock{Text: "more"}}},
					{Kind: &RequestBlock_CacheBreakpoint{CacheBreakpoint: &RequestCacheBreakpoint{MarkerJson: []byte(`{"type":"ephemeral"}`)}}},
				},
			},
		},
	}
}

func observablePrefix(t *testing.T, req *ChatRequest) []byte {
	t.Helper()
	b, err := RequestObservablePrefix(req)
	if err != nil {
		t.Fatalf("RequestObservablePrefix: %v", err)
	}
	return b
}

// TestObservablePrefixTopLevelInventory is the bidirectional descriptor
// inventory: every top-level field of ChatRequest is mutated (to a
// validation-valid value) and the prefix must change for INCLUDED fields and
// stay identical for EXCLUDED fields. A field missing from the tables fails
// the test — the include/exclude decision must be deliberate.
func TestObservablePrefixTopLevelInventory(t *testing.T) {
	// included: a valid mutation of the field must change the prefix
	included := map[string]func(*ChatRequest){
		"model":                    func(r *ChatRequest) { r.Model = "m2" },
		"tools":                    func(r *ChatRequest) { r.Tools[0].Description = "d2" },
		"messages":                 func(r *ChatRequest) { r.Messages[0].Blocks[0].GetText().Text = "changed" },
		"max_tokens":               func(r *ChatRequest) { r.MaxTokens = proto.Int32(128) },
		"temperature":              func(r *ChatRequest) { r.Temperature = proto.Float64(0.25) },
		"top_p":                    func(r *ChatRequest) { r.TopP = proto.Float64(0.1) },
		"stop_sequences":           func(r *ChatRequest) { r.StopSequences = []string{"END", "STOP"} },
		"provider_extensions_json": func(r *ChatRequest) { r.ProviderExtensionsJson = []byte(`{"custom":{"a":2,"b":1}}`) },
		"safety_settings_json":     func(r *ChatRequest) { r.SafetySettingsJson = []byte(`[]`) },
	}
	// excluded: mutating the field must NOT change the prefix
	excluded := map[string]func(*ChatRequest){
		"stream":           func(r *ChatRequest) { r.Stream = true },
		"torana_meta_json": func(r *ChatRequest) { r.ToranaMetaJson = []byte(`{"_provider":"x"}`) },
	}
	// The complete decision, fail-closed: every descriptor field must appear
	// in exactly one table (or the ruling changed deliberately).
	desc := (&ChatRequest{}).ProtoReflect().Descriptor()
	for i := 0; i < desc.Fields().Len(); i++ {
		name := string(desc.Fields().Get(i).Name())
		_, in := included[name]
		_, ex := excluded[name]
		if in == ex {
			t.Fatalf("top-level field %q is missing from (or duplicated across) the include/exclude tables — deliberate ruling required", name)
		}
	}
	for name, mutate := range included {
		t.Run("included/"+name, func(t *testing.T) {
			base := baseObservableRequest()
			before := observablePrefix(t, base)
			mutate(base)
			if got := observablePrefix(t, base); bytes.Equal(got, before) {
				t.Errorf("included field %s did not change the prefix", name)
			}
		})
	}
	for name, mutate := range excluded {
		t.Run("excluded/"+name, func(t *testing.T) {
			base := baseObservableRequest()
			before := observablePrefix(t, base)
			mutate(base)
			if got := observablePrefix(t, base); !bytes.Equal(got, before) {
				t.Errorf("excluded field %s changed the prefix", name)
			}
		})
	}
}

// --- marker model pins ---

func TestObservablePrefixToolOnlyMarker(t *testing.T) {
	base := baseObservableRequest()
	base.Messages[1].Blocks = base.Messages[1].Blocks[:1] // drop the top-level marker
	// Move the marker into the tools section; messages must vanish from the
	// prefix entirely.
	base.Tools[0].CacheControlJson = []byte(`{"type":"ephemeral"}`)
	base.Messages = append(base.Messages, &Message{Role: "user", Blocks: []*RequestBlock{{Kind: &RequestBlock_Text{Text: &RequestTextBlock{Text: "suffix"}}}}})
	prefix := observablePrefix(t, base)

	// A suffix message and ANY message change are invisible.
	withMsgChange := proto.Clone(base).(*ChatRequest)
	withMsgChange.Messages[0].Blocks[0].GetText().Text = "changed"
	if got := observablePrefix(t, withMsgChange); !bytes.Equal(got, prefix) {
		t.Fatal("message content changed the prefix with a tools-section marker")
	}
	// Tools after the marker tool are excluded; tools through it fold.
	withToolAfter := proto.Clone(base).(*ChatRequest)
	withToolAfter.Tools = append(withToolAfter.Tools, &ToolDef{Name: "extra", Description: "e", ParametersJson: []byte(`{}`)})
	if got := observablePrefix(t, withToolAfter); !bytes.Equal(got, prefix) {
		t.Fatal("a tool after the marker tool changed the prefix")
	}
	withToolBefore := proto.Clone(base).(*ChatRequest)
	withToolBefore.Tools[0].Description = "d2"
	if got := observablePrefix(t, withToolBefore); bytes.Equal(got, prefix) {
		t.Fatal("a tool through the marker did not change the prefix")
	}
	// The marker's own bytes fold.
	withMarker := proto.Clone(base).(*ChatRequest)
	withMarker.Tools[0].CacheControlJson = []byte(`{"type":"standard"}`)
	if got := observablePrefix(t, withMarker); bytes.Equal(got, prefix) {
		t.Fatal("the tools-section marker bytes did not change the prefix")
	}
}

func TestObservablePrefixTopLevelMarker(t *testing.T) {
	base := baseObservableRequest()
	prefix := observablePrefix(t, base)

	// Blocks after the marker block are excluded; the marker block itself
	// (its MarkerJson) folds.
	withSuffixBlock := proto.Clone(base).(*ChatRequest)
	withSuffixBlock.Messages[1].Blocks = append(withSuffixBlock.Messages[1].Blocks, &RequestBlock{Kind: &RequestBlock_Text{Text: &RequestTextBlock{Text: "after"}}})
	if got := observablePrefix(t, withSuffixBlock); !bytes.Equal(got, prefix) {
		t.Fatal("a block after the top-level marker changed the prefix")
	}
	withMarkerChange := proto.Clone(base).(*ChatRequest)
	withMarkerChange.Messages[1].Blocks[1].GetCacheBreakpoint().MarkerJson = []byte(`{"type":"standard"}`)
	if got := observablePrefix(t, withMarkerChange); bytes.Equal(got, prefix) {
		t.Fatal("the top-level marker bytes did not change the prefix")
	}
	withBefore := proto.Clone(base).(*ChatRequest)
	withBefore.Messages[1].Blocks[0].GetText().Text = "changed"
	if got := observablePrefix(t, withBefore); bytes.Equal(got, prefix) {
		t.Fatal("a block before the marker did not change the prefix")
	}
}

func TestObservablePrefixNestedMarker(t *testing.T) {
	base := baseObservableRequest()
	base.Messages[1].Blocks = base.Messages[1].Blocks[:1] // drop the top-level marker
	// A nested marker inside a tool-result's content on messages[0].
	base.Messages[0].Blocks = append(base.Messages[0].Blocks, &RequestBlock{Kind: &RequestBlock_ToolResult{ToolResult: &RequestToolResultBlock{
		ToolCallId: "c1",
		Content: []*ToolResultContentBlock{
			{Kind: &ToolResultContentBlock_Text{Text: &ToolResultTextBlock{Text: "a"}}},
			{Kind: &ToolResultContentBlock_Text{Text: &ToolResultTextBlock{Text: "b"}}},
			{Kind: &ToolResultContentBlock_CacheBreakpoint{CacheBreakpoint: &ToolResultCacheBreakpoint{MarkerJson: []byte(`{"type":"ephemeral"}`)}}},
			{Kind: &ToolResultContentBlock_Text{Text: &ToolResultTextBlock{Text: "c"}}},
		},
	}}})
	prefix := observablePrefix(t, base)

	// Content after the nested marker is excluded.
	withSuffix := proto.Clone(base).(*ChatRequest)
	withSuffix.Messages[0].Blocks[1].GetToolResult().Content[3].GetText().Text = "changed"
	if got := observablePrefix(t, withSuffix); !bytes.Equal(got, prefix) {
		t.Fatal("content after the nested marker changed the prefix")
	}
	// Content BEFORE the marker folds; the marker bytes fold.
	withBefore := proto.Clone(base).(*ChatRequest)
	withBefore.Messages[0].Blocks[1].GetToolResult().Content[0].GetText().Text = "changed"
	if got := observablePrefix(t, withBefore); bytes.Equal(got, prefix) {
		t.Fatal("content before the nested marker did not change the prefix")
	}
	withMarker := proto.Clone(base).(*ChatRequest)
	withMarker.Messages[0].Blocks[1].GetToolResult().Content[2].GetCacheBreakpoint().MarkerJson = []byte(`{"type":"standard"}`)
	if got := observablePrefix(t, withMarker); bytes.Equal(got, prefix) {
		t.Fatal("the nested marker bytes did not change the prefix")
	}
	// Exact nested cut: removing the trailing content item entirely must be
	// invisible (the projection already cuts there).
	withoutSuffix := proto.Clone(base).(*ChatRequest)
	withoutSuffix.Messages[0].Blocks[1].GetToolResult().Content = withoutSuffix.Messages[0].Blocks[1].GetToolResult().Content[:3]
	if got := observablePrefix(t, withoutSuffix); !bytes.Equal(got, prefix) {
		t.Fatal("the exact nested cut does not match the projection")
	}
}

func TestObservablePrefixLastWins(t *testing.T) {
	// Markers in tools[0] AND messages[1]: the message marker closes the
	// prefix, so the full tools section folds (the tool marker is not a
	// cut-off) — but the BOUNDARY is the last marker.
	base := baseObservableRequest()
	base.Tools[0].CacheControlJson = []byte(`{"type":"ephemeral"}`)
	prefix := observablePrefix(t, base)

	// A message AFTER the last marker is excluded.
	withSuffix := proto.Clone(base).(*ChatRequest)
	withSuffix.Messages = append(withSuffix.Messages, &Message{Role: "user", Blocks: []*RequestBlock{{Kind: &RequestBlock_Text{Text: &RequestTextBlock{Text: "suffix"}}}}})
	if got := observablePrefix(t, withSuffix); !bytes.Equal(got, prefix) {
		t.Fatal("a message after the last marker changed the prefix")
	}
	// Moving the last marker EARLIER (messages[1] -> messages[0]) changes the
	// boundary: messages[1] stops folding.
	earlier := proto.Clone(base).(*ChatRequest)
	earlier.Messages[0].Blocks = append(earlier.Messages[0].Blocks, &RequestBlock{Kind: &RequestBlock_CacheBreakpoint{CacheBreakpoint: &RequestCacheBreakpoint{MarkerJson: []byte(`{"type":"ephemeral"}`)}}})
	if got := observablePrefix(t, earlier); bytes.Equal(got, prefix) {
		t.Fatal("marker-position movement (last marker earlier) did not change the prefix")
	}
}

func TestObservablePrefixNoMarkerWholeRequest(t *testing.T) {
	base := proto.Clone(baseObservableRequest()).(*ChatRequest)
	base.Messages[1].Blocks = base.Messages[1].Blocks[:1] // drop the marker
	prefix := observablePrefix(t, base)
	withChange := proto.Clone(base).(*ChatRequest)
	withChange.Messages[0].Blocks[0].GetText().Text = "changed"
	if got := observablePrefix(t, withChange); bytes.Equal(got, prefix) {
		t.Fatal("a message change did not move the no-marker prefix")
	}
	withTool := proto.Clone(base).(*ChatRequest)
	withTool.Tools[0].Description = "d2"
	if got := observablePrefix(t, withTool); bytes.Equal(got, prefix) {
		t.Fatal("a tool change did not move the no-marker prefix")
	}
}

func TestObservablePrefixRawLexemesAndOrder(t *testing.T) {
	base := baseObservableRequest()
	prefix := observablePrefix(t, base)

	// Raw JSON lexeme/order: reordering members must change the prefix
	// (the bytes travel verbatim to the provider).
	reordered := proto.Clone(base).(*ChatRequest)
	reordered.ProviderExtensionsJson = []byte(`{"custom":{"a":2,"b":1}}`)
	if got := observablePrefix(t, reordered); bytes.Equal(got, prefix) {
		t.Fatal("extension member order did not change the prefix")
	}
	// Presence of the optional scalars.
	absent := proto.Clone(base).(*ChatRequest)
	absent.MaxTokens = nil
	if got := observablePrefix(t, absent); bytes.Equal(got, prefix) {
		t.Fatal("max_tokens presence did not change the prefix")
	}
	zeroTemp := proto.Clone(base).(*ChatRequest)
	zeroTemp.Temperature = proto.Float64(0)
	if got := observablePrefix(t, zeroTemp); bytes.Equal(got, prefix) {
		t.Fatal("temperature presence (0 vs absent) did not change the prefix")
	}
	// Stops: cardinality AND order.
	withStops := proto.Clone(base).(*ChatRequest)
	withStops.StopSequences = []string{"END", "STOP"}
	if got := observablePrefix(t, withStops); bytes.Equal(got, prefix) {
		t.Fatal("stop cardinality did not change the prefix")
	}
	reorderedStops := proto.Clone(base).(*ChatRequest)
	reorderedStops.StopSequences = []string{"STOP", "END"}
	if got := observablePrefix(t, reorderedStops); bytes.Equal(got, prefix) {
		t.Fatal("stop order did not change the prefix")
	}
}

func TestObservablePrefixSafetySettingsArray(t *testing.T) {
	base := baseObservableRequest()
	prefix := observablePrefix(t, base)
	empty := proto.Clone(base).(*ChatRequest)
	empty.SafetySettingsJson = []byte(`[]`)
	if got := observablePrefix(t, empty); bytes.Equal(got, prefix) {
		t.Fatal("safety settings cardinality did not change the prefix")
	}
	// {} is NOT a valid safety-settings value (array shape) — fail-closed.
	bad := proto.Clone(base).(*ChatRequest)
	bad.SafetySettingsJson = []byte(`{}`)
	if _, err := RequestObservablePrefix(bad); err == nil {
		t.Fatal("object-shaped safety_settings_json was not rejected")
	}
}

func TestObservablePrefixFailClosedAndNonAliasing(t *testing.T) {
	// Out-of-domain request: error, never a partial projection.
	bad := baseObservableRequest()
	bad.MaxTokens = proto.Int32(0)
	if _, err := RequestObservablePrefix(bad); err == nil {
		t.Fatal("out-of-domain request produced a prefix")
	}

	// Non-aliasing + non-mutation: the call neither mutates the input nor
	// aliases it in the result.
	base := baseObservableRequest()
	before := proto.Clone(base).(*ChatRequest)
	prefix := observablePrefix(t, base)
	if !proto.Equal(base, before) {
		t.Fatal("RequestObservablePrefix mutated its input")
	}
	base.Messages[0].Blocks[0].GetText().Text = "changed"
	after := observablePrefix(t, base)
	if bytes.Equal(after, prefix) {
		t.Fatal("prefix result aliased the input request")
	}
}

// --- ReplaceLastCacheBreakpoint pins ---

func TestReplaceLastCacheBreakpointToolCarrier(t *testing.T) {
	base := baseObservableRequest()
	base.Messages[1].Blocks = base.Messages[1].Blocks[:1] // drop the top-level marker
	base.Tools[0].CacheControlJson = []byte(`{"type":"ephemeral"}`)
	marker := []byte(`{"type":"standard"}`)
	changed, err := ReplaceLastCacheBreakpoint(base, marker)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v, want changed", changed, err)
	}
	if !bytes.Equal(base.Tools[0].CacheControlJson, marker) {
		t.Fatalf("tool marker = %s, want %s", base.Tools[0].CacheControlJson, marker)
	}
	// Byte-identical: unchanged, no-op.
	changed, err = ReplaceLastCacheBreakpoint(base, marker)
	if err != nil || changed {
		t.Fatalf("byte-identical marker: changed=%v err=%v, want no-op", changed, err)
	}
	// Lexically different: conservative change.
	changed, err = ReplaceLastCacheBreakpoint(base, []byte(`{"type":"standard","extra":1}`))
	if err != nil || !changed {
		t.Fatalf("lexical change: changed=%v err=%v, want changed", changed, err)
	}
}

func TestReplaceLastCacheBreakpointTopLevelAndNested(t *testing.T) {
	// Top-level carrier.
	base := baseObservableRequest()
	marker := []byte(`{"type":"standard"}`)
	changed, err := ReplaceLastCacheBreakpoint(base, marker)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if got := base.Messages[1].Blocks[1].GetCacheBreakpoint().MarkerJson; !bytes.Equal(got, marker) {
		t.Fatalf("top-level marker = %s, want %s", got, marker)
	}

	// Nested carrier: the LAST carrier wins — a nested marker in a block
	// AFTER the outer marker (serialization order) is the boundary.
	nested := baseObservableRequest()
	nested.Messages[1].Blocks = append(nested.Messages[1].Blocks, &RequestBlock{Kind: &RequestBlock_ToolResult{ToolResult: &RequestToolResultBlock{
		ToolCallId: "c1",
		Content: []*ToolResultContentBlock{
			{Kind: &ToolResultContentBlock_Text{Text: &ToolResultTextBlock{Text: "a"}}},
			{Kind: &ToolResultContentBlock_CacheBreakpoint{CacheBreakpoint: &ToolResultCacheBreakpoint{MarkerJson: []byte(`{"type":"ephemeral"}`)}}},
		},
	}}})
	changed, err = ReplaceLastCacheBreakpoint(nested, marker)
	if err != nil || !changed {
		t.Fatalf("nested: changed=%v err=%v", changed, err)
	}
	if got := nested.Messages[1].Blocks[2].GetToolResult().Content[1].GetCacheBreakpoint().MarkerJson; !bytes.Equal(got, marker) {
		t.Fatalf("nested marker = %s, want %s", got, marker)
	}
	// The earlier outer marker is untouched.
	if got := nested.Messages[1].Blocks[1].GetCacheBreakpoint().MarkerJson; !bytes.Equal(got, []byte(`{"type":"ephemeral"}`)) {
		t.Fatalf("earlier outer marker was disturbed: %s", got)
	}
}

func TestReplaceLastCacheBreakpointNoMarkerSentinel(t *testing.T) {
	base := baseObservableRequest()
	base.Messages[1].Blocks = base.Messages[1].Blocks[:1] // drop the marker
	before := proto.Clone(base).(*ChatRequest)
	changed, err := ReplaceLastCacheBreakpoint(base, []byte(`{"type":"ephemeral"}`))
	if !errors.Is(err, ErrNoCacheBreakpoint) {
		t.Fatalf("err = %v, want ErrNoCacheBreakpoint", err)
	}
	if changed {
		t.Fatal("changed=true on the no-marker sentinel")
	}
	if !proto.Equal(base, before) {
		t.Fatal("no-marker sentinel mutated the request")
	}
}

func TestReplaceLastCacheBreakpointAtomicity(t *testing.T) {
	// Invalid marker: error, request unchanged.
	base := baseObservableRequest()
	before := proto.Clone(base).(*ChatRequest)
	if _, err := ReplaceLastCacheBreakpoint(base, []byte(`[1,2]`)); err == nil {
		t.Fatal("array marker was accepted")
	}
	if !proto.Equal(base, before) {
		t.Fatal("invalid marker mutated the request")
	}
	// Invalid request: error, request unchanged.
	bad := baseObservableRequest()
	bad.MaxTokens = proto.Int32(0)
	beforeBad := proto.Clone(bad).(*ChatRequest)
	if _, err := ReplaceLastCacheBreakpoint(bad, []byte(`{"type":"ephemeral"}`)); err == nil {
		t.Fatal("out-of-domain request was accepted")
	}
	if !proto.Equal(bad, beforeBad) {
		t.Fatal("invalid request was mutated before the error")
	}

	// Defensive copy: mutating the caller's marker after the call must not
	// change the request.
	base2 := baseObservableRequest()
	marker := []byte(`{"type":"standard"}`)
	if _, err := ReplaceLastCacheBreakpoint(base2, marker); err != nil {
		t.Fatalf("replace: %v", err)
	}
	marker[0] = 'X'
	if got := base2.Messages[1].Blocks[1].GetCacheBreakpoint().MarkerJson; bytes.Equal(got, marker) {
		t.Fatal("the request aliases the caller's marker slice")
	}
}
