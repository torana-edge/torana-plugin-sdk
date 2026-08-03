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
// PRECONDITION: the message must be absolutely validated (the host runs
// ChatRequest.ValidateReplacement before fingerprinting). The defensive
// nil/unknown branches below exist only for totality — hashing a
// non-validated message must never be mistaken for accepting it.
//
// fingerprintFieldCoverage (below) is the executable inventory: the
// reflection-backed test forces a decision for every additive block field —
// a new field in the request body fails until it is either hashed here or
// explicitly excluded with a documented reason.

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
	"github.com/torana-edge/torana-plugin-sdk/pb/v2/jsontext"
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
	"torana.v2.RequestTextBlock.part_metadata_json",
	"torana.v2.RequestThinkingBlock.text",
	"torana.v2.RequestThinkingBlock.signature",
	"torana.v2.RequestThinkingBlock.part_metadata_json",
	"torana.v2.RequestRedactedThinkingBlock.data",
	"torana.v2.RequestToolUseBlock.id",
	"torana.v2.RequestToolUseBlock.name",
	"torana.v2.RequestToolUseBlock.arguments_json",
	"torana.v2.RequestToolUseBlock.signature",
	"torana.v2.RequestToolUseBlock.part_metadata_json",
	"torana.v2.RequestToolResultBlock.tool_call_id",
	"torana.v2.RequestToolResultBlock.tool_name",
	"torana.v2.RequestToolResultBlock.content",
	"torana.v2.RequestToolResultBlock.part_metadata_json",
	"torana.v2.RequestToolResultBlock.will_continue",
	"torana.v2.RequestToolResultBlock.scheduling",
	"torana.v2.RequestToolResultBlock.signature",
	"torana.v2.RequestCacheBreakpoint.marker_json",
	"torana.v2.RequestUnknownBlock.kind",
	"torana.v2.RequestUnknownBlock.payload_json",
	"torana.v2.RequestUnknownBlock.part_metadata_json",
	"torana.v2.RequestUnknownBlock.signature",
	"torana.v2.RequestTrailingSignatureBlock.signature",
	"torana.v2.RequestTrailingSignatureBlock.part_metadata_json",
	// Nested tool-result content.
	"torana.v2.ToolResultContentBlock.text",
	"torana.v2.ToolResultContentBlock.unknown",
	"torana.v2.ToolResultContentBlock.cache_breakpoint",
	"torana.v2.ToolResultTextBlock.text",
	"torana.v2.ToolResultUnknownBlock.kind",
	"torana.v2.ToolResultUnknownBlock.payload_json",
	"torana.v2.ToolResultCacheBreakpoint.marker_json",
}

// toolResultContentDomainFrame is the explicit domain/version frame hashed
// FIRST by ToolResultContentFingerprint — "v1 in prose" is not a versioned
// digest; the frame is part of the bytes.
const toolResultContentDomainFrame = "torana/tool-result-content/v1"

// ToolResultContentFingerprint is the SDK-owned digest of an ordered
// tool-result content sequence, TOTAL and VERSIONED: nil elements and
// typed-nil oneof arms are ERRORS (never an ordinary digest), and an
// unknown payload or cache marker that is not a strict JSON object is an
// ERROR too — even if a future caller forgets outer request validation.
//
// PREIMAGE (the approved binary layout; the ABI for the digest embedded
// by callers, NOT an implementation detail):
//
//	u64be(len(domain)) || domain           // domain = "torana/tool-result-content/v1"
//	u64be(element count)
//	per element, in wire order:
//	  u8 arm tag (1 = text, 2 = unknown, 3 = cache)
//	  u8 presence byte (1)
//	  text:    u64be(len(text))  || text
//	  unknown: u64be(len(kind))  || kind || u64be(len(payload)) || payload
//	  cache:   u64be(len(marker)) || marker
//
// All lengths/counts are fixed-width unsigned big-endian (uint64). There
// are no textual tags or indexes inside this nested contract; order is
// implicit in the sequence. The layout is pinned by the independent
// golden encoder in requestblocks_test.go (TestToolResultContentFingerprint
// BinaryLayout), which hard-codes the v1 domain literal and a fixed golden
// digest for a fixed input — a drift in either direction fails.
//
// RequestBlocksFingerprint and the host write-grant verifier call THIS
// implementation — no equivalent framing is ever re-implemented.
func ToolResultContentFingerprint(content []*pbv2.ToolResultContentBlock) ([32]byte, error) {
	h := sha256.New()
	u64 := func(v uint64) {
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], v)
		h.Write(b[:])
	}
	bytes := func(v []byte) {
		u64(uint64(len(v)))
		h.Write(v)
	}
	str := func(v string) { bytes([]byte(v)) }
	byte1 := func(v byte) { h.Write([]byte{v}) }

	domain := []byte(toolResultContentDomainFrame)
	u64(uint64(len(domain)))
	h.Write(domain)
	u64(uint64(len(content)))
	for j, c := range content {
		if c == nil {
			return [32]byte{}, fmt.Errorf("tool-result content[%d]: nil element", j)
		}
		switch nk := c.Kind.(type) {
		case *pbv2.ToolResultContentBlock_Text:
			byte1(1) // arm tag: text
			if nk.Text == nil {
				return [32]byte{}, fmt.Errorf("tool-result content[%d]: typed-nil text arm", j)
			}
			byte1(1) // presence
			str(nk.Text.Text)
		case *pbv2.ToolResultContentBlock_Unknown:
			byte1(2) // arm tag: unknown
			if nk.Unknown == nil {
				return [32]byte{}, fmt.Errorf("tool-result content[%d]: typed-nil unknown arm", j)
			}
			byte1(1) // presence
			str(nk.Unknown.Kind)
			if err := validateStrictObject(nk.Unknown.PayloadJson); err != nil {
				return [32]byte{}, fmt.Errorf("tool-result content[%d]: unknown payload: %w", j, err)
			}
			bytes(nk.Unknown.PayloadJson)
		case *pbv2.ToolResultContentBlock_CacheBreakpoint:
			byte1(3) // arm tag: cache
			if nk.CacheBreakpoint == nil {
				return [32]byte{}, fmt.Errorf("tool-result content[%d]: typed-nil cache arm", j)
			}
			byte1(1) // presence
			if err := validateStrictObject(nk.CacheBreakpoint.MarkerJson); err != nil {
				return [32]byte{}, fmt.Errorf("tool-result content[%d]: cache marker: %w", j, err)
			}
			bytes(nk.CacheBreakpoint.MarkerJson)
		default:
			return [32]byte{}, fmt.Errorf("tool-result content[%d]: no oneof arm", j)
		}
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// validateStrictObject checks the shared strict JSON-text rules and the
// top-level object shape.
func validateStrictObject(raw []byte) error {
	if err := jsontext.Validate(raw); err != nil {
		return err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("expected a JSON object")
	}
	return nil
}

// RequestBlocksFingerprint returns the hex sha256 of the message body under
// typed length framing, or an error for invalid input — a nil message, nil
// block elements, typed-nil oneof arms, or an invalid nested payload NEVER
// yield a usable fingerprint (the enclosing callers fail closed). The
// nested content section is hashed by ToolResultContentFingerprint — the
// same SDK primitive the verifier consumes. Deterministic for identical
// inputs; any content difference (kind, presence, order, identity, raw
// bytes, signature tokens, provider part metadata, willContinue/scheduling
// presence+value, nested content, cache position) changes the digest.
func RequestBlocksFingerprint(msg *pbv2.Message) (string, error) {
	if msg == nil {
		return "", fmt.Errorf("request blocks fingerprint: nil message")
	}
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
	frame("role", msg.Role)
	for i, b := range msg.Blocks {
		frame("block", strconv.Itoa(i))
		if b == nil {
			return "", fmt.Errorf("request blocks fingerprint: blocks[%d] is nil", i)
		}
		switch k := b.Kind.(type) {
		case *pbv2.RequestBlock_Text:
			frame("kind", "text")
			if k.Text == nil {
				return "", fmt.Errorf("request blocks fingerprint: blocks[%d] typed-nil text arm", i)
			}
			frame("text", k.Text.Text)
			frame("sig", k.Text.Signature)
			frameBytes("pmeta", k.Text.PartMetadataJson)
		case *pbv2.RequestBlock_Thinking:
			frame("kind", "thinking")
			if k.Thinking == nil {
				return "", fmt.Errorf("request blocks fingerprint: blocks[%d] typed-nil thinking arm", i)
			}
			frame("text", k.Thinking.Text)
			frame("sig", k.Thinking.Signature)
			frameBytes("pmeta", k.Thinking.PartMetadataJson)
		case *pbv2.RequestBlock_RedactedThinking:
			frame("kind", "redacted")
			if k.RedactedThinking == nil {
				return "", fmt.Errorf("request blocks fingerprint: blocks[%d] typed-nil redacted arm", i)
			}
			frame("data", k.RedactedThinking.Data)
		case *pbv2.RequestBlock_ToolUse:
			frame("kind", "tool_use")
			if k.ToolUse == nil {
				return "", fmt.Errorf("request blocks fingerprint: blocks[%d] typed-nil tool_use arm", i)
			}
			frame("id", k.ToolUse.Id)
			frame("name", k.ToolUse.Name)
			frameBytes("args", k.ToolUse.ArgumentsJson)
			frame("sig", k.ToolUse.Signature)
			frameBytes("pmeta", k.ToolUse.PartMetadataJson)
		case *pbv2.RequestBlock_ToolResult:
			frame("kind", "tool_result")
			if k.ToolResult == nil {
				return "", fmt.Errorf("request blocks fingerprint: blocks[%d] typed-nil tool_result arm", i)
			}
			frame("tcid", k.ToolResult.ToolCallId)
			frame("tname", k.ToolResult.ToolName)
			frameBytes("pmeta", k.ToolResult.PartMetadataJson)
			// Presence-aware scalars: frame presence + value (absent is not
			// an implicit false/empty).
			if k.ToolResult.WillContinue != nil {
				frame("wc", "1")
				frame("wcval", strconv.FormatBool(*k.ToolResult.WillContinue))
			} else {
				frame("wc", "0")
			}
			if k.ToolResult.Scheduling != nil {
				frame("sched", "1")
				frame("schedval", *k.ToolResult.Scheduling)
			} else {
				frame("sched", "0")
			}
			frame("trsig", k.ToolResult.Signature)
			nestedSum, err := ToolResultContentFingerprint(k.ToolResult.Content)
			if err != nil {
				return "", fmt.Errorf("request blocks fingerprint: blocks[%d] nested content: %w", i, err)
			}
			frameBytes("nested", nestedSum[:])
		case *pbv2.RequestBlock_CacheBreakpoint:
			frame("kind", "cache")
			if k.CacheBreakpoint == nil {
				return "", fmt.Errorf("request blocks fingerprint: blocks[%d] typed-nil cache arm", i)
			}
			frameBytes("marker", k.CacheBreakpoint.MarkerJson)
		case *pbv2.RequestBlock_Unknown:
			frame("kind", "unknown")
			if k.Unknown == nil {
				return "", fmt.Errorf("request blocks fingerprint: blocks[%d] typed-nil unknown arm", i)
			}
			frame("kind", k.Unknown.Kind)
			frameBytes("payload", k.Unknown.PayloadJson)
			frameBytes("pmeta", k.Unknown.PartMetadataJson)
			frame("usig", k.Unknown.Signature)
		case *pbv2.RequestBlock_TrailingSignature:
			frame("kind", "trailing")
			if k.TrailingSignature == nil {
				return "", fmt.Errorf("request blocks fingerprint: blocks[%d] typed-nil trailing arm", i)
			}
			frame("sig", k.TrailingSignature.Signature)
			frameBytes("pmeta", k.TrailingSignature.PartMetadataJson)
		default:
			return "", fmt.Errorf("request blocks fingerprint: blocks[%d] has no oneof arm", i)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
