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
	"fmt"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
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

// checkObservablePrefixInventory validates the include/exclude tables
// TRULY bidirectionally: every key must resolve to a live ChatRequest
// descriptor field, no key may appear in both tables, and the key set must
// equal the descriptor field set exactly (cardinality + membership). A
// removed/renamed field leaves a stale map row that fails here.
func checkObservablePrefixInventory(included, excluded map[string]func(*ChatRequest)) error {
	if len(included) == 0 || len(excluded) == 0 {
		return errors.New("inventory tables must be non-empty")
	}
	desc := (&ChatRequest{}).ProtoReflect().Descriptor()
	live := make(map[string]bool, desc.Fields().Len())
	for i := 0; i < desc.Fields().Len(); i++ {
		live[string(desc.Fields().Get(i).Name())] = true
	}
	for name := range included {
		if !live[name] {
			return fmt.Errorf("included key %q is not a live ChatRequest field (stale row)", name)
		}
	}
	for name := range excluded {
		if !live[name] {
			return fmt.Errorf("excluded key %q is not a live ChatRequest field (stale row)", name)
		}
	}
	seen := make(map[string]bool, len(included)+len(excluded))
	for name := range included {
		if seen[name] {
			return fmt.Errorf("duplicate key %q across the tables", name)
		}
		seen[name] = true
	}
	for name := range excluded {
		if seen[name] {
			return fmt.Errorf("duplicate key %q across the tables", name)
		}
		seen[name] = true
	}
	if len(seen) != len(live) {
		return fmt.Errorf("inventory covers %d fields, live descriptor has %d — every field needs a deliberate ruling", len(seen), len(live))
	}
	return nil
}

// TestObservablePrefixTopLevelInventory is the bidirectional descriptor
// inventory: every top-level field of ChatRequest is mutated (to a
// validation-valid value) and the prefix must change for INCLUDED fields and
// stay identical for EXCLUDED fields. The inventory helper additionally
// proves the tables name exactly the live descriptor fields (no stale or
// invented rows, no duplicates) — an additive OR removed field fails closed
// until the ruling is updated deliberately.
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
	if err := checkObservablePrefixInventory(included, excluded); err != nil {
		t.Fatalf("inventory is not bidirectional: %v", err)
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

// TestObservablePrefixInventoryRegressions proves the inventory helper
// detects stale rows and missing fields — a removed field must not leave a
// green map behind.
func TestObservablePrefixInventoryRegressions(t *testing.T) {
	stale := map[string]func(*ChatRequest){
		"model":    func(r *ChatRequest) { r.Model = "m2" },
		"invented": func(r *ChatRequest) { r.Model = "m3" }, // not a descriptor field
	}
	if err := checkObservablePrefixInventory(stale, map[string]func(*ChatRequest){"stream": func(r *ChatRequest) { r.Stream = true }}); err == nil {
		t.Fatal("an invented key passed the inventory")
	}
	missing := map[string]func(*ChatRequest){
		"model": func(r *ChatRequest) { r.Model = "m2" },
	}
	excl := map[string]func(*ChatRequest){
		"stream":           func(r *ChatRequest) { r.Stream = true },
		"torana_meta_json": func(r *ChatRequest) { r.ToranaMetaJson = []byte(`{"_provider":"x"}`) },
	}
	if err := checkObservablePrefixInventory(missing, excl); err == nil {
		t.Fatal("an incomplete field set passed the inventory")
	}
}

// TestMarkerCarrierInventory is the executable carrier inventory: every
// field/arm of the three carrier-holder types is classified as carrier or
// non-carrier, exactly once — an ABI addition cannot silently become a
// fourth marker carrier without a deliberate decision (cache-boundary
// classification is a ruling, not a serialization side effect).
func TestMarkerCarrierInventory(t *testing.T) {
	type holder struct {
		name        string
		desc        protoreflect.MessageDescriptor
		fields      func(protoreflect.MessageDescriptor) []protoreflect.FieldDescriptor
		carrier     map[string]bool
		trueCarrier string // exactly one carrier per holder
	}
	holders := []holder{
		{"ToolDef", (&ToolDef{}).ProtoReflect().Descriptor(), func(d protoreflect.MessageDescriptor) []protoreflect.FieldDescriptor {
			var out []protoreflect.FieldDescriptor
			for i := 0; i < d.Fields().Len(); i++ {
				out = append(out, d.Fields().Get(i))
			}
			return out
		}, map[string]bool{
			"name":               false,
			"description":        false,
			"parameters_json":    false,
			"strict":             false,
			"cache_control_json": true,
		}, ""},
		{"RequestBlock", (&RequestBlock{}).ProtoReflect().Descriptor(), func(d protoreflect.MessageDescriptor) []protoreflect.FieldDescriptor {
			var out []protoreflect.FieldDescriptor
			oneofs := d.Oneofs()
			for o := 0; o < oneofs.Len(); o++ {
				oneof := oneofs.Get(o)
				if oneof.IsSynthetic() {
					continue // proto3 optional scalars are not block arms
				}
				for f := 0; f < oneof.Fields().Len(); f++ {
					out = append(out, oneof.Fields().Get(f))
				}
			}
			return out
		}, map[string]bool{
			"text":               false,
			"thinking":           false,
			"redacted_thinking":  false,
			"tool_use":           false,
			"tool_result":        false,
			"cache_breakpoint":   true,
			"unknown":            false,
			"trailing_signature": false,
		}, ""},
		{"ToolResultContentBlock", (&ToolResultContentBlock{}).ProtoReflect().Descriptor(), func(d protoreflect.MessageDescriptor) []protoreflect.FieldDescriptor {
			var out []protoreflect.FieldDescriptor
			oneofs := d.Oneofs()
			for o := 0; o < oneofs.Len(); o++ {
				oneof := oneofs.Get(o)
				if oneof.IsSynthetic() {
					continue
				}
				for f := 0; f < oneof.Fields().Len(); f++ {
					out = append(out, oneof.Fields().Get(f))
				}
			}
			return out
		}, map[string]bool{
			"text":             false,
			"unknown":          false,
			"cache_breakpoint": true,
		}, ""},
	}
	for _, h := range holders {
		t.Run(h.name, func(t *testing.T) {
			fields := h.fields(h.desc)
			seen := make(map[string]bool, len(fields))
			for _, fd := range fields {
				name := string(fd.Name())
				isCarrier, ruling := h.carrier[name]
				if !ruling {
					t.Fatalf("%s field %q is missing from the carrier ruling — a deliberate classification is required", h.name, name)
				}
				// The bool VALUE is executable: exactly one true carrier per
				// holder (marking everything false must fail).
				if isCarrier {
					if h.trueCarrier != "" {
						t.Fatalf("%s has two true carriers (%q and %q) — exactly one is allowed", h.name, h.trueCarrier, name)
					}
					h.trueCarrier = name
				}
				seen[name] = true
			}
			if len(seen) != len(h.carrier) {
				t.Fatalf("%s carrier table names %d fields, holder has %d — stale ruling", h.name, len(h.carrier), len(seen))
			}
			if h.trueCarrier == "" {
				t.Fatalf("%s has NO true carrier — the classification is not executable", h.name)
			}
		})
	}
}

// --- structural reference table ---

// expectedPrefixBytes deterministic-marshals an INDEPENDENTLY built expected
// projection (the row author constructs the truncated ChatRequest by hand —
// never via lastCacheMarker/truncateMessage) and compares it byte-for-byte
// with RequestObservablePrefix.
func expectedPrefixBytes(t *testing.T, expected *ChatRequest) []byte {
	t.Helper()
	expected.Stream = false
	expected.ToranaMetaJson = nil
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(expected)
	if err != nil {
		t.Fatalf("expected marshal: %v", err)
	}
	return b
}

// TestObservablePrefixStructuralReference builds, for every marker shape, an
// input request and the EXACT expected truncated projection (hand-built),
// then asserts byte-for-byte equality with RequestObservablePrefix.
func TestObservablePrefixStructuralReference(t *testing.T) {
	text := func(s string) *RequestBlock {
		return &RequestBlock{Kind: &RequestBlock_Text{Text: &RequestTextBlock{Text: s}}}
	}
	toolRes := func(cs ...*ToolResultContentBlock) *RequestBlock {
		return &RequestBlock{Kind: &RequestBlock_ToolResult{ToolResult: &RequestToolResultBlock{ToolCallId: "c1", Content: cs}}}
	}
	trText := func(s string) *ToolResultContentBlock {
		return &ToolResultContentBlock{Kind: &ToolResultContentBlock_Text{Text: &ToolResultTextBlock{Text: s}}}
	}
	trMarker := func(m string) *ToolResultContentBlock {
		return &ToolResultContentBlock{Kind: &ToolResultContentBlock_CacheBreakpoint{CacheBreakpoint: &ToolResultCacheBreakpoint{MarkerJson: []byte(m)}}}
	}
	marker := func(m string) *RequestBlock {
		return &RequestBlock{Kind: &RequestBlock_CacheBreakpoint{CacheBreakpoint: &RequestCacheBreakpoint{MarkerJson: []byte(m)}}}
	}
	baseReq := func(blocks ...[]*RequestBlock) *ChatRequest {
		var msgs []*Message
		for _, bs := range blocks {
			msgs = append(msgs, &Message{Role: "user", Blocks: bs})
		}
		return &ChatRequest{
			Model:    "m",
			Tools:    []*ToolDef{{Name: "read", Description: "d", ParametersJson: []byte(`{"type":"object"}`)}},
			Messages: msgs,
		}
	}

	rows := []struct {
		name     string
		input    *ChatRequest
		expected *ChatRequest // hand-built truncated projection
	}{
		{
			"tool-only marker",
			func() *ChatRequest {
				r := baseReq([]*RequestBlock{text("hi")})
				r.Tools[0].CacheControlJson = []byte(`{"type":"ephemeral"}`)
				return r
			}(),
			// Prefix = tools through the marker tool, NO messages.
			&ChatRequest{Model: "m", Tools: []*ToolDef{{Name: "read", Description: "d", ParametersJson: []byte(`{"type":"object"}`), CacheControlJson: []byte(`{"type":"ephemeral"}`)}}},
		},
		{
			"outer marker",
			func() *ChatRequest {
				return baseReq(
					[]*RequestBlock{text("a"), text("b"), marker(`{"type":"ephemeral"}`), text("after")},
					[]*RequestBlock{text("second-msg")},
				)
			}(),
			// Messages truncated inclusive at the marker block; the message
			// AFTER it is dropped entirely.
			&ChatRequest{Model: "m",
				Tools:    []*ToolDef{{Name: "read", Description: "d", ParametersJson: []byte(`{"type":"object"}`)}},
				Messages: []*Message{{Role: "user", Blocks: []*RequestBlock{text("a"), text("b"), marker(`{"type":"ephemeral"}`)}}}},
		},
		{
			"nested marker",
			func() *ChatRequest {
				return baseReq(
					[]*RequestBlock{text("a"), toolRes(trText("r1"), trMarker(`{"type":"ephemeral"}`), trText("r2"))},
				)
			}(),
			// The marker block's content cut at the exact nested position.
			&ChatRequest{Model: "m",
				Tools:    []*ToolDef{{Name: "read", Description: "d", ParametersJson: []byte(`{"type":"object"}`)}},
				Messages: []*Message{{Role: "user", Blocks: []*RequestBlock{text("a"), toolRes(trText("r1"), trMarker(`{"type":"ephemeral"}`))}}}},
		},
		{
			"two messages, marker on the second",
			func() *ChatRequest {
				return baseReq(
					[]*RequestBlock{text("first")},
					[]*RequestBlock{text("second"), marker(`{"type":"ephemeral"}`), text("after")},
				)
			}(),
			&ChatRequest{Model: "m",
				Tools: []*ToolDef{{Name: "read", Description: "d", ParametersJson: []byte(`{"type":"object"}`)}},
				Messages: []*Message{
					{Role: "user", Blocks: []*RequestBlock{text("first")}},
					{Role: "user", Blocks: []*RequestBlock{text("second"), marker(`{"type":"ephemeral"}`)}},
				}},
		},
		{
			"two outer markers in one message",
			func() *ChatRequest {
				return baseReq([]*RequestBlock{text("a"), marker(`{"type":"ephemeral"}`), text("b"), marker(`{"type":"standard"}`), text("after")})
			}(),
			&ChatRequest{Model: "m",
				Tools:    []*ToolDef{{Name: "read", Description: "d", ParametersJson: []byte(`{"type":"object"}`)}},
				Messages: []*Message{{Role: "user", Blocks: []*RequestBlock{text("a"), marker(`{"type":"ephemeral"}`), text("b"), marker(`{"type":"standard"}`)}}}},
		},
		{
			"two nested markers in one tool result",
			func() *ChatRequest {
				return baseReq([]*RequestBlock{toolRes(trText("r0"), trMarker(`{"type":"ephemeral"}`), trMarker(`{"type":"standard"}`), trText("r3"))})
			}(),
			&ChatRequest{Model: "m",
				Tools:    []*ToolDef{{Name: "read", Description: "d", ParametersJson: []byte(`{"type":"object"}`)}},
				Messages: []*Message{{Role: "user", Blocks: []*RequestBlock{toolRes(trText("r0"), trMarker(`{"type":"ephemeral"}`), trMarker(`{"type":"standard"}`))}}}},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			got, err := RequestObservablePrefix(row.input)
			if err != nil {
				t.Fatalf("RequestObservablePrefix: %v", err)
			}
			want := expectedPrefixBytes(t, row.expected)
			if !bytes.Equal(got, want) {
				t.Fatalf("prefix mismatch\n got: %x\nwant: %x", got, want)
			}
		})
	}

	// Boundary movement: for each pair, content BETWEEN the old and new
	// boundaries must be visible on one side and invisible on the other.
	// Removing the former last marker alone cannot prove this — marker bytes
	// themselves vanished — so each row mutates between-boundary content and
	// asserts it folds BEFORE the removal and is excluded AFTER it.
	movement := []struct {
		name    string
		input   func() *ChatRequest
		drop    func(*ChatRequest) // removes the LAST marker
		between func(*ChatRequest) // mutates content between the two boundaries
	}{
		{
			"tool to outer",
			func() *ChatRequest {
				r := baseReq([]*RequestBlock{text("a"), marker(`{"type":"ephemeral"}`)})
				r.Tools[0].CacheControlJson = []byte(`{"type":"ephemeral"}`)
				return r
			},
			func(r *ChatRequest) { r.Messages[0].Blocks = r.Messages[0].Blocks[:1] },
			func(r *ChatRequest) { r.Messages[0].Blocks[0].GetText().Text = "a2" },
		},
		{
			"tool to nested",
			func() *ChatRequest {
				r := baseReq([]*RequestBlock{toolRes(trText("r0"), trMarker(`{"type":"ephemeral"}`))})
				r.Tools[0].CacheControlJson = []byte(`{"type":"ephemeral"}`)
				return r
			},
			func(r *ChatRequest) {
				r.Messages[0].Blocks[0].GetToolResult().Content = r.Messages[0].Blocks[0].GetToolResult().Content[:1]
			},
			func(r *ChatRequest) { r.Messages[0].Blocks[0].GetToolResult().Content[0].GetText().Text = "r0x" },
		},
		{
			"outer to nested",
			func() *ChatRequest {
				return baseReq([]*RequestBlock{marker(`{"type":"ephemeral"}`), toolRes(trText("r0"), trMarker(`{"type":"standard"}`))})
			},
			func(r *ChatRequest) {
				r.Messages[0].Blocks[1].GetToolResult().Content = r.Messages[0].Blocks[1].GetToolResult().Content[:1]
			},
			func(r *ChatRequest) { r.Messages[0].Blocks[1].GetToolResult().Content[0].GetText().Text = "r0x" },
		},
		{
			"two messages",
			func() *ChatRequest {
				return baseReq(
					[]*RequestBlock{text("first"), marker(`{"type":"ephemeral"}`)},
					[]*RequestBlock{text("second"), marker(`{"type":"standard"}`)},
				)
			},
			func(r *ChatRequest) { r.Messages[1].Blocks = r.Messages[1].Blocks[:1] },
			func(r *ChatRequest) { r.Messages[1].Blocks[0].GetText().Text = "second2" },
		},
		{
			"two outer markers in one message",
			func() *ChatRequest {
				return baseReq([]*RequestBlock{marker(`{"type":"ephemeral"}`), text("mid"), marker(`{"type":"standard"}`)})
			},
			func(r *ChatRequest) { r.Messages[0].Blocks = r.Messages[0].Blocks[:2] },
			func(r *ChatRequest) { r.Messages[0].Blocks[1].GetText().Text = "mid2" },
		},
		{
			"two nested markers in one tool result",
			func() *ChatRequest {
				return baseReq([]*RequestBlock{toolRes(trMarker(`{"type":"ephemeral"}`), trText("mid"), trMarker(`{"type":"standard"}`))})
			},
			func(r *ChatRequest) {
				r.Messages[0].Blocks[0].GetToolResult().Content = r.Messages[0].Blocks[0].GetToolResult().Content[:2]
			},
			func(r *ChatRequest) { r.Messages[0].Blocks[0].GetToolResult().Content[1].GetText().Text = "mid2" },
		},
	}
	for _, row := range movement {
		t.Run("movement/"+row.name, func(t *testing.T) {
			with := row.input()
			withBetween := row.input()
			row.between(withBetween)
			gotWith := observablePrefix(t, with)
			gotWithBetween := observablePrefix(t, withBetween)
			if bytes.Equal(gotWith, gotWithBetween) {
				t.Fatal("between-boundary content is invisible BEFORE the removal — the boundary does not include it")
			}
			row.drop(with)
			row.drop(withBetween)
			gotDropped := observablePrefix(t, with)
			gotDroppedBetween := observablePrefix(t, withBetween)
			if !bytes.Equal(gotDropped, gotDroppedBetween) {
				t.Fatal("between-boundary content is still visible AFTER the removal — the boundary did not move")
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

// TestObservablePrefixScalarMatrix pins absent/present and value SEPARATELY
// for all three optional scalars (including present zero for the floats),
// and stops with same-elements-swapped, cardinality-only, and
// element-value rows.
func TestObservablePrefixScalarMatrix(t *testing.T) {
	base := func() *ChatRequest {
		r := baseObservableRequest()
		r.MaxTokens = nil
		r.Temperature = nil
		r.TopP = nil
		r.StopSequences = nil
		return r
	}
	pairs := []struct {
		name string
		a, b func(*ChatRequest)
	}{
		// max_tokens: absent vs present; value.
		{"max_tokens absent vs present", func(r *ChatRequest) {}, func(r *ChatRequest) { r.MaxTokens = proto.Int32(64) }},
		{"max_tokens value", func(r *ChatRequest) { r.MaxTokens = proto.Int32(64) }, func(r *ChatRequest) { r.MaxTokens = proto.Int32(128) }},
		// temperature: absent vs present (nonzero), absent vs present ZERO, zero vs nonzero.
		{"temperature absent vs present", func(r *ChatRequest) {}, func(r *ChatRequest) { r.Temperature = proto.Float64(0.5) }},
		{"temperature absent vs present zero", func(r *ChatRequest) {}, func(r *ChatRequest) { r.Temperature = proto.Float64(0) }},
		{"temperature zero vs nonzero", func(r *ChatRequest) { r.Temperature = proto.Float64(0) }, func(r *ChatRequest) { r.Temperature = proto.Float64(0.5) }},
		// top_p: absent vs present; absent vs present ZERO; zero vs nonzero.
		{"top_p absent vs present", func(r *ChatRequest) {}, func(r *ChatRequest) { r.TopP = proto.Float64(0.9) }},
		{"top_p absent vs present zero", func(r *ChatRequest) {}, func(r *ChatRequest) { r.TopP = proto.Float64(0) }},
		{"top_p zero vs nonzero", func(r *ChatRequest) { r.TopP = proto.Float64(0) }, func(r *ChatRequest) { r.TopP = proto.Float64(0.9) }},
		// stops: cardinality-only, same-elements-swapped (order), element value.
		{"stops cardinality", func(r *ChatRequest) { r.StopSequences = []string{"END"} }, func(r *ChatRequest) { r.StopSequences = []string{"END", "STOP"} }},
		{"stops order", func(r *ChatRequest) { r.StopSequences = []string{"END", "STOP"} }, func(r *ChatRequest) { r.StopSequences = []string{"STOP", "END"} }},
		{"stops element value", func(r *ChatRequest) { r.StopSequences = []string{"END"} }, func(r *ChatRequest) { r.StopSequences = []string{"STOP"} }},
	}
	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			ra, rb := base(), base()
			p.a(ra)
			p.b(rb)
			pa := observablePrefix(t, ra)
			pb := observablePrefix(t, rb)
			if bytes.Equal(pa, pb) {
				t.Fatal("the two scalars configurations produced the same prefix")
			}
		})
	}
}

// TestObservablePrefixRawFieldPins pins raw JSON lexeme/order sensitivity for
// provider extensions, safety settings, AND an in-prefix tool/block raw field
// (tool parameters_json).
func TestObservablePrefixRawFieldPins(t *testing.T) {
	// Extensions: member order.
	ext := baseObservableRequest()
	ext.ProviderExtensionsJson = []byte(`{"custom":{"a":1,"b":2}}`)
	extReordered := baseObservableRequest()
	extReordered.ProviderExtensionsJson = []byte(`{"custom":{"b":2,"a":1}}`)
	if bytes.Equal(observablePrefix(t, ext), observablePrefix(t, extReordered)) {
		t.Fatal("extension member order did not change the prefix")
	}

	// Safety settings: member order AND element value.
	safety := baseObservableRequest()
	safety.SafetySettingsJson = []byte(`[{"category":"A","threshold":"B"}]`)
	safetyReordered := baseObservableRequest()
	safetyReordered.SafetySettingsJson = []byte(`[{"threshold":"B","category":"A"}]`)
	if bytes.Equal(observablePrefix(t, safety), observablePrefix(t, safetyReordered)) {
		t.Fatal("safety member order did not change the prefix")
	}
	safetyValue := baseObservableRequest()
	safetyValue.SafetySettingsJson = []byte(`[{"category":"A","threshold":"C"}]`)
	if bytes.Equal(observablePrefix(t, safety), observablePrefix(t, safetyValue)) {
		t.Fatal("safety element value did not change the prefix")
	}

	// In-prefix tool raw field: parameters_json member order.
	tool := baseObservableRequest()
	tool.Tools[0].ParametersJson = []byte(`{"type":"object","properties":{"a":{"type":"string"}}}`)
	toolReordered := baseObservableRequest()
	toolReordered.Tools[0].ParametersJson = []byte(`{"properties":{"a":{"type":"string"}},"type":"object"}`)
	if bytes.Equal(observablePrefix(t, tool), observablePrefix(t, toolReordered)) {
		t.Fatal("tool parameters_json member order did not change the prefix")
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
	// Mutating the ACTUAL returned slice (not a copy) must not affect the
	// input request: retain a copy for comparison, then write into the
	// returned bytes directly.
	retained := append([]byte(nil), prefix...)
	prefix[0] = 'X'
	prefix[1] = 'Y'
	if !proto.Equal(base, before) {
		t.Fatal("mutating the returned prefix corrupted the input request")
	}
	// Retaining a copy of the first result: input mutation must move the
	// next result away from the retained copy.
	base.Messages[0].Blocks[0].GetText().Text = "changed"
	after := observablePrefix(t, base)
	if bytes.Equal(after, retained) {
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

// TestReplaceLastCacheBreakpointMixedCarriers: for every ordering
// combination of the three carriers, exactly the ACTUAL last carrier
// changes and every earlier carrier remains byte-identical; an exact-byte
// replay is changed=false and changes nothing.
func TestReplaceLastCacheBreakpointMixedCarriers(t *testing.T) {
	trText := func(s string) *ToolResultContentBlock {
		return &ToolResultContentBlock{Kind: &ToolResultContentBlock_Text{Text: &ToolResultTextBlock{Text: s}}}
	}
	trMarker := func(m string) *ToolResultContentBlock {
		return &ToolResultContentBlock{Kind: &ToolResultContentBlock_CacheBreakpoint{CacheBreakpoint: &ToolResultCacheBreakpoint{MarkerJson: []byte(m)}}}
	}
	marker := func(m string) *RequestBlock {
		return &RequestBlock{Kind: &RequestBlock_CacheBreakpoint{CacheBreakpoint: &RequestCacheBreakpoint{MarkerJson: []byte(m)}}}
	}
	base := func() *ChatRequest {
		return &ChatRequest{
			Model: "m",
			Tools: []*ToolDef{{Name: "read", Description: "d", ParametersJson: []byte(`{}`)}},
			Messages: []*Message{{Role: "user", Blocks: []*RequestBlock{
				{Kind: &RequestBlock_Text{Text: &RequestTextBlock{Text: "a"}}},
			}}},
		}
	}
	rows := []struct {
		name       string
		build      func(*ChatRequest)
		lastTool   bool
		lastTop    [2]int // msg, block; -1 when not applicable
		lastNested [3]int // msg, block, nested; -1 when not applicable
	}{
		{"tool + outer (last = outer)", func(r *ChatRequest) {
			r.Tools[0].CacheControlJson = []byte(`{"type":"ephemeral"}`)
			r.Messages[0].Blocks = append(r.Messages[0].Blocks, marker(`{"type":"ephemeral"}`))
		}, false, [2]int{0, 1}, [3]int{-1, -1, -1}},
		{"tool + nested (last = nested)", func(r *ChatRequest) {
			r.Tools[0].CacheControlJson = []byte(`{"type":"ephemeral"}`)
			r.Messages[0].Blocks = append(r.Messages[0].Blocks, &RequestBlock{Kind: &RequestBlock_ToolResult{ToolResult: &RequestToolResultBlock{
				ToolCallId: "c1", Content: []*ToolResultContentBlock{trText("r"), trMarker(`{"type":"ephemeral"}`)},
			}}})
		}, false, [2]int{-1, -1}, [3]int{0, 1, 1}},
		{"outer + nested (last = nested)", func(r *ChatRequest) {
			r.Messages[0].Blocks = append(r.Messages[0].Blocks, marker(`{"type":"ephemeral"}`))
			r.Messages[0].Blocks = append(r.Messages[0].Blocks, &RequestBlock{Kind: &RequestBlock_ToolResult{ToolResult: &RequestToolResultBlock{
				ToolCallId: "c1", Content: []*ToolResultContentBlock{trText("r"), trMarker(`{"type":"ephemeral"}`)},
			}}})
		}, false, [2]int{-1, -1}, [3]int{0, 2, 1}},
		{"tool + outer + nested (last = nested)", func(r *ChatRequest) {
			r.Tools[0].CacheControlJson = []byte(`{"type":"ephemeral"}`)
			r.Messages[0].Blocks = append(r.Messages[0].Blocks, marker(`{"type":"ephemeral"}`))
			r.Messages[0].Blocks = append(r.Messages[0].Blocks, &RequestBlock{Kind: &RequestBlock_ToolResult{ToolResult: &RequestToolResultBlock{
				ToolCallId: "c1", Content: []*ToolResultContentBlock{trText("r"), trMarker(`{"type":"ephemeral"}`)},
			}}})
		}, false, [2]int{-1, -1}, [3]int{0, 2, 1}},
		{"two outer markers in one message (last = later block)", func(r *ChatRequest) {
			r.Messages[0].Blocks = append(r.Messages[0].Blocks, marker(`{"type":"ephemeral"}`))
			r.Messages[0].Blocks = append(r.Messages[0].Blocks, marker(`{"type":"standard"}`))
		}, false, [2]int{0, 2}, [3]int{-1, -1, -1}},
		{"two nested markers in one tool result (last = later item)", func(r *ChatRequest) {
			r.Messages[0].Blocks = append(r.Messages[0].Blocks, &RequestBlock{Kind: &RequestBlock_ToolResult{ToolResult: &RequestToolResultBlock{
				ToolCallId: "c1", Content: []*ToolResultContentBlock{trMarker(`{"type":"ephemeral"}`), trText("mid"), trMarker(`{"type":"standard"}`)},
			}}})
		}, false, [2]int{-1, -1}, [3]int{0, 1, 2}},
	}
	replacement := []byte(`{"type":"1h"}`)
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			r := base()
			row.build(r)
			before := proto.Clone(r).(*ChatRequest)
			changed, err := ReplaceLastCacheBreakpoint(r, replacement)
			if err != nil || !changed {
				t.Fatalf("changed=%v err=%v, want changed", changed, err)
			}
			// Exactly the actual last carrier changed.
			switch {
			case row.lastTool:
				if !bytes.Equal(r.Tools[0].CacheControlJson, replacement) {
					t.Fatalf("tool carrier = %s, want %s", r.Tools[0].CacheControlJson, replacement)
				}
			case row.lastNested[0] >= 0:
				got := r.Messages[row.lastNested[0]].Blocks[row.lastNested[1]].GetToolResult().Content[row.lastNested[2]].GetCacheBreakpoint().MarkerJson
				if !bytes.Equal(got, replacement) {
					t.Fatalf("nested carrier = %s, want %s", got, replacement)
				}
			case row.lastTop[0] >= 0:
				got := r.Messages[row.lastTop[0]].Blocks[row.lastTop[1]].GetCacheBreakpoint().MarkerJson
				if !bytes.Equal(got, replacement) {
					t.Fatalf("outer carrier = %s, want %s", got, replacement)
				}
			}
			// Every earlier carrier byte-identical.
			after := proto.Clone(r).(*ChatRequest)
			r2 := base()
			row.build(r2)
			earlier := proto.Clone(r2).(*ChatRequest)
			// Replay on the clone of the ORIGINAL: only the last carrier may differ.
			if _, err := ReplaceLastCacheBreakpoint(earlier, replacement); err != nil {
				t.Fatalf("replay: %v", err)
			}
			// Compare every carrier region: count differing carrier fields.
			diffs := 0
			if !bytes.Equal(earlier.Tools[0].CacheControlJson, r2.Tools[0].CacheControlJson) {
				diffs++
			}
			for mi := range r2.Messages {
				for bi := range r2.Messages[mi].Blocks {
					b2 := r2.Messages[mi].Blocks[bi]
					b1 := earlier.Messages[mi].Blocks[bi]
					if b2.GetCacheBreakpoint() != nil && b1.GetCacheBreakpoint() != nil {
						if !bytes.Equal(b1.GetCacheBreakpoint().MarkerJson, b2.GetCacheBreakpoint().MarkerJson) {
							diffs++
						}
					}
					if tr2 := b2.GetToolResult(); tr2 != nil {
						tr1 := b1.GetToolResult()
						for ci := range tr2.Content {
							c2 := tr2.Content[ci]
							c1 := tr1.Content[ci]
							if c2.GetCacheBreakpoint() != nil && c1.GetCacheBreakpoint() != nil {
								if !bytes.Equal(c1.GetCacheBreakpoint().MarkerJson, c2.GetCacheBreakpoint().MarkerJson) {
									diffs++
								}
							}
						}
					}
				}
			}
			if diffs != 1 {
				t.Fatalf("%d carrier fields changed, want exactly 1 (the actual last carrier)", diffs)
			}
			// Exact-byte replay: changed=false, nothing changes.
			changed2, err := ReplaceLastCacheBreakpoint(after, replacement)
			if err != nil || changed2 {
				t.Fatalf("replay: changed=%v err=%v, want no-op", changed2, err)
			}
			if !proto.Equal(after, r) {
				t.Fatal("exact-byte replay mutated the request")
			}
			_ = before
		})
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
