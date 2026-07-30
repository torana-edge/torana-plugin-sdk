package v2

import (
	"maps"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

// Deriving known arms from the descriptor (not a hand list) is what keeps
// DecodeHookResult correct when HookResult.action grows. This pins that the
// cached set is exactly the oneof inventory.
func TestKnownHookResultActionFieldsMatchDescriptor(t *testing.T) {
	got := knownHookResultActionFields()
	want := actionFieldNumbers((&HookResult{}).ProtoReflect().Descriptor())
	if !maps.Equal(got, want) {
		t.Fatalf("known arms %v != descriptor action fields %v", keys(got), keys(want))
	}
	if len(got) < 6 {
		t.Fatalf("expected at least the six current action arms, got %d", len(got))
	}
}

func keys(m map[protowire.Number]struct{}) []protowire.Number {
	out := make([]protowire.Number, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	return out
}
