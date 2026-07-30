package v2

import (
	"fmt"
	"sync"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// hookResultActionFields is every field number in HookResult.action, derived
// from the generated descriptor so a new action arm cannot silently reopen
// last-wins acceptance of multi-arm frames.
var (
	hookResultActionFields     map[protowire.Number]struct{}
	hookResultActionFieldsOnce sync.Once
)

func knownHookResultActionFields() map[protowire.Number]struct{} {
	hookResultActionFieldsOnce.Do(func() {
		hookResultActionFields = actionFieldNumbers((&HookResult{}).ProtoReflect().Descriptor())
	})
	return hookResultActionFields
}

// actionFieldNumbers returns the field numbers belonging to the "action" oneof
// on md. Panics if the descriptor has no such oneof — that would mean the
// generated message no longer matches this decoder's contract.
func actionFieldNumbers(md protoreflect.MessageDescriptor) map[protowire.Number]struct{} {
	ones := md.Oneofs()
	for i := 0; i < ones.Len(); i++ {
		od := ones.Get(i)
		if od.Name() != "action" {
			continue
		}
		fs := od.Fields()
		out := make(map[protowire.Number]struct{}, fs.Len())
		for j := 0; j < fs.Len(); j++ {
			out[fs.Get(j).Number()] = struct{}{}
		}
		if len(out) == 0 {
			panic("torana pb/v2: HookResult.action oneof has no fields")
		}
		return out
	}
	panic("torana pb/v2: HookResult has no action oneof")
}

// DecodeHookResult unmarshals a guest-produced HookResult and refuses a wire
// frame that encodes more than one known action arm (including the same arm
// repeated).
//
// Protobuf's standard unmarshal applies last-known-arm-wins and drops the
// earlier arm, so a post-unmarshal ValidateFor cannot recover a double-arm
// handwritten frame. Unknown/future arms still land in unknown fields and are
// refused by ValidateFor. Call DecodeHookResult at the host boundary before
// ValidateFor.
func DecodeHookResult(b []byte) (*HookResult, error) {
	if err := refuseMultipleKnownFields(b, knownHookResultActionFields()); err != nil {
		return nil, fmt.Errorf("hook result: %w", err)
	}
	var r HookResult
	if err := proto.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// refuseMultipleKnownFields walks a protobuf message wire encoding and reports
// an error when more than one field number from known appears at the top level,
// including repeated occurrences of the same number. Nested length-delimited
// payloads are not descended into.
func refuseMultipleKnownFields(b []byte, known map[protowire.Number]struct{}) error {
	seen := 0
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return fmt.Errorf("invalid wire tag")
		}
		b = b[n:]
		n = protowire.ConsumeFieldValue(num, typ, b)
		if n < 0 {
			return fmt.Errorf("invalid wire value for field %d", num)
		}
		b = b[n:]
		if _, ok := known[num]; ok {
			seen++
			if seen > 1 {
				return fmt.Errorf("encodes more than one known oneof arm")
			}
		}
	}
	return nil
}
