package plugin_sdk

// Request message-body helpers: the sanctioned mutation surface for plugins.
//
// The request message body is the ordered RequestBlock sequence (pb/v2
// Message.blocks) — the SOLE authority for every content fact. Plugins do
// not parse provider-shaped raw arrays and do not manipulate oneof internals
// for their ordinary tasks; these helpers read and mutate blocks with
// explicit errors, out-of-range/wrong-kind rejection, and the
// signature-clearing rules they own:
//
//   - changing the text of a text/thinking block clears that block's
//     provenance-governed signature token;
//   - changing ANY text/thinking content clears a trailing-signature block
//     whose TrailingStandalone scope covers it (the final standalone token
//     binds the preceding closed text/thinking content);
//   - replacing a tool-use block's id/name/arguments clears the call-bound
//     signature token (its covered content changed);
//   - cache-breakpoint mutations never touch signature tokens.
//
// All reads return COPIED views (never internal pointers into the message).
// All writes mutate the message in place and return an explicit error; a
// write is never "refused" by returning nil.

import (
	"bytes"
	"encoding/json"
	"fmt"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
	"github.com/torana-edge/torana-plugin-sdk/pb/v2/jsontext"
)

// TextSegment is a copied view of one text block's text.
type TextSegment struct {
	// Block is the block index inside Message.blocks.
	Block int
	Text  string
}

// TextSegments returns the text blocks' texts in wire order (copied views).
// A nil message or nil blocks list yields nil.
func TextSegments(msg *pbv2.Message) []TextSegment {
	if msg == nil {
		return nil
	}
	var out []TextSegment
	for i, b := range msg.Blocks {
		if t := b.GetText(); t != nil {
			out = append(out, TextSegment{Block: i, Text: t.Text})
		}
	}
	return out
}

// Text returns the concatenation of every text block's text in wire order.
// Documented as a plain concatenation: no separators, no semantic inference.
func Text(msg *pbv2.Message) string {
	var out bytes.Buffer
	for _, seg := range TextSegments(msg) {
		out.WriteString(seg.Text)
	}
	return out.String()
}

// SetTextAt replaces the text of the text block at block with text. A block
// that is not a text block, or an out-of-range index, is an error. The
// block's signature token is cleared when the text actually changes (the
// token is provenance-governed), and a trailing-signature block whose
// covered content changed is cleared too.
func SetTextAt(msg *pbv2.Message, block int, text string) error {
	if msg == nil {
		return fmt.Errorf("set text: nil message")
	}
	if block < 0 || block >= len(msg.Blocks) {
		return fmt.Errorf("set text: block %d out of range (0..%d)", block, len(msg.Blocks)-1)
	}
	if msg.Blocks[block] == nil {
		return fmt.Errorf("set text: block %d is nil", block)
	}
	tb, ok := msg.Blocks[block].Kind.(*pbv2.RequestBlock_Text)
	if !ok || tb.Text == nil {
		return fmt.Errorf("set text: block %d is not a text block", block)
	}
	if tb.Text.Text != text {
		tb.Text.Text = text
		tb.Text.Signature = "" // stale: changed covered content
		clearTrailingSignature(msg)
	}
	return nil
}

// ReplaceAllText collapses the message's text to exactly text:
//
//   - the FIRST text block keeps its position and receives text;
//   - every other text block is REMOVED from the body;
//   - a message with no text block gets one APPENDED at the end (after any
//     final trailing-signature block is removed — appending content after
//     the token's covered scope makes it stale and breaks finality);
//   - a semantic NO-OP (one text block already equal to text, nothing to
//     collapse) leaves every provenance token byte-for-byte untouched;
//   - a real change clears the signature token on every touched text block
//     and removes a trailing-signature block whose scope was invalidated.
func ReplaceAllText(msg *pbv2.Message, text string) error {
	if msg == nil {
		return fmt.Errorf("replace all text: nil message")
	}
	first := -1
	changed := false
	for i, b := range msg.Blocks {
		if tb := b.GetText(); tb != nil {
			if first < 0 {
				first = i
				if tb.Text != text {
					tb.Text = text
					tb.Signature = ""
					changed = true
				}
			} else {
				// A further text block is always a collapse -> a change.
				changed = true
			}
		}
	}
	if first < 0 {
		// A message with no text block gets one APPENDED — even for the
		// empty string: an explicit empty text arm is first-class and
		// distinct from absence (ordered topology is semantic).
		// A trailing-signature block cannot stay final if content is
		// appended after it; the appended text is also outside the provider
		// token's covered scope, so the token is stale and removed first.
		clearTrailingSignature(msg)
		msg.Blocks = append(msg.Blocks, &pbv2.RequestBlock{
			Kind: &pbv2.RequestBlock_Text{Text: &pbv2.RequestTextBlock{Text: text}},
		})
		return nil
	}
	if !changed {
		// Semantic no-op: one text block already equal to text.
		return nil
	}
	// Drop every text block after the first (collapse semantics).
	kept := msg.Blocks[:0]
	seen := 0
	for _, b := range msg.Blocks {
		if tb := b.GetText(); tb != nil {
			seen++
			if seen > 1 {
				continue
			}
		}
		kept = append(kept, b)
	}
	msg.Blocks = kept
	clearTrailingSignature(msg)
	return nil
}

// checkInsertBeforeTrailing refuses an insertion position after a final
// trailing-signature block: the token is assistant-only and FINAL, so
// nothing may be inserted after it.
func checkInsertBeforeTrailing(msg *pbv2.Message, at int) error {
	if len(msg.Blocks) == 0 {
		return nil
	}
	last := msg.Blocks[len(msg.Blocks)-1]
	if last == nil || last.GetTrailingSignature() == nil {
		return nil
	}
	if at > len(msg.Blocks)-1 {
		return fmt.Errorf("position %d is after the final trailing-signature block", at)
	}
	return nil
}

// clearTrailingSignature removes a final trailing-signature block whose
// TrailingStandalone scope covered the changed content.
func clearTrailingSignature(msg *pbv2.Message) {
	if len(msg.Blocks) == 0 {
		return
	}
	last := msg.Blocks[len(msg.Blocks)-1]
	if ts := last.GetTrailingSignature(); ts != nil {
		msg.Blocks = msg.Blocks[:len(msg.Blocks)-1]
	}
}

// ToolResultScalarText reports whether v — a ToolResultView produced by
// ToolResults — is SCALAR-COMPATIBLE and returns its scalar text.
//
// A scalar-compatible tool result has EXACTLY ONE text arm (an EXPLICIT
// EMPTY text arm is compatible — its position is real), ZERO unknown/
// provider arms, and any number of cache-marker arms. Zero text arms
// (marker-only), multiple text arms, or any unknown arm are NOT compatible:
// plain concatenation is not injective (["ab","c"] vs ["a","bc"] collide),
// and the flat scalar era had no such shapes. The caller must decline
// incompatible results UNCHANGED.
//
// The view must come from ToolResults: a manually fabricated/ambiguous view
// cannot enable mutation of an incompatible real block, because
// ReplaceToolResultText re-validates against the REAL block arms and is the
// total, self-validating, atomic boundary.
func ToolResultScalarText(v ToolResultView) (string, bool) {
	text := ""
	textArms := 0
	for _, c := range v.Content {
		if c.UnknownKind != "" {
			return "", false
		}
		if c.CacheMarker != nil {
			continue
		}
		textArms++
		text = c.Text
	}
	if textArms != 1 {
		return "", false
	}
	return text, true
}

// ReplaceToolResultText replaces the text of the SINGLE text arm of the
// tool-result block at block with text. It is the TOTAL, self-validating,
// ATOMIC boundary for tool-result text mutation:
//
//   - every error (nil message, out-of-range block, non-tool-result block,
//     zero/multiple text arms, any unknown arm) leaves the message
//     byte/structurally UNCHANGED;
//   - on success the EXACT nested arm count/order is retained and every
//     non-text arm's bytes (cache markers) are untouched — only the
//     designated text arm's value changes, so the provider cached-prefix
//     boundary does not move;
//   - a byte-identical text value is a structural NO-OP (changed=false):
//     every provenance token — the tool-result signature,
//     part_metadata_json, and a final trailing-signature block — is
//     preserved byte-for-byte;
//   - a REAL text change preserves part_metadata_json but clears every
//     provenance token whose covered scope changed: the tool-result
//     signature (its scope is the result content) and a final
//     trailing-signature block (its covered scope is the message body).
func ReplaceToolResultText(msg *pbv2.Message, block int, text string) (bool, error) {
	if msg == nil {
		return false, fmt.Errorf("replace tool result text: nil message")
	}
	if block < 0 || block >= len(msg.Blocks) {
		return false, fmt.Errorf("replace tool result text: block %d out of range (0..%d)", block, len(msg.Blocks)-1)
	}
	b := msg.Blocks[block]
	if b == nil || b.GetToolResult() == nil {
		return false, fmt.Errorf("replace tool result text: block %d is not a tool-result block", block)
	}
	tr := b.GetToolResult()
	// TOTAL self-validation against the REAL nested arms: every malformed
	// element (nil element, arm-less element, typed-nil oneof arm,
	// malformed/incorrect-shape/duplicate-key cache marker) fails closed
	// BEFORE any mutation — the SDK-owned strict-object rules (jsontext +
	// shape discrimination) are reused, not re-derived.
	textIdx := -1
	for i, c := range tr.Content {
		if c == nil {
			return false, fmt.Errorf("replace tool result text: block %d content[%d] is nil", block, i)
		}
		switch k := c.Kind.(type) {
		case *pbv2.ToolResultContentBlock_Text:
			if k.Text == nil {
				return false, fmt.Errorf("replace tool result text: block %d content[%d] is a typed-nil text arm", block, i)
			}
			if textIdx >= 0 {
				return false, fmt.Errorf("replace tool result text: block %d has multiple text arms", block)
			}
			textIdx = i
		case *pbv2.ToolResultContentBlock_Unknown:
			if k.Unknown == nil {
				return false, fmt.Errorf("replace tool result text: block %d content[%d] is a typed-nil unknown arm", block, i)
			}
			return false, fmt.Errorf("replace tool result text: block %d has an unknown content arm", block)
		case *pbv2.ToolResultContentBlock_CacheBreakpoint:
			if k.CacheBreakpoint == nil {
				return false, fmt.Errorf("replace tool result text: block %d content[%d] is a typed-nil cache arm", block, i)
			}
			if err := validJSONObject(k.CacheBreakpoint.MarkerJson); err != nil {
				return false, fmt.Errorf("replace tool result text: block %d content[%d] has a malformed cache marker: %w", block, i, err)
			}
		default:
			return false, fmt.Errorf("replace tool result text: block %d content[%d] is arm-less", block, i)
		}
	}
	if textIdx < 0 {
		return false, fmt.Errorf("replace tool result text: block %d has no text arm", block)
	}
	tb := tr.Content[textIdx].GetText()
	if tb.Text == text {
		return false, nil // structural no-op: every provenance token preserved
	}
	tb.Text = text
	// The trailing-signature carrier covers ONLY preceding text/thinking and
	// its own metadata — NOT tool-result content — so it is PRESERVED
	// byte-for-byte here; only the containing result signature is cleared
	// (its covered content changed).
	tr.Signature = ""
	return true, nil
}

// ToolCallView is a copied view of one tool-use block.
type ToolCallView struct {
	// Block is the block index inside Message.blocks.
	Block     int
	Id        string
	Name      string
	Arguments []byte // exact authoritative raw bytes (verbatim)
	Signature string
}

// ToolCalls returns the tool-use blocks' views in wire order.
func ToolCalls(msg *pbv2.Message) []ToolCallView {
	if msg == nil {
		return nil
	}
	var out []ToolCallView
	for i, b := range msg.Blocks {
		if tu := b.GetToolUse(); tu != nil {
			out = append(out, ToolCallView{
				Block:     i,
				Id:        tu.Id,
				Name:      tu.Name,
				Arguments: append([]byte(nil), tu.ArgumentsJson...),
				Signature: tu.Signature,
			})
		}
	}
	return out
}

// ToolResultView is a copied view of one tool-result block.
type ToolResultView struct {
	// Block is the block index inside Message.blocks.
	Block      int
	ToolCallId string
	ToolName   string
	Content    []ToolResultContentView // ordered nested content, copied
}

// ToolResultContentView is a copied view of one nested tool-result content
// element.
type ToolResultContentView struct {
	Text        string // when the element is a text arm
	UnknownKind string // when the element is an unknown/provider arm
	UnknownData []byte // exact authoritative raw payload bytes
	CacheMarker []byte // when the element is a nested cache breakpoint
}

// ToolResults returns the tool-result blocks' views in wire order.
func ToolResults(msg *pbv2.Message) []ToolResultView {
	if msg == nil {
		return nil
	}
	var out []ToolResultView
	for i, b := range msg.Blocks {
		if tr := b.GetToolResult(); tr != nil {
			v := ToolResultView{Block: i, ToolCallId: tr.ToolCallId, ToolName: tr.ToolName}
			for _, c := range tr.Content {
				v.Content = append(v.Content, toolResultContentView(c))
			}
			out = append(out, v)
		}
	}
	return out
}

func toolResultContentView(c *pbv2.ToolResultContentBlock) ToolResultContentView {
	var v ToolResultContentView
	switch k := c.Kind.(type) {
	case *pbv2.ToolResultContentBlock_Text:
		if k.Text != nil {
			v.Text = k.Text.Text
		}
	case *pbv2.ToolResultContentBlock_Unknown:
		if k.Unknown != nil {
			v.UnknownKind = k.Unknown.Kind
			v.UnknownData = append([]byte(nil), k.Unknown.PayloadJson...)
		}
	case *pbv2.ToolResultContentBlock_CacheBreakpoint:
		if k.CacheBreakpoint != nil {
			v.CacheMarker = append([]byte(nil), k.CacheBreakpoint.MarkerJson...)
		}
	}
	return v
}

// ToolCallInput is a copied-value tool-use description for AddToolCall and
// ReplaceToolCall.
type ToolCallInput struct {
	Id        string
	Name      string
	Arguments []byte // exact authoritative raw bytes; must be a JSON object
}

// validateCallInput checks the structural leaf contract of a tool call.
func validateCallInput(call ToolCallInput) error {
	if call.Id == "" {
		return fmt.Errorf("tool call id must be non-empty")
	}
	if call.Name == "" {
		return fmt.Errorf("tool call name must be non-empty")
	}
	if err := validJSONObject(call.Arguments); err != nil {
		return fmt.Errorf("tool call arguments: %w", err)
	}
	return nil
}

// AddToolCall inserts a tool-use block at position at (0..len(blocks)).
// Out-of-range positions and insertion AFTER a final trailing-signature
// block are errors (the signature must stay final).
func AddToolCall(msg *pbv2.Message, at int, call ToolCallInput) error {
	if msg == nil {
		return fmt.Errorf("add tool call: nil message")
	}
	if at < 0 || at > len(msg.Blocks) {
		return fmt.Errorf("add tool call: position %d out of range (0..%d)", at, len(msg.Blocks))
	}
	if err := checkInsertBeforeTrailing(msg, at); err != nil {
		return fmt.Errorf("add tool call: %w", err)
	}
	if err := validateCallInput(call); err != nil {
		return err
	}
	block := &pbv2.RequestBlock{
		Kind: &pbv2.RequestBlock_ToolUse{ToolUse: &pbv2.RequestToolUseBlock{
			Id:            call.Id,
			Name:          call.Name,
			ArgumentsJson: append([]byte(nil), call.Arguments...),
		}},
	}
	msg.Blocks = append(msg.Blocks, nil)
	copy(msg.Blocks[at+1:], msg.Blocks[at:])
	msg.Blocks[at] = block
	return nil
}

// ReplaceToolCall replaces the tool-use block at block with call (identity,
// arguments). The block must be a tool-use block; its call-bound signature
// token is cleared (its covered content changed).
func ReplaceToolCall(msg *pbv2.Message, block int, call ToolCallInput) error {
	if msg == nil {
		return fmt.Errorf("replace tool call: nil message")
	}
	if block < 0 || block >= len(msg.Blocks) {
		return fmt.Errorf("replace tool call: block %d out of range (0..%d)", block, len(msg.Blocks)-1)
	}
	if msg.Blocks[block] == nil {
		return fmt.Errorf("replace tool call: block %d is nil", block)
	}
	tu, ok := msg.Blocks[block].Kind.(*pbv2.RequestBlock_ToolUse)
	if !ok || tu.ToolUse == nil {
		return fmt.Errorf("replace tool call: block %d is not a tool-use block", block)
	}
	if err := validateCallInput(call); err != nil {
		return err
	}
	// Exact-change semantics: an identical replacement (id, name, and the
	// exact argument bytes) is a no-op that PRESERVES the call-bound
	// provenance token; only a real change clears it (stale).
	if tu.ToolUse.Id == call.Id && tu.ToolUse.Name == call.Name &&
		bytes.Equal(tu.ToolUse.ArgumentsJson, call.Arguments) {
		return nil
	}
	tu.ToolUse.Id = call.Id
	tu.ToolUse.Name = call.Name
	tu.ToolUse.ArgumentsJson = append([]byte(nil), call.Arguments...)
	tu.ToolUse.Signature = "" // stale: changed covered content
	return nil
}

// CacheBreakpointView is a copied view of one cache-breakpoint block.
type CacheBreakpointView struct {
	// Block is the block index inside Message.blocks.
	Block  int
	Marker []byte // exact authoritative raw marker bytes (verbatim)
}

// CacheBreakpoints returns the cache-breakpoint blocks' views in wire
// order. A breakpoint CLOSES the cached prefix at its position; multiple
// markers per message are naturally representable.
func CacheBreakpoints(msg *pbv2.Message) []CacheBreakpointView {
	if msg == nil {
		return nil
	}
	var out []CacheBreakpointView
	for i, b := range msg.Blocks {
		if cb := b.GetCacheBreakpoint(); cb != nil {
			out = append(out, CacheBreakpointView{Block: i, Marker: append([]byte(nil), cb.MarkerJson...)})
		}
	}
	return out
}

// CacheControl returns the marker of the LAST cache-breakpoint block of the
// message — the prefix-closing boundary — or nil when the message carries no
// breakpoint. The marker bytes are decoded for the caller's convenience; the
// raw bytes remain available via CacheBreakpoints.
func CacheControl(msg *pbv2.Message) map[string]any {
	if msg == nil {
		return nil
	}
	var last []byte
	for i, b := range msg.Blocks {
		if cb := b.GetCacheBreakpoint(); cb != nil {
			last = cb.MarkerJson
			_ = i
		}
	}
	if len(last) == 0 {
		return nil
	}
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(last))
	dec.UseNumber() // contract-valid markers like {"n":1e999} must not overflow float64
	if err := dec.Decode(&m); err != nil {
		return nil
	}
	return m
}

// SetCacheBreakpoint replaces the marker of the cache-breakpoint block at
// block. The block must be a cache-breakpoint block and marker a valid JSON
// object (raw bytes preserved verbatim).
func SetCacheBreakpoint(msg *pbv2.Message, block int, marker []byte) error {
	if msg == nil {
		return fmt.Errorf("set cache breakpoint: nil message")
	}
	if block < 0 || block >= len(msg.Blocks) {
		return fmt.Errorf("set cache breakpoint: block %d out of range (0..%d)", block, len(msg.Blocks)-1)
	}
	if msg.Blocks[block] == nil {
		return fmt.Errorf("set cache breakpoint: block %d is nil", block)
	}
	cb, ok := msg.Blocks[block].Kind.(*pbv2.RequestBlock_CacheBreakpoint)
	if !ok || cb.CacheBreakpoint == nil {
		return fmt.Errorf("set cache breakpoint: block %d is not a cache-breakpoint block", block)
	}
	if err := validJSONObject(marker); err != nil {
		return fmt.Errorf("set cache breakpoint: %w", err)
	}
	cb.CacheBreakpoint.MarkerJson = append([]byte(nil), marker...)
	return nil
}

// AddCacheBreakpoint inserts a cache-breakpoint block at position at
// (0..len(blocks)), closing the cached prefix at that position. Insertion
// AFTER a final trailing-signature block is an error.
func AddCacheBreakpoint(msg *pbv2.Message, at int, marker []byte) error {
	if msg == nil {
		return fmt.Errorf("add cache breakpoint: nil message")
	}
	if at < 0 || at > len(msg.Blocks) {
		return fmt.Errorf("add cache breakpoint: position %d out of range (0..%d)", at, len(msg.Blocks))
	}
	if err := checkInsertBeforeTrailing(msg, at); err != nil {
		return fmt.Errorf("add cache breakpoint: %w", err)
	}
	if err := validJSONObject(marker); err != nil {
		return fmt.Errorf("add cache breakpoint: %w", err)
	}
	block := &pbv2.RequestBlock{
		Kind: &pbv2.RequestBlock_CacheBreakpoint{CacheBreakpoint: &pbv2.RequestCacheBreakpoint{
			MarkerJson: append([]byte(nil), marker...),
		}},
	}
	msg.Blocks = append(msg.Blocks, nil)
	copy(msg.Blocks[at+1:], msg.Blocks[at:])
	msg.Blocks[at] = block
	return nil
}

// DeleteCacheBreakpoint removes the cache-breakpoint block at block.
func DeleteCacheBreakpoint(msg *pbv2.Message, block int) error {
	if msg == nil {
		return fmt.Errorf("delete cache breakpoint: nil message")
	}
	if block < 0 || block >= len(msg.Blocks) {
		return fmt.Errorf("delete cache breakpoint: block %d out of range (0..%d)", block, len(msg.Blocks)-1)
	}
	if msg.Blocks[block] == nil {
		return fmt.Errorf("delete cache breakpoint: block %d is nil", block)
	}
	if _, ok := msg.Blocks[block].Kind.(*pbv2.RequestBlock_CacheBreakpoint); !ok {
		return fmt.Errorf("delete cache breakpoint: block %d is not a cache-breakpoint block", block)
	}
	msg.Blocks = append(msg.Blocks[:block], msg.Blocks[block+1:]...)
	return nil
}

// validJSONObject reports whether raw is a strict single JSON object (the
// shared jsontext rules; shape discrimination via a UseNumber decode so
// large numerics never fail a shape check).
func validJSONObject(raw []byte) error {
	if len(raw) == 0 {
		return fmt.Errorf("must be a non-empty JSON object")
	}
	if err := jsontext.Validate(raw); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return err
	}
	if _, ok := v.(map[string]any); !ok {
		return fmt.Errorf("must be a JSON object")
	}
	return nil
}
