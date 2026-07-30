package v2_test

import (
	"strings"
	"testing"

	v2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

func TestNegativeStreamIndexesAreRejected(t *testing.T) {
	cases := []struct {
		name string
		ev   *v2.StreamEvent
		want string
	}{
		{
			name: "content block start",
			ev: &v2.StreamEvent{Event: &v2.StreamEvent_ContentBlockStart{
				ContentBlockStart: &v2.ContentBlockStart{
					Index: -1,
					Block: &v2.ContentBlockStart_Text{Text: &v2.TextBlock{}},
				},
			}},
			want: "negative",
		},
		{
			name: "content block stop",
			ev: &v2.StreamEvent{Event: &v2.StreamEvent_ContentBlockStop{
				ContentBlockStop: &v2.ContentBlockStop{Index: -1},
			}},
			want: "negative",
		},
		{
			name: "tool call delta",
			ev: &v2.StreamEvent{Event: &v2.StreamEvent_ToolCallDelta{
				ToolCallDelta: &v2.ToolCallDelta{Index: -1, ArgumentsDelta: "{}"},
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

	ok := &v2.StreamEvent{Event: &v2.StreamEvent_ContentBlockStart{
		ContentBlockStart: &v2.ContentBlockStart{
			Index: 0,
			Block: &v2.ContentBlockStart_Text{Text: &v2.TextBlock{}},
		},
	}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPStatusRange(t *testing.T) {
	for _, status := range []int32{0, 99, 600, -1} {
		err := (&v2.HttpResponse{Status: status}).Validate()
		if err == nil || !strings.Contains(err.Error(), "100") {
			t.Fatalf("status %d: got %v", status, err)
		}
		err = (&v2.BlockRequestArgs{Status: status, Code: "x"}).Validate()
		if err == nil || !strings.Contains(err.Error(), "100") {
			t.Fatalf("block status %d: got %v", status, err)
		}
	}
	for _, status := range []int32{100, 200, 404, 422, 599} {
		if err := (&v2.HttpResponse{Status: status}).Validate(); err != nil {
			t.Fatalf("status %d: %v", status, err)
		}
		if err := (&v2.BlockRequestArgs{Status: status, Code: "x"}).Validate(); err != nil {
			t.Fatalf("block status %d: %v", status, err)
		}
	}
}

func TestNegativeTickActionsAreRejected(t *testing.T) {
	if err := (&v2.TickOutcome{Actions: -1}).Validate(); err == nil {
		t.Fatal("negative actions must fail")
	}
	if err := (&v2.TickOutcome{Actions: 0}).Validate(); err != nil {
		t.Fatal(err)
	}
	bad := &v2.HookResult{Action: &v2.HookResult_TickOutcome{
		TickOutcome: &v2.TickOutcome{Actions: -3, Note: "x"},
	}}
	if err := bad.ValidateFor(v2.Hook_HOOK_ON_TICK); err == nil {
		t.Fatal("hook result with negative tick actions must fail")
	}
}

func TestServeHTTPValidatesStatus(t *testing.T) {
	bad := &v2.HookResult{Action: &v2.HookResult_ServeHttp{
		ServeHttp: &v2.HttpResponse{Status: 0},
	}}
	if err := bad.ValidateFor(v2.Hook_HOOK_ON_HTTP_REQUEST); err == nil {
		t.Fatal("serve_http with status 0 must fail")
	}
	ok := &v2.HookResult{Action: &v2.HookResult_ServeHttp{
		ServeHttp: &v2.HttpResponse{Status: 204},
	}}
	if err := ok.ValidateFor(v2.Hook_HOOK_ON_HTTP_REQUEST); err != nil {
		t.Fatal(err)
	}
}

func TestRemainingTypedNilStreamAndBlockVariants(t *testing.T) {
	for _, tc := range []struct {
		name  string
		check func() error
	}{
		{"text delta wrapper", func() error {
			return (&v2.StreamEvent{Event: (*v2.StreamEvent_TextDelta)(nil)}).Validate()
		}},
		{"thinking delta wrapper", func() error {
			return (&v2.StreamEvent{Event: (*v2.StreamEvent_ThinkingDelta)(nil)}).Validate()
		}},
		{"signature delta wrapper", func() error {
			return (&v2.StreamEvent{Event: (*v2.StreamEvent_SignatureDelta)(nil)}).Validate()
		}},
		{"tool call delta nil", func() error {
			return (&v2.StreamEvent{Event: &v2.StreamEvent_ToolCallDelta{ToolCallDelta: nil}}).Validate()
		}},
		{"usage nil", func() error {
			return (&v2.StreamEvent{Event: &v2.StreamEvent_Usage{Usage: nil}}).Validate()
		}},
		{"error nil", func() error {
			return (&v2.StreamEvent{Event: &v2.StreamEvent_Error{Error: nil}}).Validate()
		}},
		{"message start nil", func() error {
			return (&v2.StreamEvent{Event: &v2.StreamEvent_MessageStart{MessageStart: nil}}).Validate()
		}},
		{"message stop nil", func() error {
			return (&v2.StreamEvent{Event: &v2.StreamEvent_MessageStop{MessageStop: nil}}).Validate()
		}},
		{"content block stop nil", func() error {
			return (&v2.StreamEvent{Event: &v2.StreamEvent_ContentBlockStop{ContentBlockStop: nil}}).Validate()
		}},
		{"content block start text nil", func() error {
			return (&v2.ContentBlockStart{Index: 0, Block: (*v2.ContentBlockStart_Text)(nil)}).Validate()
		}},
		{"content block start thinking nil", func() error {
			return (&v2.ContentBlockStart{Index: 0, Block: (*v2.ContentBlockStart_Thinking)(nil)}).Validate()
		}},
		{"content block start text nested nil", func() error {
			return (&v2.ContentBlockStart{Index: 0, Block: &v2.ContentBlockStart_Text{Text: nil}}).Validate()
		}},
		{"HookResult replace response", func() error {
			return (&v2.HookResult{Action: (*v2.HookResult_ReplaceResponse)(nil)}).
				ValidateFor(v2.Hook_HOOK_AFTER_RESPONSE)
		}},
		{"HookResult serve http", func() error {
			return (&v2.HookResult{Action: (*v2.HookResult_ServeHttp)(nil)}).
				ValidateFor(v2.Hook_HOOK_ON_HTTP_REQUEST)
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
