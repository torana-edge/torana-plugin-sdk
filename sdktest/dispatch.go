//go:build !wasip1

package sdktest

import (
	"sync/atomic"
	"testing"

	"google.golang.org/protobuf/proto"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

var nextRequestID atomic.Uint64

// Request is one simulated Torana request. Hooks dispatched through the same
// Request share request metadata and request ID; separate Requests do not.
// Harness convenience methods create a fresh Request for each call.
type Request struct {
	h         *Harness
	requestID uint64
	meta      map[string]string
}

// NewRequest starts an explicit request scope for a related hook chain.
func (h *Harness) NewRequest() *Request {
	h.t.Helper()
	return &Request{h: h, requestID: nextRequestID.Add(1), meta: make(map[string]string)}
}

func (r *Request) with(fn func()) {
	r.h.mu.Lock()
	previous := r.h.meta
	r.h.meta = r.meta
	r.h.mu.Unlock()
	r.h.with(fn)
	r.h.mu.Lock()
	r.meta = r.h.meta
	r.h.meta = previous
	r.h.mu.Unlock()
}

// RequestResult is the outcome of a before-request dispatch.
type RequestResult struct {
	Request       *pbv1.ChatRequest
	PassedThrough bool
	Err           error
	Raw           *pbv1.HookResult
}

// BeforeRequest dispatches run_before_request via the v1 trampoline path.
func (h *Harness) BeforeRequest(req *pbv1.ChatRequest) RequestResult {
	return h.NewRequest().BeforeRequest(req)
}

// BeforeRequest dispatches run_before_request in this request scope.
func (r *Request) BeforeRequest(req *pbv1.ChatRequest) RequestResult {
	h := r.h
	h.t.Helper()
	if sdk.RegisteredBeforeRequest() == nil {
		h.t.Fatal("sdktest: no run_before_request handler registered — " +
			"registration must happen in init(), not main()")
	}
	in := &pbv1.HookInput{
		RequestId: r.requestID,
		Payload:   &pbv1.HookInput_ChatRequest{ChatRequest: req},
	}
	var raw []byte
	var err error
	r.with(func() { raw, err = sdk.DispatchHook(in) })
	res := RequestResult{Err: err, PassedThrough: err == nil && len(raw) == 0}
	if err != nil || len(raw) == 0 {
		return res
	}
	var hr pbv1.HookResult
	if uerr := proto.Unmarshal(raw, &hr); uerr != nil {
		res.Err = uerr
		res.PassedThrough = false
		return res
	}
	res.Raw = &hr
	res.Request = hr.GetReplaceRequest()
	return res
}

// ResponseResult is the outcome of an after-response dispatch.
//
// Replacement is the guest's ReplaceResponse proposal. Applied is set only
// when Mutable is true — the host discards replacements on observational
// (mutable=false) dispatches, so tests must not treat Replacement as applied
// output in that case.
type ResponseResult struct {
	Replacement   *pbv1.ChatResponse
	Applied       *pbv1.ChatResponse
	Mutable       bool
	PassedThrough bool
	Err           error
	Raw           *pbv1.HookResult
}

// AfterResponse dispatches run_after_response.
func (h *Harness) AfterResponse(resp *pbv1.ChatResponse, mutable bool) ResponseResult {
	return h.NewRequest().AfterResponse(resp, mutable)
}

// AfterResponse dispatches run_after_response in this request scope.
func (r *Request) AfterResponse(resp *pbv1.ChatResponse, mutable bool) ResponseResult {
	h := r.h
	h.t.Helper()
	if sdk.RegisteredAfterResponse() == nil {
		h.t.Fatal("sdktest: no run_after_response handler registered")
	}
	in := &pbv1.HookInput{
		RequestId: r.requestID,
		Payload: &pbv1.HookInput_AfterResponse{AfterResponse: &pbv1.AfterResponse{
			Response: resp,
			Mutable:  mutable,
		}},
	}
	var raw []byte
	var err error
	r.with(func() { raw, err = sdk.DispatchHook(in) })
	res := ResponseResult{Err: err, Mutable: mutable, PassedThrough: err == nil && len(raw) == 0}
	if err != nil || len(raw) == 0 {
		return res
	}
	var hr pbv1.HookResult
	if uerr := proto.Unmarshal(raw, &hr); uerr != nil {
		res.Err = uerr
		res.PassedThrough = false
		return res
	}
	res.Raw = &hr
	res.Replacement = hr.GetReplaceResponse()
	if mutable {
		res.Applied = res.Replacement
	}
	return res
}

// StreamResult is the outcome of a stream-chunk dispatch.
type StreamResult struct {
	Events        []*pbv1.StreamEvent
	PassedThrough bool
	Suppressed    bool
	Err           error
	Raw           *pbv1.HookResult
}

// StreamChunk dispatches run_on_stream_chunk.
func (h *Harness) StreamChunk(ev *pbv1.StreamEvent) StreamResult {
	return h.NewRequest().StreamChunk(ev)
}

// StreamChunk dispatches run_on_stream_chunk in this request scope.
func (r *Request) StreamChunk(ev *pbv1.StreamEvent) StreamResult {
	h := r.h
	h.t.Helper()
	if sdk.RegisteredStreamChunk() == nil {
		h.t.Fatal("sdktest: no run_on_stream_chunk handler registered")
	}
	in := &pbv1.HookInput{
		RequestId: r.requestID,
		Payload:   &pbv1.HookInput_StreamEvent{StreamEvent: ev},
	}
	var raw []byte
	var err error
	r.with(func() { raw, err = sdk.DispatchHook(in) })
	res := StreamResult{Err: err, PassedThrough: err == nil && len(raw) == 0}
	if err != nil || len(raw) == 0 {
		return res
	}
	var hr pbv1.HookResult
	if uerr := proto.Unmarshal(raw, &hr); uerr != nil {
		res.Err = uerr
		res.PassedThrough = false
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
	Response      *pbv1.HttpResponse
	PassedThrough bool
	Err           error
}

// HTTPRequest dispatches run_on_http_request.
func (h *Harness) HTTPRequest(req *pbv1.HttpRequest) HTTPResult {
	return h.NewRequest().HTTPRequest(req)
}

// HTTPRequest dispatches run_on_http_request in this request scope.
func (r *Request) HTTPRequest(req *pbv1.HttpRequest) HTTPResult {
	h := r.h
	h.t.Helper()
	if sdk.RegisteredHTTPRequest() == nil {
		h.t.Fatal("sdktest: no run_on_http_request handler registered")
	}
	in := &pbv1.HookInput{
		RequestId: r.requestID,
		Payload:   &pbv1.HookInput_HttpRequest{HttpRequest: req},
	}
	var raw []byte
	var err error
	r.with(func() { raw, err = sdk.DispatchHook(in) })
	res := HTTPResult{Err: err, PassedThrough: err == nil && len(raw) == 0}
	if err != nil || len(raw) == 0 {
		return res
	}
	var hr pbv1.HookResult
	if uerr := proto.Unmarshal(raw, &hr); uerr != nil {
		res.Err = uerr
		res.PassedThrough = false
		return res
	}
	res.Response = hr.GetServeHttp()
	return res
}

// TickResult is the outcome of a tick dispatch.
type TickResult struct {
	Outcome       *pbv1.TickOutcome
	PassedThrough bool
	Err           error
}

// Tick dispatches run_on_tick.
func (h *Harness) Tick(req *pbv1.TickRequest) TickResult {
	return h.NewRequest().Tick(req)
}

// Tick dispatches run_on_tick in this request scope.
func (r *Request) Tick(req *pbv1.TickRequest) TickResult {
	h := r.h
	h.t.Helper()
	if sdk.RegisteredTick() == nil {
		h.t.Fatal("sdktest: no run_on_tick handler registered")
	}
	in := &pbv1.HookInput{
		RequestId: r.requestID,
		Payload:   &pbv1.HookInput_TickRequest{TickRequest: req},
	}
	var raw []byte
	var err error
	r.with(func() { raw, err = sdk.DispatchHook(in) })
	res := TickResult{Err: err, PassedThrough: err == nil && len(raw) == 0}
	if err != nil || len(raw) == 0 {
		return res
	}
	var hr pbv1.HookResult
	if uerr := proto.Unmarshal(raw, &hr); uerr != nil {
		res.Err = uerr
		res.PassedThrough = false
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
func DecodeBlockArgs(t testing.TB, args string) *pbv1.BlockRequestArgs {
	t.Helper()
	var a pbv1.BlockRequestArgs
	if err := proto.Unmarshal([]byte(args), &a); err != nil {
		t.Fatal(err)
	}
	return &a
}
