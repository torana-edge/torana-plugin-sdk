package plugin_sdk

// RequestBlocksFingerprint matrix: determinism, sensitivity (every field
// change alters the digest), and the reflection-backed coverage inventory
// that forces a decision for additive block fields.

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
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

// TestToolResultContentFingerprintBinaryLayout — the nested digest layout
// is pinned by a GENUINELY INDEPENDENT encoder: it hard-codes the v1
// domain literal (never the production constant), emits the binary
// preimage with its own big-endian writer, and asserts BOTH the live
// digest and a fixed GOLDEN digest for a fixed input. A drift in the
// implementation OR the encoder fails; the golden hex additionally pins
// the digest value itself (a change in domain framing, length encoding,
// arm numbering, or presence bytes changes the hex).
func TestToolResultContentFingerprintBinaryLayout(t *testing.T) {
	content := []*pbv2.ToolResultContentBlock{
		{Kind: &pbv2.ToolResultContentBlock_Text{Text: &pbv2.ToolResultTextBlock{Text: "ok"}}},
		{Kind: &pbv2.ToolResultContentBlock_Unknown{Unknown: &pbv2.ToolResultUnknownBlock{Kind: "json", PayloadJson: []byte(`{"v":1}`)}}},
		{Kind: &pbv2.ToolResultContentBlock_CacheBreakpoint{CacheBreakpoint: &pbv2.ToolResultCacheBreakpoint{MarkerJson: []byte(`{"type":"default"}`)}}},
	}

	// The independent reference encoder (spec): literal v1 domain, its own
	// fixed-width unsigned big-endian writer, numeric arm bytes, literal
	// presence bytes, exact payload bytes.
	ref := func() [32]byte {
		h := sha256.New()
		u64 := func(v uint64) { var b [8]byte; binary.BigEndian.PutUint64(b[:], v); h.Write(b[:]) }
		by := func(v []byte) { u64(uint64(len(v))); h.Write(v) }
		str := func(v string) { by([]byte(v)) }
		domain := []byte("torana/tool-result-content/v1")
		u64(uint64(len(domain)))
		h.Write(domain)
		u64(uint64(len(content)))
		h.Write([]byte{1, 1}) // text arm, present
		str("ok")
		h.Write([]byte{2, 1}) // unknown arm, present
		str("json")
		by([]byte(`{"v":1}`))
		h.Write([]byte{3, 1}) // cache arm, present
		by([]byte(`{"type":"default"}`))
		var sum [32]byte
		copy(sum[:], h.Sum(nil))
		return sum
	}
	want := ref()

	got, err := ToolResultContentFingerprint(content)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if got != want {
		t.Fatalf("digest mismatch:\n got %x\nwant %x", got, want)
	}
	// Fixed golden digest (pinned value; regenerate deliberately with the
	// reference encoder when the layout changes).
	const golden = "9a0e95e92fb7b7e617d1bc5e5aeceb73abc63e7a9f22df4cad70283d96b50d47"
	if hex.EncodeToString(got[:]) != golden {
		t.Fatalf("golden digest mismatch:\n got %x\nwant %s", got, golden)
	}

	// Cross-domain negative: the same content under a DIFFERENT domain
	// frame is a different digest (the domain is hashed, not prose).
	other := sha256.New()
	ou64 := func(v uint64) { var b [8]byte; binary.BigEndian.PutUint64(b[:], v); other.Write(b[:]) }
	oby := func(v []byte) { ou64(uint64(len(v))); other.Write(v) }
	ostr := func(v string) { oby([]byte(v)) }
	d2 := []byte("torana/tool-result-content/v2")
	ou64(uint64(len(d2)))
	other.Write(d2)
	ou64(3)
	other.Write([]byte{1, 1})
	ostr("ok")
	other.Write([]byte{2, 1})
	ostr("json")
	oby([]byte(`{"v":1}`))
	other.Write([]byte{3, 1})
	oby([]byte(`{"type":"default"}`))
	var osum [32]byte
	copy(osum[:], other.Sum(nil))
	if osum == got {
		t.Fatal("domain frame change produced an identical digest")
	}
}

// TestToolResultContentFingerprintLayoutSweep — per-arm and per-edge
// behavioral pins over the binary layout: order sensitivity, empty
// values, large payload lengths (beyond any 1-byte/2-byte framing), nil
// element, typed-nil arm, no arm, invalid object, and domain change.
func TestToolResultContentFingerprintLayoutSweep(t *testing.T) {
	text := func(s string) *pbv2.ToolResultContentBlock {
		return &pbv2.ToolResultContentBlock{Kind: &pbv2.ToolResultContentBlock_Text{Text: &pbv2.ToolResultTextBlock{Text: s}}}
	}
	unknown := func(kind string, payload []byte) *pbv2.ToolResultContentBlock {
		return &pbv2.ToolResultContentBlock{Kind: &pbv2.ToolResultContentBlock_Unknown{Unknown: &pbv2.ToolResultUnknownBlock{Kind: kind, PayloadJson: payload}}}
	}
	cache := func(marker []byte) *pbv2.ToolResultContentBlock {
		return &pbv2.ToolResultContentBlock{Kind: &pbv2.ToolResultContentBlock_CacheBreakpoint{CacheBreakpoint: &pbv2.ToolResultCacheBreakpoint{MarkerJson: marker}}}
	}

	// Order sensitivity: [text, unknown] != [unknown, text].
	a, err := ToolResultContentFingerprint([]*pbv2.ToolResultContentBlock{text("x"), unknown("json", []byte(`{"a":1}`))})
	if err != nil {
		t.Fatal(err)
	}
	b, err := ToolResultContentFingerprint([]*pbv2.ToolResultContentBlock{unknown("json", []byte(`{"a":1}`)), text("x")})
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("element order is not part of the digest")
	}

	// Empty values are length-framed (empty text vs absent is a distinct
	// arm layout; an empty text string is a legal, hashable value).
	empty, err := ToolResultContentFingerprint([]*pbv2.ToolResultContentBlock{text("")})
	if err != nil {
		t.Fatalf("empty text value refused: %v", err)
	}
	full, err := ToolResultContentFingerprint([]*pbv2.ToolResultContentBlock{text("x")})
	if err != nil {
		t.Fatal(err)
	}
	if empty == full {
		t.Fatal("empty vs non-empty text hashed identically")
	}

	// Large payload length (65536 bytes) — the u64 length framing must
	// handle values no single-byte or ASCII-decimal framing could.
	big := []byte(`{"a":"`)
	big = append(big, bytes.Repeat([]byte("x"), 65536-8)...) // {"a":" + x*65528 + "}
	big = append(big, '"', '}')
	if len(big) != 65536 {
		t.Fatalf("payload size %d != 65536", len(big))
	}
	if _, err := ToolResultContentFingerprint([]*pbv2.ToolResultContentBlock{unknown("json", big)}); err != nil {
		t.Fatalf("large payload refused: %v", err)
	}

	// Error rows (totality): nil element, typed-nil arms, no arm, invalid
	// object payload, invalid marker.
	for name, in := range map[string][]*pbv2.ToolResultContentBlock{
		"nil element":        {nil},
		"typed-nil text":     {{Kind: &pbv2.ToolResultContentBlock_Text{}}},
		"typed-nil unknown":  {{Kind: &pbv2.ToolResultContentBlock_Unknown{}}},
		"typed-nil cache":    {{Kind: &pbv2.ToolResultContentBlock_CacheBreakpoint{}}},
		"no arm":             {{}},
		"non-object payload": {unknown("json", []byte(`[1,2]`))},
		"invalid marker":     {cache([]byte(`nope`))},
	} {
		if _, err := ToolResultContentFingerprint(in); err == nil {
			t.Errorf("%s: expected an error, got a digest", name)
		}
	}
}
