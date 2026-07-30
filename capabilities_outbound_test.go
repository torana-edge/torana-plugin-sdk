package plugin_sdk

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// Every field of every outbound message the verifier will inspect, mapped to
// a grant section or HostOwnedField. Reflection proves coverage: a hand-written
// mutation list only demonstrates the cases someone thought of.
func TestEveryOutboundProtoFieldHasAGrantSection(t *testing.T) {
	for _, tc := range []struct {
		name     string
		msg      proto.Message
		sections map[string]string
	}{
		{"ChatResponse", &pbv2.ChatResponse{}, ChatResponseFieldSections},
		{"Response Message", &pbv2.Message{}, ResponseMessageFieldSections},
		{"Response ToolCall", &pbv2.ToolCall{}, ResponseToolCallFieldSections},
		{"Usage", &pbv2.Usage{}, UsageFieldSections},
		{"ToolCallDelta", &pbv2.ToolCallDelta{}, ToolCallDeltaFieldSections},
		{"StreamError", &pbv2.StreamError{}, StreamErrorFieldSections},
		{"MessageStart", &pbv2.MessageStart{}, MessageStartFieldSections},
		{"MessageStop", &pbv2.MessageStop{}, MessageStopFieldSections},
		{"ContentBlockStart", &pbv2.ContentBlockStart{}, ContentBlockStartFieldSections},
		{"ToolCallRef", &pbv2.ToolCallRef{}, ToolCallRefFieldSections},
		{"ProviderBlock", &pbv2.ProviderBlock{}, ProviderBlockFieldSections},
		{"ContentBlockStop", &pbv2.ContentBlockStop{}, ContentBlockStopFieldSections},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertFieldInventory(t, tc.name, tc.msg.ProtoReflect().Descriptor(), tc.sections)
		})
	}
}

func TestEveryStreamEventVariantHasAGrantSection(t *testing.T) {
	desc := (&pbv2.StreamEvent{}).ProtoReflect().Descriptor()
	oneof := desc.Oneofs().ByName("event")
	if oneof == nil {
		t.Fatal("StreamEvent has no event oneof")
	}
	seen := map[string]bool{}
	for i := 0; i < oneof.Fields().Len(); i++ {
		name := string(oneof.Fields().Get(i).Name())
		seen[name] = true
		section, ok := StreamEventVariantSections[name]
		if !ok {
			t.Errorf("StreamEvent.%s belongs to no grant section — assign it or mark it %s",
				name, HostOwnedField)
			continue
		}
		if section != HostOwnedField && !IsWritePermission(section) {
			t.Errorf("StreamEvent.%s maps to %q, which is not a write grant", name, section)
		}
	}
	for name := range StreamEventVariantSections {
		if !seen[name] {
			t.Errorf("StreamEvent.%s is mapped but no longer exists in the proto", name)
		}
	}
}

func TestOutboundWriteGrantsAreRequestable(t *testing.T) {
	if !IsWritePermission(string(SectionStreamWrite)) {
		t.Fatal("ir.stream.write must be in WritePermissions")
	}
	if !IsPermission(string(SectionStreamWrite)) {
		t.Fatal("ir.stream.write must be in Permissions")
	}
	if !IsPermission("env.set_identity") {
		t.Fatal("env.set_identity must be in Permissions — it is a v2 verdict host call")
	}
	if StreamSuppressSection() != SectionStreamWrite {
		t.Fatal("Suppress requires ir.stream.write")
	}
}

func assertFieldInventory(t *testing.T, typeName string, desc protoreflect.MessageDescriptor, sections map[string]string) {
	t.Helper()
	fields := desc.Fields()
	seen := map[string]bool{}
	for i := 0; i < fields.Len(); i++ {
		name := string(fields.Get(i).Name())
		seen[name] = true
		section, ok := sections[name]
		if !ok {
			t.Errorf("%s.%s belongs to no grant section — a plugin could change it "+
				"with no grant and no detection. Assign it, or mark it %s",
				typeName, name, HostOwnedField)
			continue
		}
		if section != HostOwnedField && !IsWritePermission(section) {
			t.Errorf("%s.%s maps to %q, which is not a write grant", typeName, name, section)
		}
	}
	for name := range sections {
		if !seen[name] {
			t.Errorf("%s.%s is mapped to a section but no longer exists in the proto",
				typeName, name)
		}
	}
}
