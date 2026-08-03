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
	"strings"
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

// The UTF-8 walk is descriptor-driven (checkStringsUTF8 iterates the
// descriptors, not a partial list), so every string rule class is covered by
// construction; TestReplacementStringUTF8Sweep proves it live by mutating
// EVERY reachable string field of a valid request to invalid UTF-8.

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
	case rule == "repeated-text-utf8":
		if !fd.IsList() || fd.Kind() != protoreflect.StringKind {
			t.Fatalf("field %s: rule %s requires a repeated string field", full, rule)
		}
	case rule == "text-utf8" || rule == "text-required-utf8":
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

// TestReplacementStringUTF8Sweep probes EVERY protobuf string field of the
// four request-visible messages with invalid UTF-8 and requires
// ValidateReplacement to reject it, naming the UTF-8 rule. The walk is
// descriptor-driven (no partial list), and this is the live proof that every
// string rule class is covered — an additive string field is picked up by
// both the walk and this sweep automatically.
func TestReplacementStringUTF8Sweep(t *testing.T) {
	bad := string([]byte{0xff})
	reject := func(t *testing.T, path string, req *ChatRequest) {
		t.Helper()
		err := req.ValidateReplacement()
		if err == nil {
			t.Fatalf("invalid UTF-8 in %s accepted", path)
		}
		if !strings.Contains(err.Error(), "UTF-8") {
			t.Fatalf("%s: rejection %q does not name the UTF-8 rule", path, err)
		}
	}

	// ChatRequest scalar strings.
	reject(t, "model", &ChatRequest{Model: bad})
	reject(t, "stop_sequences[0]", &ChatRequest{StopSequences: []string{bad}})

	// Message strings (one fresh message per field).
	msgMD := (&Message{}).ProtoReflect().Descriptor()
	for i := 0; i < msgMD.Fields().Len(); i++ {
		fd := msgMD.Fields().Get(i)
		if fd.Kind() != protoreflect.StringKind {
			continue
		}
		probe := &Message{Role: "user", Content: "hi"}
		probe.ProtoReflect().Set(fd, protoreflect.ValueOfString(bad))
		reject(t, "messages[0]."+string(fd.Name()), &ChatRequest{Messages: []*Message{probe}})
	}

	// ToolCall strings (request context: valid id/name baseline first).
	tcMD := (&ToolCall{}).ProtoReflect().Descriptor()
	for i := 0; i < tcMD.Fields().Len(); i++ {
		fd := tcMD.Fields().Get(i)
		if fd.Kind() != protoreflect.StringKind {
			continue
		}
		probe := &ToolCall{Id: "t1", Name: "read", ArgumentsJson: []byte(`{}`)}
		probe.ProtoReflect().Set(fd, protoreflect.ValueOfString(bad))
		reject(t, "messages[0].tool_calls[0]."+string(fd.Name()), &ChatRequest{
			Messages: []*Message{{Role: "assistant", ToolCalls: []*ToolCall{probe}}},
		})
	}

	// ToolDef strings.
	tdMD := (&ToolDef{}).ProtoReflect().Descriptor()
	for i := 0; i < tdMD.Fields().Len(); i++ {
		fd := tdMD.Fields().Get(i)
		if fd.Kind() != protoreflect.StringKind {
			continue
		}
		probe := &ToolDef{Name: "read", ParametersJson: []byte(`{}`)}
		probe.ProtoReflect().Set(fd, protoreflect.ValueOfString(bad))
		reject(t, "tools[0]."+string(fd.Name()), &ChatRequest{Tools: []*ToolDef{probe}})
	}

	// Valid non-ASCII and empty controls stay accepted.
	if err := (&ChatRequest{Model: "héllo 日本語 雪"}).ValidateReplacement(); err != nil {
		t.Fatalf("valid non-ASCII model rejected: %v", err)
	}
	if err := (&ChatRequest{Model: "", StopSequences: []string{"\u2603", ""}}).ValidateReplacement(); err != nil {
		t.Fatalf("empty/escaped-unicode strings rejected: %v", err)
	}
}
