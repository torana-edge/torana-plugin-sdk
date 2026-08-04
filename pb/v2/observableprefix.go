package v2

// The shared observable request-prefix projection and the exact-carrier
// cache-breakpoint replacement (cache-tier reconciliation REV 2, seam
// approved). ONE SDK-owned implementation is consumed by both the Edge host
// (CachePrefixKeyTopology: this projection + host-only topology framing) and
// the official cache_tier_selector plugin (its decision-key domain) — no
// second request-prefix algorithm exists to drift.
//
// The marker model is the ordered-prefix model: the cache marker has three
// carriers in serialization order (tools first, then messages: outer blocks,
// then nested tool-result content):
//
//   - ToolDef.cache_control_json — the prefix ends in the tools section and
//     NO message is part of it;
//   - RequestBlock.cache_breakpoint (outer) — messages truncated inclusive at
//     the marker block;
//   - ToolResultContentBlock.cache_breakpoint (nested) — the marker block's
//     tool-result content cut at the exact nested position.
//
// The LAST carrier in that order closes the prefix; no marker means the whole
// request is the prefix (automatic prefix caching).

import (
	"bytes"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
)

// ErrNoCacheBreakpoint is returned by ReplaceLastCacheBreakpoint when the
// request carries no cache-breakpoint carrier. It is a stable typed sentinel:
// the official plugin treats it as pass (decline without state or mutation).
var ErrNoCacheBreakpoint = errors.New("request carries no cache breakpoint carrier")

// markerCarrierKind distinguishes the three cache-marker carriers.
type markerCarrierKind int

const (
	markerCarrierTool markerCarrierKind = iota + 1
	markerCarrierTopLevel
	markerCarrierNested
)

// cacheMarkerPos is one marker's position in the ordered prefix.
type cacheMarkerPos struct {
	kind   markerCarrierKind
	tool   int // markerCarrierTool
	msg    int // markerCarrierTopLevel / markerCarrierNested
	block  int // markerCarrierTopLevel / markerCarrierNested
	nested int // markerCarrierNested; -1 otherwise
}

// lastCacheMarker returns the LAST marker in serialization order (tools
// first, then messages: outer blocks, then nested tool-result content), or
// nil when the request carries no marker.
func lastCacheMarker(req *ChatRequest) *cacheMarkerPos {
	var last *cacheMarkerPos
	for i, t := range req.Tools {
		if len(t.CacheControlJson) > 0 {
			last = &cacheMarkerPos{kind: markerCarrierTool, tool: i}
		}
	}
	for i, m := range req.Messages {
		for j, b := range m.Blocks {
			if b.GetCacheBreakpoint() != nil {
				last = &cacheMarkerPos{kind: markerCarrierTopLevel, msg: i, block: j, nested: -1}
			}
			if tr := b.GetToolResult(); tr != nil {
				for k, c := range tr.Content {
					if c.GetCacheBreakpoint() != nil {
						last = &cacheMarkerPos{kind: markerCarrierNested, msg: i, block: j, nested: k}
					}
				}
			}
		}
	}
	return last
}

// truncateMessage returns the message truncated at the marker position,
// inclusive: blocks[0..lastBlock], with the marker block's tool-result
// content cut at nested[0..lastNested] when the marker is nested. PURE PB
// construction (proto.Clone + fresh slices) — the input is never mutated or
// aliased.
func truncateMessage(m *Message, lastBlock, lastNested int) *Message {
	out := &Message{Role: m.Role}
	for j, b := range m.Blocks {
		if j > lastBlock {
			break
		}
		if j == lastBlock && lastNested >= 0 && b.GetToolResult() != nil {
			nb := proto.Clone(b).(*RequestBlock)
			tr := nb.GetToolResult()
			tr.Content = tr.Content[:lastNested+1]
			out.Blocks = append(out.Blocks, nb)
			continue
		}
		out.Blocks = append(out.Blocks, b)
	}
	return out
}

// RequestObservablePrefix returns the deterministic protobuf serialization of
// the provider-visible cached prefix:
//
//   - the owning gate: ValidateReplacement runs on the request FIRST, so an
//     out-of-domain request is an ERROR (never a partial projection — the
//     fail-closed parity invariant);
//   - the prefix is closed at the LAST marker in the tools-first/outer/nested
//     serialization order (tool-only marker ⇒ the tools section through the
//     marker tool and NO messages; outer/nested marker ⇒ messages through the
//     marker message truncated at the exact block/nested position); no marker
//     ⇒ the whole request;
//   - the projection carries model, tools (through the tool cut-off), the
//     truncated messages, provider_extensions_json, safety_settings_json, and
//     the generation params (max_tokens/temperature/top_p/stop_sequences,
//     presence-aware). It clears ONLY stream and torana_meta_json — every
//     other top-level field folds (additive fields are picked up by the
//     bidirectional descriptor inventory, which fails closed).
//
// The returned bytes are a fresh deterministic serialization of a fresh
// proto.Clone: the input is never mutated and never aliased by the result.
func RequestObservablePrefix(req *ChatRequest) ([]byte, error) {
	if err := req.ValidateReplacement(); err != nil {
		return nil, err
	}
	out := proto.Clone(req).(*ChatRequest)
	// The ONLY cleared fields: stream and host metadata are not part of the
	// provider's cached prefix.
	out.Stream = false
	out.ToranaMetaJson = nil

	last := lastCacheMarker(out)
	if last == nil {
		// No marker: the whole request is the prefix.
		return proto.MarshalOptions{Deterministic: true}.Marshal(out)
	}
	if last.kind == markerCarrierTool {
		out.Tools = out.Tools[:last.tool+1]
		out.Messages = nil
		return proto.MarshalOptions{Deterministic: true}.Marshal(out)
	}
	out.Messages = out.Messages[:last.msg+1]
	out.Messages[last.msg] = truncateMessage(out.Messages[last.msg], last.block, last.nested)
	return proto.MarshalOptions{Deterministic: true}.Marshal(out)
}

// ReplaceLastCacheBreakpoint replaces the marker on the LAST EXISTING carrier
// in the shared tools-first/outer/nested order with a defensive copy of
// marker (a strict JSON object). It never inserts a marker and never guesses
// a position:
//
//   - no carrier ⇒ ErrNoCacheBreakpoint and the request is unchanged (the
//     official plugin maps the sentinel to pass);
//   - ATOMIC: the request and the marker are validated and the carrier is
//     located BEFORE any mutation; on every error the request is byte- and
//     structurally unchanged;
//   - changed=false when the existing marker bytes are byte-identical to
//     marker (a lexically different but semantically identical marker is a
//     CHANGE — conservative: the provider observes the new bytes);
//   - on success exactly that carrier is mutated with a defensive byte copy,
//     so later caller mutation of marker cannot alter the request.
func ReplaceLastCacheBreakpoint(req *ChatRequest, marker []byte) (changed bool, err error) {
	if err := req.ValidateReplacement(); err != nil {
		return false, err
	}
	if err := validateJSONField(marker, "cache breakpoint marker", jsonFieldRule{shape: "object", required: true}); err != nil {
		return false, err
	}
	last := lastCacheMarker(req)
	if last == nil {
		return false, ErrNoCacheBreakpoint
	}
	// Determine the outcome before mutating.
	switch last.kind {
	case markerCarrierTool:
		existing := req.Tools[last.tool].CacheControlJson
		if bytes.Equal(existing, marker) {
			return false, nil
		}
		req.Tools[last.tool].CacheControlJson = append([]byte(nil), marker...)
		return true, nil
	case markerCarrierTopLevel:
		cb := req.Messages[last.msg].Blocks[last.block].GetCacheBreakpoint()
		if bytes.Equal(cb.MarkerJson, marker) {
			return false, nil
		}
		cb.MarkerJson = append([]byte(nil), marker...)
		return true, nil
	case markerCarrierNested:
		cb := req.Messages[last.msg].Blocks[last.block].GetToolResult().Content[last.nested].GetCacheBreakpoint()
		if bytes.Equal(cb.MarkerJson, marker) {
			return false, nil
		}
		cb.MarkerJson = append([]byte(nil), marker...)
		return true, nil
	}
	return false, fmt.Errorf("cache breakpoint carrier %d is not a marker carrier", last.kind)
}
