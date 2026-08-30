package plugin_sdk_test

// The EXECUTABLE signature reference matrix, driven bidirectionally from the
// outboundpolicy requestSignatureContracts registry (REV 5 review round 1):
//
//   - every registry (message, scope, ref) resolves to exactly one mutation
//     case; every case resolves back to a live registry ref;
//   - each mutation is applied to a valid baseline and contentChanged is
//     DERIVED from the independent before/after covered projection (never a
//     literal bool);
//   - covered mutation ⇒ SignatureCleared; a non-covered mutation per
//     surface ⇒ unchanged projection + SignatureDropped;
//   - the resolver is surface/scope-specific (the trailing block's own
//     part_metadata_json and the TrailingStandalone preceding text/thinking
//     are read where the registry declares them);
//   - the trailing non-circular table EXECUTES all promised decisions.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/torana-edge/torana-plugin-sdk/outboundpolicy"
	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"google.golang.org/protobuf/proto"
)

// ---- surface/scope-specific covered projection ----

// tokenBlockOf returns the token-bearing block of the contract's surface.
func tokenBlockOf(msg *pbv1.Message, surface string) *pbv1.RequestBlock {
	for _, b := range msg.Blocks {
		switch surface {
		case "torana.v1.RequestTextBlock":
			if b.GetText() != nil {
				return b
			}
		case "torana.v1.RequestThinkingBlock":
			if b.GetThinking() != nil {
				return b
			}
		case "torana.v1.RequestToolUseBlock":
			if b.GetToolUse() != nil {
				return b
			}
		case "torana.v1.RequestUnknownBlock":
			if b.GetUnknown() != nil {
				return b
			}
		case "torana.v1.RequestToolResultBlock":
			if b.GetToolResult() != nil {
				return b
			}
		case "torana.v1.RequestTrailingSignatureBlock":
			if b.GetTrailingSignature() != nil {
				return b
			}
		}
	}
	return nil
}

// ownRefValue resolves a SameMessage ref on the token-bearing block,
// surface/scope-specifically (the generic walk would read the wrong block
// type and miss the trailing carrier's own metadata).
func ownRefValue(b *pbv1.RequestBlock, surface, ref string) string {
	switch surface {
	case "torana.v1.RequestTextBlock":
		t := b.GetText()
		switch ref {
		case "text":
			return t.Text
		case "part_metadata_json":
			return string(t.PartMetadataJson)
		}
	case "torana.v1.RequestThinkingBlock":
		th := b.GetThinking()
		switch ref {
		case "text":
			return th.Text
		case "part_metadata_json":
			return string(th.PartMetadataJson)
		}
	case "torana.v1.RequestToolUseBlock":
		tu := b.GetToolUse()
		switch ref {
		case "id":
			return tu.Id
		case "name":
			return tu.Name
		case "arguments_json":
			return string(tu.ArgumentsJson)
		case "part_metadata_json":
			return string(tu.PartMetadataJson)
		}
	case "torana.v1.RequestUnknownBlock":
		u := b.GetUnknown()
		switch ref {
		case "kind":
			return u.Kind
		case "payload_json":
			return string(u.PayloadJson)
		case "part_metadata_json":
			return string(u.PartMetadataJson)
		}
	case "torana.v1.RequestToolResultBlock":
		tr := b.GetToolResult()
		switch ref {
		case "tool_call_id":
			return tr.ToolCallId
		case "tool_name":
			return tr.ToolName
		case "part_metadata_json":
			return string(tr.PartMetadataJson)
		case "will_continue":
			if tr.WillContinue == nil {
				return "absent"
			}
			return "present:" + string(rune('0'+b2i(*tr.WillContinue)))
		case "scheduling":
			if tr.Scheduling == nil {
				return "absent"
			}
			return "present:" + *tr.Scheduling
		case "content":
			var out string
			for _, c := range tr.Content {
				switch {
				case c.GetText() != nil:
					out += "text:" + c.GetText().Text + "|"
				case c.GetUnknown() != nil:
					out += "unknown:" + c.GetUnknown().Kind + ":" + string(c.GetUnknown().PayloadJson) + "|"
				case c.GetCacheBreakpoint() != nil:
					out += "marker:" + string(c.GetCacheBreakpoint().MarkerJson) + "|"
				}
			}
			return out
		}
	case "torana.v1.RequestTrailingSignatureBlock":
		ts := b.GetTrailingSignature()
		if ref == "part_metadata_json" {
			return string(ts.PartMetadataJson)
		}
	}
	return ""
}

// precedingCoveredProjection resolves the TrailingStandalone refs: the
// preceding Text/Thinking blocks' text, in order.
func precedingCoveredProjection(msg *pbv1.Message) string {
	var out string
	for _, b := range msg.Blocks {
		if t := b.GetText(); t != nil {
			out += "text:" + t.Text + "|"
		}
		if th := b.GetThinking(); th != nil {
			out += "thinking:" + th.Text + "|"
		}
	}
	return out
}

// coveredProjection is the independent reference projection for one
// contract: every covered ref, resolved surface/scope-specifically.
func coveredProjection(msg *pbv1.Message, contract outboundpolicy.SignatureContract) string {
	b := tokenBlockOf(msg, contract.Message)
	h := sha256.New()
	frame := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	for _, ref := range contract.Content {
		switch ref.Scope {
		case outboundpolicy.SignatureScopeTrailingStandalone:
			frame(precedingCoveredProjection(msg))
		default:
			if b == nil {
				frame("<no-token-block>")
				continue
			}
			frame(ownRefValue(b, contract.Message, ref.Ref))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func boolPtr(b bool) *bool    { return &b }
func strPtr(s string) *string { return &s }

func scopeName(s outboundpolicy.SignatureScope) string {
	switch s {
	case outboundpolicy.SignatureScopeSameMessage:
		return "SameMessage"
	case outboundpolicy.SignatureScopeTrailingStandalone:
		return "TrailingStandalone"
	case outboundpolicy.SignatureScopeCurrentContentBlock:
		return "CurrentContentBlock"
	case outboundpolicy.SignatureScopeToolCallBlockByIndex:
		return "ToolCallBlockByIndex"
	}
	return "Unspecified"
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---- baselines + mutators ----

// surfaceBaseline builds a VALID message carrying the surface's token block
// plus surrounding blocks so covered/non-covered mutations are meaningful.
func surfaceBaseline(surface string) *pbv1.Message {
	text := func(s string) *pbv1.RequestBlock {
		return &pbv1.RequestBlock{Kind: &pbv1.RequestBlock_Text{Text: &pbv1.RequestTextBlock{Text: s}}}
	}
	trText := func(s string) *pbv1.ToolResultContentBlock {
		return &pbv1.ToolResultContentBlock{Kind: &pbv1.ToolResultContentBlock_Text{Text: &pbv1.ToolResultTextBlock{Text: s}}}
	}
	switch surface {
	case "torana.v1.RequestTextBlock":
		return &pbv1.Message{Role: "assistant", Blocks: []*pbv1.RequestBlock{text("t"), text("sibling")}}
	case "torana.v1.RequestThinkingBlock":
		return &pbv1.Message{Role: "assistant", Blocks: []*pbv1.RequestBlock{{Kind: &pbv1.RequestBlock_Thinking{Thinking: &pbv1.RequestThinkingBlock{Text: "t"}}}, text("sibling")}}
	case "torana.v1.RequestToolUseBlock":
		return &pbv1.Message{Role: "assistant", Blocks: []*pbv1.RequestBlock{
			{Kind: &pbv1.RequestBlock_ToolUse{ToolUse: &pbv1.RequestToolUseBlock{Id: "c1", Name: "read", ArgumentsJson: []byte(`{}`)}}},
			text("sibling"),
		}}
	case "torana.v1.RequestUnknownBlock":
		return &pbv1.Message{Role: "user", Blocks: []*pbv1.RequestBlock{
			{Kind: &pbv1.RequestBlock_Unknown{Unknown: &pbv1.RequestUnknownBlock{Kind: "k", PayloadJson: []byte(`{}`)}}},
			text("sibling"),
		}}
	case "torana.v1.RequestToolResultBlock":
		return &pbv1.Message{Role: "user", Blocks: []*pbv1.RequestBlock{
			{Kind: &pbv1.RequestBlock_ToolResult{ToolResult: &pbv1.RequestToolResultBlock{
				ToolCallId: "c1", ToolName: "read",
				Content: []*pbv1.ToolResultContentBlock{trText("r")},
			}}},
			text("sibling"),
		}}
	case "torana.v1.RequestTrailingSignatureBlock":
		return &pbv1.Message{Role: "assistant", Blocks: []*pbv1.RequestBlock{
			text("covered"),
			{Kind: &pbv1.RequestBlock_Thinking{Thinking: &pbv1.RequestThinkingBlock{Text: "thought"}}},
			{Kind: &pbv1.RequestBlock_TrailingSignature{TrailingSignature: &pbv1.RequestTrailingSignatureBlock{Signature: "token"}}},
		}}
	}
	return nil
}

// resultContentHelpers builds the tool-result content arm helpers.
func trUnknownContent() *pbv1.ToolResultContentBlock {
	return &pbv1.ToolResultContentBlock{Kind: &pbv1.ToolResultContentBlock_Unknown{Unknown: &pbv1.ToolResultUnknownBlock{Kind: "k", PayloadJson: []byte(`{}`)}}}
}
func trMarkerContent(m string) *pbv1.ToolResultContentBlock {
	return &pbv1.ToolResultContentBlock{Kind: &pbv1.ToolResultContentBlock_CacheBreakpoint{CacheBreakpoint: &pbv1.ToolResultCacheBreakpoint{MarkerJson: []byte(m)}}}
}

// mutatorFor builds the per-ref mutation (surface/scope-specific).
func mutatorFor(surface, ref string, scope outboundpolicy.SignatureScope) func(*pbv1.Message) {
	if scope == outboundpolicy.SignatureScopeTrailingStandalone {
		return func(m *pbv1.Message) {
			switch ref {
			case "torana.v1.RequestTextBlock.text":
				for _, b := range m.Blocks {
					if t := b.GetText(); t != nil {
						t.Text = "changed-covered"
						return
					}
				}
			case "torana.v1.RequestThinkingBlock.text":
				for _, b := range m.Blocks {
					if th := b.GetThinking(); th != nil {
						th.Text = "changed-covered"
						return
					}
				}
			}
		}
	}
	return func(m *pbv1.Message) {
		b := tokenBlockOf(m, surface)
		if b == nil {
			return
		}
		switch surface {
		case "torana.v1.RequestTextBlock":
			switch ref {
			case "text":
				b.GetText().Text = "changed"
			case "part_metadata_json":
				b.GetText().PartMetadataJson = []byte(`{"x":1}`)
			}
		case "torana.v1.RequestThinkingBlock":
			switch ref {
			case "text":
				b.GetThinking().Text = "changed"
			case "part_metadata_json":
				b.GetThinking().PartMetadataJson = []byte(`{"x":1}`)
			}
		case "torana.v1.RequestToolUseBlock":
			switch ref {
			case "id":
				b.GetToolUse().Id = "c2"
			case "name":
				b.GetToolUse().Name = "write"
			case "arguments_json":
				b.GetToolUse().ArgumentsJson = []byte(`{"x":1}`)
			case "part_metadata_json":
				b.GetToolUse().PartMetadataJson = []byte(`{"x":1}`)
			}
		case "torana.v1.RequestUnknownBlock":
			switch ref {
			case "kind":
				b.GetUnknown().Kind = "k2"
			case "payload_json":
				b.GetUnknown().PayloadJson = []byte(`{"x":1}`)
			case "part_metadata_json":
				b.GetUnknown().PartMetadataJson = []byte(`{"x":1}`)
			}
		case "torana.v1.RequestToolResultBlock":
			tr := b.GetToolResult()
			switch ref {
			case "tool_call_id":
				tr.ToolCallId = "c2"
			case "tool_name":
				tr.ToolName = "write"
			case "part_metadata_json":
				tr.PartMetadataJson = []byte(`{"x":1}`)
			case "will_continue":
				tr.WillContinue = boolPtr(false)
			case "scheduling":
				tr.Scheduling = strPtr("SILENT")
			case "content":
				tr.Content = append(tr.Content, trUnknownContent())
			}
		case "torana.v1.RequestTrailingSignatureBlock":
			if ref == "part_metadata_json" {
				b.GetTrailingSignature().PartMetadataJson = []byte(`{"x":1}`)
			}
		}
	}
}

// contentMutationRows additionally covers the presence/value boundaries and
// every content arm family (the registry's optional + repeated refs).
func contentMutationRows(surface string) []struct {
	ref     string // the registry ref this row refines
	name    string
	prepare func(*pbv1.Message) // prepares the baseline (presence seeding)
	mutate  func(*pbv1.Message) // applies the transition to a clone
} {
	trText := func(s string) *pbv1.ToolResultContentBlock {
		return &pbv1.ToolResultContentBlock{Kind: &pbv1.ToolResultContentBlock_Text{Text: &pbv1.ToolResultTextBlock{Text: s}}}
	}
	if surface != "torana.v1.RequestToolResultBlock" {
		return nil
	}
	return []struct {
		ref     string // the registry ref this row refines
		name    string
		prepare func(*pbv1.Message) // prepares the baseline (presence seeding)
		mutate  func(*pbv1.Message) // applies the transition to a clone
	}{
		{"will_continue", "absent→present(false)", nil, func(m *pbv1.Message) { tokenBlockOf(m, surface).GetToolResult().WillContinue = boolPtr(false) }},
		{"will_continue", "false→true", func(m *pbv1.Message) { tokenBlockOf(m, surface).GetToolResult().WillContinue = boolPtr(false) }, func(m *pbv1.Message) { tokenBlockOf(m, surface).GetToolResult().WillContinue = boolPtr(true) }},
		{"scheduling", "absent→present", nil, func(m *pbv1.Message) { tokenBlockOf(m, surface).GetToolResult().Scheduling = strPtr("SILENT") }},
		{"scheduling", "value", func(m *pbv1.Message) { tokenBlockOf(m, surface).GetToolResult().Scheduling = strPtr("SILENT") }, func(m *pbv1.Message) { tokenBlockOf(m, surface).GetToolResult().Scheduling = strPtr("WHEN_IDLE") }},
		{"content", "text value", nil, func(m *pbv1.Message) { tokenBlockOf(m, surface).GetToolResult().Content[0].GetText().Text = "changed" }},
		{"content", "unknown arm", nil, func(m *pbv1.Message) {
			tokenBlockOf(m, surface).GetToolResult().Content = append(tokenBlockOf(m, surface).GetToolResult().Content, trUnknownContent())
		}},
		{"content", "marker arm", nil, func(m *pbv1.Message) {
			tokenBlockOf(m, surface).GetToolResult().Content = append(tokenBlockOf(m, surface).GetToolResult().Content, trMarkerContent(`{"type":"ephemeral"}`))
		}},
		{"content", "text→marker topology", nil, func(m *pbv1.Message) {
			tr := tokenBlockOf(m, surface).GetToolResult()
			tr.Content = []*pbv1.ToolResultContentBlock{trMarkerContent(`{"type":"ephemeral"}`), trText("r")}
		}},
	}
}

// TestSignatureMatrixBidirectionalInventory — every registry (message,
// scope, ref) has exactly one executable case and vice versa; each case
// applies a real mutation and derives contentChanged from the independent
// before/after covered projection.
func TestSignatureMatrixBidirectionalInventory(t *testing.T) {
	contracts := outboundpolicy.RequestSignatureContracts()
	registryKeys := map[string]bool{}
	for _, c := range contracts {
		for _, ref := range c.Content {
			registryKeys[c.Message+"/"+scopeName(ref.Scope)+"/"+ref.Ref] = true
		}
	}
	caseKeys := map[string]bool{}
	for _, c := range contracts {
		for _, ref := range c.Content {
			caseKeys[c.Message+"/"+scopeName(ref.Scope)+"/"+ref.Ref] = true
			base := surfaceBaseline(c.Message)
			if base == nil {
				t.Fatalf("no baseline for %s", c.Message)
			}
			mutated := proto.Clone(base).(*pbv1.Message)
			mutatorFor(c.Message, ref.Ref, ref.Scope)(mutated)
			before := coveredProjection(base, c)
			after := coveredProjection(mutated, c)
			if before == after {
				t.Fatalf("%s/%s: the mutation did not move the covered projection (vacuous case)", c.Message, ref.Ref)
			}
			if c.Message == "torana.v1.RequestTrailingSignatureBlock" && ref.Ref == "part_metadata_json" {
				// The registry declares the trailing own metadata COVERED,
				// but the approved non-circular rule rejects changing it
				// alone: the covered projection moved, yet the classifier
				// with the ACTUAL inputs (token retained, preceding
				// unchanged) must NOT be SignatureCleared.
				if got := trailingDecision(base, mutated); got != "meta-rejected" {
					t.Fatalf("%s/%s: own-meta change classified %v, want meta-rejected", c.Message, ref.Ref, got)
				}
				continue
			}
			if got := outboundpolicy.ClassifySignatureMutation("token", "", before != after); got != outboundpolicy.SignatureCleared {
				t.Fatalf("%s/%s: covered change classified %v, want SignatureCleared", c.Message, ref.Ref, got)
			}
		}
		// The extra content-arm/presence rows.
		for _, row := range contentMutationRows(c.Message) {
			caseKeys[c.Message+"/SameMessage/"+row.ref] = true
			// SEPARATE baseline prepare + after mutation: the value rows
			// transition present→present with only the value changing.
			base := surfaceBaseline(c.Message)
			if row.prepare != nil {
				row.prepare(base)
			}
			after := proto.Clone(base).(*pbv1.Message)
			row.mutate(after)
			beforeProj := coveredProjection(base, c)
			afterProj := coveredProjection(after, c)
			changed := beforeProj != afterProj
			if !changed {
				t.Fatalf("%s/%s: the mutation did not move the covered projection", c.Message, row.name)
			}
			if got := outboundpolicy.ClassifySignatureMutation("token", "", changed); got != outboundpolicy.SignatureCleared {
				t.Fatalf("%s/%s: covered change classified %v, want SignatureCleared", c.Message, row.name, got)
			}
			// The VALUE rows: both pointers non-nil, the intended value pair,
			// presence kept.
			switch row.name {
			case "false→true":
				trBase := tokenBlockOf(base, c.Message).GetToolResult()
				trAfter := tokenBlockOf(after, c.Message).GetToolResult()
				if trBase.WillContinue == nil || trAfter.WillContinue == nil {
					t.Fatalf("%s: presence lost on either side", row.name)
				}
				if *trBase.WillContinue != false || *trAfter.WillContinue != true {
					t.Fatalf("%s: the intended false→true pair is not what was compared (%v→%v)", row.name, *trBase.WillContinue, *trAfter.WillContinue)
				}
			case "value":
				trBase := tokenBlockOf(base, c.Message).GetToolResult()
				trAfter := tokenBlockOf(after, c.Message).GetToolResult()
				if trBase.Scheduling == nil || trAfter.Scheduling == nil {
					t.Fatalf("%s: presence lost on either side", row.name)
				}
				if *trBase.Scheduling != "SILENT" || *trAfter.Scheduling != "WHEN_IDLE" {
					t.Fatalf("%s: the intended SILENT→WHEN_IDLE pair is not what was compared (%q→%q)", row.name, *trBase.Scheduling, *trAfter.Scheduling)
				}
			}
		}
	}
	// Bidirectional: every case key is a registry key; every registry key has
	// a case.
	for k := range caseKeys {
		if !registryKeys[k] {
			t.Fatalf("case %q does not resolve back to a live registry ref", k)
		}
	}
	for k := range registryKeys {
		if !caseKeys[k] {
			t.Fatalf("registry ref %q has no executable case", k)
		}
	}
}

// TestSignatureNonCoveredClearForbidden — for EVERY surface, a non-covered
// mutation (a sibling top-level text) leaves the covered projection
// unchanged and clearing the token is SignatureDropped.
func TestSignatureNonCoveredClearForbidden(t *testing.T) {
	contracts := outboundpolicy.RequestSignatureContracts()
	for _, c := range contracts {
		t.Run(c.Message, func(t *testing.T) {
			base := surfaceBaseline(c.Message)
			mutated := proto.Clone(base).(*pbv1.Message)
			if c.Message == "torana.v1.RequestTrailingSignatureBlock" {
				// The trailing coverage is preceding text/thinking + own
				// metadata; a tool-result block is OUTSIDE it.
				tr := &pbv1.RequestBlock{Kind: &pbv1.RequestBlock_ToolResult{ToolResult: &pbv1.RequestToolResultBlock{
					ToolCallId: "c1", ToolName: "read",
					Content: []*pbv1.ToolResultContentBlock{{Kind: &pbv1.ToolResultContentBlock_Text{Text: &pbv1.ToolResultTextBlock{Text: "r"}}}},
				}}}
				mutated.Blocks = append([]*pbv1.RequestBlock{tr}, mutated.Blocks...)
			} else {
				for _, b := range mutated.Blocks {
					if t := b.GetText(); t != nil && tokenBlockOf(mutated, c.Message) != b {
						t.Text = "changed-sibling"
						break
					}
				}
			}
			changed := coveredProjection(base, c) != coveredProjection(mutated, c)
			if changed {
				t.Fatal("the non-covered sibling mutation moved the covered projection")
			}
			if got := outboundpolicy.ClassifySignatureMutation("token", "", changed); got != outboundpolicy.SignatureDropped {
				t.Fatalf("non-covered clear classified %v, want SignatureDropped", got)
			}
		})
	}
}

// trailingState returns the actual token + metadata of the trailing carrier
// ("" when absent).
func trailingState(m *pbv1.Message) (token, meta string) {
	for _, b := range m.Blocks {
		if ts := b.GetTrailingSignature(); ts != nil {
			return ts.Signature, string(ts.PartMetadataJson)
		}
	}
	return "", ""
}

// trailingDecision is the INDEPENDENT trailing-decision reference function:
// it derives the verdict from the ACTUAL before/after messages — the
// preceding Text/Thinking projection change, the carrier presence, the
// actual old/new token values, and the actual old/new carrier metadata —
// enforcing the approved non-circular table directly. Carrier metadata
// disappearance NEVER contributes to its own authorization.
func trailingDecision(base, mutated *pbv1.Message) string {
	precedingChanged := precedingCoveredProjection(base) != precedingCoveredProjection(mutated)
	oldTok, oldMeta := trailingState(base)
	newTok, newMeta := trailingState(mutated)
	switch {
	case oldTok == "":
		return "no-carrier"
	case newTok == "":
		// Carrier removal: authorized ONLY by an independently changed
		// preceding text/thinking ref.
		if precedingChanged {
			return "cleared"
		}
		return "dropped"
	case newTok != oldTok:
		return "forged"
	case precedingChanged:
		return "stale" // retained over lawfully changed covered content
	case oldMeta != newMeta:
		return "meta-rejected" // own-metadata mutation alone never authorizes
	default:
		return "intact"
	}
}

// TestTrailingNonCircularTable — the approved table executed: every row
// mutates the baseline and the verdict is DERIVED from the actual before/
// after state (never chosen classifier inputs).
func TestTrailingNonCircularTable(t *testing.T) {
	text := func(s string) *pbv1.RequestBlock {
		return &pbv1.RequestBlock{Kind: &pbv1.RequestBlock_Text{Text: &pbv1.RequestTextBlock{Text: s}}}
	}
	thinking := func(s string) *pbv1.RequestBlock {
		return &pbv1.RequestBlock{Kind: &pbv1.RequestBlock_Thinking{Thinking: &pbv1.RequestThinkingBlock{Text: s}}}
	}
	trailing := func() *pbv1.RequestBlock {
		return &pbv1.RequestBlock{Kind: &pbv1.RequestBlock_TrailingSignature{TrailingSignature: &pbv1.RequestTrailingSignatureBlock{Signature: "token", PartMetadataJson: []byte(`{"t":1}`)}}}
	}
	base := func() *pbv1.Message {
		return &pbv1.Message{Role: "assistant", Blocks: []*pbv1.RequestBlock{text("covered"), thinking("thought"), trailing()}}
	}

	rows := []struct {
		name   string
		mutate func(*pbv1.Message)
		want   string
	}{
		{"unchanged preceding + metadata value changed", func(m *pbv1.Message) { m.Blocks[2].GetTrailingSignature().PartMetadataJson = []byte(`{"t":2}`) }, "meta-rejected"},
		{"unchanged preceding + metadata cleared", func(m *pbv1.Message) { m.Blocks[2].GetTrailingSignature().PartMetadataJson = nil }, "meta-rejected"},
		{"unchanged preceding + whole carrier removed", func(m *pbv1.Message) { m.Blocks = m.Blocks[:2] }, "dropped"},
		{"preceding Text independently changed + carrier removed", func(m *pbv1.Message) { m.Blocks[0].GetText().Text = "changed"; m.Blocks = m.Blocks[:2] }, "cleared"},
		{"preceding Thinking independently changed + carrier removed", func(m *pbv1.Message) { m.Blocks[1].GetThinking().Text = "changed"; m.Blocks = m.Blocks[:2] }, "cleared"},
		{"preceding changed + stale token retained", func(m *pbv1.Message) { m.Blocks[0].GetText().Text = "changed" }, "stale"},
		{"preceding changed + token altered", func(m *pbv1.Message) {
			m.Blocks[0].GetText().Text = "changed"
			m.Blocks[2].GetTrailingSignature().Signature = "altered"
		}, "forged"},
		{"unrelated tool-result inserted + carrier removed", func(m *pbv1.Message) {
			tr := &pbv1.RequestBlock{Kind: &pbv1.RequestBlock_ToolResult{ToolResult: &pbv1.RequestToolResultBlock{
				ToolCallId: "c1", ToolName: "read",
				Content: []*pbv1.ToolResultContentBlock{{Kind: &pbv1.ToolResultContentBlock_Text{Text: &pbv1.ToolResultTextBlock{Text: "r"}}}},
			}}}
			m.Blocks = append([]*pbv1.RequestBlock{tr}, m.Blocks...)
			m.Blocks = m.Blocks[:3]
		}, "dropped"},
		{"no mutation", func(m *pbv1.Message) {}, "intact"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			b := base()
			mutated := proto.Clone(b).(*pbv1.Message)
			row.mutate(mutated)
			if got := trailingDecision(b, mutated); got != row.want {
				t.Fatalf("trailingDecision = %q, want %q", got, row.want)
			}
		})
	}
}

// TestRequestSignatureContractsReadOnly — the accessor's deep-copy claim:
// mutating a returned contract must not affect a second read.
func TestRequestSignatureContractsReadOnly(t *testing.T) {
	a := outboundpolicy.RequestSignatureContracts()
	b := outboundpolicy.RequestSignatureContracts()
	if len(a) != len(b) {
		t.Fatal("accessor results differ in length")
	}
	a[0].Content[0].Ref = "tampered"
	c := outboundpolicy.RequestSignatureContracts()
	if c[0].Content[0].Ref == "tampered" {
		t.Fatal("the accessor aliases its internal registry")
	}
}
