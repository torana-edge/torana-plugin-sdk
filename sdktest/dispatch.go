//go:build !wasip1

package sdktest

import (
	"fmt"
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
	h              *Harness
	requestID      uint64
	meta           map[string]string
	calls          []HostCallEntry
	accepted       []HostCallEntry
	invocationHook string
}

// NewRequest starts an explicit request scope for a related hook chain.
func (h *Harness) NewRequest() *Request {
	h.t.Helper()
	return &Request{h: h, requestID: nextRequestID.Add(1), meta: make(map[string]string)}
}

func (r *Request) with(fn func()) {
	r.h.with(func() {
		r.h.mu.Lock()
		previous, previousActive := r.h.meta, r.h.active
		r.h.meta, r.h.active = r.meta, r
		r.h.mu.Unlock()
		defer func() {
			r.h.mu.Lock()
			r.meta = r.h.meta
			r.h.meta, r.h.active = previous, previousActive
			r.h.mu.Unlock()
		}()
		fn()
	})
}

// dispatch mirrors one host invocation, including outcome rollback on traps.
func (r *Request) dispatch(in *pbv1.HookInput, hook string) (raw []byte, err error) {
	r.with(func() {
		previous := r.invocationHook
		r.invocationHook = hook
		defer func() {
			if failure := recover(); failure != nil {
				err = fmt.Errorf("sdktest: handler panic: %v", failure)
			}
			r.finalize(err)
			r.invocationHook = previous
		}()
		raw, err = sdk.DispatchHook(in)
	})
	return
}

// Run executes code inside this request's host and observation scope.
func (r *Request) Run(fn func()) { r.with(fn) }

// Calls returns calls observed in this request scope.
func (r *Request) Calls() []HostCallEntry {
	r.h.mu.Lock()
	defer r.h.mu.Unlock()
	return append([]HostCallEntry(nil), r.calls...)
}

// AcceptedCalls returns successful value-arm calls observed in this scope.
func (r *Request) AcceptedCalls() []HostCallEntry {
	r.h.mu.Lock()
	defer r.h.mu.Unlock()
	return append([]HostCallEntry(nil), r.accepted...)
}
func (r *Request) EffectiveBlockCalls() []HostCallEntry {
	var out []HostCallEntry
	for _, c := range r.AcceptedCalls() {
		if c.Command == "env.block_request" && c.Effective {
			out = append(out, c)
		}
	}
	return out
}
func (r *Request) EffectiveRespondCalls() []HostCallEntry {
	var out []HostCallEntry
	for _, c := range r.AcceptedCalls() {
		if c.Command == "env.respond_request" && c.Effective {
			out = append(out, c)
		}
	}
	return out
}
func (r *Request) EffectiveRouteCalls() []HostCallEntry {
	var out []HostCallEntry
	for _, c := range r.AcceptedCalls() {
		if c.Command == "env.route_request" && c.Effective {
			out = append(out, c)
		}
	}
	return out
}

// EffectiveIdentityCalls returns the identity retained by the host.
func (r *Request) EffectiveIdentityCalls() []HostCallEntry {
	var out []HostCallEntry
	for _, c := range r.AcceptedCalls() {
		if c.Command == "env.set_identity" && c.Effective {
			out = append(out, c)
		}
	}
	return out
}

func (r *Request) finalize(err error) {
	r.h.mu.Lock()
	defer r.h.mu.Unlock()
	first := map[string]bool{}
	last := map[string]int{}
	for i := range r.accepted {
		c := &r.accepted[i]
		if !c.Effective {
			continue
		}
		switch c.Command {
		case "env.block_request", "env.respond_request":
			if c.Command == "env.respond_request" && err != nil {
				c.Effective = false
				continue
			}
			if first[c.Command] {
				c.Effective = false
			}
			first[c.Command] = true
		case "env.route_request", "env.set_identity":
			if err != nil {
				c.Effective = false
				continue
			}
			if previous, ok := last[c.Command]; ok {
				r.accepted[previous].Effective = false
			}
			last[c.Command] = i
		}
	}
	// Edge retains the first respond verdict for diagnostics, but request
	// dispatch serves the block whenever both were recorded. Effective getters
	// describe that client-visible outcome; AcceptedCalls still exposes the
	// retained successful respond call.
	blocked := false
	for i := range r.accepted {
		if r.accepted[i].Command == "env.block_request" && r.accepted[i].Effective {
			blocked = true
			break
		}
	}
	if blocked {
		for i := range r.accepted {
			if r.accepted[i].Command == "env.respond_request" {
				r.accepted[i].Effective = false
			}
		}
	}
	for _, entry := range r.accepted {
		index := entry.index
		r.h.calls[index].Effective = entry.Effective
		for j := range r.calls {
			if r.calls[j].index == index {
				r.calls[j].Effective = entry.Effective
			}
		}
		for j := range r.h.accepted {
			if r.h.accepted[j].index == index {
				r.h.accepted[j].Effective = entry.Effective
			}
		}
	}
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
	if !r.h.hookAllowed("run_before_request") {
		return RequestResult{Err: fmt.Errorf("sdktest: hook run_before_request is not declared")}
	}
	h := r.h
	h.t.Helper()
	if sdk.RegisteredBeforeRequest() == nil {
		h.t.Fatal("sdktest: no run_before_request handler registered — " +
			"registration must happen in init(), not main()")
	}
	in := &pbv1.HookInput{
		ContractRevision: sdk.ContractRevision,
		RequestId:        r.requestID,
		Execution:        &pbv1.ExecutionInfo{ConversationId: proto.String(h.conversationID)},
		Payload:          &pbv1.HookInput_ChatRequest{ChatRequest: req},
	}
	raw, err := r.dispatch(in, "run_before_request")
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
	if !r.h.hookAllowed("run_after_response") {
		return ResponseResult{Err: fmt.Errorf("sdktest: hook run_after_response is not declared")}
	}
	h := r.h
	h.t.Helper()
	if sdk.RegisteredAfterResponse() == nil {
		h.t.Fatal("sdktest: no run_after_response handler registered")
	}
	in := &pbv1.HookInput{
		ContractRevision: sdk.ContractRevision,
		RequestId:        r.requestID,
		Payload: &pbv1.HookInput_AfterResponse{AfterResponse: &pbv1.AfterResponse{
			Response: resp,
			Mutable:  mutable,
		}},
	}
	raw, err := r.dispatch(in, "run_after_response")
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
	if !r.h.hookAllowed("run_on_stream_chunk") {
		return StreamResult{Err: fmt.Errorf("sdktest: hook run_on_stream_chunk is not declared")}
	}
	h := r.h
	h.t.Helper()
	if sdk.RegisteredStreamChunk() == nil {
		h.t.Fatal("sdktest: no run_on_stream_chunk handler registered")
	}
	in := &pbv1.HookInput{
		ContractRevision: sdk.ContractRevision,
		RequestId:        r.requestID,
		Payload:          &pbv1.HookInput_StreamEvent{StreamEvent: ev},
	}
	raw, err := r.dispatch(in, "run_on_stream_chunk")
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
	if !r.h.hookAllowed("run_on_http_request") {
		return HTTPResult{Err: fmt.Errorf("sdktest: hook run_on_http_request is not declared")}
	}
	h := r.h
	h.t.Helper()
	if sdk.RegisteredHTTPRequest() == nil {
		h.t.Fatal("sdktest: no run_on_http_request handler registered")
	}
	in := &pbv1.HookInput{
		ContractRevision: sdk.ContractRevision,
		RequestId:        r.requestID,
		Payload:          &pbv1.HookInput_HttpRequest{HttpRequest: req},
	}
	raw, err := r.dispatch(in, "run_on_http_request")
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
	if !r.h.hookAllowed("run_on_tick") {
		return TickResult{Err: fmt.Errorf("sdktest: hook run_on_tick is not declared")}
	}
	h := r.h
	h.t.Helper()
	if sdk.RegisteredTick() == nil {
		h.t.Fatal("sdktest: no run_on_tick handler registered")
	}
	in := &pbv1.HookInput{
		ContractRevision: sdk.ContractRevision,
		RequestId:        r.requestID,
		Payload:          &pbv1.HookInput_TickRequest{TickRequest: req},
	}
	raw, err := r.dispatch(in, "run_on_tick")
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
