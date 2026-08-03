package v2

// Wire-number pins for the signed-Part carriers. These fields are ADDITIVE
// flat members (never oneof members); their numbers are ABI surface —
// protoc renumbering or a move into a oneof changes the wire layout and
// fails here.

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
)

func checkPinnedField(t *testing.T, msg protoreflect.MessageDescriptor, name string, wantNumber protoreflect.FieldNumber) {
	t.Helper()
	fd := msg.Fields().ByName(protoreflect.Name(name))
	if fd == nil {
		t.Fatalf("%s.%s: field does not exist", msg.FullName(), name)
	}
	if fd.Number() != wantNumber {
		t.Fatalf("%s.%s: number = %d, want %d (wire ABI changed)", msg.FullName(), name, fd.Number(), wantNumber)
	}
	if od := fd.ContainingOneof(); od != nil && !od.IsSynthetic() {
		t.Fatalf("%s.%s: must be a flat additive field, not a oneof member", msg.FullName(), name)
	}
}

func TestSignedPartWirePins(t *testing.T) {
	checkPinnedField(t, (&RequestTextBlock{}).ProtoReflect().Descriptor(), "part_metadata_json", 3)
	checkPinnedField(t, (&RequestThinkingBlock{}).ProtoReflect().Descriptor(), "part_metadata_json", 3)
	checkPinnedField(t, (&RequestToolUseBlock{}).ProtoReflect().Descriptor(), "part_metadata_json", 5)
	checkPinnedField(t, (&RequestToolResultBlock{}).ProtoReflect().Descriptor(), "part_metadata_json", 4)
	checkPinnedField(t, (&RequestToolResultBlock{}).ProtoReflect().Descriptor(), "will_continue", 5)
	checkPinnedField(t, (&RequestToolResultBlock{}).ProtoReflect().Descriptor(), "scheduling", 6)
	checkPinnedField(t, (&RequestToolResultBlock{}).ProtoReflect().Descriptor(), "signature", 7)
	checkPinnedField(t, (&RequestUnknownBlock{}).ProtoReflect().Descriptor(), "part_metadata_json", 3)
	checkPinnedField(t, (&RequestUnknownBlock{}).ProtoReflect().Descriptor(), "signature", 4)
	checkPinnedField(t, (&RequestTrailingSignatureBlock{}).ProtoReflect().Descriptor(), "part_metadata_json", 2)
}
