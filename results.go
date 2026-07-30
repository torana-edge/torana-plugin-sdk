package plugin_sdk

import (
	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// What a hook returns.
//
// v1 returned bare messages and attached meaning to nil: a nil request meant
// pass through, an all-defaults StreamEventResult meant "not handled", and
// three of five hooks carried a `handled` bool to disambiguate a wire artifact.
// An author had to know all of that, and the most common mistake — building a
// result by hand and forgetting `handled` — silently did nothing.
//
// These types say what they mean. The zero value is a pass, because a plugin
// that returns before deciding anything should change nothing, and because
// `return Result{}, err` on an error path is the reflex.
//
// Nothing here is a proto message. Authors never touch an envelope: the
// trampolines frame these, validate them, and hand the host bytes.

// RequestResult is what a run_before_request handler returns.
type RequestResult struct{ inner *pbv2.HookResult }

// PassRequest leaves the request as the host built it.
func PassRequest() RequestResult {
	return RequestResult{inner: &pbv2.HookResult{
		Disposition: pbv2.Disposition_DISPOSITION_PASS,
	}}
}

// ReplaceRequest sends req upstream instead of what the host had.
//
// A nil request is a pass rather than a panic: the alternative is an author
// writing `if req == nil { return PassRequest(), nil }` at the top of every
// handler, and the two mean the same thing.
func ReplaceRequest(req *pbv2.ChatRequest) RequestResult {
	if req == nil {
		return PassRequest()
	}
	return RequestResult{inner: &pbv2.HookResult{
		Disposition: pbv2.Disposition_DISPOSITION_REPLACE,
		Payload:     &pbv2.HookResult_ChatRequest{ChatRequest: req},
	}}
}

func (r RequestResult) hookResult() *pbv2.HookResult { return orPass(r.inner) }

// ResponseResult is what a run_after_response handler returns.
type ResponseResult struct{ inner *pbv2.HookResult }

// PassResponse leaves the response as the provider sent it.
func PassResponse() ResponseResult {
	return ResponseResult{inner: &pbv2.HookResult{
		Disposition: pbv2.Disposition_DISPOSITION_PASS,
	}}
}

// ReplaceResponse returns resp to the caller instead.
//
// The host discards this when the dispatch is observational — a streamed or
// errored response, where the bytes have already gone or there is no body to
// rewrite. HookInput.Mutable says which, so a handler can check rather than
// discover it by having no effect.
func ReplaceResponse(resp *pbv2.ChatResponse) ResponseResult {
	if resp == nil {
		return PassResponse()
	}
	return ResponseResult{inner: &pbv2.HookResult{
		Disposition: pbv2.Disposition_DISPOSITION_REPLACE,
		Payload:     &pbv2.HookResult_ChatResponse{ChatResponse: resp},
	}}
}

func (r ResponseResult) hookResult() *pbv2.HookResult { return orPass(r.inner) }

// StreamResult is what a run_on_stream_chunk handler returns.
type StreamResult struct{ inner *pbv2.HookResult }

// PassEvent forwards the event unchanged.
func PassEvent() StreamResult {
	return StreamResult{inner: &pbv2.HookResult{
		Disposition: pbv2.Disposition_DISPOSITION_PASS,
	}}
}

// SuppressEvent drops the event, emitting nothing.
//
// Use this rather than emitting an empty list: a REPLACE with no events emits
// nothing too, and one action with two encodings is what the v2 contract exists
// to remove. The host rejects the empty form.
func SuppressEvent() StreamResult {
	return StreamResult{inner: &pbv2.HookResult{
		Disposition: pbv2.Disposition_DISPOSITION_SUPPRESS,
	}}
}

// EmitEvents replaces the event with the ones given — one to rewrite it,
// several to fan out, as a stream plugin does when it releases buffered
// tool-call arguments and then closes the block.
//
// Emitting nothing is SuppressEvent, so that is what an empty call returns.
func EmitEvents(events ...*pbv2.StreamEvent) StreamResult {
	kept := make([]*pbv2.StreamEvent, 0, len(events))
	for _, e := range events {
		if e != nil {
			kept = append(kept, e)
		}
	}
	if len(kept) == 0 {
		return SuppressEvent()
	}
	return StreamResult{inner: &pbv2.HookResult{
		Disposition: pbv2.Disposition_DISPOSITION_REPLACE,
		Payload: &pbv2.HookResult_StreamEvents{
			StreamEvents: &pbv2.StreamEvents{Events: kept},
		},
	}}
}

func (r StreamResult) hookResult() *pbv2.HookResult { return orPass(r.inner) }

// HTTPResult is what a run_on_http_request handler returns.
type HTTPResult struct{ inner *pbv2.HookResult }

// PassHTTP declines to serve the request, and the host answers 404.
func PassHTTP() HTTPResult {
	return HTTPResult{inner: &pbv2.HookResult{
		Disposition: pbv2.Disposition_DISPOSITION_PASS,
	}}
}

// ServeHTTP answers the request with resp.
func ServeHTTP(resp *pbv2.HttpResponse) HTTPResult {
	if resp == nil {
		return PassHTTP()
	}
	return HTTPResult{inner: &pbv2.HookResult{
		Disposition: pbv2.Disposition_DISPOSITION_REPLACE,
		Payload:     &pbv2.HookResult_HttpResponse{HttpResponse: resp},
	}}
}

func (r HTTPResult) hookResult() *pbv2.HookResult { return orPass(r.inner) }

// TickResult is what a run_on_tick handler returns.
type TickResult struct{ inner *pbv2.HookResult }

// TickIdle reports that the tick did nothing.
func TickIdle() TickResult {
	return TickResult{inner: &pbv2.HookResult{
		Disposition: pbv2.Disposition_DISPOSITION_PASS,
	}}
}

// TickDid reports what the tick did. The host attaches no meaning to either
// field; they exist so an operator can see that a background plugin is alive
// and doing something.
func TickDid(actions int32, note string) TickResult {
	return TickResult{inner: &pbv2.HookResult{
		Disposition: pbv2.Disposition_DISPOSITION_REPLACE,
		Payload: &pbv2.HookResult_TickOutcome{
			TickOutcome: &pbv2.TickOutcome{Actions: actions, Note: note},
		},
	}}
}

func (r TickResult) hookResult() *pbv2.HookResult { return orPass(r.inner) }

// orPass turns a zero-value result into an explicit pass.
//
// The zero value arises from `return Result{}, err` and from a handler that
// returns before deciding. Both mean "change nothing", so they get the frame
// that says so rather than a nil the trampoline would have to interpret.
func orPass(r *pbv2.HookResult) *pbv2.HookResult {
	if r == nil {
		return &pbv2.HookResult{Disposition: pbv2.Disposition_DISPOSITION_PASS}
	}
	return r
}
