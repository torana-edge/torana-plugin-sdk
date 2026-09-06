package plugin_sdk_test

import (
	"testing"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"google.golang.org/protobuf/proto"
)

func textPointer(v string) *string { return &v }

func TestFreeformToolCallViewsAndReplacement(t *testing.T) {
	input := "echo hi"
	msg := &pbv1.Message{Role: "assistant", Blocks: []*pbv1.RequestBlock{{Kind: &pbv1.RequestBlock_ToolUse{ToolUse: &pbv1.RequestToolUseBlock{
		Id: "c1", Name: "exec", InputText: &input,
		InvocationKind: pbv1.ToolInvocationKind_TOOL_INVOCATION_KIND_FREEFORM,
		Signature:      "bound",
	}}}}}

	views := sdk.ToolCalls(msg)
	if len(views) != 1 || views[0].InvocationKind != pbv1.ToolInvocationKind_TOOL_INVOCATION_KIND_FREEFORM ||
		views[0].InputText == nil || *views[0].InputText != input || len(views[0].Arguments) != 0 {
		t.Fatalf("free-form view mismatch: %+v", views)
	}
	*views[0].InputText = "mutated copy"
	if got := msg.Blocks[0].GetToolUse().GetInputText(); got != input {
		t.Fatalf("view aliases request: got %q", got)
	}

	before := proto.Clone(msg).(*pbv1.Message)
	if err := sdk.ReplaceToolCall(msg, 0, sdk.ToolCallInput{
		Id: "c1", Name: "exec", InputText: &input,
		InvocationKind: pbv1.ToolInvocationKind_TOOL_INVOCATION_KIND_FREEFORM,
	}); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(msg, before) {
		t.Fatal("byte-identical free-form replacement was not a structural no-op")
	}

	changed := "echo bye"
	if err := sdk.ReplaceToolCall(msg, 0, sdk.ToolCallInput{
		Id: "c1", Name: "exec", InputText: &changed,
		InvocationKind: pbv1.ToolInvocationKind_TOOL_INVOCATION_KIND_FREEFORM,
	}); err != nil {
		t.Fatal(err)
	}
	if msg.Blocks[0].GetToolUse().Signature != "" || msg.Blocks[0].GetToolUse().GetInputText() != changed {
		t.Fatalf("changed input did not clear provenance: %+v", msg.Blocks[0].GetToolUse())
	}
}

func TestFreeformInputAndKindAreFingerprintRelevant(t *testing.T) {
	base := &pbv1.Message{Role: "assistant", Blocks: []*pbv1.RequestBlock{{Kind: &pbv1.RequestBlock_ToolUse{ToolUse: &pbv1.RequestToolUseBlock{
		Id: "c1", Name: "exec", InvocationKind: pbv1.ToolInvocationKind_TOOL_INVOCATION_KIND_FREEFORM,
		InputText: textPointer("echo hi"),
	}}}}}
	key, err := sdk.RequestBlocksFingerprint(base)
	if err != nil {
		t.Fatal(err)
	}
	changed := proto.Clone(base).(*pbv1.Message)
	changed.Blocks[0].GetToolUse().InputText = textPointer("echo bye")
	changedKey, err := sdk.RequestBlocksFingerprint(changed)
	if err != nil {
		t.Fatal(err)
	}
	if key == changedKey {
		t.Fatal("free-form input did not move request fingerprint")
	}
	changed = proto.Clone(base).(*pbv1.Message)
	changed.Blocks[0].GetToolUse().InvocationKind = pbv1.ToolInvocationKind_TOOL_INVOCATION_KIND_FUNCTION
	changedKey, err = sdk.RequestBlocksFingerprint(changed)
	if err != nil {
		t.Fatal(err)
	}
	if key == changedKey {
		t.Fatal("invocation kind did not move request fingerprint")
	}
}

func TestFreeformResultViewCarriesInvocationKind(t *testing.T) {
	msg := &pbv1.Message{Role: "tool", Blocks: []*pbv1.RequestBlock{{Kind: &pbv1.RequestBlock_ToolResult{ToolResult: &pbv1.RequestToolResultBlock{
		ToolCallId: "c1", InvocationKind: pbv1.ToolInvocationKind_TOOL_INVOCATION_KIND_FREEFORM,
		Content: []*pbv1.ToolResultContentBlock{{Kind: &pbv1.ToolResultContentBlock_Text{Text: &pbv1.ToolResultTextBlock{Text: "out"}}}},
	}}}}}
	views := sdk.ToolResults(msg)
	if len(views) != 1 || views[0].InvocationKind != pbv1.ToolInvocationKind_TOOL_INVOCATION_KIND_FREEFORM || views[0].Content[0].Text != "out" {
		t.Fatalf("free-form result view mismatch: %+v", views)
	}
}
