// Package providerschema vendors the pinned provider schema snapshot the
// Gemini Part inventory is validated against.
//
// The snapshot is MACHINE-READABLE and REVIEWED: the inventory test in this
// package compares the SDK's Part/FunctionResponse member tables against
// this snapshot, so a provider schema change is only noticed after the
// snapshot is deliberately re-vendored (see generate.sh for the update
// workflow). A hand-written "future member" fallback is NOT an inventory.
//
// Provenance (see generate.sh):
//
//   - Public Gemini: googleapis master, google/ai/generativelanguage/v1beta/
//     content.proto, revision fetched 2026-08-03.
//   - Vertex-compatible: googleapis master, google/cloud/aiplatform/v1/
//     content.proto, revision fetched 2026-08-03.
//
// License provenance: both protos are from googleapis (Apache-2.0); this
// file contains only extracted member tables, not the protos themselves.
package providerschema

// SnapshotRevision pins the exact upstream state the tables were generated
// from. Regenerate + update this string together (generate.sh).
const SnapshotRevision = "googleapis-master-2026-08-03"

// PartArm is one Gemini Part data arm.
type PartArm struct {
	// Wire member name (camelCase JSON).
	Member string
	// Surfaces that document this arm ("gemini", "vertex", "agent-platform").
	Surfaces []string
}

// PartAncillary is one Gemini Part ancillary member.
type PartAncillary struct {
	// Wire member name (camelCase JSON).
	Member string
	// Legal carriers; empty = any Part.
	Carriers []string
	// Value vocabulary when the member is an enum ("MEDIA_RESOLUTION_LOW",
	// ...), empty otherwise.
	Vocabulary []string
}

// PartArms is the pinned data-arm table.
var PartArms = []PartArm{
	{Member: "text", Surfaces: []string{"gemini", "vertex"}},
	{Member: "inlineData", Surfaces: []string{"gemini", "vertex"}},
	{Member: "fileData", Surfaces: []string{"gemini", "vertex"}},
	{Member: "functionCall", Surfaces: []string{"gemini", "vertex"}},
	{Member: "functionResponse", Surfaces: []string{"gemini", "vertex"}},
	{Member: "executableCode", Surfaces: []string{"gemini", "vertex"}},
	{Member: "codeExecutionResult", Surfaces: []string{"gemini", "vertex"}},
	// Server-side tool invocation arms (agent-platform/reasoning surfaces;
	// not present in the two fetched protos — recorded from the reviewer-
	// cited current contract; a snapshot refresh must confirm them).
	{Member: "toolCall", Surfaces: []string{"agent-platform"}},
	{Member: "toolResponse", Surfaces: []string{"agent-platform"}},
}

// PartAncillaries is the pinned ancillary table.
var PartAncillaries = []PartAncillary{
	{Member: "thought", Carriers: []string{"text"}},
	{Member: "thoughtSignature", Carriers: nil},
	{Member: "videoMetadata", Carriers: []string{"inlineData", "fileData"}},
	{Member: "mediaResolution", Carriers: []string{"inlineData", "fileData"},
		Vocabulary: []string{"MEDIA_RESOLUTION_LOW", "MEDIA_RESOLUTION_MEDIUM",
			"MEDIA_RESOLUTION_HIGH", "MEDIA_RESOLUTION_ULTRA_HIGH"}},
	{Member: "partMetadata", Carriers: nil},
}

// FunctionResponseMembers is the pinned FunctionResponse member table.
// willContinue presence is meaningful; scheduling is the exact wire enum
// string (SILENT / WHEN_IDLE / INTERRUPT; SCHEDULING_UNSPECIFIED is
// documented unused and treated like an unknown value at the adapter
// boundary — absence remains the provider default WHEN_IDLE).
var FunctionResponseMembers = map[string]string{
	"id":           "optional",
	"name":         "required",
	"response":     "required-object",
	"parts":        "optional-ordered",
	"willContinue": "optional-bool",
	"scheduling":   "optional-enum",
}

// SchedulingVocabulary is the pinned scheduling enum.
var SchedulingVocabulary = []string{"SILENT", "WHEN_IDLE", "INTERRUPT"}

// FunctionResponsePartArms is the pinned FunctionResponsePart sealed union.
// The nested grammar is its OWN union — top-level Part ancillaries are NOT
// legal inside a FunctionResponsePart.
var FunctionResponsePartArms = []PartArm{
	{Member: "inlineData", Surfaces: []string{"gemini", "vertex"}},
	{Member: "fileData", Surfaces: []string{"vertex"}},
}

// FunctionResponseBlobMembers is the pinned FunctionResponseBlob table
// (inlineData arm).
var FunctionResponseBlobMembers = map[string]string{
	"mimeType":    "required",
	"data":        "required",
	"displayName": "optional",
}

// FunctionResponseFileDataMembers is the pinned FunctionResponseFileData
// table (fileData arm).
var FunctionResponseFileDataMembers = map[string]string{
	"mimeType":    "required",
	"fileUri":     "required",
	"displayName": "optional",
}
