package v1_test

import (
	"strings"
	"testing"

	v1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

func TestNegativeStreamIndexesAreRejected(t *testing.T) {
	cases := []struct {
		name string
		ev   *v1.StreamEvent
		want string
	}{
		{
			name: "content block start",
			ev: &v1.StreamEvent{Event: &v1.StreamEvent_ContentBlockStart{
				ContentBlockStart: &v1.ContentBlockStart{
					Index: -1,
					Block: &v1.ContentBlockStart_Text{Text: &v1.TextBlock{}},
				},
			}},
			want: "negative",
		},
		{
			name: "content block stop",
			ev: &v1.StreamEvent{Event: &v1.StreamEvent_ContentBlockStop{
				ContentBlockStop: &v1.ContentBlockStop{Index: -1},
			}},
			want: "negative",
		},
		{
			name: "tool call delta",
			ev: &v1.StreamEvent{Event: &v1.StreamEvent_ToolCallDelta{
				ToolCallDelta: &v1.ToolCallDelta{Index: -1, ArgumentsDelta: "{}"},
			}},
			want: "negative",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.ev.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want substring %q", err, tc.want)
			}
		})
	}

	ok := &v1.StreamEvent{Event: &v1.StreamEvent_ContentBlockStart{
		ContentBlockStart: &v1.ContentBlockStart{
			Index: 0,
			Block: &v1.ContentBlockStart_Text{Text: &v1.TextBlock{}},
		},
	}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPStatusRange(t *testing.T) {
	// Shared out-of-band values.
	for _, status := range []int32{0, 99, 600, -1} {
		if err := (&v1.HttpResponse{Status: status}).Validate(); err == nil {
			t.Fatalf("HttpResponse status %d must fail", status)
		}
		if err := (&v1.BlockRequestArgs{Status: status, Code: "x"}).Validate(); err == nil {
			t.Fatalf("BlockRequestArgs status %d must fail", status)
		}
	}
	// Informational and other non-final codes are invalid for both APIs.
	for _, status := range []int32{100, 199} {
		if err := (&v1.HttpResponse{Status: status}).Validate(); err == nil {
			t.Fatalf("HttpResponse status %d must fail", status)
		}
		if err := (&v1.BlockRequestArgs{Status: status, Code: "x"}).Validate(); err == nil {
			t.Fatalf("BlockRequestArgs status %d must fail", status)
		}
	}
	// HttpResponse: final 200–599.
	for _, status := range []int32{200, 404, 422, 599} {
		if err := (&v1.HttpResponse{Status: status}).Validate(); err != nil {
			t.Fatalf("HttpResponse status %d: %v", status, err)
		}
	}
	// BlockRequestArgs: rejection 400–599 only.
	for _, status := range []int32{200, 399} {
		if err := (&v1.BlockRequestArgs{Status: status, Code: "x"}).Validate(); err == nil {
			t.Fatalf("BlockRequestArgs status %d must fail", status)
		}
	}
	for _, status := range []int32{400, 422, 599} {
		if err := (&v1.BlockRequestArgs{Status: status, Code: "x"}).Validate(); err != nil {
			t.Fatalf("BlockRequestArgs status %d: %v", status, err)
		}
	}
}

func TestHttpResponseBodylessStatuses(t *testing.T) {
	for _, status := range []int32{204, 205, 304} {
		if err := (&v1.HttpResponse{Status: status}).Validate(); err != nil {
			t.Fatalf("status %d with empty body must pass: %v", status, err)
		}
		err := (&v1.HttpResponse{Status: status, Body: []byte("x")}).Validate()
		if err == nil || !strings.Contains(err.Error(), "must not carry a body") {
			t.Fatalf("status %d with body: got %v", status, err)
		}
		bad := &v1.HookResult{Action: &v1.HookResult_ServeHttp{
			ServeHttp: &v1.HttpResponse{Status: status, Body: []byte("leak")},
		}}
		if err := bad.ValidateFor(v1.Hook_HOOK_ON_HTTP_REQUEST); err == nil {
			t.Fatalf("serve_http status %d with body must fail via ValidateFor", status)
		}
		ok := &v1.HookResult{Action: &v1.HookResult_ServeHttp{
			ServeHttp: &v1.HttpResponse{Status: status},
		}}
		if err := ok.ValidateFor(v1.Hook_HOOK_ON_HTTP_REQUEST); err != nil {
			t.Fatalf("serve_http status %d empty body: %v", status, err)
		}
	}
}

func TestNegativeTickActionsAreRejected(t *testing.T) {
	if err := (&v1.TickOutcome{Actions: -1}).Validate(); err == nil {
		t.Fatal("negative actions must fail")
	}
	if err := (&v1.TickOutcome{Actions: 0}).Validate(); err != nil {
		t.Fatal(err)
	}
	bad := &v1.HookResult{Action: &v1.HookResult_TickOutcome{
		TickOutcome: &v1.TickOutcome{Actions: -3, Note: "x"},
	}}
	if err := bad.ValidateFor(v1.Hook_HOOK_ON_TICK); err == nil {
		t.Fatal("hook result with negative tick actions must fail")
	}
}

func TestServeHTTPValidatesStatus(t *testing.T) {
	bad := &v1.HookResult{Action: &v1.HookResult_ServeHttp{
		ServeHttp: &v1.HttpResponse{Status: 100},
	}}
	if err := bad.ValidateFor(v1.Hook_HOOK_ON_HTTP_REQUEST); err == nil {
		t.Fatal("serve_http with informational status must fail")
	}
	ok := &v1.HookResult{Action: &v1.HookResult_ServeHttp{
		ServeHttp: &v1.HttpResponse{Status: 200, Body: []byte("ok")},
	}}
	if err := ok.ValidateFor(v1.Hook_HOOK_ON_HTTP_REQUEST); err != nil {
		t.Fatal(err)
	}
}

func TestRemainingTypedNilStreamAndBlockVariants(t *testing.T) {
	for _, tc := range []struct {
		name  string
		check func() error
	}{
		{"text delta wrapper", func() error {
			return (&v1.StreamEvent{Event: (*v1.StreamEvent_TextDelta)(nil)}).Validate()
		}},
		{"thinking delta wrapper", func() error {
			return (&v1.StreamEvent{Event: (*v1.StreamEvent_ThinkingDelta)(nil)}).Validate()
		}},
		{"signature delta wrapper", func() error {
			return (&v1.StreamEvent{Event: (*v1.StreamEvent_SignatureDelta)(nil)}).Validate()
		}},
		{"tool call delta nil", func() error {
			return (&v1.StreamEvent{Event: &v1.StreamEvent_ToolCallDelta{ToolCallDelta: nil}}).Validate()
		}},
		{"usage nil", func() error {
			return (&v1.StreamEvent{Event: &v1.StreamEvent_Usage{Usage: nil}}).Validate()
		}},
		{"error nil", func() error {
			return (&v1.StreamEvent{Event: &v1.StreamEvent_Error{Error: nil}}).Validate()
		}},
		{"message start nil", func() error {
			return (&v1.StreamEvent{Event: &v1.StreamEvent_MessageStart{MessageStart: nil}}).Validate()
		}},
		{"message stop nil", func() error {
			return (&v1.StreamEvent{Event: &v1.StreamEvent_MessageStop{MessageStop: nil}}).Validate()
		}},
		{"content block stop nil", func() error {
			return (&v1.StreamEvent{Event: &v1.StreamEvent_ContentBlockStop{ContentBlockStop: nil}}).Validate()
		}},
		{"content block start text nil", func() error {
			return (&v1.ContentBlockStart{Index: 0, Block: (*v1.ContentBlockStart_Text)(nil)}).Validate()
		}},
		{"content block start thinking nil", func() error {
			return (&v1.ContentBlockStart{Index: 0, Block: (*v1.ContentBlockStart_Thinking)(nil)}).Validate()
		}},
		{"content block start text nested nil", func() error {
			return (&v1.ContentBlockStart{Index: 0, Block: &v1.ContentBlockStart_Text{Text: nil}}).Validate()
		}},
		{"HookResult replace response", func() error {
			return (&v1.HookResult{Action: (*v1.HookResult_ReplaceResponse)(nil)}).
				ValidateFor(v1.Hook_HOOK_AFTER_RESPONSE)
		}},
		{"HookResult serve http", func() error {
			return (&v1.HookResult{Action: (*v1.HookResult_ServeHttp)(nil)}).
				ValidateFor(v1.Hook_HOOK_ON_HTTP_REQUEST)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("validation panicked instead of returning an error: %v", r)
				}
			}()
			if err := tc.check(); err == nil {
				t.Error("typed-nil / nil nested variant was accepted")
			}
		})
	}
}

func TestReplaceResponseValidatesNestedToolCalls(t *testing.T) {
	okContent := "hi"
	ok := &v1.HookResult{Action: &v1.HookResult_ReplaceResponse{
		ReplaceResponse: &v1.ChatResponse{
			Message: &v1.ResponseMessage{
				Content: &okContent,
				ToolCalls: []*v1.ToolCall{{
					Id: "c1", Name: "read", ArgumentsJson: []byte(`{"p":1}`),
				}},
			},
		},
	}}
	if err := ok.ValidateFor(v1.Hook_HOOK_AFTER_RESPONSE); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		resp *v1.ChatResponse
		want string
	}{
		{
			name: "empty tool name",
			resp: &v1.ChatResponse{Message: &v1.ResponseMessage{
				ToolCalls: []*v1.ToolCall{{Id: "c1", Name: "", ArgumentsJson: []byte(`{}`)}},
			}},
			want: "empty name",
		},
		{
			name: "empty arguments json",
			resp: &v1.ChatResponse{Message: &v1.ResponseMessage{
				ToolCalls: []*v1.ToolCall{{Id: "c1", Name: "read", ArgumentsJson: nil}},
			}},
			want: "non-empty JSON object",
		},
		{
			name: "invalid arguments json",
			resp: &v1.ChatResponse{Message: &v1.ResponseMessage{
				ToolCalls: []*v1.ToolCall{{Id: "c1", Name: "read", ArgumentsJson: []byte(`{`)}},
			}},
			want: "valid JSON",
		},
		{
			name: "arguments not object",
			resp: &v1.ChatResponse{Message: &v1.ResponseMessage{
				ToolCalls: []*v1.ToolCall{{Id: "c1", Name: "read", ArgumentsJson: []byte(`[1]`)}},
			}},
			want: "JSON object",
		},
		{
			name: "nil tool call element",
			resp: &v1.ChatResponse{Message: &v1.ResponseMessage{
				ToolCalls: []*v1.ToolCall{nil},
			}},
			want: "nil",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &v1.HookResult{Action: &v1.HookResult_ReplaceResponse{ReplaceResponse: tc.resp}}
			err := r.ValidateFor(v1.Hook_HOOK_AFTER_RESPONSE)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestResponseMessageContentPresence(t *testing.T) {
	empty := ""
	msg := &v1.ResponseMessage{Content: &empty}
	if !msg.HasContent() {
		t.Fatal("empty string with presence must report HasContent")
	}
	if (&v1.ResponseMessage{}).HasContent() {
		t.Fatal("absent content must not report HasContent")
	}
}
