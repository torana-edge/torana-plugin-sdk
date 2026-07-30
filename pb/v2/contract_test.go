package v2_test

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	v2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// The generated oneof wrappers carry unexported methods, so an external test
// cannot name their interface. Build whole frames instead.
func inputFor(h v2.Hook) *v2.HookInput {
	switch h {
	case v2.Hook_HOOK_BEFORE_REQUEST:
		return &v2.HookInput{Payload: &v2.HookInput_ChatRequest{ChatRequest: &v2.ChatRequest{Model: "m"}}}
	case v2.Hook_HOOK_AFTER_RESPONSE:
		return &v2.HookInput{Payload: &v2.HookInput_ChatResponse{ChatResponse: &v2.ChatResponse{Model: "m"}}}
	case v2.Hook_HOOK_ON_STREAM_CHUNK:
		// A real event: an empty StreamEvent carries no variant and is refused,
		// which is the point of validating inputs at all.
		return &v2.HookInput{Payload: &v2.HookInput_StreamEvent{
			StreamEvent: &v2.StreamEvent{Event: &v2.StreamEvent_TextDelta{TextDelta: "x"}},
		}}
	case v2.Hook_HOOK_ON_HTTP_REQUEST:
		return &v2.HookInput{Payload: &v2.HookInput_HttpRequest{HttpRequest: &v2.HttpRequest{Method: "GET"}}}
	case v2.Hook_HOOK_ON_TICK:
		return &v2.HookInput{Payload: &v2.HookInput_TickRequest{TickRequest: &v2.TickRequest{TickId: 1}}}
	}
	return &v2.HookInput{}
}

// actionFor builds a result carrying h's own action.
func actionFor(h v2.Hook) *v2.HookResult {
	r := &v2.HookResult{}
	switch h {
	case v2.Hook_HOOK_BEFORE_REQUEST:
		r.Action = &v2.HookResult_ReplaceRequest{ReplaceRequest: &v2.ChatRequest{Model: "m"}}
	case v2.Hook_HOOK_AFTER_RESPONSE:
		r.Action = &v2.HookResult_ReplaceResponse{ReplaceResponse: &v2.ChatResponse{Model: "m"}}
	case v2.Hook_HOOK_ON_STREAM_CHUNK:
		// One event: emitting nothing is suppression, and is refused.
		r.Action = &v2.HookResult_EmitEvents{EmitEvents: &v2.StreamEvents{
			Events: []*v2.StreamEvent{{Event: &v2.StreamEvent_TextDelta{TextDelta: "x"}}},
		}}
	case v2.Hook_HOOK_ON_HTTP_REQUEST:
		r.Action = &v2.HookResult_ServeHttp{ServeHttp: &v2.HttpResponse{Status: 200}}
	case v2.Hook_HOOK_ON_TICK:
		r.Action = &v2.HookResult_TickOutcome{TickOutcome: &v2.TickOutcome{}}
	}
	return r
}

// allHooks is every hook the contract defines.
var allHooks = []v2.Hook{
	v2.Hook_HOOK_BEFORE_REQUEST,
	v2.Hook_HOOK_AFTER_RESPONSE,
	v2.Hook_HOOK_ON_STREAM_CHUNK,
	v2.Hook_HOOK_ON_HTTP_REQUEST,
	v2.Hook_HOOK_ON_TICK,
}

// v2 exists to fix things v1 could only document its way around. Each test here
// pins one of those fixes to observable behaviour, so the contract is checked
// rather than merely described.

// v1's central defect: a payload delivered to the wrong hook decoded as an
// EMPTY message of the expected type, because protobuf treats unknown fields as
// unknown rather than as an error. Every plugin then read that as a legitimate
// empty request.
//
// v2 makes the payload the sole discriminator, so a frame cannot claim one hook
// while carrying another's. An earlier draft of this file carried a hook enum
// ALONGSIDE the payload, which made HOOK_BEFORE_REQUEST + tick_request perfectly
// valid protobuf — the guarantee then rested on someone remembering to compare
// two fields, and this test only observed them.
func TestPayloadIsTheSoleDiscriminator(t *testing.T) {
	// The v1 failure, still reproducible with a bare message.
	bare, err := proto.Marshal(&v2.TickRequest{TickId: 7, UnixMillis: 1234})
	if err != nil {
		t.Fatal(err)
	}
	var asRequest v2.ChatRequest
	if err := proto.Unmarshal(bare, &asRequest); err != nil {
		t.Fatalf("v1's failure mode is that this does NOT error: %v", err)
	}
	if len(asRequest.Messages) != 0 || asRequest.Model != "" {
		t.Fatal("precondition: the mis-decode should look like an empty request")
	}

	// v2: the hook comes from the payload, so there is nothing to contradict.
	for _, want := range allHooks {
		t.Run(want.String(), func(t *testing.T) {
			raw, err := proto.Marshal(inputFor(want))
			if err != nil {
				t.Fatal(err)
			}
			var got v2.HookInput
			if err := proto.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if h := got.HookOf(); h != want {
				t.Fatalf("HookOf = %v, want %v", h, want)
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("well-formed input rejected: %v", err)
			}
		})
	}
}

// The export a plugin was called through is a second discriminator, and nothing
// in the envelope forces the two to agree: a tick payload delivered to
// run_before_request is a well-formed input to the wrong hook. Removing the hook
// field stopped a frame contradicting ITSELF; this is the other half.
func TestInputPayloadMustMatchTheInvokedHook(t *testing.T) {
	for _, invoked := range allHooks {
		for _, carried := range allHooks {
			err := inputFor(carried).ValidateFor(invoked)
			if invoked == carried {
				if err != nil {
					t.Errorf("%v with its own payload rejected: %v", invoked, err)
				}
				continue
			}
			if err == nil {
				t.Errorf("%v accepted a %v payload — input misdispatch is not caught",
					invoked, carried)
			}
		}
	}
}

// An input with no payload names no hook, and must be rejected rather than
// dispatched to a guess.
func TestInputWithoutPayloadIsRejected(t *testing.T) {
	var in v2.HookInput
	if h := in.HookOf(); h != v2.Hook_HOOK_UNSPECIFIED {
		t.Fatalf("HookOf on an empty input = %v, want HOOK_UNSPECIFIED", h)
	}
	if err := in.Validate(); err == nil {
		t.Fatal("an input carrying no payload must be rejected")
	}
	for _, h := range allHooks {
		if err := in.ValidateFor(h); err == nil {
			t.Errorf("%v accepted an input with no payload", h)
		}
	}
}

// Every hook/payload combination, including all twenty wrong ones. A result
// carrying another hook's payload is the misdispatch this contract claims to
// catch, so it is worth enumerating rather than sampling.
func TestResultPayloadMustMatchTheDispatchedHook(t *testing.T) {
	for _, dispatched := range allHooks {
		for _, carried := range allHooks {
			err := actionFor(carried).ValidateFor(dispatched)
			if dispatched == carried {
				if err != nil {
					t.Errorf("%v with its own payload rejected: %v", dispatched, err)
				}
				continue
			}
			if err == nil {
				t.Errorf("%v accepted a %v payload — misdispatch is not caught", dispatched, carried)
			}
		}
	}
}

// Suppression must be expressible without being confusable with pass-through.
//
// v1 needed a `handled` bool on three of five result types because an
// all-defaults message marshals to zero bytes. v2 makes suppression an action,
// and an empty message inside a oneof still marshals to a tag and a zero
// length — so it is distinguishable, and no flag is needed.
func TestSuppressIsDistinguishableFromPassThrough(t *testing.T) {
	suppress, err := proto.Marshal(&v2.HookResult{
		Action: &v2.HookResult_Suppress{Suppress: &v2.Suppress{}},
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
	if got.GetSuppress() == nil {
		t.Fatalf("suppress did not survive the wire: %+v", &got)
	}
	if err := got.ValidateFor(v2.Hook_HOOK_ON_STREAM_CHUNK); err != nil {
		t.Fatalf("the host would reject this: %v", err)
	}
}

// Every way a result can be malformed must be rejected, not interpreted. These
// used to be two tests that read enum zero values and demonstrated no rejection
// at all.
func TestMalformedResultsAreRejected(t *testing.T) {
	ev := func() *v2.StreamEvent {
		return &v2.StreamEvent{Event: &v2.StreamEvent_TextDelta{TextDelta: "x"}}
	}

	for _, tc := range []struct {
		name   string
		hook   v2.Hook
		result *v2.HookResult
		want   string
	}{
		{
			"suppress outside stream", v2.Hook_HOOK_BEFORE_REQUEST,
			&v2.HookResult{Action: &v2.HookResult_Suppress{Suppress: &v2.Suppress{}}},
			"only a stream chunk can be suppressed",
		},
		{
			"another hook's action", v2.Hook_HOOK_BEFORE_REQUEST,
			&v2.HookResult{Action: &v2.HookResult_TickOutcome{TickOutcome: &v2.TickOutcome{}}},
			"a result must answer the hook that was dispatched",
		},
		{
			"emit no events", v2.Hook_HOOK_ON_STREAM_CHUNK,
			&v2.HookResult{Action: &v2.HookResult_EmitEvents{EmitEvents: &v2.StreamEvents{}}},
			"emitting nothing is suppression, and should say so",
		},
		{
			"emit a nil event", v2.Hook_HOOK_ON_STREAM_CHUNK,
			&v2.HookResult{Action: &v2.HookResult_EmitEvents{EmitEvents: &v2.StreamEvents{
				Events: []*v2.StreamEvent{nil}}}},
			"a list of nothing emits nothing",
		},
		{
			"emit one good and one empty event", v2.Hook_HOOK_ON_STREAM_CHUNK,
			&v2.HookResult{Action: &v2.HookResult_EmitEvents{EmitEvents: &v2.StreamEvents{
				Events: []*v2.StreamEvent{ev(), {}}}}},
			"every event is validated, not just the first",
		},
		{
			"emit a kindless content block", v2.Hook_HOOK_ON_STREAM_CHUNK,
			&v2.HookResult{Action: &v2.HookResult_EmitEvents{EmitEvents: &v2.StreamEvents{
				Events: []*v2.StreamEvent{{Event: &v2.StreamEvent_ContentBlockStart{
					ContentBlockStart: &v2.ContentBlockStart{Index: 0}}}}}}},
			"a block that names no kind cannot be assembled",
		},
		{
			"nil result", v2.Hook_HOOK_BEFORE_REQUEST, nil,
			"a nil result must not read as a valid answer",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.result.ValidateFor(tc.hook); err == nil {
				t.Fatalf("accepted: %s", tc.want)
			}
		})
	}
}

// A result with no action means the same as returning nothing: leave the input
// alone. There is one encoding of that, so it is accepted rather than treated
// as a malformed frame the host could never actually receive.
func TestResultWithNoActionIsPassThrough(t *testing.T) {
	empty := &v2.HookResult{}

	raw, err := proto.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 0 {
		t.Fatalf("an actionless result should marshal to zero bytes, got %d — "+
			"if it did not, the host could tell it apart from pass-through and "+
			"would need a rule for it", len(raw))
	}
	for _, hook := range allHooks {
		if err := empty.ValidateFor(hook); err != nil {
			t.Errorf("%v: an actionless result was rejected, but the host cannot "+
				"distinguish it from pass-through: %v", hook, err)
		}
	}
}

// The well-formed shapes must all be accepted, or the validation above is just
// a way to reject everything.
func TestWellFormedResultsAreAccepted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		hook   v2.Hook
		result *v2.HookResult
	}{
		{"replace request", v2.Hook_HOOK_BEFORE_REQUEST, actionFor(v2.Hook_HOOK_BEFORE_REQUEST)},
		{"replace response", v2.Hook_HOOK_AFTER_RESPONSE, actionFor(v2.Hook_HOOK_AFTER_RESPONSE)},
		{"serve http", v2.Hook_HOOK_ON_HTTP_REQUEST, actionFor(v2.Hook_HOOK_ON_HTTP_REQUEST)},
		{"tick outcome", v2.Hook_HOOK_ON_TICK, actionFor(v2.Hook_HOOK_ON_TICK)},
		{"suppress a stream event", v2.Hook_HOOK_ON_STREAM_CHUNK,
			&v2.HookResult{Action: &v2.HookResult_Suppress{Suppress: &v2.Suppress{}}}},
		{"fan out stream events", v2.Hook_HOOK_ON_STREAM_CHUNK,
			&v2.HookResult{Action: &v2.HookResult_EmitEvents{EmitEvents: &v2.StreamEvents{
				Events: []*v2.StreamEvent{
					{Event: &v2.StreamEvent_ToolCallDelta{ToolCallDelta: &v2.ToolCallDelta{
						Index: 0, ArgumentsDelta: `{"path":"a.go"}`}}},
					{Event: &v2.StreamEvent_ContentBlockStop{
						ContentBlockStop: &v2.ContentBlockStop{Index: 0}}},
				}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.result.ValidateFor(tc.hook); err != nil {
				t.Fatalf("well-formed result rejected: %v", err)
			}
		})
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
// than discovering it by their having no effect.
//
// This applies to run_after_response when the response was streamed or was an
// upstream error — the bytes have already gone to the caller, or there is no
// body to rewrite. run_on_stream_chunk is always mutable: replacing, suppressing
// and fanning out events is the whole point of that hook.
func TestObservationalDispatchIsMarked(t *testing.T) {
	in := &v2.HookInput{
		Payload: &v2.HookInput_ChatResponse{ChatResponse: &v2.ChatResponse{}},
		Mutable: false,
	}
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

	// A stream chunk is never observational.
	stream := &v2.HookInput{
		Payload: &v2.HookInput_StreamEvent{StreamEvent: &v2.StreamEvent{}},
		Mutable: true,
	}
	if stream.HookOf() != v2.Hook_HOOK_ON_STREAM_CHUNK || !stream.Mutable {
		t.Fatal("stream chunks are mutable: a plugin replaces, suppresses and fans out events")
	}
}

// A block's kind is a oneof, so contradictions like "tool_call with no
// metadata" or "text carrying tool metadata" cannot be written down. An earlier
// draft used a kind string beside an optional tool_call field, which allowed
// both.
func TestContentBlockKindIsTyped(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start *v2.ContentBlockStart
	}{
		{"text", &v2.ContentBlockStart{Index: 0, Block: &v2.ContentBlockStart_Text{Text: &v2.TextBlock{}}}},
		{"thinking", &v2.ContentBlockStart{Index: 1, Block: &v2.ContentBlockStart_Thinking{Thinking: &v2.ThinkingBlock{}}}},
		{"tool_call", &v2.ContentBlockStart{Index: 2, Block: &v2.ContentBlockStart_ToolCall{
			ToolCall: &v2.ToolCallRef{Id: "c1", Name: "read"}}}},
		{"provider", &v2.ContentBlockStart{Index: 3, Block: &v2.ContentBlockStart_Provider{
			Provider: &v2.ProviderBlock{Kind: "redacted"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// An empty submessage in a oneof must still survive the round trip,
			// or "text" would be indistinguishable from "no kind set".
			raw, err := proto.Marshal(&v2.StreamEvent{
				Event: &v2.StreamEvent_ContentBlockStart{ContentBlockStart: tc.start},
			})
			if err != nil {
				t.Fatal(err)
			}
			var got v2.StreamEvent
			if err := proto.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			block := got.GetContentBlockStart()
			if block == nil || block.Block == nil {
				t.Fatalf("block kind lost across the wire: %+v", &got)
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("well-formed block rejected: %v", err)
			}
		})
	}

	// Only a tool-call block can carry tool metadata; there is no way to attach
	// it to a text block, which is the point.
	text := &v2.ContentBlockStart{Block: &v2.ContentBlockStart_Text{Text: &v2.TextBlock{}}}
	if text.GetToolCall() != nil {
		t.Fatal("a text block must not be able to carry tool metadata")
	}
}

// The oneof stops a text block carrying tool metadata, but not a tool-call block
// carrying EMPTY metadata. A block that cannot say which tool it opens cannot be
// assembled: its deltas have nothing to attach to and its result has nothing to
// correlate against.
func TestBlockKindsRequireTheirMetadata(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start *v2.ContentBlockStart
		why   string
	}{
		{
			"tool call with no metadata at all",
			&v2.ContentBlockStart{Block: &v2.ContentBlockStart_ToolCall{ToolCall: &v2.ToolCallRef{}}},
			"a tool call with neither id nor name is not a tool call",
		},
		{
			"tool call with no id",
			&v2.ContentBlockStart{Block: &v2.ContentBlockStart_ToolCall{
				ToolCall: &v2.ToolCallRef{Name: "read"}}},
			"without an id the result cannot be correlated back to the call",
		},
		{
			"tool call with no name",
			&v2.ContentBlockStart{Block: &v2.ContentBlockStart_ToolCall{
				ToolCall: &v2.ToolCallRef{Id: "c1"}}},
			"without a name nothing knows which tool was invoked",
		},
		{
			"tool call wrapper present but nil",
			&v2.ContentBlockStart{Block: &v2.ContentBlockStart_ToolCall{}},
			"the variant is set but carries nothing",
		},
		{
			"provider block with no kind",
			&v2.ContentBlockStart{Block: &v2.ContentBlockStart_Provider{Provider: &v2.ProviderBlock{}}},
			"the kind is the only thing that makes a provider block actionable",
		},
		{
			"provider wrapper present but nil",
			&v2.ContentBlockStart{Block: &v2.ContentBlockStart_Provider{}},
			"the variant is set but carries nothing",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.start.Validate(); err == nil {
				t.Errorf("accepted: %s", tc.why)
			}
			// And it must be refused when it arrives inside a returned event,
			// not only when validated directly.
			ev := &v2.StreamEvent{Event: &v2.StreamEvent_ContentBlockStart{ContentBlockStart: tc.start}}
			if err := ev.Validate(); err == nil {
				t.Errorf("accepted inside a stream event: %s", tc.why)
			}
			res := &v2.HookResult{
				Action: &v2.HookResult_EmitEvents{EmitEvents: &v2.StreamEvents{
					Events: []*v2.StreamEvent{ev},
				}},
			}
			if err := res.ValidateFor(v2.Hook_HOOK_ON_STREAM_CHUNK); err == nil {
				t.Errorf("accepted inside an emit-events result: %s", tc.why)
			}
		})
	}

	// Text and thinking blocks need nothing beyond their variant.
	for _, ok := range []*v2.ContentBlockStart{
		{Block: &v2.ContentBlockStart_Text{Text: &v2.TextBlock{}}},
		{Block: &v2.ContentBlockStart_Thinking{Thinking: &v2.ThinkingBlock{}}},
		{Block: &v2.ContentBlockStart_ToolCall{ToolCall: &v2.ToolCallRef{Id: "c1", Name: "read"}}},
		{Block: &v2.ContentBlockStart_Provider{Provider: &v2.ProviderBlock{Kind: "redacted"}}},
	} {
		if err := ok.Validate(); err != nil {
			t.Errorf("well-formed block rejected: %v", err)
		}
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

// A oneof wrapper carrying a nil message is a well-formed protobuf frame that
// names a hook and then hands over nothing. Checking only that the wrapper
// exists let all of these through — so a handwritten guest could submit a frame
// the normative validator accepted and the host then dereferenced.
func TestNilNestedPayloadsAreRejected(t *testing.T) {
	t.Run("results", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			hook v2.Hook
			r    *v2.HookResult
		}{
			{"replace request", v2.Hook_HOOK_BEFORE_REQUEST, &v2.HookResult{
				Action: &v2.HookResult_ReplaceRequest{}}},
			{"replace response", v2.Hook_HOOK_AFTER_RESPONSE, &v2.HookResult{
				Action: &v2.HookResult_ReplaceResponse{}}},
			{"emit events", v2.Hook_HOOK_ON_STREAM_CHUNK, &v2.HookResult{
				Action: &v2.HookResult_EmitEvents{}}},
			{"serve http", v2.Hook_HOOK_ON_HTTP_REQUEST, &v2.HookResult{
				Action: &v2.HookResult_ServeHttp{}}},
			{"tick outcome", v2.Hook_HOOK_ON_TICK, &v2.HookResult{
				Action: &v2.HookResult_TickOutcome{}}},
			{"suppress", v2.Hook_HOOK_ON_STREAM_CHUNK, &v2.HookResult{
				Action: &v2.HookResult_Suppress{}}},
		} {
			if err := tc.r.ValidateFor(tc.hook); err == nil {
				t.Errorf("%s: an action with a nil payload was accepted", tc.name)
			}
		}
	})

	t.Run("inputs", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			in   *v2.HookInput
		}{
			{"chat request", &v2.HookInput{Payload: &v2.HookInput_ChatRequest{}}},
			{"chat response", &v2.HookInput{Payload: &v2.HookInput_ChatResponse{}}},
			{"stream event", &v2.HookInput{Payload: &v2.HookInput_StreamEvent{}}},
			{"http request", &v2.HookInput{Payload: &v2.HookInput_HttpRequest{}}},
			{"tick request", &v2.HookInput{Payload: &v2.HookInput_TickRequest{}}},
		} {
			if err := tc.in.Validate(); err == nil {
				t.Errorf("%s: an input with a nil payload was accepted", tc.name)
			}
			if err := tc.in.ValidateFor(tc.in.HookOf()); err == nil {
				t.Errorf("%s: ValidateFor accepted an input with a nil payload", tc.name)
			}
		}
	})

	// An input carrying a malformed stream event must be refused too — the host
	// validates guest output, and this is the mirror on the way in.
	t.Run("input carrying an empty stream event", func(t *testing.T) {
		in := &v2.HookInput{Payload: &v2.HookInput_StreamEvent{StreamEvent: &v2.StreamEvent{}}}
		if err := in.Validate(); err == nil {
			t.Error("an input carrying an event with no variant set was accepted")
		}
	})
}

// A oneof wrapper can be a TYPED nil: Payload: (*HookInput_ChatRequest)(nil).
// The interface is non-nil because it carries a type, so a bare field access
// dereferences nothing and panics.
//
// A validator that crashes on malformed input is worse than one that misses it.
// The host runs this on bytes a guest controls, so a panic here hands the guest
// the choice of when the host dies. Every case must return an error instead.
func TestTypedNilWrappersAreRejectedNotPanics(t *testing.T) {
	for _, tc := range []struct {
		name  string
		check func() error
	}{
		{"HookInput chat request", func() error {
			return (&v2.HookInput{Payload: (*v2.HookInput_ChatRequest)(nil)}).Validate()
		}},
		{"HookInput chat response", func() error {
			return (&v2.HookInput{Payload: (*v2.HookInput_ChatResponse)(nil)}).Validate()
		}},
		{"HookInput stream event", func() error {
			return (&v2.HookInput{Payload: (*v2.HookInput_StreamEvent)(nil)}).Validate()
		}},
		{"HookInput http request", func() error {
			return (&v2.HookInput{Payload: (*v2.HookInput_HttpRequest)(nil)}).Validate()
		}},
		{"HookInput tick request", func() error {
			return (&v2.HookInput{Payload: (*v2.HookInput_TickRequest)(nil)}).Validate()
		}},
		{"HookResult replace request", func() error {
			return (&v2.HookResult{Action: (*v2.HookResult_ReplaceRequest)(nil)}).
				ValidateFor(v2.Hook_HOOK_BEFORE_REQUEST)
		}},
		{"HookResult emit events", func() error {
			return (&v2.HookResult{Action: (*v2.HookResult_EmitEvents)(nil)}).
				ValidateFor(v2.Hook_HOOK_ON_STREAM_CHUNK)
		}},
		{"HookResult tick outcome", func() error {
			return (&v2.HookResult{Action: (*v2.HookResult_TickOutcome)(nil)}).
				ValidateFor(v2.Hook_HOOK_ON_TICK)
		}},
		{"HookResult suppress", func() error {
			return (&v2.HookResult{Action: (*v2.HookResult_Suppress)(nil)}).
				ValidateFor(v2.Hook_HOOK_ON_STREAM_CHUNK)
		}},
		{"StreamEvent content block start", func() error {
			return (&v2.StreamEvent{Event: (*v2.StreamEvent_ContentBlockStart)(nil)}).Validate()
		}},
		{"ContentBlockStart tool call", func() error {
			return (&v2.ContentBlockStart{Block: (*v2.ContentBlockStart_ToolCall)(nil)}).Validate()
		}},
		{"ContentBlockStart provider", func() error {
			return (&v2.ContentBlockStart{Block: (*v2.ContentBlockStart_Provider)(nil)}).Validate()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("validation panicked instead of returning an error: %v", r)
				}
			}()
			if err := tc.check(); err == nil {
				t.Error("a typed-nil wrapper was accepted")
			}
		})
	}
}

// An action this build cannot name must trap, not be discarded.
//
// Protobuf keeps an unrecognised field in the message's unknown fields rather
// than failing, so a future action unmarshals with Action nil — exactly like a
// genuinely empty frame. Treating both as pass-through would mean an older host
// silently DOES NOTHING when a newer guest asked for something, which is the
// failure ABI-minor evolution produces the first time an action is added.
//
// The two are distinguishable: an empty frame has no unknown fields.
func TestUnknownActionIsRejectedNotIgnored(t *testing.T) {
	// Field 99, length-delimited: a future action.
	raw := protowire.AppendTag(nil, 99, protowire.BytesType)
	raw = protowire.AppendBytes(raw, nil)

	var r v2.HookResult
	if err := proto.Unmarshal(raw, &r); err != nil {
		t.Fatalf("a future action must still decode: %v", err)
	}
	if r.Action != nil {
		t.Fatal("precondition: an unknown action leaves Action nil")
	}
	if len(r.ProtoReflect().GetUnknown()) == 0 {
		t.Fatal("precondition: the unknown action should be retained in unknown fields")
	}

	for _, hook := range allHooks {
		if err := r.ValidateFor(hook); err == nil {
			t.Errorf("%v: an unrecognised action validated as pass-through, so a newer "+
				"guest's request would be silently discarded", hook)
		}
	}

	// A genuinely empty frame still passes — the distinction has to hold in
	// both directions, or every pass-through becomes an error.
	if err := (&v2.HookResult{}).ValidateFor(v2.Hook_HOOK_BEFORE_REQUEST); err != nil {
		t.Errorf("an empty frame must remain pass-through: %v", err)
	}
}

// The same distinction on the way in: a payload this build cannot name must be
// refused rather than read as "no payload".
func TestUnknownInputPayloadIsRejected(t *testing.T) {
	raw := protowire.AppendTag(nil, 99, protowire.BytesType)
	raw = protowire.AppendBytes(raw, nil)

	var in v2.HookInput
	if err := proto.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	err := in.Validate()
	if err == nil {
		t.Fatal("an unrecognised payload was accepted")
	}
	// The message must say WHY, because "carries no payload" would send the
	// reader looking for a host bug rather than a version mismatch.
	if !strings.Contains(err.Error(), "does not recognise") {
		t.Errorf("error does not identify this as a version mismatch: %v", err)
	}
}

// A frame carrying a known action AND unknown fields is still honoured: the
// action is understood, and the extra bytes are additive fields a newer ABI
// attached to it.
func TestKnownActionWithUnknownFieldsIsAccepted(t *testing.T) {
	known, err := proto.Marshal(&v2.HookResult{
		Action: &v2.HookResult_TickOutcome{TickOutcome: &v2.TickOutcome{Actions: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	extra := protowire.AppendTag(nil, 99, protowire.VarintType)
	extra = protowire.AppendVarint(extra, 7)

	var r v2.HookResult
	if err := proto.Unmarshal(append(known, extra...), &r); err != nil {
		t.Fatal(err)
	}
	if err := r.ValidateFor(v2.Hook_HOOK_ON_TICK); err != nil {
		t.Fatalf("a known action with additive unknown fields must be honoured: %v", err)
	}
}
