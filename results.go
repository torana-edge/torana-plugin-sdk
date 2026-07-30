package plugin_sdk

import (
	"errors"
	"fmt"

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
//
// A constructor given nil records an error rather than reinterpreting it. An
// earlier draft turned ReplaceRequest(nil) into a pass, which is worse than it
// sounds: a sanitizer that fails and returns nil would then have sent the
// UNSANITIZED request upstream, silently, with the plugin's failure_mode never
// consulted. Reinterpreting an invalid argument hides the author's bug and
// converts it into the least safe outcome. The trampoline surfaces the error,
// the guest traps, and failure_mode decides — which is what a plugin declaring
// block asked for.

// RequestResult is what a run_before_request handler returns.
type RequestResult struct {
	inner *pbv2.HookResult
	err   error
}

// PassRequest leaves the request as the host built it.
func PassRequest() RequestResult {
	return RequestResult{inner: &pbv2.HookResult{
		Disposition: pbv2.Disposition_DISPOSITION_PASS,
	}}
}

// ReplaceRequest sends req upstream instead of what the host had.
//
// A nil request is an error, not a pass. Passing would mean the host's own
// request goes upstream — so a sanitizer that failed would have its output
// discarded and the unsanitized original sent instead.
func ReplaceRequest(req *pbv2.ChatRequest) RequestResult {
	if req == nil {
		return RequestResult{err: errors.New(
			"ReplaceRequest(nil): there is nothing to replace with. Return " +
				"PassRequest() if that is what you mean — passing nil would send the " +
				"host's own request upstream")}
	}
	return RequestResult{inner: &pbv2.HookResult{
		Disposition: pbv2.Disposition_DISPOSITION_REPLACE,
		Payload:     &pbv2.HookResult_ChatRequest{ChatRequest: req},
	}}
}

func (r RequestResult) hookResult() (*pbv2.HookResult, error) { return orPass(r.inner, r.err) }

// ResponseResult is what a run_after_response handler returns.
type ResponseResult struct {
	inner *pbv2.HookResult
	err   error
}

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
		return ResponseResult{err: errors.New(
			"ReplaceResponse(nil): there is nothing to replace with. Return " +
				"PassResponse() if that is what you mean")}
	}
	return ResponseResult{inner: &pbv2.HookResult{
		Disposition: pbv2.Disposition_DISPOSITION_REPLACE,
		Payload:     &pbv2.HookResult_ChatResponse{ChatResponse: resp},
	}}
}

func (r ResponseResult) hookResult() (*pbv2.HookResult, error) { return orPass(r.inner, r.err) }

// StreamResult is what a run_on_stream_chunk handler returns.
type StreamResult struct {
	inner *pbv2.HookResult
	err   error
}

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
// Emitting nothing, or any nil event, is an error. Both would emit less than
// the author wrote: silently dropping a nil produces partial output, which on a
// stream means a truncated tool call the agent then tries to execute. Say
// SuppressEvent when you mean to emit nothing.
func EmitEvents(events ...*pbv2.StreamEvent) StreamResult {
	if len(events) == 0 {
		return StreamResult{err: errors.New(
			"EmitEvents() with no events emits nothing. Return SuppressEvent() " +
				"if that is what you mean")}
	}
	for i, e := range events {
		if e == nil {
			return StreamResult{err: fmt.Errorf(
				"EmitEvents: event %d is nil. Dropping it would emit less than you "+
					"wrote, which on a stream means a truncated tool call", i)}
		}
	}
	kept := events
	return StreamResult{inner: &pbv2.HookResult{
		Disposition: pbv2.Disposition_DISPOSITION_REPLACE,
		Payload: &pbv2.HookResult_StreamEvents{
			StreamEvents: &pbv2.StreamEvents{Events: kept},
		},
	}}
}

func (r StreamResult) hookResult() (*pbv2.HookResult, error) { return orPass(r.inner, r.err) }

// HTTPResult is what a run_on_http_request handler returns.
type HTTPResult struct {
	inner *pbv2.HookResult
	err   error
}

// PassHTTP declines to serve the request, and the host answers 404.
func PassHTTP() HTTPResult {
	return HTTPResult{inner: &pbv2.HookResult{
		Disposition: pbv2.Disposition_DISPOSITION_PASS,
	}}
}

// ServeHTTP answers the request with resp.
func ServeHTTP(resp *pbv2.HttpResponse) HTTPResult {
	if resp == nil {
		return HTTPResult{err: errors.New(
			"ServeHTTP(nil): there is no response to serve. Return PassHTTP() to " +
				"decline the request")}
	}
	return HTTPResult{inner: &pbv2.HookResult{
		Disposition: pbv2.Disposition_DISPOSITION_REPLACE,
		Payload:     &pbv2.HookResult_HttpResponse{HttpResponse: resp},
	}}
}

func (r HTTPResult) hookResult() (*pbv2.HookResult, error) { return orPass(r.inner, r.err) }

// TickResult is what a run_on_tick handler returns.
type TickResult struct {
	inner *pbv2.HookResult
	err   error
}

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

func (r TickResult) hookResult() (*pbv2.HookResult, error) { return orPass(r.inner, r.err) }

// orPass turns a zero-value result into an explicit pass, and surfaces any
// error the constructor recorded.
//
// The zero value arises from `return Result{}, err` and from a handler that
// returns before deciding. Both mean "change nothing", so they get the frame
// that says so rather than a nil the trampoline would have to interpret. A
// recorded error is different: the author asked for something impossible, and
// the trampoline must trap rather than substitute a guess.
func orPass(r *pbv2.HookResult, err error) (*pbv2.HookResult, error) {
	if err != nil {
		return nil, err
	}
	if r == nil {
		return &pbv2.HookResult{Disposition: pbv2.Disposition_DISPOSITION_PASS}, nil
	}
	return r, nil
}
