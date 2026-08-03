// Package providerschema vendors the pinned provider schema the SDK's
// Part/FunctionResponse inventory is validated against.
//
// The SCHEMA FACTS live in snapshot.gen.go, GENERATED from the vendored
// artifacts (source/*.proto) by generate.go — see source/manifest.json for
// the immutable upstream revisions (repository, commit SHA, path/URL,
// fetch date, Apache-2.0 license) and generate.sh for the deterministic
// update command. Ordinary tests and CI compare against the checked-in
// generated file and the vendored bytes; they never need the network.
//
// This file holds the REVIEWED DECISIONS on top of those facts: which ABI
// carrier (or explicit rejection) each schema node maps to, and the
// agent-platform arms that are NOT part of the vendored descriptors.
// TestSnapshotInventoryBidirectional requires the decision set to be
// EXACTLY the generated node set in both directions, so a new provider
// member fails until a decision is recorded and a stale decision fails
// until it is removed.
package providerschema

// SnapshotRevision pins the exact upstream state the snapshot was
// generated from (must match source/manifest.json).
const SnapshotRevision = "googleapis bc7e3baa28fbb223fa93782e130260fab8205bfc (generativelanguage v1beta) + fb6e47ad850029fd0c4deb96815550bd47bb42f2 (aiplatform v1) — 2026-08-03"

// Documented carrier decisions (markers for facts carried structurally
// rather than by a single field).
const (
	// CarrierPreservedMediaPayload: the member travels inside the raw JSON
	// payload of the media Unknown block (never narrowed: every pinned
	// object member of the vendored shape is preserved).
	CarrierPreservedMediaPayload = "PRESERVED-MEDIA-PAYLOAD"
	// CarrierSignatureToken: the member is projected into the block's
	// signature field.
	CarrierSignatureToken = "SIGNATURE-TOKEN"
	// CarrierPartMetadata: the member is projected into the block's
	// part_metadata_json field.
	CarrierPartMetadata = "PART_METADATA_CARRIER"
	// CarrierResponseTextElement: the function response object becomes the
	// FIRST nested Text element (single authority; exact raw bytes).
	CarrierResponseTextElement = "RESPONSE-TEXT-ELEMENT"
	// CarrierNestedMediaElements: the ordered function-response parts
	// become ordered nested Unknown elements.
	CarrierNestedMediaElements = "NESTED-MEDIA-ELEMENTS"
	// DecisionExcludedValue: the enum value is excluded from the usable
	// vocabulary (unknown-value handling: value-free 400 at the adapter
	// boundary; absence stays distinct).
	DecisionExcludedValue = "EXCLUDED-VALUE"
)

// schemaCarrierDecisions maps every generated schema node ID to its ABI
// carrier decision. The value is a protobuf message.field, a message-level
// carrier (the arm maps to the block as a whole), one of the documented
// carrier markers above, or DecisionExcludedValue for enum values that are
// deliberately unusable.
var schemaCarrierDecisions = map[string]string{
	// Part data arms.
	"part.arm.text":                "torana.v2.RequestTextBlock.text",
	"part.arm.inlineData":          "torana.v2.RequestUnknownBlock.payload_json",
	"part.arm.fileData":            "torana.v2.RequestUnknownBlock.payload_json",
	"part.arm.functionCall":        "torana.v2.RequestToolUseBlock",
	"part.arm.functionResponse":    "torana.v2.RequestToolResultBlock",
	"part.arm.executableCode":      "torana.v2.RequestUnknownBlock.payload_json",
	"part.arm.codeExecutionResult": "torana.v2.RequestUnknownBlock.payload_json",
	// Part ancillaries.
	"part.ancillary.thought":          "torana.v2.RequestThinkingBlock",
	"part.ancillary.thoughtSignature": CarrierSignatureToken,
	"part.ancillary.videoMetadata":    CarrierPreservedMediaPayload,
	"part.ancillary.mediaResolution":  CarrierPreservedMediaPayload,
	"part.ancillary.partMetadata":     CarrierPartMetadata,
	// FunctionResponse members.
	"function-response.member.id":           "torana.v2.RequestToolResultBlock.tool_call_id",
	"function-response.member.name":         "torana.v2.RequestToolResultBlock.tool_name",
	"function-response.member.response":     CarrierResponseTextElement,
	"function-response.member.parts":        CarrierNestedMediaElements,
	"function-response.member.willContinue": "torana.v2.RequestToolResultBlock.will_continue",
	"function-response.member.scheduling":   "torana.v2.RequestToolResultBlock.scheduling",
	// FunctionResponsePart union arms (nested media elements).
	"function-response-part.arm.inlineData": "torana.v2.ToolResultUnknownBlock.payload_json",
	"function-response-part.arm.fileData":   "torana.v2.ToolResultUnknownBlock.payload_json",
	// Nested media member objects (preserved verbatim in the nested
	// unknown payload; never narrowed).
	"function-response-blob.member.mimeType":         CarrierPreservedMediaPayload,
	"function-response-blob.member.data":             CarrierPreservedMediaPayload,
	"function-response-blob.member.displayName":      CarrierPreservedMediaPayload,
	"function-response-file-data.member.mimeType":    CarrierPreservedMediaPayload,
	"function-response-file-data.member.fileUri":     CarrierPreservedMediaPayload,
	"function-response-file-data.member.displayName": CarrierPreservedMediaPayload,
	// mediaResolution object grammar: the level member is preserved inside
	// the media payload; the usable vocabulary excludes the UNSPECIFIED
	// value (unknown-value handling at the adapter boundary).
	"media-resolution.member.level":                            CarrierPreservedMediaPayload,
	"media-resolution.level.enum.MEDIA_RESOLUTION_LOW":         "torana.v2.RequestUnknownBlock.payload_json",
	"media-resolution.level.enum.MEDIA_RESOLUTION_MEDIUM":      "torana.v2.RequestUnknownBlock.payload_json",
	"media-resolution.level.enum.MEDIA_RESOLUTION_HIGH":        "torana.v2.RequestUnknownBlock.payload_json",
	"media-resolution.level.enum.MEDIA_RESOLUTION_ULTRA_HIGH":  "torana.v2.RequestUnknownBlock.payload_json",
	"media-resolution.level.enum.MEDIA_RESOLUTION_UNSPECIFIED": DecisionExcludedValue,
	// scheduling vocabulary: UNSPECIFIED is documented unused by the
	// provider and receives the same value-free 400 as any unknown value;
	// absence remains the provider default WHEN_IDLE and is distinct.
	"scheduling.enum.SILENT":                 "torana.v2.RequestToolResultBlock.scheduling",
	"scheduling.enum.WHEN_IDLE":              "torana.v2.RequestToolResultBlock.scheduling",
	"scheduling.enum.INTERRUPT":              "torana.v2.RequestToolResultBlock.scheduling",
	"scheduling.enum.SCHEDULING_UNSPECIFIED": DecisionExcludedValue,
}

// AgentPlatformArms are Part data arms of the agent-platform surface. They
// are NOT present in any vendored artifact (grep-verified by
// TestAgentPlatformArmsHonest); they are a REVIEWED non-descriptor
// decision, cited from the agent-platform surface by the reviewing round
// (SDK_SIGNED_PART_CHECKPOINT.md §0) and carried like the other
// future/unknown arms. A snapshot refresh must confirm them before any
// generated table can claim descriptor provenance for them.
var AgentPlatformArms = []SchemaNode{
	{ID: "part.arm.toolCall", Member: "toolCall", Kind: "message", Surfaces: []string{"agent-platform"}},
	{ID: "part.arm.toolResponse", Member: "toolResponse", Kind: "message", Surfaces: []string{"agent-platform"}},
}

// SchemaNodes returns the generated node set plus the reviewed
// agent-platform arms (the complete Part surface union).
func SchemaNodes() []SchemaNode {
	out := make([]SchemaNode, 0, len(schemaNodes)+len(AgentPlatformArms))
	out = append(out, schemaNodes...)
	out = append(out, AgentPlatformArms...)
	return out
}

// AgentPlatformCarrierDecisions maps the reviewed non-descriptor arms to
// their carriers. Kept SEPARATE from schemaCarrierDecisions so the
// bidirectional inventory over the generated nodes can never claim
// descriptor provenance for them; TestAgentPlatformArmsHonest checks this
// map and the artifacts' absence.
var AgentPlatformCarrierDecisions = map[string]string{
	"part.arm.toolCall":     "torana.v2.RequestUnknownBlock.payload_json",
	"part.arm.toolResponse": "torana.v2.RequestUnknownBlock.payload_json",
}

// CarrierFor returns the decision for a node ID ("" when absent).
func CarrierFor(id string) string { return schemaCarrierDecisions[id] }

// UsableEnumValues returns the usable vocabulary for an enum node ID
// (e.g. "scheduling.enum." or "media-resolution.level.enum."): every value
// in ARTIFACT order with a non-excluded decision.
func UsableEnumValues(enumIDPrefix string) []string {
	var order []string
	switch enumIDPrefix {
	case "scheduling.enum.":
		order = schedulingArtifactOrder
	case "media-resolution.level.enum.":
		order = mediaResolutionLevelArtifactOrder
	}
	var out []string
	for _, v := range order {
		if schemaCarrierDecisions[enumIDPrefix+v] != DecisionExcludedValue {
			out = append(out, v)
		}
	}
	return out
}
