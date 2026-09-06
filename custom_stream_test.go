//go:build !wasip1

package plugin_sdk

import (
	"context"
	"testing"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

func freeformStream(index int32, input string, sig string) []*pbv1.StreamEvent {
	first, second := input[:len(input)/2], input[len(input)/2:]
	return []*pbv1.StreamEvent{
		{Event: &pbv1.StreamEvent_ContentBlockStart{ContentBlockStart: &pbv1.ContentBlockStart{
			Index: index,
			Block: &pbv1.ContentBlockStart_ToolCall{ToolCall: &pbv1.ToolCallRef{
				Id: "call", Name: "shell", Signature: sig,
				InvocationKind: pbv1.ToolInvocationKind_TOOL_INVOCATION_KIND_FREEFORM,
			}},
		}}},
		{Event: &pbv1.StreamEvent_ToolCallDelta{ToolCallDelta: &pbv1.ToolCallDelta{Index: index, InputTextDelta: &first}}},
		{Event: &pbv1.StreamEvent_ToolCallDelta{ToolCallDelta: &pbv1.ToolCallDelta{Index: index, InputTextDelta: &second}}},
		{Event: &pbv1.StreamEvent_ContentBlockStop{ContentBlockStop: &pbv1.ContentBlockStop{Index: index}}},
	}
}

func TestStreamAssemblerFreeformInputIsPresenceSensitive(t *testing.T) {
	for _, input := range []string{"", "printf '%s' hello"} {
		t.Run(input, func(t *testing.T) {
			m := newMetaHost()
			withMetaHost(m, func() {
				asm := NewStreamAssembler().WithToolAssembly()
				var complete *ToolCall
				for _, event := range freeformStream(4, input, "signed") {
					result := asm.Feed(event)
					if result.Err != nil || !result.Suppress {
						t.Fatalf("feed result %+v", result)
					}
					if result.Complete != nil {
						complete = result.Complete
					}
				}
				if complete == nil || complete.InvocationKind != pbv1.ToolInvocationKind_TOOL_INVOCATION_KIND_FREEFORM ||
					complete.InputText == nil || *complete.InputText != input || complete.Arguments != "" {
					t.Fatalf("assembled %+v", complete)
				}
			})
		})
	}
}

func TestEmitAssembledFreeformInputPreservesOrClearsSignature(t *testing.T) {
	original := "echo original"
	call := ToolCall{
		Index: 5, ID: "call", Name: "shell", Signature: "signed",
		InvocationKind: pbv1.ToolInvocationKind_TOOL_INVOCATION_KIND_FREEFORM,
		InputText:      &original,
	}
	for _, tc := range []struct {
		name, input, wantSig string
	}{
		{"pass", original, "signed"},
		{"replace", "echo safe", ""},
		{"replace with explicit empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := EmitAssembledToolCall(call, tc.input)
			ref := events[0].GetContentBlockStart().GetToolCall()
			delta := events[1].GetToolCallDelta()
			if ref.GetInvocationKind() != pbv1.ToolInvocationKind_TOOL_INVOCATION_KIND_FREEFORM || ref.GetSignature() != tc.wantSig {
				t.Fatalf("ref %+v", ref)
			}
			if delta.InputTextDelta == nil || *delta.InputTextDelta != tc.input || delta.ArgumentsDelta != "" {
				t.Fatalf("delta %+v", delta)
			}
		})
	}
}

func TestStreamHandlerFreeformReplacementAndWrongFamilyFailOpen(t *testing.T) {
	for _, tc := range []struct {
		name    string
		action  ToolCallAction
		want    string
		wantSig string
	}{
		{"freeform replacement", ReplaceToolInput("echo safe"), "echo safe", ""},
		{"function replacement is wrong family", ReplaceToolArguments(`{"x":1}`), "echo original", "signed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMetaHost()
			withMetaHost(m, func() {
				h := NewStreamHandler().OnToolCall(func(_ context.Context, call ToolCall) (ToolCallAction, error) {
					if call.InputText == nil || *call.InputText != "echo original" || call.Arguments != "" {
						t.Fatalf("callback call %+v", call)
					}
					return tc.action, nil
				})
				var result StreamResult
				for _, event := range freeformStream(1, "echo original", "signed") {
					var err error
					result, err = h.Handle(context.Background(), event)
					if err != nil {
						t.Fatal(err)
					}
				}
				events := result.inner.GetEmitEvents().GetEvents()
				if len(events) != 3 {
					t.Fatalf("emitted %d events", len(events))
				}
				if delta := events[1].GetToolCallDelta(); delta.InputTextDelta == nil || *delta.InputTextDelta != tc.want {
					t.Fatalf("delta %+v", delta)
				}
				if got := events[0].GetContentBlockStart().GetToolCall().GetSignature(); got != tc.wantSig {
					t.Fatalf("signature %q, want %q", got, tc.wantSig)
				}
			})
		})
	}
}
