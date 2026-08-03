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

// TestReplacementMaxTokens: when present, max_tokens must be strictly
// positive (zero and negatives are invalid on every provider surface; absent
// is fine). Written before the enforcement existed.
func TestReplacementMaxTokens(t *testing.T) {
	zero := int32(0)
	neg := int32(-1)
	min := int32(math.MinInt32)
	for _, tc := range []struct {
		name string
		v    int32
	}{
		{"zero", zero},
		{"negative", neg},
		{"MinInt32", min},
	} {
		req := baseRequest()
		req.MaxTokens = &tc.v
		mustRejectReplacement(t, tc.name, req)
	}
	pos := int32(1)
	req := baseRequest()
	req.MaxTokens = &pos
	mustAcceptReplacement(t, "positive", req)
	mustAcceptReplacement(t, "absent", baseRequest())
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

	// badRow is one invalid sample with its failure family. The family maps
	// to a stable substring of the validator's error so a wrong-shape
	// rejection cannot satisfy a duplicate/surrogate/utf8 row.
	type badRow struct {
		name   string
		sample string
		family string // expected substring of the rejection
	}
	cases := []struct {
		name   string
		mutate func(*v2.ChatRequest, []byte)
		absent func(*v2.ChatRequest)
		// absentOK marks absent-capable fields; required fields must reject
		// empty bytes (the canonical empty shape is {} or []).
		absentOK bool
		valid    []string
		bad      []badRow
	}{
		{
			name:     "content_parts_json array",
			absentOK: true,
			absent: func(r *v2.ChatRequest) {
				r.Messages[0].ContentPartsJson = nil
			},
			mutate: func(r *v2.ChatRequest, b []byte) { r.Messages[0].ContentPartsJson = b },
			valid:  []string{`[]`, `[{"type":"text","text":"x"}]`},
			bad: []badRow{
				{"malformed", malformed, "jsontext:"},
				{"wrong-shape-object", `{}`, "must be a JSON array"},
				{"wrong-shape-string", `"str"`, "must be a JSON array"},
				{"duplicate", `[{"a":1,"a":2}]`, "duplicate"},
				{"surrogate", `[{"a":"\ud800"}]`, "surrogate"},
				{"utf8", `["` + "\xff" + `"]`, "UTF-8"},
				{"null", `null`, "must be a JSON array"},
			},
		},
		{
			name:     "message cache_control_json object",
			absentOK: true,
			absent: func(r *v2.ChatRequest) {
				r.Messages[0].CacheControlJson = nil
			},
			mutate: func(r *v2.ChatRequest, b []byte) { r.Messages[0].CacheControlJson = b },
			valid:  []string{`{}`, `{"type":"ephemeral"}`},
			bad: []badRow{
				{"malformed", malformed, "jsontext:"},
				{"wrong-shape-array", `[]`, "must be a JSON object"},
				{"wrong-shape-string", `"str"`, "must be a JSON object"},
				{"duplicate", dup, "duplicate"},
				{"surrogate", surrogate, "surrogate"},
				{"utf8", badUTF8, "UTF-8"},
				{"null", `null`, "must be a JSON object"},
			},
		},
		{
			name: "tool call arguments_json object required",
			absent: func(r *v2.ChatRequest) {
				r.Messages[0].ToolCalls[0].ArgumentsJson = nil
			},
			mutate: func(r *v2.ChatRequest, b []byte) { r.Messages[0].ToolCalls[0].ArgumentsJson = b },
			// absentOK deliberately false: arguments are required.
			valid: []string{`{}`, `{"path":"server.go"}`, `{"big":1e999,"neg":-1e999}`},
			bad: []badRow{
				{"malformed", malformed, "jsontext:"},
				{"wrong-shape-array", `[]`, "must be a JSON object"},
				{"wrong-shape-number", `1`, "must be a JSON object"},
				{"duplicate", dup, "duplicate"},
				{"surrogate", surrogate, "surrogate"},
				{"utf8", badUTF8, "UTF-8"},
				{"null", `null`, "must be a JSON object"},
			},
		},
		{
			name: "tool def parameters_json object required",
			absent: func(r *v2.ChatRequest) {
				r.Tools[0].ParametersJson = nil
			},
			mutate: func(r *v2.ChatRequest, b []byte) { r.Tools[0].ParametersJson = b },
			// absentOK deliberately false: parameters are required.
			valid: []string{`{}`, `{"type":"object","properties":{}}`},
			bad: []badRow{
				{"malformed", malformed, "jsontext:"},
				{"wrong-shape-array", `[]`, "must be a JSON object"},
				{"wrong-shape-string", `"str"`, "must be a JSON object"},
				{"duplicate", dup, "duplicate"},
				{"surrogate", surrogate, "surrogate"},
				{"utf8", badUTF8, "UTF-8"},
				{"null", `null`, "must be a JSON object"},
			},
		},
		{
			name:     "tool def cache_control_json object",
			absentOK: true,
			absent: func(r *v2.ChatRequest) {
				r.Tools[0].CacheControlJson = nil
			},
			mutate: func(r *v2.ChatRequest, b []byte) { r.Tools[0].CacheControlJson = b },
			valid:  []string{`{}`, `{"type":"ephemeral"}`},
			bad: []badRow{
				{"malformed", malformed, "jsontext:"},
				{"wrong-shape-array", `[]`, "must be a JSON object"},
				{"wrong-shape-string", `"str"`, "must be a JSON object"},
				{"duplicate", dup, "duplicate"},
				{"surrogate", surrogate, "surrogate"},
				{"utf8", badUTF8, "UTF-8"},
				{"null", `null`, "must be a JSON object"},
			},
		},
		{
			name:     "provider_extensions_json object",
			absentOK: true,
			absent: func(r *v2.ChatRequest) {
				r.ProviderExtensionsJson = nil
			},
			mutate: func(r *v2.ChatRequest, b []byte) { r.ProviderExtensionsJson = b },
			valid:  []string{`{}`, `{"stream_options":{"include_usage":true}}`},
			bad: []badRow{
				{"malformed", malformed, "jsontext:"},
				{"wrong-shape-array", `[]`, "must be a JSON object"},
				{"wrong-shape-string", `"str"`, "must be a JSON object"},
				{"duplicate", dup, "duplicate"},
				{"surrogate", surrogate, "surrogate"},
				{"utf8", badUTF8, "UTF-8"},
				{"null", `null`, "must be a JSON object"},
			},
		},
		{
			name:     "safety_settings_json array",
			absentOK: true,
			absent: func(r *v2.ChatRequest) {
				r.SafetySettingsJson = nil
			},
			mutate: func(r *v2.ChatRequest, b []byte) { r.SafetySettingsJson = b },
			valid:  []string{`[]`, `[{"category":"HARM_CATEGORY_HATE","threshold":"BLOCK"}]`},
			bad: []badRow{
				{"malformed", malformed, "jsontext:"},
				{"wrong-shape-object", `{}`, "must be a JSON array"},
				{"wrong-shape-string", `"str"`, "must be a JSON array"},
				{"duplicate", `[{"a":1,"a":2}]`, "duplicate"},
				{"surrogate", `[{"a":"\ud800"}]`, "surrogate"},
				{"utf8", `["` + "\xff" + `"]`, "UTF-8"},
				{"null", `null`, "must be a JSON array"},
			},
		},
		{
			name:     "torana_meta_json object host-owned",
			absentOK: true,
			absent: func(r *v2.ChatRequest) {
				r.ToranaMetaJson = nil
			},
			mutate: func(r *v2.ChatRequest, b []byte) { r.ToranaMetaJson = b },
			valid:  []string{`{}`, `{"_provider":"oai"}`},
			bad: []badRow{
				{"malformed", malformed, "jsontext:"},
				{"wrong-shape-array", `[]`, "must be a JSON object"},
				{"wrong-shape-string", `"str"`, "must be a JSON object"},
				{"duplicate", dup, "duplicate"},
				{"surrogate", surrogate, "surrogate"},
				{"utf8", badUTF8, "UTF-8"},
				{"null", `null`, "must be a JSON object"},
			},
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
			t.Run(c.name+"/bad:"+b.name, func(t *testing.T) {
				req := validBase()
				c.mutate(req, []byte(b.sample))
				err := req.ValidateReplacement()
				if err == nil {
					t.Fatalf("%s (%s): invalid field accepted", b.name, b.family)
				}
				if !strings.Contains(err.Error(), b.family) {
					t.Fatalf("%s: rejection %q does not name family %q", b.name, err, b.family)
				}
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

	// Invalid UTF-8 in a Go-constructed string is rejected by ValidateFor
	// BEFORE proto.Marshal would refuse it — the Go-guest and handwritten-
	// wire-guest domains are the same.
	badModel := &v2.ChatRequest{Model: string([]byte{0xff})}
	hr = &v2.HookResult{Action: &v2.HookResult_ReplaceRequest{ReplaceRequest: badModel}}
	if err := hr.ValidateFor(v2.Hook_HOOK_BEFORE_REQUEST); err == nil ||
		!strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("invalid Go-side UTF-8 accepted or wrong error: %v", err)
	}

	// A valid Unicode replacement passes the same gate.
	uni := &v2.ChatRequest{Model: "héllo 日本語", Messages: []*v2.Message{{Role: "user", Content: "雪"}}}
	hr = &v2.HookResult{Action: &v2.HookResult_ReplaceRequest{ReplaceRequest: uni}}
	if err := hr.ValidateFor(v2.Hook_HOOK_BEFORE_REQUEST); err != nil {
		t.Fatalf("valid unicode replacement rejected: %v", err)
	}
}

// TestReplacementMessageRoleNonEmpty: the request-domain Message.role is
// non-empty UTF-8 — a nil Message element survives protobuf transport as a
// zero-length message that decodes to an empty non-nil Message{}, so the
// decoded form must be rejected. The catch-all role decision stays open:
// any NON-EMPTY role (known or unmodelled) is accepted.
func TestReplacementMessageRoleNonEmpty(t *testing.T) {
	// Decisive wire round trip: nil element -> zero-length wire -> empty
	// non-nil Message{} on decode -> rejected by the shared contract.
	req := &v2.ChatRequest{Messages: []*v2.Message{nil}}
	raw, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var decoded v2.ChatRequest
	if err := proto.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Messages) != 1 || decoded.Messages[0] == nil || decoded.Messages[0].Role != "" {
		t.Fatalf("decoded = %+v; want one non-nil empty message (the nil survived as zero-length)", decoded.Messages)
	}
	if err := decoded.ValidateReplacement(); err == nil {
		t.Fatal("decoded zero-length message accepted")
	}

	// Explicitly empty Message{} rejected.
	empty := &v2.ChatRequest{Messages: []*v2.Message{{}}}
	if err := empty.ValidateReplacement(); err == nil {
		t.Fatal("explicitly empty message accepted")
	}

	// Valid known roles accepted.
	for _, role := range []string{"user", "assistant", "system", "tool"} {
		r := &v2.ChatRequest{Messages: []*v2.Message{{Role: role, Content: "hi"}}}
		if err := r.ValidateReplacement(); err != nil {
			t.Fatalf("known role %q rejected: %v", role, err)
		}
	}
	// Valid NON-EMPTY unmodelled role accepted (the catch-all stays open).
	other := &v2.ChatRequest{Messages: []*v2.Message{{Role: "developer-plus", Content: "hi"}}}
	if err := other.ValidateReplacement(); err != nil {
		t.Fatalf("unmodelled non-empty role rejected: %v", err)
	}
}
