package v2

import (
	"fmt"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// hookResultActionFields are the known HookResult.action oneof field numbers.
// Every top-level field of HookResult is an action arm, so seeing more than one
// of these on the wire means a guest encoded two actions at once.
var hookResultActionFields = map[protowire.Number]struct{}{
	1: {}, // replace_request
	2: {}, // replace_response
	3: {}, // emit_events
	4: {}, // serve_http
	5: {}, // tick_outcome
	6: {}, // suppress
}

// DecodeHookResult unmarshals a guest-produced HookResult and refuses a wire
// frame that encodes more than one known action arm.
//
// Protobuf's standard unmarshal applies last-known-arm-wins and drops the
// earlier arm, so a post-unmarshal ValidateFor cannot recover a double-arm
// handwritten frame. Unknown/future arms still land in unknown fields and are
// refused by ValidateFor. Call DecodeHookResult at the host boundary before
// ValidateFor.
func DecodeHookResult(b []byte) (*HookResult, error) {
	if err := refuseMultipleKnownFields(b, hookResultActionFields); err != nil {
		return nil, fmt.Errorf("hook result: %w", err)
	}
	var r HookResult
	if err := proto.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// refuseMultipleKnownFields walks a protobuf message wire encoding and reports
// an error when more than one field number from known appears at the top level.
// Nested length-delimited payloads are not descended into.
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
