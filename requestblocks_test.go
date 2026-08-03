package plugin_sdk

// RequestBlocksFingerprint matrix: determinism, sensitivity (every field
// change alters the digest), and the reflection-backed coverage inventory
// that forces a decision for additive block fields.

import (
	"fmt"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

func fingerprintSeed() *pbv2.Message {
	return &pbv2.Message{
		Role: "assistant",
		Blocks: []*pbv2.RequestBlock{
			{Kind: &pbv2.RequestBlock_Thinking{Thinking: &pbv2.RequestThinkingBlock{Text: "r", Signature: "s", PartMetadataJson: []byte(`{"src":"x"}`)}}},
			{Kind: &pbv2.RequestBlock_Text{Text: &pbv2.RequestTextBlock{Text: "hi", Signature: "s", PartMetadataJson: []byte(`{"src":"x"}`)}}},
			{Kind: &pbv2.RequestBlock_RedactedThinking{RedactedThinking: &pbv2.RequestRedactedThinkingBlock{Data: "d"}}},
			{Kind: &pbv2.RequestBlock_ToolUse{ToolUse: &pbv2.RequestToolUseBlock{
				Id: "t1", Name: "read", ArgumentsJson: []byte(`{"z":1,"a":2}`), Signature: "s", PartMetadataJson: []byte(`{}`),
			}}},
			{Kind: &pbv2.RequestBlock_ToolResult{ToolResult: &pbv2.RequestToolResultBlock{
				ToolCallId: "t1", ToolName: "read",
				PartMetadataJson: []byte(`{}`),
				WillContinue:     boolPtr(true),
				Scheduling:       strPtr("WHEN_IDLE"),
				Signature:        "trsig",
				Content: []*pbv2.ToolResultContentBlock{
					{Kind: &pbv2.ToolResultContentBlock_Text{Text: &pbv2.ToolResultTextBlock{Text: "ok"}}},
					{Kind: &pbv2.ToolResultContentBlock_Unknown{Unknown: &pbv2.ToolResultUnknownBlock{
						Kind: "json", PayloadJson: []byte(`{"score":42}`),
					}}},
					{Kind: &pbv2.ToolResultContentBlock_CacheBreakpoint{CacheBreakpoint: &pbv2.ToolResultCacheBreakpoint{
						MarkerJson: []byte(`{}`),
					}}},
				},
			}}},
			{Kind: &pbv2.RequestBlock_CacheBreakpoint{CacheBreakpoint: &pbv2.RequestCacheBreakpoint{
				MarkerJson: []byte(`{"type":"ephemeral"}`),
			}}},
			{Kind: &pbv2.RequestBlock_Unknown{Unknown: &pbv2.RequestUnknownBlock{
				Kind: "custom", PayloadJson: []byte(`{"v":1e999}`), PartMetadataJson: []byte(`{}`), Signature: "usig",
			}}},
			{Kind: &pbv2.RequestBlock_TrailingSignature{TrailingSignature: &pbv2.RequestTrailingSignatureBlock{Signature: "t", PartMetadataJson: []byte(`{}`)}}},
		},
	}
}

func TestRequestBlocksFingerprintDeterministic(t *testing.T) {
	a, err := RequestBlocksFingerprint(fingerprintSeed())
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	b, err := RequestBlocksFingerprint(fingerprintSeed())
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if a != b {
		t.Fatalf("fingerprint nondeterministic: %s vs %s", a, b)
	}
	if a == "" {
		t.Fatal("empty fingerprint")
	}
}

func TestRequestBlocksFingerprintSensitive(t *testing.T) {
	base, err := RequestBlocksFingerprint(fingerprintSeed())
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	mutate := func(name string, f func(*pbv2.Message)) {
		t.Helper()
		m := fingerprintSeed()
		f(m)
		got, err := RequestBlocksFingerprint(m)
		if err != nil {
			t.Fatalf("%s: fingerprint error: %v", name, err)
		}
		if got == base {
			t.Fatalf("%s: fingerprint unchanged", name)
		}
	}

	mutate("role", func(m *pbv2.Message) { m.Role = "user" })
	mutate("text", func(m *pbv2.Message) { m.Blocks[1].GetText().Text = "HI" })
	mutate("text signature", func(m *pbv2.Message) { m.Blocks[1].GetText().Signature = "S2" })
	mutate("thinking", func(m *pbv2.Message) { m.Blocks[0].GetThinking().Text = "R" })
	mutate("redacted", func(m *pbv2.Message) { m.Blocks[2].GetRedactedThinking().Data = "D" })
	mutate("tool id", func(m *pbv2.Message) { m.Blocks[3].GetToolUse().Id = "t2" })
	mutate("tool name", func(m *pbv2.Message) { m.Blocks[3].GetToolUse().Name = "write" })
	mutate("arguments order", func(m *pbv2.Message) {
		m.Blocks[3].GetToolUse().ArgumentsJson = []byte(`{"a":2,"z":1}`) // key order is prefix-visible
	})
	mutate("arguments content", func(m *pbv2.Message) {
		m.Blocks[3].GetToolUse().ArgumentsJson = []byte(`{"z":2,"a":2}`)
	})
	mutate("tool call signature", func(m *pbv2.Message) { m.Blocks[3].GetToolUse().Signature = "s2" })
	mutate("result id", func(m *pbv2.Message) { m.Blocks[4].GetToolResult().ToolCallId = "t2" })
	mutate("result name", func(m *pbv2.Message) { m.Blocks[4].GetToolResult().ToolName = "write" })
	mutate("nested text", func(m *pbv2.Message) { m.Blocks[4].GetToolResult().Content[0].GetText().Text = "OK" })
	mutate("nested unknown kind", func(m *pbv2.Message) { m.Blocks[4].GetToolResult().Content[1].GetUnknown().Kind = "xml" })
	mutate("nested unknown payload", func(m *pbv2.Message) {
		m.Blocks[4].GetToolResult().Content[1].GetUnknown().PayloadJson = []byte(`{"score":43}`)
	})
	mutate("nested marker", func(m *pbv2.Message) {
		m.Blocks[4].GetToolResult().Content[2].GetCacheBreakpoint().MarkerJson = []byte(`{"type":"default"}`)
	})
	mutate("cache marker", func(m *pbv2.Message) {
		m.Blocks[5].GetCacheBreakpoint().MarkerJson = []byte(`{"type":"default"}`)
	})
	mutate("unknown kind", func(m *pbv2.Message) { m.Blocks[6].GetUnknown().Kind = "custom2" })
	mutate("unknown payload", func(m *pbv2.Message) { m.Blocks[6].GetUnknown().PayloadJson = []byte(`{"v":2}`) })
	mutate("trailing signature", func(m *pbv2.Message) {
		m.Blocks[7].GetTrailingSignature().Signature = "t2"
	})
	mutate("text part metadata", func(m *pbv2.Message) {
		m.Blocks[1].GetText().PartMetadataJson = []byte(`{"src":"y"}`)
	})
	mutate("thinking part metadata", func(m *pbv2.Message) {
		m.Blocks[0].GetThinking().PartMetadataJson = []byte(`{"src":"y"}`)
	})
	mutate("tool-use part metadata", func(m *pbv2.Message) {
		m.Blocks[3].GetToolUse().PartMetadataJson = []byte(`{"src":"y"}`)
	})
	mutate("result part metadata", func(m *pbv2.Message) {
		m.Blocks[4].GetToolResult().PartMetadataJson = []byte(`{"src":"y"}`)
	})
	mutate("result will_continue false", func(m *pbv2.Message) {
		m.Blocks[4].GetToolResult().WillContinue = boolPtr(false)
	})
	mutate("result will_continue absent", func(m *pbv2.Message) {
		m.Blocks[4].GetToolResult().WillContinue = nil
	})
	mutate("result scheduling value", func(m *pbv2.Message) {
		m.Blocks[4].GetToolResult().Scheduling = strPtr("SILENT")
	})
	mutate("result scheduling absent", func(m *pbv2.Message) {
		m.Blocks[4].GetToolResult().Scheduling = nil
	})
	mutate("result signature", func(m *pbv2.Message) {
		m.Blocks[4].GetToolResult().Signature = "trsig2"
	})
	mutate("unknown part metadata", func(m *pbv2.Message) {
		m.Blocks[6].GetUnknown().PartMetadataJson = []byte(`{"src":"y"}`)
	})
	mutate("unknown signature", func(m *pbv2.Message) {
		m.Blocks[6].GetUnknown().Signature = "usig2"
	})
	mutate("trailing part metadata", func(m *pbv2.Message) {
		m.Blocks[7].GetTrailingSignature().PartMetadataJson = []byte(`{"src":"y"}`)
	})
	mutate("block order", func(m *pbv2.Message) {
		m.Blocks[0], m.Blocks[1] = m.Blocks[1], m.Blocks[0]
	})
	mutate("block count", func(m *pbv2.Message) { m.Blocks = m.Blocks[:len(m.Blocks)-1] })
	mutate("nested order", func(m *pbv2.Message) {
		c := m.Blocks[4].GetToolResult().Content
		c[0], c[1] = c[1], c[0]
	})
}

func boolPtr(v bool) *bool { return &v }

func strPtr(v string) *string { return &v }

// TestRequestBlocksFingerprintTotality — invalid input NEVER yields a
// usable fingerprint: nil message, nil blocks, typed-nil arms, and invalid
// nested payloads are errors, not ordinary digests.
func TestRequestBlocksFingerprintTotality(t *testing.T) {
	cases := map[string]*pbv2.Message{
		"nil message": nil,
		"nil block element": {
			Role:   "user",
			Blocks: []*pbv2.RequestBlock{nil},
		},
		"typed-nil text arm": {
			Role: "user",
			Blocks: []*pbv2.RequestBlock{
				{Kind: &pbv2.RequestBlock_Text{}},
			},
		},
		"typed-nil tool result": {
			Role: "user",
			Blocks: []*pbv2.RequestBlock{
				{Kind: &pbv2.RequestBlock_ToolResult{}},
			},
		},
		"typed-nil unknown": {
			Role: "user",
			Blocks: []*pbv2.RequestBlock{
				{Kind: &pbv2.RequestBlock_Unknown{}},
			},
		},
		"nested nil element": {
			Role: "user",
			Blocks: []*pbv2.RequestBlock{
				{Kind: &pbv2.RequestBlock_ToolResult{ToolResult: &pbv2.RequestToolResultBlock{
					ToolCallId: "c1",
					Content:    []*pbv2.ToolResultContentBlock{nil},
				}}},
			},
		},
		"nested typed-nil unknown": {
			Role: "user",
			Blocks: []*pbv2.RequestBlock{
				{Kind: &pbv2.RequestBlock_ToolResult{ToolResult: &pbv2.RequestToolResultBlock{
					ToolCallId: "c1",
					Content: []*pbv2.ToolResultContentBlock{
						{Kind: &pbv2.ToolResultContentBlock_Unknown{}},
					},
				}}},
			},
		},
		"nested invalid payload": {
			Role: "user",
			Blocks: []*pbv2.RequestBlock{
				{Kind: &pbv2.RequestBlock_ToolResult{ToolResult: &pbv2.RequestToolResultBlock{
					ToolCallId: "c1",
					Content: []*pbv2.ToolResultContentBlock{
						{Kind: &pbv2.ToolResultContentBlock_Unknown{Unknown: &pbv2.ToolResultUnknownBlock{
							Kind: "json", PayloadJson: []byte(`[1,2]`),
						}}},
					},
				}}},
			},
		},
		"nested invalid marker": {
			Role: "user",
			Blocks: []*pbv2.RequestBlock{
				{Kind: &pbv2.RequestBlock_ToolResult{ToolResult: &pbv2.RequestToolResultBlock{
					ToolCallId: "c1",
					Content: []*pbv2.ToolResultContentBlock{
						{Kind: &pbv2.ToolResultContentBlock_CacheBreakpoint{CacheBreakpoint: &pbv2.ToolResultCacheBreakpoint{
							MarkerJson: []byte(`nope`),
						}}},
					},
				}}},
			},
		},
		"nested no arm": {
			Role: "user",
			Blocks: []*pbv2.RequestBlock{
				{Kind: &pbv2.RequestBlock_ToolResult{ToolResult: &pbv2.RequestToolResultBlock{
					ToolCallId: "c1",
					Content:    []*pbv2.ToolResultContentBlock{{}},
				}}},
			},
		},
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := RequestBlocksFingerprint(m); err == nil {
				t.Fatalf("invalid input produced a usable fingerprint %q", got)
			}
		})
	}
}

// checkFingerprintInventory is the ONE guard behind both fingerprint
// inventory tests: it requires the supplied declared set and the
// descriptor-derived visited set to be exactly equal in BOTH directions.
// The stale-field negative uses this same helper, so deleting either
// direction here fails that test too — the regression is connected to the
// guard, not a private copy.
func checkFingerprintInventory(declared []string) error {
	decl := map[string]bool{}
	for _, f := range declared {
		if decl[f] {
			return fmt.Errorf("coverage declares %s twice", f)
		}
		decl[f] = true
	}

	visited := map[string]bool{}
	var walk func(md protoreflect.MessageDescriptor)
	walk = func(md protoreflect.MessageDescriptor) {
		fields := md.Fields()
		for i := 0; i < fields.Len(); i++ {
			fd := fields.Get(i)
			visited[string(fd.FullName())] = true
			if fd.Kind() == protoreflect.MessageKind {
				sub := fd.Message()
				if sub != nil && sub.FullName() != "torana.v2.Message" {
					walk(sub)
				}
			}
		}
	}
	walk((&pbv2.Message{}).ProtoReflect().Descriptor())

	// Direction 1: descriptor minus declared empty — every visited field has
	// a decision.
	for f := range visited {
		if !decl[f] {
			return fmt.Errorf("request-body field %s has no fingerprint coverage decision", f)
		}
	}
	// Direction 2: declared minus descriptor empty — a stale or misspelled
	// declaration (even one sharing the body prefix) fails.
	for f := range decl {
		if !visited[f] {
			return fmt.Errorf("fingerprint coverage declares %s which is not a request-body field", f)
		}
	}
	return nil
}

// TestRequestFingerprintCoverageExact: the real inventory must pass the
// shared bidirectional guard.
func TestRequestFingerprintCoverageExact(t *testing.T) {
	if err := checkFingerprintInventory(fingerprintFieldCoverage); err != nil {
		t.Fatal(err)
	}
}

// TestRequestFingerprintCoverageRejectsStaleField: a plausible same-prefix
// stale declaration must fail the SAME shared guard (the reverse direction
// lives inside checkFingerprintInventory; deleting it fails this test).
func TestRequestFingerprintCoverageRejectsStaleField(t *testing.T) {
	stale := append(append([]string{}, fingerprintFieldCoverage...), "torana.v2.RequestTextBlock.removed_field")
	if err := checkFingerprintInventory(stale); err == nil {
		t.Fatal("stale same-prefix fingerprint declaration was not caught")
	}
}
