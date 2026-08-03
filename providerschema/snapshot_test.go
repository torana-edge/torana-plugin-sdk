package providerschema

import (
	"strings"
	"testing"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// The provider-schema inventory (offline): every snapshot member must have
// a DELIBERATE ABI carrier mapping (or an explicit value-free-rejected
// decision), and every carrier must be backed by a real proto field. A new
// provider member fails here until a supported-versus-rejected decision is
// recorded in carrierTable — a hand-written "future member" fallback is
// not an inventory.

// carrierTable maps each snapshot member to its ABI carrier. The value is
// the protobuf field that carries the fact (message.field); a marker value
// documents a deliberate decision that the member is preserved inside
// another carrier's payload or rejected.
var carrierTable = map[string]string{
	// Part data arms.
	"text":                "torana.v2.RequestTextBlock.text",
	"inlineData":          "torana.v2.RequestUnknownBlock.payload_json",
	"fileData":            "torana.v2.RequestUnknownBlock.payload_json",
	"functionCall":        "torana.v2.RequestToolUseBlock",
	"functionResponse":    "torana.v2.RequestToolResultBlock",
	"executableCode":      "torana.v2.RequestUnknownBlock.payload_json",
	"codeExecutionResult": "torana.v2.RequestUnknownBlock.payload_json",
	"toolCall":            "torana.v2.RequestUnknownBlock.payload_json",
	"toolResponse":        "torana.v2.RequestUnknownBlock.payload_json",
	// Part ancillaries.
	"thought":          "torana.v2.RequestThinkingBlock",
	"thoughtSignature": "SIGNATURE-TOKEN",         // the arm's signature field
	"videoMetadata":    "PRESERVED-MEDIA-PAYLOAD", // payload member of the media Unknown block
	"mediaResolution":  "PRESERVED-MEDIA-PAYLOAD",
	"partMetadata":     "PART_METADATA_CARRIER", // the arm's part_metadata_json
	// FunctionResponse members.
	"id":           "torana.v2.RequestToolResultBlock.tool_call_id",
	"name":         "torana.v2.RequestToolResultBlock.tool_name",
	"response":     "RESPONSE-TEXT-ELEMENT", // first nested Text element (single authority)
	"parts":        "NESTED-MEDIA-ELEMENTS", // ordered nested Unknown elements
	"willContinue": "torana.v2.RequestToolResultBlock.will_continue",
	"scheduling":   "torana.v2.RequestToolResultBlock.scheduling",
	// FunctionResponsePart arms.
	"FunctionResponsePart.inlineData": "torana.v2.ToolResultUnknownBlock.payload_json",
	"FunctionResponsePart.fileData":   "torana.v2.ToolResultUnknownBlock.payload_json",
}

// TestSnapshotInventoryComplete — every snapshot arm/ancillary/member has a
// declared carrier, and every declared carrier is backed by a real proto
// field (or a documented marker decision).
func TestSnapshotInventoryComplete(t *testing.T) {
	for _, arm := range PartArms {
		if _, ok := carrierTable[arm.Member]; !ok {
			t.Errorf("Part arm %q has no declared ABI carrier", arm.Member)
		}
	}
	for _, anc := range PartAncillaries {
		if _, ok := carrierTable[anc.Member]; !ok {
			t.Errorf("Part ancillary %q has no declared ABI carrier", anc.Member)
		}
	}
	for member := range FunctionResponseMembers {
		if _, ok := carrierTable[member]; !ok {
			t.Errorf("FunctionResponse member %q has no declared ABI carrier", member)
		}
	}
	for _, arm := range FunctionResponsePartArms {
		key := "FunctionResponsePart." + arm.Member
		if _, ok := carrierTable[key]; !ok {
			t.Errorf("FunctionResponsePart arm %q has no declared ABI carrier", arm.Member)
		}
	}

	// Every field-backed carrier resolves to a real descriptor field.
	msgDescs := map[protoreflect.FullName]protoreflect.MessageDescriptor{}
	for _, inst := range []protoreflect.MessageDescriptor{
		(&pbv2.RequestTextBlock{}).ProtoReflect().Descriptor(),
		(&pbv2.RequestThinkingBlock{}).ProtoReflect().Descriptor(),
		(&pbv2.RequestToolUseBlock{}).ProtoReflect().Descriptor(),
		(&pbv2.RequestToolResultBlock{}).ProtoReflect().Descriptor(),
		(&pbv2.RequestUnknownBlock{}).ProtoReflect().Descriptor(),
		(&pbv2.RequestTrailingSignatureBlock{}).ProtoReflect().Descriptor(),
		(&pbv2.ToolResultUnknownBlock{}).ProtoReflect().Descriptor(),
	} {
		msgDescs[inst.FullName()] = inst
	}
	for member, carrier := range carrierTable {
		if carrier == "" || strings.HasPrefix(carrier, "PRESERVED") ||
			carrier == "SIGNATURE-TOKEN" || carrier == "PART_METADATA_CARRIER" ||
			carrier == "RESPONSE-TEXT-ELEMENT" || carrier == "NESTED-MEDIA-ELEMENTS" {
			continue // documented carrier decisions, not proto fields
		}
		// A message-level carrier (the arm maps to the block as a whole)
		// is resolved directly; otherwise the last dot separates the
		// message from the field ("torana.v2.Message.field").
		if md, ok := msgDescs[protoreflect.FullName(carrier)]; ok {
			_ = md
			continue
		}
		dot := strings.LastIndex(carrier, ".")
		if dot < 0 {
			t.Errorf("%s: carrier %q is neither a known message nor message.field", member, carrier)
			continue
		}
		md, ok := msgDescs[protoreflect.FullName(carrier[:dot])]
		if !ok {
			t.Errorf("%s: carrier message %s unknown", member, carrier[:dot])
			continue
		}
		if md.Fields().ByName(protoreflect.Name(carrier[dot+1:])) == nil {
			t.Errorf("%s: carrier field %s does not exist", member, carrier)
		}
	}
}

// TestSnapshotVocabularyPinned — the pinned enum vocabularies are exact and
// canonical.
func TestSnapshotVocabularyPinned(t *testing.T) {
	for _, anc := range PartAncillaries {
		switch anc.Member {
		case "mediaResolution":
			want := []string{"MEDIA_RESOLUTION_LOW", "MEDIA_RESOLUTION_MEDIUM",
				"MEDIA_RESOLUTION_HIGH", "MEDIA_RESOLUTION_ULTRA_HIGH"}
			if strings.Join(anc.Vocabulary, ",") != strings.Join(want, ",") {
				t.Errorf("mediaResolution vocabulary = %v, want %v", anc.Vocabulary, want)
			}
		}
	}
	wantSched := []string{"SILENT", "WHEN_IDLE", "INTERRUPT"}
	if strings.Join(SchedulingVocabulary, ",") != strings.Join(wantSched, ",") {
		t.Errorf("scheduling vocabulary = %v, want %v", SchedulingVocabulary, wantSched)
	}
	// SCHEDULING_UNSPECIFIED is documented unused: it must be absent from
	// the usable vocabulary (treated like an unknown value at the adapter).
	for _, v := range SchedulingVocabulary {
		if v == "SCHEDULING_UNSPECIFIED" {
			t.Fatal("SCHEDULING_UNSPECIFIED must not be in the usable vocabulary")
		}
	}
}

// TestSnapshotMemberSpellingsCamelCase — wire member names are camelCase
// (proto-style snake_case names are never the wire grammar).
func TestSnapshotMemberSpellingsCamelCase(t *testing.T) {
	all := append([]string{}, PartArmsNames()...)
	for _, m := range PartAncillaries {
		all = append(all, m.Member)
	}
	all = append(all, FunctionResponsePartArmsNames()...)
	for _, member := range all {
		if strings.Contains(member, "_") {
			t.Errorf("member %q is not a camelCase wire spelling", member)
		}
	}
}

func PartArmsNames() []string {
	out := make([]string, 0, len(PartArms))
	for _, a := range PartArms {
		out = append(out, a.Member)
	}
	return out
}

func FunctionResponsePartArmsNames() []string {
	out := make([]string, 0, len(FunctionResponsePartArms))
	for _, a := range FunctionResponsePartArms {
		out = append(out, a.Member)
	}
	return out
}
