package plugin_sdk

import (
	"testing"

	"google.golang.org/protobuf/proto"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// errOf and frameOf adapt the two-value hookResult where only one half matters.
func errOf(_ *pbv2.HookResult, err error) error            { return err }
func frameOf(r *pbv2.HookResult, _ error) *pbv2.HookResult { return r }

// Every result an author can construct must be one the host accepts.
//
// The trampolines validate before returning, so a constructor that produced an
// invalid frame would turn a correct-looking plugin into a trap at runtime.
// Checking each against the same validation the host runs is the only way to
// know the two agree.
func TestEveryConstructorProducesAValidResult(t *testing.T) {
	ev := &pbv2.StreamEvent{Event: &pbv2.StreamEvent_TextDelta{TextDelta: "x"}}

	for _, tc := range []struct {
		name  string
		hook  pbv2.Hook
		build func() (*pbv2.HookResult, error)
	}{
		{"ReplaceRequest", pbv2.Hook_HOOK_BEFORE_REQUEST,
			ReplaceRequest(&pbv2.ChatRequest{Model: "m"}).hookResult},
		{"ReplaceResponse", pbv2.Hook_HOOK_AFTER_RESPONSE,
			ReplaceResponse(&pbv2.ChatResponse{Model: "m"}).hookResult},
		{"SuppressEvent", pbv2.Hook_HOOK_ON_STREAM_CHUNK, SuppressEvent().hookResult},
		{"EmitEvents one", pbv2.Hook_HOOK_ON_STREAM_CHUNK, EmitEvents(ev).hookResult},
		{"EmitEvents fan-out", pbv2.Hook_HOOK_ON_STREAM_CHUNK, EmitEvents(ev, ev).hookResult},
		{"ServeHTTP", pbv2.Hook_HOOK_ON_HTTP_REQUEST,
			ServeHTTP(&pbv2.HttpResponse{Status: 200}).hookResult},
		{"TickDid", pbv2.Hook_HOOK_ON_TICK, TickDid(3, "warmed").hookResult},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.build()
			if err != nil {
				t.Fatalf("constructor reported an error: %v", err)
			}
			if err := got.ValidateFor(tc.hook); err != nil {
				t.Fatalf("the host would reject this: %v", err)
			}
		})
	}
}

// Pass produces no frame at all. Zero bytes is the ABI's pass-through and its
// only encoding — an explicit PASS frame would marshal to zero bytes anyway,
// which is how the earlier two-axis shape ended up with a rule it could not
// enforce.
func TestPassProducesNoFrame(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func() (*pbv2.HookResult, error)
	}{
		{"PassRequest", PassRequest().hookResult},
		{"PassResponse", PassResponse().hookResult},
		{"PassEvent", PassEvent().hookResult},
		{"PassHTTP", PassHTTP().hookResult},
		{"TickIdle", TickIdle().hookResult},
		{"zero request", RequestResult{}.hookResult},
		{"zero response", ResponseResult{}.hookResult},
		{"zero stream", StreamResult{}.hookResult},
		{"zero http", HTTPResult{}.hookResult},
		{"zero tick", TickResult{}.hookResult},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.build()
			if err != nil {
				t.Fatalf("reported an error: %v", err)
			}
			if got != nil {
				t.Fatalf("produced a frame, want none: %+v", got)
			}
		})
	}
}

// Suppress must produce a frame, or it would be indistinguishable from pass.
// An empty message inside a oneof still marshals to a tag and a zero length,
// which is what makes suppression expressible as an action.
func TestSuppressProducesADistinguishableFrame(t *testing.T) {
	got, err := SuppressEvent().hookResult()
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("suppress produced no frame, so it would read as pass-through")
	}
	raw, err := proto.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatal("suppress marshalled to zero bytes, which the ABI reads as pass-through")
	}
	if err := got.ValidateFor(pbv2.Hook_HOOK_ON_STREAM_CHUNK); err != nil {
		t.Fatalf("the host would reject this: %v", err)
	}
}

// A constructor given nothing usable must report an error, not substitute a
// meaning. Reinterpreting hides the author's bug and picks the least safe
// outcome available: ReplaceRequest(nil) as a pass would send the host's own
// request upstream, so a sanitizer that failed would have its output discarded
// and the UNSANITISED original sent instead.
func TestInvalidArgumentsAreErrors(t *testing.T) {
	ev := &pbv2.StreamEvent{Event: &pbv2.StreamEvent_TextDelta{TextDelta: "x"}}

	for _, tc := range []struct {
		name string
		err  error
		why  string
	}{
		{"ReplaceRequest(nil)", errOf(ReplaceRequest(nil).hookResult()),
			"passing would send the host's own request upstream"},
		{"ReplaceResponse(nil)", errOf(ReplaceResponse(nil).hookResult()),
			"passing would return the provider's own response"},
		{"ServeHTTP(nil)", errOf(ServeHTTP(nil).hookResult()),
			"there is no response to serve"},
		{"EmitEvents()", errOf(EmitEvents().hookResult()),
			"emitting nothing is SuppressEvent, and should say so"},
		{"EmitEvents(nil)", errOf(EmitEvents(nil).hookResult()),
			"a nil event emits nothing"},
		{"EmitEvents(ev, nil)", errOf(EmitEvents(ev, nil).hookResult()),
			"dropping the nil would emit less than the author wrote"},
		{"EmitEvents(nil, ev)", errOf(EmitEvents(nil, ev).hookResult()),
			"same, with the nil first"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err == nil {
				t.Fatalf("accepted: %s", tc.why)
			}
		})
	}
}

// An error must not also produce a frame — the trampoline has to trap, not
// choose between the two.
func TestErrorResultsCarryNoFrame(t *testing.T) {
	r, err := ReplaceRequest(nil).hookResult()
	if err == nil {
		t.Fatal("expected an error")
	}
	if r != nil {
		t.Errorf("an error result also produced a frame: %+v", r)
	}
}

// A result carries its own hook's payload, so a handler cannot answer the wrong
// hook by construction.
func TestResultsCarryTheirOwnHookPayload(t *testing.T) {
	ev := &pbv2.StreamEvent{Event: &pbv2.StreamEvent_TextDelta{TextDelta: "x"}}
	pairs := []struct {
		hook   pbv2.Hook
		result *pbv2.HookResult
	}{
		{pbv2.Hook_HOOK_BEFORE_REQUEST, frameOf(ReplaceRequest(&pbv2.ChatRequest{Model: "m"}).hookResult())},
		{pbv2.Hook_HOOK_AFTER_RESPONSE, frameOf(ReplaceResponse(&pbv2.ChatResponse{Model: "m"}).hookResult())},
		{pbv2.Hook_HOOK_ON_STREAM_CHUNK, frameOf(EmitEvents(ev).hookResult())},
		{pbv2.Hook_HOOK_ON_HTTP_REQUEST, frameOf(ServeHTTP(&pbv2.HttpResponse{Status: 200}).hookResult())},
		{pbv2.Hook_HOOK_ON_TICK, frameOf(TickDid(1, "").hookResult())},
	}
	for _, a := range pairs {
		for _, b := range pairs {
			if a.hook == b.hook {
				continue
			}
			if err := a.result.ValidateFor(b.hook); err == nil {
				t.Errorf("a %v result was accepted as an answer to %v", a.hook, b.hook)
			}
		}
	}
}
