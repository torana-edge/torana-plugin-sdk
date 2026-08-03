package v2

// Reflection-backed inventory tests for the request-replacement rules.
//
// These live in package v2 (not v2_test) so they can inspect the private
// rule tables directly; the tables stay unexported — this PR establishes no
// plugin-author use case for a public rule accessor.
//
// Exactness is BIDIRECTIONAL: every descriptor field of the four
// request-visible messages must have exactly one declared rule, and every
// declared rule must name a real field. A removed/renamed field leaves a
// stale entry and fails here, just as an additive field fails until a rule
// is decided. Rule classes are also validated against descriptor
// kind/cardinality so a rule cannot silently drift from its field.

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestReplacementFieldInventoryExact(t *testing.T) {
	declared := map[string]string{}
	for f, r := range requestJSONFields {
		s := "json-" + r.shape
		if r.required {
			s += "-required"
		}
		declared[f] = s
	}
	for f, r := range requestScalarRules {
		if _, dup := declared[f]; dup {
			t.Fatalf("field %s declared in both rule tables", f)
		}
		declared[f] = r
	}

	// Direction 1: every descriptor field has exactly one declared rule.
	instances := []proto.Message{&ChatRequest{}, &Message{}, &ToolCall{}, &ToolDef{}}
	descriptors := map[string]protoreflect.MessageDescriptor{}
	for _, inst := range instances {
		md := inst.ProtoReflect().Descriptor()
		descriptors[string(md.FullName())] = md
		fields := md.Fields()
		for i := 0; i < fields.Len(); i++ {
			fd := fields.Get(i)
			full := string(fd.FullName())
			rule, ok := declared[full]
			if !ok {
				t.Fatalf("field %s has no declared replacement rule; add one", full)
			}
			checkRuleMatchesDescriptor(t, fd, rule)
		}
	}

	// Direction 2: every declared rule names a real descriptor field.
	for full := range declared {
		msgName, fieldName := splitFullName(full)
		md, ok := descriptors[msgName]
		if !ok {
			t.Fatalf("rule declares unknown message %s", msgName)
		}
		fd := md.Fields().ByName(protoreflect.Name(fieldName))
		if fd == nil {
			t.Fatalf("rule declares unknown field %s", full)
		}
	}
}

// splitFullName splits "torana.v2.ChatRequest.model" at the last dot.
func splitFullName(full string) (msg, field string) {
	for i := len(full) - 1; i >= 0; i-- {
		if full[i] == '.' {
			return full[:i], full[i+1:]
		}
	}
	return "", full
}

// checkRuleMatchesDescriptor validates that a rule class agrees with the
// descriptor's kind and cardinality.
func checkRuleMatchesDescriptor(t *testing.T, fd protoreflect.FieldDescriptor, rule string) {
	t.Helper()
	full := string(fd.FullName())
	switch {
	case rule == "json-object" || rule == "json-array" ||
		rule == "json-object-required" || rule == "json-array-required":
		if fd.Kind() != protoreflect.BytesKind || fd.IsList() {
			t.Fatalf("field %s: JSON rule %s requires a singular bytes field", full, rule)
		}
	case rule == "repeated-message-nonnil":
		if !fd.IsList() || fd.Kind() != protoreflect.MessageKind {
			t.Fatalf("field %s: rule %s requires a repeated message field", full, rule)
		}
	case rule == "repeated-text":
		if !fd.IsList() || fd.Kind() != protoreflect.StringKind {
			t.Fatalf("field %s: rule %s requires a repeated string field", full, rule)
		}
	case rule == "text" || rule == "text-required":
		if fd.IsList() || fd.Kind() != protoreflect.StringKind {
			t.Fatalf("field %s: rule %s requires a singular string field", full, rule)
		}
	case rule == "bool":
		if fd.IsList() || fd.Kind() != protoreflect.BoolKind {
			t.Fatalf("field %s: rule %s requires a bool field", full, rule)
		}
	case rule == "int32-positive-optional":
		if fd.IsList() || fd.Kind() != protoreflect.Int32Kind || !fd.HasOptionalKeyword() {
			t.Fatalf("field %s: rule %s requires an optional int32 field", full, rule)
		}
	case rule == "float-finite-optional":
		if fd.IsList() || fd.Kind() != protoreflect.DoubleKind || !fd.HasOptionalKeyword() {
			t.Fatalf("field %s: rule %s requires an optional double field", full, rule)
		}
	default:
		t.Fatalf("field %s: unrecognised rule class %q", full, rule)
	}
}
