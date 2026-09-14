package v1

import (
	"fmt"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// DecodeHookResult unmarshals a guest-produced HookResult and refuses a wire
// frame that encodes more than one known action arm (including the same arm
// repeated).
//
// Protobuf's standard unmarshal applies last-known-arm-wins and drops the
// earlier arm, so a post-unmarshal ValidateFor cannot recover a double-arm
// handwritten frame. The closed wire validator also rejects unknown/future
// fields. Call DecodeHookResult at the host boundary before ValidateFor.
func DecodeHookResult(b []byte) (*HookResult, error) {
	if err := ValidateWire(b, (&HookResult{}).ProtoReflect().Descriptor()); err != nil {
		return nil, fmt.Errorf("hook result wire: %w", err)
	}
	var r HookResult
	if err := proto.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ValidateWire validates a closed protobuf message before unmarshal. It
// rejects unknown fields, wrong wire types, and repeated/conflicting oneof
// members recursively through nested messages. Repeated numeric fields accept
// both packed and unpacked encodings, as required by protobuf. Nesting is
// bounded to 100 messages, including the root, to protect future recursive schemas.
func ValidateWire(b []byte, md protoreflect.MessageDescriptor) error {
	return validateWireMessage(b, md, md.FullName(), 1)
}

func validateWireMessage(b []byte, md protoreflect.MessageDescriptor, name protoreflect.FullName, depth int) error {
	if depth > 100 {
		return fmt.Errorf("%s: message nesting exceeds 100", name)
	}
	seenOneof := map[protoreflect.Name]protoreflect.FieldNumber{}
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return fmt.Errorf("%s: invalid wire tag", name)
		}
		b = b[n:]
		fd := md.Fields().ByNumber(num)
		if fd == nil {
			return fmt.Errorf("%s: unknown field %d", name, num)
		}
		expected := wireTypeFor(fd)
		packed := fd.IsList() && typ == protowire.BytesType && (expected == protowire.VarintType || expected == protowire.Fixed32Type || expected == protowire.Fixed64Type)
		if expected != typ && !packed {
			return fmt.Errorf("%s.%s: wrong wire type", name, fd.Name())
		}
		if od := fd.ContainingOneof(); od != nil && !od.IsSynthetic() {
			if old, ok := seenOneof[od.Name()]; ok {
				return fmt.Errorf("%s: encodes more than one known oneof arm (fields %d and %d)", name, old, num)
			}
			seenOneof[od.Name()] = num
		}
		value := b
		consumed := protowire.ConsumeFieldValue(num, typ, value)
		if consumed < 0 {
			return fmt.Errorf("%s.%s: invalid field", name, fd.Name())
		}
		b = b[consumed:]
		if packed {
			payload, _ := protowire.ConsumeBytes(value) // ConsumeFieldValue checked the length.
			for len(payload) > 0 {
				n := protowire.ConsumeFieldValue(num, expected, payload)
				if n < 0 {
					return fmt.Errorf("%s.%s: invalid packed field", name, fd.Name())
				}
				payload = payload[n:]
			}
		}
		if fd.Kind() == protoreflect.MessageKind && typ == protowire.BytesType {
			payload, m := protowire.ConsumeBytes(value)
			if m < 0 {
				return fmt.Errorf("%s.%s: invalid message", name, fd.Name())
			}
			if err := validateWireMessage(payload, fd.Message(), fd.Message().FullName(), depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func wireTypeFor(fd protoreflect.FieldDescriptor) protowire.Type {
	switch fd.Kind() {
	case protoreflect.MessageKind, protoreflect.StringKind, protoreflect.BytesKind:
		return protowire.BytesType
	case protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind, protoreflect.FloatKind:
		return protowire.Fixed32Type
	case protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind, protoreflect.DoubleKind:
		return protowire.Fixed64Type
	default:
		return protowire.VarintType
	}
}

func DecodeHookInput(b []byte) (*HookInput, error) {
	if err := ValidateWire(b, (&HookInput{}).ProtoReflect().Descriptor()); err != nil {
		return nil, err
	}
	var v HookInput
	if err := proto.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return &v, nil
}
func DecodeHostCallResult(b []byte) (*HostCallResult, error) {
	if err := ValidateWire(b, (&HostCallResult{}).ProtoReflect().Descriptor()); err != nil {
		return nil, err
	}
	var v HostCallResult
	if err := proto.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	if err := v.Validate(); err != nil {
		return nil, err
	}
	return &v, nil
}

func DecodeChatRequest(b []byte) (*ChatRequest, error) {
	if err := ValidateWire(b, (&ChatRequest{}).ProtoReflect().Descriptor()); err != nil {
		return nil, err
	}
	var v ChatRequest
	if err := proto.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return &v, nil
}
