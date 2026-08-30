package v1

// Reflection-backed inventory tests for the request-replacement rules.
//
// These live in package v1 (not v1_test) so they can inspect the private
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
	"fmt"
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
	instances := []proto.Message{
		&ChatRequest{},
		&Message{},
		&RequestBlock{},
		&RequestTextBlock{},
		&RequestThinkingBlock{},
		&RequestRedactedThinkingBlock{},
		&RequestToolUseBlock{},
		&RequestToolResultBlock{},
		&RequestCacheBreakpoint{},
		&RequestUnknownBlock{},
		&RequestTrailingSignatureBlock{},
		&ToolResultContentBlock{},
		&ToolResultTextBlock{},
		&ToolResultUnknownBlock{},
		&ToolResultCacheBreakpoint{},
		&ToolCall{},
		&ToolDef{},
	}
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

// splitFullName splits "torana.v1.ChatRequest.model" at the last dot.
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
	case rule == "oneof-message-member":
		if fd.IsList() || fd.Kind() != protoreflect.MessageKind {
			t.Fatalf("field %s: rule %s requires a singular message field (oneof member)", full, rule)
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
	case rule == "bool-optional":
		if fd.IsList() || fd.Kind() != protoreflect.BoolKind || !fd.HasOptionalKeyword() {
			t.Fatalf("field %s: rule %s requires an optional bool field (presence-aware)", full, rule)
		}
	case rule == "text-utf8-optional":
		if fd.IsList() || fd.Kind() != protoreflect.StringKind || !fd.HasOptionalKeyword() {
			t.Fatalf("field %s: rule %s requires an optional string field (presence-aware)", full, rule)
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
// request-visible messages (the whole block tree) with invalid UTF-8 and requires
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

	// ChatRequest string fields, descriptor-driven: every singular string
	// field is probed directly and the repeated stop_sequences list is
	// probed through its list value, so an additive ChatRequest string field
	// is genuinely picked up by this sweep, matching the walk's own
	// descriptor-driven coverage.
	crMD := (&ChatRequest{}).ProtoReflect().Descriptor()
	for i := 0; i < crMD.Fields().Len(); i++ {
		fd := crMD.Fields().Get(i)
		if fd.Kind() != protoreflect.StringKind {
			continue
		}
		probe := &ChatRequest{}
		if fd.IsList() {
			list := probe.ProtoReflect().Mutable(fd).List()
			list.Append(protoreflect.ValueOfString(bad))
			reject(t, string(fd.Name())+"[0]", probe)
		} else {
			probe.ProtoReflect().Set(fd, protoreflect.ValueOfString(bad))
			reject(t, string(fd.Name()), probe)
		}
	}

	// Message body strings: one valid request exercising EVERY block kind
	// (and every nested tool-result kind), then a full-tree reflection walk
	// mutating each reachable string field to invalid UTF-8. Descriptor-
	// driven by construction: an additive string field anywhere in the
	// request tree is picked up by both the walk and this sweep.
	seed := func() *ChatRequest {
		return &ChatRequest{
			Model: "m",
			Messages: []*Message{{
				Role: "assistant",
				Blocks: []*RequestBlock{
					{Kind: &RequestBlock_Thinking{Thinking: &RequestThinkingBlock{Text: "r", Signature: "s"}}},
					{Kind: &RequestBlock_Text{Text: &RequestTextBlock{Text: "hi", Signature: "s"}}},
					{Kind: &RequestBlock_RedactedThinking{RedactedThinking: &RequestRedactedThinkingBlock{Data: "d"}}},
					{Kind: &RequestBlock_ToolUse{ToolUse: &RequestToolUseBlock{
						Id: "t1", Name: "read", ArgumentsJson: []byte(`{}`), Signature: "s",
					}}},
					{Kind: &RequestBlock_ToolResult{ToolResult: &RequestToolResultBlock{
						ToolCallId: "t1", ToolName: "read",
						Content: []*ToolResultContentBlock{
							{Kind: &ToolResultContentBlock_Text{Text: &ToolResultTextBlock{Text: "ok"}}},
							{Kind: &ToolResultContentBlock_Unknown{Unknown: &ToolResultUnknownBlock{
								Kind: "json", PayloadJson: []byte(`{}`),
							}}},
							{Kind: &ToolResultContentBlock_CacheBreakpoint{CacheBreakpoint: &ToolResultCacheBreakpoint{
								MarkerJson: []byte(`{}`),
							}}},
						},
					}}},
					{Kind: &RequestBlock_CacheBreakpoint{CacheBreakpoint: &RequestCacheBreakpoint{
						MarkerJson: []byte(`{}`),
					}}},
					{Kind: &RequestBlock_Unknown{Unknown: &RequestUnknownBlock{
						Kind: "custom", PayloadJson: []byte(`{}`),
					}}},
					{Kind: &RequestBlock_TrailingSignature{TrailingSignature: &RequestTrailingSignatureBlock{
						Signature: "s",
					}}},
				},
			}},
			Tools: []*ToolDef{{Name: "read", ParametersJson: []byte(`{}`)}},
		}
	}
	// The seed must be valid before any mutation.
	if err := seed().ValidateReplacement(); err != nil {
		t.Fatalf("UTF-8 sweep seed is not a valid replacement: %v", err)
	}
	// Collect every reachable string field path of the seed (descriptor
	// driven), then probe each one with invalid UTF-8 on a fresh clone.
	paths := collectStringPaths(seed().ProtoReflect(), "")
	if len(paths) == 0 {
		t.Fatal("sweep found no string fields to probe")
	}
	for _, p := range paths {
		probe := seed()
		mutateStringAt(probe.ProtoReflect(), p, bad)
		reject(t, strings.TrimPrefix(p.path, "."), probe)
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

// stringPath is one step in a reflection walk: a field, plus an optional
// list index.
type stringPath struct {
	fd    protoreflect.FieldDescriptor
	index int // -1 for singular fields
	path  string
}

// collectStringPaths walks the descriptor tree of a request and returns the
// path of every reachable string field (list elements probed once each).
func collectStringPaths(m protoreflect.Message, prefix string) []stringPath {
	var out []stringPath
	fields := m.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		full := prefix + "." + string(fd.Name())
		switch {
		case fd.Kind() == protoreflect.StringKind && !fd.IsList():
			out = append(out, stringPath{fd: fd, index: -1, path: full})
		case fd.Kind() == protoreflect.StringKind && fd.IsList():
			list := m.Get(fd).List()
			if list.Len() > 0 {
				out = append(out, stringPath{fd: fd, index: 0, path: full + "[0]"})
			}
		case fd.Kind() == protoreflect.MessageKind && !fd.IsList():
			sub := m.Get(fd).Message()
			if sub.IsValid() {
				out = append(out, collectStringPaths(sub, full)...)
			}
		case fd.Kind() == protoreflect.MessageKind && fd.IsList():
			list := m.Get(fd).List()
			for j := 0; j < list.Len(); j++ {
				elem := list.Get(j).Message()
				if elem.IsValid() {
					out = append(out, collectStringPaths(elem, fmt.Sprintf("%s[%d]", full, j))...)
				}
			}
		}
	}
	return out
}

// mutateStringAt applies a single stringPath to a fresh probe by replaying
// the path: singular string fields are set directly; list string fields set
// element 0; message fields recurse.
func mutateStringAt(m protoreflect.Message, target stringPath, bad string) {
	steps := splitPath(target.path)
	cur := m
	for i, step := range steps {
		fd := cur.Descriptor().Fields().ByName(protoreflect.Name(step.field))
		if fd == nil {
			panic("sweep path replay: unknown field " + step.field)
		}
		if i == len(steps)-1 {
			if fd.IsList() {
				list := cur.Mutable(fd).List()
				list.Set(0, protoreflect.ValueOfString(bad))
			} else {
				cur.Set(fd, protoreflect.ValueOfString(bad))
			}
			return
		}
		if fd.IsList() {
			cur = cur.Mutable(fd).List().Get(step.index).Message()
		} else {
			cur = cur.Mutable(fd).Message()
		}
	}
	panic("sweep path replay: path exhausted")
}

// splitPath parses "a.b[0].c" into steps.
func splitPath(path string) []struct {
	field string
	index int
} {
	var steps []struct {
		field string
		index int
	}
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			continue
		}
		if i := strings.Index(part, "["); i >= 0 {
			idx := 0
			fmt.Sscanf(part[i+1:], "%d]", &idx)
			steps = append(steps, struct {
				field string
				index int
			}{field: part[:i], index: idx})
		} else {
			steps = append(steps, struct {
				field string
				index int
			}{field: part, index: 0})
		}
	}
	return steps
}
