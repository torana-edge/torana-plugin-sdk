package plugin_sdk

import (
	"testing"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// Every result an author can construct must be one the host accepts.
//
// The trampolines validate before returning, so a constructor that produced an
// invalid frame would turn a correct-looking plugin into a trap at runtime.
// Checking each against the same validation the host runs is the only way to
// know the two agree.
func TestEveryConstructorProducesAValidResult(t *testing.T) {
	ev := &pbv2.StreamEvent{Event: &pbv2.StreamEvent_TextDelta{TextDelta: "x"}}

	for _, tc := range []struct {
		name   string
		hook   pbv2.Hook
		result *pbv2.HookResult
	}{
		{"PassRequest", pbv2.Hook_HOOK_BEFORE_REQUEST, PassRequest().hookResult()},
		{"ReplaceRequest", pbv2.Hook_HOOK_BEFORE_REQUEST,
			ReplaceRequest(&pbv2.ChatRequest{Model: "m"}).hookResult()},
		{"PassResponse", pbv2.Hook_HOOK_AFTER_RESPONSE, PassResponse().hookResult()},
		{"ReplaceResponse", pbv2.Hook_HOOK_AFTER_RESPONSE,
			ReplaceResponse(&pbv2.ChatResponse{Model: "m"}).hookResult()},
		{"PassEvent", pbv2.Hook_HOOK_ON_STREAM_CHUNK, PassEvent().hookResult()},
		{"SuppressEvent", pbv2.Hook_HOOK_ON_STREAM_CHUNK, SuppressEvent().hookResult()},
		{"EmitEvents one", pbv2.Hook_HOOK_ON_STREAM_CHUNK, EmitEvents(ev).hookResult()},
		{"EmitEvents fan-out", pbv2.Hook_HOOK_ON_STREAM_CHUNK, EmitEvents(ev, ev).hookResult()},
		{"PassHTTP", pbv2.Hook_HOOK_ON_HTTP_REQUEST, PassHTTP().hookResult()},
		{"ServeHTTP", pbv2.Hook_HOOK_ON_HTTP_REQUEST,
			ServeHTTP(&pbv2.HttpResponse{Status: 200}).hookResult()},
		{"TickIdle", pbv2.Hook_HOOK_ON_TICK, TickIdle().hookResult()},
		{"TickDid", pbv2.Hook_HOOK_ON_TICK, TickDid(3, "warmed").hookResult()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.result.ValidateFor(tc.hook); err != nil {
				t.Fatalf("the host would reject this: %v", err)
			}
		})
	}
}

// The zero value must be a pass. `return RequestResult{}, err` is the reflex on
// an error path, and a handler that returns before deciding anything should
// change nothing.
func TestZeroValueIsAPass(t *testing.T) {
	for _, tc := range []struct {
		name   string
		hook   pbv2.Hook
		result *pbv2.HookResult
	}{
		{"request", pbv2.Hook_HOOK_BEFORE_REQUEST, RequestResult{}.hookResult()},
		{"response", pbv2.Hook_HOOK_AFTER_RESPONSE, ResponseResult{}.hookResult()},
		{"stream", pbv2.Hook_HOOK_ON_STREAM_CHUNK, StreamResult{}.hookResult()},
		{"http", pbv2.Hook_HOOK_ON_HTTP_REQUEST, HTTPResult{}.hookResult()},
		{"tick", pbv2.Hook_HOOK_ON_TICK, TickResult{}.hookResult()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.result.Disposition != pbv2.Disposition_DISPOSITION_PASS {
				t.Errorf("zero value has disposition %v, want PASS", tc.result.Disposition)
			}
			if err := tc.result.ValidateFor(tc.hook); err != nil {
				t.Errorf("zero value is invalid: %v", err)
			}
		})
	}
}

// Emitting no events means suppress, and must produce the SUPPRESS frame rather
// than an empty REPLACE — which the host rejects, precisely so one action has
// one encoding.
func TestEmitNothingIsSuppress(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result StreamResult
	}{
		{"no arguments", EmitEvents()},
		{"only nils", EmitEvents(nil, nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.result.hookResult()
			if got.Disposition != pbv2.Disposition_DISPOSITION_SUPPRESS {
				t.Errorf("disposition = %v, want SUPPRESS", got.Disposition)
			}
			if err := got.ValidateFor(pbv2.Hook_HOOK_ON_STREAM_CHUNK); err != nil {
				t.Errorf("the host would reject this: %v", err)
			}
		})
	}
}

// A nil among real events is dropped rather than carried through, since the
// host refuses a list containing one.
func TestNilEventsAreDropped(t *testing.T) {
	ev := &pbv2.StreamEvent{Event: &pbv2.StreamEvent_TextDelta{TextDelta: "x"}}
	got := EmitEvents(nil, ev, nil).hookResult()

	if err := got.ValidateFor(pbv2.Hook_HOOK_ON_STREAM_CHUNK); err != nil {
		t.Fatalf("the host would reject this: %v", err)
	}
	events := got.GetStreamEvents().GetEvents()
	if len(events) != 1 {
		t.Fatalf("kept %d events, want 1", len(events))
	}
}

// Replacing with nil means there is nothing to replace with, which is a pass.
// The alternative is every handler opening with a nil check.
func TestReplaceWithNilIsAPass(t *testing.T) {
	if d := ReplaceRequest(nil).hookResult().Disposition; d != pbv2.Disposition_DISPOSITION_PASS {
		t.Errorf("ReplaceRequest(nil) = %v, want PASS", d)
	}
	if d := ReplaceResponse(nil).hookResult().Disposition; d != pbv2.Disposition_DISPOSITION_PASS {
		t.Errorf("ReplaceResponse(nil) = %v, want PASS", d)
	}
	if d := ServeHTTP(nil).hookResult().Disposition; d != pbv2.Disposition_DISPOSITION_PASS {
		t.Errorf("ServeHTTP(nil) = %v, want PASS", d)
	}
}

// A result carries its own hook's payload, so a handler cannot answer the wrong
// hook by construction.
func TestResultsCarryTheirOwnHookPayload(t *testing.T) {
	pairs := []struct {
		hook   pbv2.Hook
		result *pbv2.HookResult
	}{
		{pbv2.Hook_HOOK_BEFORE_REQUEST, ReplaceRequest(&pbv2.ChatRequest{Model: "m"}).hookResult()},
		{pbv2.Hook_HOOK_AFTER_RESPONSE, ReplaceResponse(&pbv2.ChatResponse{Model: "m"}).hookResult()},
		{pbv2.Hook_HOOK_ON_HTTP_REQUEST, ServeHTTP(&pbv2.HttpResponse{Status: 200}).hookResult()},
		{pbv2.Hook_HOOK_ON_TICK, TickDid(1, "").hookResult()},
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
