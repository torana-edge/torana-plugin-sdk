package plugin_sdk

// RequestBlocksFingerprint matrix: determinism, sensitivity (every field
// change alters the digest), and the reflection-backed coverage inventory
// that forces a decision for additive block fields.

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

func fingerprintSeed() *pbv2.Message {
	return &pbv2.Message{
		Role: "assistant",
		Blocks: []*pbv2.RequestBlock{
			{Kind: &pbv2.RequestBlock_Thinking{Thinking: &pbv2.RequestThinkingBlock{Text: "r", Signature: "s"}}},
			{Kind: &pbv2.RequestBlock_Text{Text: &pbv2.RequestTextBlock{Text: "hi", Signature: "s"}}},
			{Kind: &pbv2.RequestBlock_RedactedThinking{RedactedThinking: &pbv2.RequestRedactedThinkingBlock{Data: "d"}}},
			{Kind: &pbv2.RequestBlock_ToolUse{ToolUse: &pbv2.RequestToolUseBlock{
				Id: "t1", Name: "read", ArgumentsJson: []byte(`{"z":1,"a":2}`), Signature: "s",
			}}},
			{Kind: &pbv2.RequestBlock_ToolResult{ToolResult: &pbv2.RequestToolResultBlock{
				ToolCallId: "t1", ToolName: "read",
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
				Kind: "custom", PayloadJson: []byte(`{"v":1e999}`),
			}}},
			{Kind: &pbv2.RequestBlock_TrailingSignature{TrailingSignature: &pbv2.RequestTrailingSignatureBlock{Signature: "t"}}},
		},
	}
}

func TestRequestBlocksFingerprintDeterministic(t *testing.T) {
	a := RequestBlocksFingerprint(fingerprintSeed())
	b := RequestBlocksFingerprint(fingerprintSeed())
	if a != b {
		t.Fatalf("fingerprint nondeterministic: %s vs %s", a, b)
	}
	if a == "" {
		t.Fatal("empty fingerprint")
	}
}

func TestRequestBlocksFingerprintSensitive(t *testing.T) {
	base := RequestBlocksFingerprint(fingerprintSeed())
	mutate := func(name string, f func(*pbv2.Message)) {
		t.Helper()
		m := fingerprintSeed()
		f(m)
		if got := RequestBlocksFingerprint(m); got == base {
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
	mutate("block order", func(m *pbv2.Message) {
		m.Blocks[0], m.Blocks[1] = m.Blocks[1], m.Blocks[0]
	})
	mutate("block count", func(m *pbv2.Message) { m.Blocks = m.Blocks[:len(m.Blocks)-1] })
	mutate("nested order", func(m *pbv2.Message) {
		c := m.Blocks[4].GetToolResult().Content
		c[0], c[1] = c[1], c[0]
	})
}

// TestRequestFingerprintCoverageExact: the reflection-backed inventory is
// BIDIRECTIONAL — the declared coverage set and the set of fields actually
// visited by the descriptor walk must be exactly equal, so an additive block
// field fails until it is hashed, and a stale or misspelled declaration fails
// even when it shares the body prefix.
func TestRequestFingerprintCoverageExact(t *testing.T) {
	declared := map[string]bool{}
	for _, f := range fingerprintFieldCoverage {
		if declared[f] {
			t.Fatalf("coverage declares %s twice", f)
		}
		declared[f] = true
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
		if !declared[f] {
			t.Fatalf("request-body field %s has no fingerprint coverage decision", f)
		}
	}
	// Direction 2: declared minus descriptor empty — a stale or misspelled
	// declaration (even one sharing the body prefix) fails.
	for f := range declared {
		if !visited[f] {
			t.Fatalf("fingerprint coverage declares %s which is not a request-body field", f)
		}
	}
}

// TestRequestFingerprintCoverageRejectsStaleField: a plausible same-prefix
// stale declaration must fail the exact inventory (the reverse direction of
// the check above).
func TestRequestFingerprintCoverageRejectsStaleField(t *testing.T) {
	original := fingerprintFieldCoverage
	t.Cleanup(func() { fingerprintFieldCoverage = original })
	fingerprintFieldCoverage = append(append([]string{}, original...), "torana.v2.RequestTextBlock.removed_field")

	declared := map[string]bool{}
	for _, f := range fingerprintFieldCoverage {
		declared[f] = true
	}
	visited := map[string]bool{}
	var walk func(md protoreflect.MessageDescriptor)
	walk = func(md protoreflect.MessageDescriptor) {
		fields := md.Fields()
		for i := 0; i < fields.Len(); i++ {
			fd := fields.Get(i)
			visited[string(fd.FullName())] = true
			if fd.Kind() == protoreflect.MessageKind {
				if sub := fd.Message(); sub != nil && sub.FullName() != "torana.v2.Message" {
					walk(sub)
				}
			}
		}
	}
	walk((&pbv2.Message{}).ProtoReflect().Descriptor())
	for f := range declared {
		if !visited[f] {
			return // the stale declaration was caught
		}
	}
	t.Fatal("stale same-prefix fingerprint declaration was not caught")
}
