package v2_test

// Normative request-replacement validation matrix (written before the
// validator, per the review process). Every rule class is pinned here:
// universal structural rules (nil nesting, unknown protobuf fields, finite
// floats, non-empty identity fields) and the JSON field table (absent /
// valid / malformed / wrong top-level / duplicate keys / lone surrogate per
// field, with the documented top-level shape and absence capability).
//
// These are REPLACEMENT-OUTPUT rules: accepted host input is normalized into
// this closed domain before plugin dispatch. Tool-call ID rules apply to the
// REQUEST context only — anonymous response tool calls keep their separate
// generic ToolCall.Validate (host-owned IDs may be absent there).

import (
	"math"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	v2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

func baseRequest() *v2.ChatRequest {
	return &v2.ChatRequest{
		Model: "m",
		Messages: []*v2.Message{
			{Role: "user", Content: "hi"},
		},
	}
}

// injectUnknown appends an unknown field (tag 99, varint) to the wire bytes
// of m and returns a fresh copy carrying it.
func injectUnknown[T proto.Message](t *testing.T, m T, newOne func() T) T {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	b = protowire.AppendTag(b, 99, protowire.VarintType)
	b = protowire.AppendVarint(b, 1)
	out := newOne()
	if err := proto.Unmarshal(b, out); err != nil {
		t.Fatalf("unmarshal with unknown field: %v", err)
	}
	return out
}

func mustRejectReplacement(t *testing.T, name string, req *v2.ChatRequest) {
	t.Helper()
	if err := req.ValidateReplacement(); err == nil {
		t.Fatalf("%s: replacement accepted", name)
	}
}

func mustAcceptReplacement(t *testing.T, name string, req *v2.ChatRequest) {
	t.Helper()
	if err := req.ValidateReplacement(); err != nil {
		t.Fatalf("%s: replacement rejected: %v", name, err)
	}
}

// --- universal rules -------------------------------------------------------

func TestReplacementNilAndNestedNil(t *testing.T) {
	mustRejectReplacement(t, "nil chat", nil)
	mustRejectReplacement(t, "nil message", &v2.ChatRequest{Messages: []*v2.Message{nil}})
	mustRejectReplacement(t, "nil tool call", &v2.ChatRequest{Messages: []*v2.Message{{
		Role: "assistant", ToolCalls: []*v2.ToolCall{nil},
	}}})
	mustRejectReplacement(t, "nil tool def", &v2.ChatRequest{Tools: []*v2.ToolDef{nil}})
}

func TestReplacementUnknownFields(t *testing.T) {
	req := injectUnknown(t, baseRequest(), func() *v2.ChatRequest { return &v2.ChatRequest{} })
	mustRejectReplacement(t, "request level", req)

	msg := injectUnknown(t, &v2.Message{Role: "user", Content: "hi"}, func() *v2.Message { return &v2.Message{} })
	mustRejectReplacement(t, "message level", &v2.ChatRequest{Messages: []*v2.Message{msg}})

	tc := injectUnknown(t, &v2.ToolCall{Id: "t1", Name: "read", ArgumentsJson: []byte(`{}`)}, func() *v2.ToolCall { return &v2.ToolCall{} })
	mustRejectReplacement(t, "tool-call level", &v2.ChatRequest{Messages: []*v2.Message{{
		Role: "assistant", ToolCalls: []*v2.ToolCall{tc},
	}}})

	td := injectUnknown(t, &v2.ToolDef{Name: "read", ParametersJson: []byte(`{}`)}, func() *v2.ToolDef { return &v2.ToolDef{} })
	mustRejectReplacement(t, "tool-def level", &v2.ChatRequest{Tools: []*v2.ToolDef{td}})
}

func TestReplacementNonFiniteFloats(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value float64
	}{
		{"temperature NaN", math.NaN()},
		{"temperature +Inf", math.Inf(1)},
		{"temperature -Inf", math.Inf(-1)},
	} {
		req := baseRequest()
		req.Temperature = &tc.value
		mustRejectReplacement(t, tc.name, req)
	}
	for _, tc := range []struct {
		name  string
		value float64
	}{
		{"top_p NaN", math.NaN()},
		{"top_p +Inf", math.Inf(1)},
		{"top_p -Inf", math.Inf(-1)},
	} {
		req := baseRequest()
		req.TopP = &tc.value
		mustRejectReplacement(t, tc.name, req)
	}
	// Finite values are accepted; no provider-specific ranges are invented.
	req := baseRequest()
	v := -3.5
	req.Temperature = &v
	w := 2000.25
	req.TopP = &w
	mustAcceptReplacement(t, "finite out-of-natural-range values accepted", req)
}

func TestReplacementIdentityFields(t *testing.T) {
	// Request-context tool calls: non-empty id, name, and object arguments.
	req := baseRequest()
	req.Messages[0] = &v2.Message{Role: "assistant", ToolCalls: []*v2.ToolCall{
		{Name: "read", ArgumentsJson: []byte(`{}`)}, // missing id
	}}
	mustRejectReplacement(t, "tool call missing id", req)

	req = baseRequest()
	req.Messages[0] = &v2.Message{Role: "assistant", ToolCalls: []*v2.ToolCall{
		{Id: "t1", ArgumentsJson: []byte(`{}`)}, // missing name
	}}
	mustRejectReplacement(t, "tool call missing name", req)

	req = baseRequest()
	req.Tools = []*v2.ToolDef{{ParametersJson: []byte(`{}`)}} // missing name
	mustRejectReplacement(t, "tool def missing name", req)

	// Valid identity fields accepted.
	req = baseRequest()
	req.Messages[0] = &v2.Message{Role: "assistant", ToolCalls: []*v2.ToolCall{
		{Id: "t1", Name: "read", ArgumentsJson: []byte(`{}`)},
	}}
	req.Tools = []*v2.ToolDef{{Name: "read", ParametersJson: []byte(`{}`)}}
	mustAcceptReplacement(t, "complete identity fields", req)
}

// --- JSON field table ------------------------------------------------------

// jsonFieldCase drives the per-field matrix: the field is set on a copy of
// the request via a mutator; absent/valid must pass, malformed/wrong-top-
// level/duplicate/surrogate must fail.
func TestReplacementJSONFieldMatrix(t *testing.T) {
	dup := `{"a":1,"a":2}`
	surrogate := `{"a":"\ud800"}`
	badUTF8 := "{\"a\":\"\xff\"}"
	malformed := `{"a":`

	cases := []struct {
		name   string
		mutate func(*v2.ChatRequest, []byte)
		absent func(*v2.ChatRequest)
		// absentOK marks absent-capable fields; required fields must reject
		// empty bytes (the canonical empty shape is {} or []).
		absentOK bool
		valid    []string
		bad      []string
	}{
		{
			name:     "content_parts_json array",
			absentOK: true,
			absent: func(r *v2.ChatRequest) {
				r.Messages[0].ContentPartsJson = nil
			},
			mutate: func(r *v2.ChatRequest, b []byte) { r.Messages[0].ContentPartsJson = b },
			valid:  []string{`[]`, `[{"type":"text","text":"x"}]`},
			bad:    []string{malformed, `{}`, `"str"`, dup, surrogate, badUTF8, `null`},
		},
		{
			name:     "message cache_control_json object",
			absentOK: true,
			absent: func(r *v2.ChatRequest) {
				r.Messages[0].CacheControlJson = nil
			},
			mutate: func(r *v2.ChatRequest, b []byte) { r.Messages[0].CacheControlJson = b },
			valid:  []string{`{}`, `{"type":"ephemeral"}`},
			bad:    []string{malformed, `[]`, `"str"`, dup, surrogate, badUTF8, `null`},
		},
		{
			name: "tool call arguments_json object required",
			absent: func(r *v2.ChatRequest) {
				r.Messages[0].ToolCalls[0].ArgumentsJson = nil
			},
			mutate: func(r *v2.ChatRequest, b []byte) { r.Messages[0].ToolCalls[0].ArgumentsJson = b },
			// absentOK deliberately false: arguments are required.
			valid: []string{`{}`, `{"path":"server.go"}`, `{"big":1e999,"neg":-1e999}`},
			bad:   []string{malformed, `[]`, `"str"`, `1`, dup, surrogate, badUTF8, `null`},
		},
		{
			name: "tool def parameters_json object required",
			absent: func(r *v2.ChatRequest) {
				r.Tools[0].ParametersJson = nil
			},
			mutate: func(r *v2.ChatRequest, b []byte) { r.Tools[0].ParametersJson = b },
			// absentOK deliberately false: parameters are required.
			valid: []string{`{}`, `{"type":"object","properties":{}}`},
			bad:   []string{malformed, `[]`, `"str"`, dup, surrogate, badUTF8, `null`},
		},
		{
			name:     "tool def cache_control_json object",
			absentOK: true,
			absent: func(r *v2.ChatRequest) {
				r.Tools[0].CacheControlJson = nil
			},
			mutate: func(r *v2.ChatRequest, b []byte) { r.Tools[0].CacheControlJson = b },
			valid:  []string{`{}`, `{"type":"ephemeral"}`},
			bad:    []string{malformed, `[]`, `"str"`, dup, surrogate, badUTF8, `null`},
		},
		{
			name:     "provider_extensions_json object",
			absentOK: true,
			absent: func(r *v2.ChatRequest) {
				r.ProviderExtensionsJson = nil
			},
			mutate: func(r *v2.ChatRequest, b []byte) { r.ProviderExtensionsJson = b },
			valid:  []string{`{}`, `{"stream_options":{"include_usage":true}}`},
			bad:    []string{malformed, `[]`, `"str"`, dup, surrogate, badUTF8, `null`},
		},
		{
			name:     "safety_settings_json array",
			absentOK: true,
			absent: func(r *v2.ChatRequest) {
				r.SafetySettingsJson = nil
			},
			mutate: func(r *v2.ChatRequest, b []byte) { r.SafetySettingsJson = b },
			valid:  []string{`[]`, `[{"category":"HARM_CATEGORY_HATE","threshold":"BLOCK"}]`},
			bad:    []string{malformed, `{}`, `"str"`, dup, surrogate, badUTF8, `null`},
		},
		{
			name:     "torana_meta_json object host-owned",
			absentOK: true,
			absent: func(r *v2.ChatRequest) {
				r.ToranaMetaJson = nil
			},
			mutate: func(r *v2.ChatRequest, b []byte) { r.ToranaMetaJson = b },
			valid:  []string{`{}`, `{"_provider":"oai"}`},
			bad:    []string{malformed, `[]`, `"str"`, dup, surrogate, badUTF8, `null`},
		},
	}

	// A base request with the identity fields required by the table.
	validBase := func() *v2.ChatRequest {
		r := baseRequest()
		r.Messages[0] = &v2.Message{Role: "assistant", ToolCalls: []*v2.ToolCall{
			{Id: "t1", Name: "read", ArgumentsJson: []byte(`{}`)},
		}}
		r.Tools = []*v2.ToolDef{{Name: "read", ParametersJson: []byte(`{}`)}}
		return r
	}

	for _, c := range cases {
		t.Run(c.name+"/absent", func(t *testing.T) {
			req := validBase()
			c.absent(req)
			if c.absentOK {
				mustAcceptReplacement(t, "absent field", req)
			} else {
				mustRejectReplacement(t, "required field absent", req)
			}
		})
		for _, v := range c.valid {
			t.Run(c.name+"/valid:"+v, func(t *testing.T) {
				req := validBase()
				c.mutate(req, []byte(v))
				mustAcceptReplacement(t, "valid field", req)
			})
		}
		for _, b := range c.bad {
			t.Run(c.name+"/bad:"+b, func(t *testing.T) {
				req := validBase()
				c.mutate(req, []byte(b))
				mustRejectReplacement(t, "invalid field", req)
			})
		}
	}
}

// TestReplacementJSONNullIsNotAbsence: the canonical empty shapes are `{}`
// / `[]`; a literal JSON null is a wrong top-level value everywhere, and
// empty bytes are absence only for absent-capable fields.
func TestReplacementJSONNullIsNotAbsence(t *testing.T) {
	req := baseRequest()
	req.Messages[0] = &v2.Message{Role: "assistant", ToolCalls: []*v2.ToolCall{
		{Id: "t1", Name: "read", ArgumentsJson: []byte(`null`)},
	}}
	mustRejectReplacement(t, "arguments null", req)

	req = baseRequest()
	req.Messages[0].ContentPartsJson = []byte(`null`)
	mustRejectReplacement(t, "content parts null", req)
}

// TestHookResultValidateForReplaceRequest: HookResult.ValidateFor(BEFORE_REQUEST)
// invokes the normative replacement validator on the nested ReplaceRequest;
// other actions are unaffected.
func TestHookResultValidateForReplaceRequest(t *testing.T) {
	valid := &v2.ChatRequest{Model: "m", Messages: []*v2.Message{{Role: "user", Content: "hi"}}}
	hr := &v2.HookResult{Action: &v2.HookResult_ReplaceRequest{ReplaceRequest: valid}}
	if err := hr.ValidateFor(v2.Hook_HOOK_BEFORE_REQUEST); err != nil {
		t.Fatalf("valid replacement rejected: %v", err)
	}

	bad := &v2.ChatRequest{Model: "m", Messages: []*v2.Message{{Role: "user", Content: "hi", ContentPartsJson: []byte(`{}`)}}}
	hr = &v2.HookResult{Action: &v2.HookResult_ReplaceRequest{ReplaceRequest: bad}}
	err := hr.ValidateFor(v2.Hook_HOOK_BEFORE_REQUEST)
	if err == nil || !strings.Contains(err.Error(), "content_parts_json") {
		t.Fatalf("invalid replacement accepted or wrong error: %v", err)
	}

	// A wrong-hook dispatch still fails before content validation.
	hr = &v2.HookResult{Action: &v2.HookResult_ReplaceRequest{ReplaceRequest: valid}}
	if err := hr.ValidateFor(v2.Hook_HOOK_AFTER_RESPONSE); err == nil {
		t.Fatal("replacement dispatched to the wrong hook accepted")
	}
}

// --- reflection-backed inventory -------------------------------------------

// TestReplacementFieldInventory: every field of ChatRequest, Message,
// ToolCall, and ToolDef must have a deliberate rule class declared in the
// inventory. An additive v2 field fails this test until a rule is decided,
// so the contract cannot grow silently.
func TestReplacementFieldInventory(t *testing.T) {
	rules := v2.ReplacementFieldRules()
	// Walk each message's descriptor through a typed instance.
	instances := []proto.Message{&v2.ChatRequest{}, &v2.Message{}, &v2.ToolCall{}, &v2.ToolDef{}}
	for _, inst := range instances {
		md := inst.ProtoReflect().Descriptor()
		fields := md.Fields()
		for i := 0; i < fields.Len(); i++ {
			fd := fields.Get(i)
			full := string(fd.FullName())
			if _, ok := rules[full]; !ok {
				t.Fatalf("field %s has no declared replacement rule; add one to ReplacementFieldRules", full)
			}
		}
	}
}
