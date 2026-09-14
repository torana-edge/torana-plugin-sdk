package v1

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// Every descriptor arm must be accepted alone and rejected when repeated or
// combined, without maintaining a second inventory in the decoder.
func TestDecodeHookResultOneofInventory(t *testing.T) {
	fields := (&HookResult{}).ProtoReflect().Descriptor().Oneofs().ByName("action").Fields()
	for i := 0; i < fields.Len(); i++ {
		a := protowire.AppendBytes(protowire.AppendTag(nil, fields.Get(i).Number(), protowire.BytesType), nil)
		if _, err := DecodeHookResult(a); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < fields.Len(); j++ {
			b := protowire.AppendBytes(protowire.AppendTag(append([]byte(nil), a...), fields.Get(j).Number(), protowire.BytesType), nil)
			if _, err := DecodeHookResult(b); err == nil {
				t.Fatalf("accepted arms %s and %s", fields.Get(i).Name(), fields.Get(j).Name())
			}
		}
	}
}

func TestValidateWireRepeatedNumeric(t *testing.T) {
	kinds := []descriptorpb.FieldDescriptorProto_Type{
		descriptorpb.FieldDescriptorProto_TYPE_DOUBLE, descriptorpb.FieldDescriptorProto_TYPE_FLOAT,
		descriptorpb.FieldDescriptorProto_TYPE_INT64, descriptorpb.FieldDescriptorProto_TYPE_UINT64,
		descriptorpb.FieldDescriptorProto_TYPE_INT32, descriptorpb.FieldDescriptorProto_TYPE_FIXED64,
		descriptorpb.FieldDescriptorProto_TYPE_FIXED32, descriptorpb.FieldDescriptorProto_TYPE_BOOL,
		descriptorpb.FieldDescriptorProto_TYPE_UINT32, descriptorpb.FieldDescriptorProto_TYPE_ENUM,
		descriptorpb.FieldDescriptorProto_TYPE_SFIXED32, descriptorpb.FieldDescriptorProto_TYPE_SFIXED64,
		descriptorpb.FieldDescriptorProto_TYPE_SINT32, descriptorpb.FieldDescriptorProto_TYPE_SINT64,
	}
	for _, kind := range kinds {
		for _, packed := range []bool{false, true} {
			field := &descriptorpb.FieldDescriptorProto{Name: proto.String("values"), Number: proto.Int32(1), Label: descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(), Type: kind.Enum(), Options: &descriptorpb.FieldOptions{Packed: proto.Bool(packed)}}
			if kind == descriptorpb.FieldDescriptorProto_TYPE_ENUM {
				field.TypeName = proto.String(".test.E")
			}
			file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{Name: proto.String("packed.proto"), Package: proto.String("test"), Syntax: proto.String("proto3"), EnumType: []*descriptorpb.EnumDescriptorProto{{Name: proto.String("E"), Value: []*descriptorpb.EnumValueDescriptorProto{{Name: proto.String("ZERO"), Number: proto.Int32(0)}}}}, MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("M"), Field: []*descriptorpb.FieldDescriptorProto{field}}}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			md := file.Messages().Get(0)
			typ := wireTypeFor(md.Fields().Get(0))
			var value, malformed []byte
			switch typ {
			case protowire.VarintType:
				value = protowire.AppendVarint(nil, 1)
				malformed = []byte{0x80}
			case protowire.Fixed32Type:
				value = protowire.AppendFixed32(nil, 1)
				malformed = []byte{1, 2, 3}
			case protowire.Fixed64Type:
				value = protowire.AppendFixed64(nil, 1)
				malformed = []byte{1, 2, 3, 4, 5, 6, 7}
			default:
				t.Fatal(typ)
			}
			unpacked := append(protowire.AppendTag(nil, 1, typ), value...)
			packedWire := protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), append(append([]byte(nil), value...), value...))
			for _, wire := range [][]byte{unpacked, packedWire, append(append([]byte(nil), unpacked...), packedWire...), protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), nil)} {
				if err := ValidateWire(wire, md); err != nil {
					t.Fatalf("%s packed=%v: %v", kind, packed, err)
				}
				if err := proto.Unmarshal(wire, dynamicpb.NewMessage(md)); err != nil {
					t.Fatal(err)
				}
			}
			bad := protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), malformed)
			if err := ValidateWire(bad, md); err == nil {
				t.Fatalf("%s packed=%v: accepted malformed packed payload", kind, packed)
			}
			if err := ValidateWire([]byte{0x0a, 0x80}, md); err == nil {
				t.Fatal("accepted truncated packed length")
			}
		}
	}
}

func TestValidateWireRecursiveDepth(t *testing.T) {
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{Name: proto.String("recursive.proto"), Syntax: proto.String("proto3"), MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Node"), Field: []*descriptorpb.FieldDescriptorProto{{Name: proto.String("child"), Number: proto.Int32(1), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: proto.String(".Node")}}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var md protoreflect.MessageDescriptor = file.Messages().Get(0)
	var wire []byte
	for i := 1; i < 100; i++ {
		wire = protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), wire)
	}
	if err := ValidateWire(wire, md); err != nil {
		t.Fatal(err)
	}
	wire = protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), wire)
	if err := ValidateWire(wire, md); err == nil || !strings.Contains(err.Error(), "nesting exceeds") {
		t.Fatalf("expected depth refusal, got %v", err)
	}
}
