package plugin_sdk

// RequestBlocksFingerprint — the canonical request message-body fingerprint.
//
// One implementation, shared by the host's CachePrefixKey (Edge frames
// host-only provider-envelope topology on top, as a distinctly named second
// layer) and the cache-tier stickiness mirror: nothing else re-implements
// block semantics.
//
// The fingerprint hashes, with typed length framing (unambiguous joins):
//
//	role; then every block in order: kind tag, block PRESENCE, order,
//	identities (tool id/name, tool_call_id/tool_name), exact authoritative
//	raw bytes (arguments, markers, unknown payloads — verbatim), signature
//	tokens, nested tool-result content (recursively framed), and cache
//	breakpoint positions.
//
// The input bytes are the SDK's authoritative raw bytes (never decoded and
// re-encoded): key order, whitespace and lexemes inside the JSON payloads
// are part of the fingerprint, exactly as they are part of the provider's
// cached prefix.
//
// fingerprintFieldCoverage (below) is the executable inventory: the
// reflection-backed test forces a decision for every additive block field —
// a new field in the request body fails until it is either hashed here or
// explicitly excluded with a documented reason.

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// fingerprintFieldCoverage names every hashed field (full protobuf name).
// RequestFingerprintCoverageExact walks the descriptors and requires the
// two sets to be identical.
var fingerprintFieldCoverage = []string{
	"torana.v2.Message.role",
	"torana.v2.Message.blocks",
	// RequestBlock oneof arms (kind tag + presence).
	"torana.v2.RequestBlock.text",
	"torana.v2.RequestBlock.thinking",
	"torana.v2.RequestBlock.redacted_thinking",
	"torana.v2.RequestBlock.tool_use",
	"torana.v2.RequestBlock.tool_result",
	"torana.v2.RequestBlock.cache_breakpoint",
	"torana.v2.RequestBlock.unknown",
	"torana.v2.RequestBlock.trailing_signature",
	// Leaf fields.
	"torana.v2.RequestTextBlock.text",
	"torana.v2.RequestTextBlock.signature",
	"torana.v2.RequestThinkingBlock.text",
	"torana.v2.RequestThinkingBlock.signature",
	"torana.v2.RequestRedactedThinkingBlock.data",
	"torana.v2.RequestToolUseBlock.id",
	"torana.v2.RequestToolUseBlock.name",
	"torana.v2.RequestToolUseBlock.arguments_json",
	"torana.v2.RequestToolUseBlock.signature",
	"torana.v2.RequestToolResultBlock.tool_call_id",
	"torana.v2.RequestToolResultBlock.tool_name",
	"torana.v2.RequestToolResultBlock.content",
	"torana.v2.RequestCacheBreakpoint.marker_json",
	"torana.v2.RequestUnknownBlock.kind",
	"torana.v2.RequestUnknownBlock.payload_json",
	"torana.v2.RequestTrailingSignatureBlock.signature",
	// Nested tool-result content.
	"torana.v2.ToolResultContentBlock.text",
	"torana.v2.ToolResultContentBlock.unknown",
	"torana.v2.ToolResultContentBlock.cache_breakpoint",
	"torana.v2.ToolResultTextBlock.text",
	"torana.v2.ToolResultUnknownBlock.kind",
	"torana.v2.ToolResultUnknownBlock.payload_json",
	"torana.v2.ToolResultCacheBreakpoint.marker_json",
}

// RequestBlocksFingerprint returns the hex sha256 of the message body under
// typed length framing. Deterministic for identical inputs; any content
// difference (kind, presence, order, identity, raw bytes, signature, nested
// content, cache position) changes the digest.
func RequestBlocksFingerprint(msg *pbv2.Message) string {
	h := sha256.New()
	frame := func(tag, value string) {
		h.Write([]byte(tag))
		h.Write([]byte(strconv.Itoa(len(value))))
		h.Write([]byte{':'})
		h.Write([]byte(value))
	}
	frameBytes := func(tag string, value []byte) {
		frame(tag, string(value))
	}
	if msg == nil {
		frame("role", "")
		frame("blocks", "")
		return hex.EncodeToString(h.Sum(nil))
	}
	frame("role", msg.Role)
	for i, b := range msg.Blocks {
		frame("block", strconv.Itoa(i))
		if b == nil {
			frame("nil", "1")
			continue
		}
		switch k := b.Kind.(type) {
		case *pbv2.RequestBlock_Text:
			frame("kind", "text")
			if k.Text == nil {
				frame("nil", "text")
				continue
			}
			frame("text", k.Text.Text)
			frame("sig", k.Text.Signature)
		case *pbv2.RequestBlock_Thinking:
			frame("kind", "thinking")
			if k.Thinking == nil {
				frame("nil", "thinking")
				continue
			}
			frame("text", k.Thinking.Text)
			frame("sig", k.Thinking.Signature)
		case *pbv2.RequestBlock_RedactedThinking:
			frame("kind", "redacted")
			if k.RedactedThinking == nil {
				frame("nil", "redacted")
				continue
			}
			frame("data", k.RedactedThinking.Data)
		case *pbv2.RequestBlock_ToolUse:
			frame("kind", "tool_use")
			if k.ToolUse == nil {
				frame("nil", "tool_use")
				continue
			}
			frame("id", k.ToolUse.Id)
			frame("name", k.ToolUse.Name)
			frameBytes("args", k.ToolUse.ArgumentsJson)
			frame("sig", k.ToolUse.Signature)
		case *pbv2.RequestBlock_ToolResult:
			frame("kind", "tool_result")
			if k.ToolResult == nil {
				frame("nil", "tool_result")
				continue
			}
			frame("tcid", k.ToolResult.ToolCallId)
			frame("tname", k.ToolResult.ToolName)
			for j, c := range k.ToolResult.Content {
				frame("nested", strconv.Itoa(j))
				if c == nil {
					frame("nil", "content")
					continue
				}
				switch nk := c.Kind.(type) {
				case *pbv2.ToolResultContentBlock_Text:
					frame("nkind", "text")
					if nk.Text != nil {
						frame("text", nk.Text.Text)
					} else {
						frame("nil", "text")
					}
				case *pbv2.ToolResultContentBlock_Unknown:
					frame("nkind", "unknown")
					if nk.Unknown != nil {
						frame("kind", nk.Unknown.Kind)
						frameBytes("payload", nk.Unknown.PayloadJson)
					} else {
						frame("nil", "unknown")
					}
				case *pbv2.ToolResultContentBlock_CacheBreakpoint:
					frame("nkind", "cache")
					if nk.CacheBreakpoint != nil {
						frameBytes("marker", nk.CacheBreakpoint.MarkerJson)
					} else {
						frame("nil", "cache")
					}
				default:
					frame("nkind", "none")
				}
			}
		case *pbv2.RequestBlock_CacheBreakpoint:
			frame("kind", "cache")
			if k.CacheBreakpoint == nil {
				frame("nil", "cache")
				continue
			}
			frameBytes("marker", k.CacheBreakpoint.MarkerJson)
		case *pbv2.RequestBlock_Unknown:
			frame("kind", "unknown")
			if k.Unknown == nil {
				frame("nil", "unknown")
				continue
			}
			frame("kind", k.Unknown.Kind)
			frameBytes("payload", k.Unknown.PayloadJson)
		case *pbv2.RequestBlock_TrailingSignature:
			frame("kind", "trailing")
			if k.TrailingSignature == nil {
				frame("nil", "trailing")
				continue
			}
			frame("sig", k.TrailingSignature.Signature)
		default:
			frame("kind", "none")
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
