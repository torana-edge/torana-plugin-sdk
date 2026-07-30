//go:build !wasip1

package sdktest

import (
	"testing"

	"google.golang.org/protobuf/proto"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// RequestResult is the outcome of a before-request dispatch.
type RequestResult struct {
	Request       *pbv2.ChatRequest
	PassedThrough bool
	Err           error
	Raw           *pbv2.HookResult
}

// BeforeRequest dispatches run_before_request via the v2 trampoline path.
func (h *Harness) BeforeRequest(req *pbv2.ChatRequest) RequestResult {
	h.t.Helper()
	if sdk.RegisteredBeforeRequest() == nil {
		h.t.Fatal("sdktest: no run_before_request handler registered — " +
			"registration must happen in init(), not main()")
	}
	in := &pbv2.HookInput{
		RequestId: 1,
		Payload:   &pbv2.HookInput_ChatRequest{ChatRequest: req},
	}
	var raw []byte
	var err error
	h.with(func() { raw, err = sdk.DispatchHook(in) })
	res := RequestResult{Err: err, PassedThrough: len(raw) == 0}
	if len(raw) == 0 {
		return res
	}
	var hr pbv2.HookResult
	if uerr := proto.Unmarshal(raw, &hr); uerr != nil {
		res.Err = uerr
		return res
	}
	res.Raw = &hr
	res.Request = hr.GetReplaceRequest()
	return res
}

// AfterResponse dispatches run_after_response.
func (h *Harness) AfterResponse(resp *pbv2.ChatResponse, mutable bool) RequestResult {
	h.t.Helper()
	if sdk.RegisteredAfterResponse() == nil {
		h.t.Fatal("sdktest: no run_after_response handler registered")
	}
	in := &pbv2.HookInput{
		RequestId: 1,
		Payload: &pbv2.HookInput_AfterResponse{AfterResponse: &pbv2.AfterResponse{
			Response: resp,
			Mutable:  mutable,
		}},
	}
	var raw []byte
	var err error
	h.with(func() { raw, err = sdk.DispatchHook(in) })
	res := RequestResult{Err: err, PassedThrough: len(raw) == 0}
	if len(raw) == 0 {
		return res
	}
	var hr pbv2.HookResult
	if uerr := proto.Unmarshal(raw, &hr); uerr != nil {
		res.Err = uerr
		return res
	}
	res.Raw = &hr
	return res
}

// StreamResult is the outcome of a stream-chunk dispatch.
type StreamResult struct {
	Events        []*pbv2.StreamEvent
	PassedThrough bool
	Suppressed    bool
	Err           error
	Raw           *pbv2.HookResult
}

// StreamChunk dispatches run_on_stream_chunk.
func (h *Harness) StreamChunk(ev *pbv2.StreamEvent) StreamResult {
	h.t.Helper()
	if sdk.RegisteredStreamChunk() == nil {
		h.t.Fatal("sdktest: no run_on_stream_chunk handler registered")
	}
	in := &pbv2.HookInput{
		RequestId: 1,
		Payload:   &pbv2.HookInput_StreamEvent{StreamEvent: ev},
	}
	var raw []byte
	var err error
	h.with(func() { raw, err = sdk.DispatchHook(in) })
	res := StreamResult{Err: err, PassedThrough: len(raw) == 0}
	if len(raw) == 0 {
		return res
	}
	var hr pbv2.HookResult
	if uerr := proto.Unmarshal(raw, &hr); uerr != nil {
		res.Err = uerr
		return res
	}
	res.Raw = &hr
	if hr.GetSuppress() != nil {
		res.Suppressed = true
		return res
	}
	if evs := hr.GetEmitEvents(); evs != nil {
		res.Events = evs.Events
	}
	return res
}

// HTTPResult is the outcome of an HTTP-request dispatch.
type HTTPResult struct {
	Response      *pbv2.HttpResponse
	PassedThrough bool
	Err           error
}

// HTTPRequest dispatches run_on_http_request.
func (h *Harness) HTTPRequest(req *pbv2.HttpRequest) HTTPResult {
	h.t.Helper()
	if sdk.RegisteredHTTPRequest() == nil {
		h.t.Fatal("sdktest: no run_on_http_request handler registered")
	}
	in := &pbv2.HookInput{
		RequestId: 1,
		Payload:   &pbv2.HookInput_HttpRequest{HttpRequest: req},
	}
	var raw []byte
	var err error
	h.with(func() { raw, err = sdk.DispatchHook(in) })
	res := HTTPResult{Err: err, PassedThrough: len(raw) == 0}
	if len(raw) == 0 {
		return res
	}
	var hr pbv2.HookResult
	if uerr := proto.Unmarshal(raw, &hr); uerr != nil {
		res.Err = uerr
		return res
	}
	res.Response = hr.GetServeHttp()
	return res
}

// TickResult is the outcome of a tick dispatch.
type TickResult struct {
	Outcome       *pbv2.TickOutcome
	PassedThrough bool
	Err           error
}

// Tick dispatches run_on_tick.
func (h *Harness) Tick(req *pbv2.TickRequest) TickResult {
	h.t.Helper()
	if sdk.RegisteredTick() == nil {
		h.t.Fatal("sdktest: no run_on_tick handler registered")
	}
	in := &pbv2.HookInput{
		RequestId: 1,
		Payload:   &pbv2.HookInput_TickRequest{TickRequest: req},
	}
	var raw []byte
	var err error
	h.with(func() { raw, err = sdk.DispatchHook(in) })
	res := TickResult{Err: err, PassedThrough: len(raw) == 0}
	if len(raw) == 0 {
		return res
	}
	var hr pbv2.HookResult
	if uerr := proto.Unmarshal(raw, &hr); uerr != nil {
		res.Err = uerr
		return res
	}
	res.Outcome = hr.GetTickOutcome()
	return res
}

// BlockCalls returns block host-calls recorded during dispatches.
func (h *Harness) BlockCalls() []HostCallEntry {
	var out []HostCallEntry
	for _, c := range h.Calls() {
		if c.Command == "env.block_request" {
			out = append(out, c)
		}
	}
	return out
}

// DecodeBlockArgs unmarshals BlockRequestArgs from a recorded call.
func DecodeBlockArgs(t testing.TB, args string) *pbv2.BlockRequestArgs {
	t.Helper()
	var a pbv2.BlockRequestArgs
	if err := proto.Unmarshal([]byte(args), &a); err != nil {
		t.Fatal(err)
	}
	return &a
}