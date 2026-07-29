package v2_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	v2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// v2 exists to fix things v1 could only document its way around. Each test here
// pins one of those fixes to observable behaviour, so the contract is checked
// rather than merely described.

// v1's central defect: a payload delivered to the wrong hook decoded as an
// EMPTY message of the expected type, because protobuf treats unknown fields as
// unknown rather than as an error. Every plugin then read that as a legitimate
// empty request.
func TestMisdispatchedPayloadIsDetectable(t *testing.T) {
	// A tick payload, encoded as v1 would have sent it: bare, unlabelled.
	bare, err := proto.Marshal(&v2.TickRequest{TickId: 7, UnixMillis: 1234})
	if err != nil {
		t.Fatal(err)
	}

	// Decoded as the request hook's payload type — the v1 failure.
	var asRequest v2.ChatRequest
	if err := proto.Unmarshal(bare, &asRequest); err != nil {
		t.Fatalf("v1's failure mode is that this does NOT error: %v", err)
	}
	if len(asRequest.Messages) != 0 || asRequest.Model != "" {
		t.Fatal("precondition: the mis-decode should look like an empty request")
	}

	// v2 labels the payload, so the same mistake is visible.
	enveloped, err := proto.Marshal(&v2.HookInput{
		Hook:    v2.Hook_HOOK_ON_TICK,
		Payload: &v2.HookInput_TickRequest{TickRequest: &v2.TickRequest{TickId: 7}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var in v2.HookInput
	if err := proto.Unmarshal(enveloped, &in); err != nil {
		t.Fatal(err)
	}
	if in.Hook != v2.Hook_HOOK_ON_TICK {
		t.Fatalf("hook = %v, want HOOK_ON_TICK", in.Hook)
	}
	if _, ok := in.Payload.(*v2.HookInput_ChatRequest); ok {
		t.Fatal("a tick payload must not present itself as a chat request")
	}
}

// An unlabelled envelope must be rejected, or the discriminator buys nothing.
func TestUnlabelledInputIsInvalid(t *testing.T) {
	var in v2.HookInput
	if in.Hook != v2.Hook_HOOK_UNSPECIFIED {
		t.Fatal("the zero value of Hook must be HOOK_UNSPECIFIED so an unset hook is detectable")
	}
}

// v1 needed a `handled` bool on three of five result types purely because an
// all-defaults protobuf message marshals to zero bytes, making "suppress"
// indistinguishable from "pass through". v2 uses an explicit disposition whose
// zero value is invalid, so a frame that says nothing is a protocol error
// rather than a guess.
func TestSuppressIsDistinguishableFromPassThrough(t *testing.T) {
	suppress, err := proto.Marshal(&v2.HookResult{
		Hook:        v2.Hook_HOOK_ON_STREAM_CHUNK,
		Disposition: v2.Disposition_DISPOSITION_SUPPRESS,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(suppress) == 0 {
		t.Fatal("a suppress frame must not marshal to zero bytes — zero bytes is pass-through")
	}

	var got v2.HookResult
	if err := proto.Unmarshal(suppress, &got); err != nil {
		t.Fatal(err)
	}
	if got.Disposition != v2.Disposition_DISPOSITION_SUPPRESS {
		t.Fatalf("disposition = %v, want SUPPRESS", got.Disposition)
	}
	if got.Payload != nil {
		t.Fatal("suppress carries no payload")
	}
}

// The zero value must be the invalid one, so an under-filled frame is caught
// rather than silently meaning something.
func TestUnsetDispositionIsInvalid(t *testing.T) {
	var r v2.HookResult
	if r.Disposition != v2.Disposition_DISPOSITION_UNSPECIFIED {
		t.Fatal("the zero value of Disposition must be UNSPECIFIED")
	}
	// And an all-defaults frame still marshals to nothing, which the ABI reads
	// as pass-through at the length level — unambiguous, because it is a length
	// and not a message.
	raw, err := proto.Marshal(&r)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 0 {
		t.Fatalf("an all-defaults result should marshal to zero bytes, got %d", len(raw))
	}
}

// v1 returned host-call results as a bare string, so an empty string meant
// "granted but no value", "cache miss", "key absent", "feature unconfigured"
// and "the host refused to write an empty payload" — all at once. Telling them
// apart took 55 lines of SDK heuristics whose own comment conceded the
// ambiguity was unresolvable.
func TestEmptyValueIsNotAnError(t *testing.T) {
	ok := &v2.HostCallResult{Result: &v2.HostCallResult_Value{Value: []byte{}}}
	if ok.GetError() != nil {
		t.Fatal("a successful call with no value must not read as an error")
	}
	if v := ok.GetValue(); v == nil || len(v) != 0 {
		t.Fatalf("value = %v, want empty and present", v)
	}

	failed := &v2.HostCallResult{Result: &v2.HostCallResult_Error{
		Error: &v2.HostError{Code: v2.ErrorCode_ERROR_CODE_NOT_FOUND, Message: "no such key"},
	}}
	if failed.GetError() == nil {
		t.Fatal("an error result must read as an error")
	}
	if failed.GetError().Code != v2.ErrorCode_ERROR_CODE_NOT_FOUND {
		t.Fatalf("code = %v", failed.GetError().Code)
	}
}

// The distinction that mattered most in practice: a plugin could not tell a
// fresh durable store from one the operator never configured.
func TestNotFoundIsDistinctFromNotConfigured(t *testing.T) {
	if v2.ErrorCode_ERROR_CODE_NOT_FOUND == v2.ErrorCode_ERROR_CODE_NOT_CONFIGURED {
		t.Fatal("NOT_FOUND and NOT_CONFIGURED must be different codes")
	}
	// And neither may be the zero value, so an unset code is never mistaken for
	// a real classification.
	if v2.ErrorCode_ERROR_CODE_UNSPECIFIED != 0 {
		t.Fatal("the zero value of ErrorCode must be UNSPECIFIED")
	}
}

// Responses get a real message. In v1 the response hook received a ChatRequest
// carrying, depending on which of three code paths produced it, a synthesized
// assistant message, the outbound request history, or nothing — a difference
// the plugin could not observe.
func TestChatResponseCarriesResponseFacts(t *testing.T) {
	resp := &v2.ChatResponse{
		Model:          "claude-sonnet-4",
		Id:             "msg_01",
		Message:        &v2.Message{Role: "assistant", Content: "done"},
		FinishReason:   "end_turn",
		Usage:          &v2.Usage{InputTokens: 10, OutputTokens: 3},
		UpstreamStatus: 200,
		DurationMs:     42,
	}
	raw, err := proto.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var got v2.ChatResponse
	if err := proto.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Message.GetContent() != "done" || got.UpstreamStatus != 200 || got.Usage.GetOutputTokens() != 3 {
		t.Fatalf("response did not round-trip: %+v", &got)
	}
}

// A plugin must be able to tell that its mutations will be discarded, rather
// than discovering it by their having no effect — which is what v1 offered on
// the streaming and error response paths.
func TestObservationalDispatchIsMarked(t *testing.T) {
	in := &v2.HookInput{Hook: v2.Hook_HOOK_AFTER_RESPONSE, Mutable: false}
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var got v2.HookInput
	if err := proto.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Mutable {
		t.Fatal("an observational dispatch must report itself as not mutable")
	}
}

// Tool arguments and tool schemas stay verbatim bytes. Decoding and re-encoding
// them reorders object keys, which changes the cacheable prompt prefix and
// costs the operator a cache hit on every request a plugin touches.
func TestToolJSONSurvivesVerbatim(t *testing.T) {
	// Key order here is deliberately not alphabetical: a decode/re-encode
	// through a Go map would sort it.
	original := []byte(`{"zebra":1,"apple":2,"middle":3}`)

	raw, err := proto.Marshal(&v2.ChatRequest{
		Messages: []*v2.Message{{
			Role:      "assistant",
			ToolCalls: []*v2.ToolCall{{Id: "c1", Name: "t", ArgumentsJson: original}},
		}},
		Tools: []*v2.ToolDef{{Name: "t", ParametersJson: original}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got v2.ChatRequest
	if err := proto.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	if string(got.Messages[0].ToolCalls[0].ArgumentsJson) != string(original) {
		t.Errorf("tool arguments changed across the boundary:\n got %s\nwant %s",
			got.Messages[0].ToolCalls[0].ArgumentsJson, original)
	}
	if string(got.Tools[0].ParametersJson) != string(original) {
		t.Errorf("tool parameters changed across the boundary:\n got %s\nwant %s",
			got.Tools[0].ParametersJson, original)
	}
}
