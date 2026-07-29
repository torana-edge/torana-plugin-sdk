//go:build !wasip1

package sdktest

import (
	"context"
	"encoding/json"
	"testing"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	"github.com/torana-edge/torana-plugin-sdk/pb"
)

// RequestResult is the outcome of a run_before_request dispatch.
//
// Err being non-nil is not an ordinary test failure to shrug at: in production
// the SDK panics on a handler error, which traps the guest, and the host then
// applies the plugin's failure_mode. A test that ignores Err is testing a
// path the host treats as a crash.
type RequestResult struct {
	// Request is what the plugin returned. Nil means pass-through — the host
	// forwards the caller's original bytes unchanged.
	Request *pb.ChatRequest
	// PassedThrough is true when the plugin declined to modify the request.
	PassedThrough bool
	Err           error

	// Verdicts the plugin set. Nil when it set none.
	Block   *BlockVerdict
	Respond *RespondVerdict
	Route   *RouteVerdict
}

type BlockVerdict struct {
	Status  int32  `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type RespondVerdict struct {
	Content string `json:"content"`
}

type RouteVerdict struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// BeforeRequest dispatches run_before_request. It fails the test if the plugin
// registered no handler, because a plugin whose hook is unreachable is the
// exact silent failure this package exists to expose.
func (h *Harness) BeforeRequest(req *pb.ChatRequest) RequestResult {
	h.t.Helper()
	handler := sdk.RegisteredBeforeRequest()
	if handler == nil {
		h.t.Fatal("sdktest: no run_before_request handler registered — " +
			"registration must happen in init(), not main(): with -buildmode=c-shared " +
			"the host calls _initialize and main() never runs")
	}

	out, err := handler(context.Background(), req)
	res := RequestResult{Request: out, PassedThrough: out == nil, Err: err}
	if out != nil {
		res.Block, res.Respond, res.Route = decodeVerdicts(h.t, out.ToranaMetaJson)
	}
	return res
}

// AfterResponse dispatches run_after_response.
//
// The payload is a pb.ChatRequest because that is what the v1 contract carries
// on the response path — the host marshals the response into the request shape
// and reads the reply back as one. There is no ChatResponse in v1.
func (h *Harness) AfterResponse(resp *pb.ChatRequest) RequestResult {
	h.t.Helper()
	handler := sdk.RegisteredAfterResponse()
	if handler == nil {
		h.t.Fatal("sdktest: no run_after_response handler registered")
	}
	out, err := handler(context.Background(), resp)
	return RequestResult{Request: out, PassedThrough: out == nil, Err: err}
}

// StreamResult is the outcome of a run_on_stream_chunk dispatch.
type StreamResult struct {
	Result *pb.StreamEventResult
	Err    error
}

// Handled reports whether the plugin took over this event. An unhandled result
// means the host forwards the original event — including when a plugin builds
// a result by hand and forgets to set Handled, which is a common silent bug
// and the reason sdk.Suppress/Replace/Emit exist.
func (s StreamResult) Handled() bool {
	return s.Result != nil && s.Result.Handled
}

// Events is what the plugin emitted in place of the original. Empty with
// Handled true means the event was suppressed.
func (s StreamResult) Events() []*pb.StreamEvent {
	if s.Result == nil {
		return nil
	}
	return s.Result.Events
}

// StreamChunk dispatches run_on_stream_chunk for one event.
func (h *Harness) StreamChunk(ev *pb.StreamEvent) StreamResult {
	h.t.Helper()
	handler := sdk.RegisteredStreamChunk()
	if handler == nil {
		h.t.Fatal("sdktest: no run_on_stream_chunk handler registered")
	}
	out, err := handler(context.Background(), ev)
	return StreamResult{Result: out, Err: err}
}

// StreamChunks dispatches a whole sequence, returning what the host would have
// forwarded downstream. Stream plugins buffer tool-call fragments across
// events, so testing them one event at a time misses the case that matters.
func (h *Harness) StreamChunks(evs ...*pb.StreamEvent) []*pb.StreamEvent {
	h.t.Helper()
	var out []*pb.StreamEvent
	for _, ev := range evs {
		res := h.StreamChunk(ev)
		if res.Err != nil {
			h.t.Fatalf("sdktest: stream chunk: %v", res.Err)
		}
		if !res.Handled() {
			out = append(out, ev)
			continue
		}
		out = append(out, res.Events()...)
	}
	return out
}

// HTTPRequest dispatches run_on_http_request.
func (h *Harness) HTTPRequest(req *pb.HttpRequest) (*pb.HttpResponse, error) {
	h.t.Helper()
	handler := sdk.RegisteredHTTPRequest()
	if handler == nil {
		h.t.Fatal("sdktest: no run_on_http_request handler registered")
	}
	return handler(context.Background(), req)
}

// Tick dispatches run_on_tick.
func (h *Harness) Tick(req *pb.TickRequest) (*pb.TickResult, error) {
	h.t.Helper()
	handler := sdk.RegisteredTick()
	if handler == nil {
		h.t.Fatal("sdktest: no run_on_tick handler registered")
	}
	return handler(context.Background(), req)
}

// decodeVerdicts pulls the verdict keys out of ToranaMetaJson, which is where
// sdk.BlockRequest and friends write them in v1.
func decodeVerdicts(t testing.TB, raw []byte) (*BlockVerdict, *RespondVerdict, *RouteVerdict) {
	t.Helper()
	if len(raw) == 0 {
		return nil, nil, nil
	}
	var meta struct {
		Block   *BlockVerdict   `json:"_block"`
		Respond *RespondVerdict `json:"_respond"`
		Route   *RouteVerdict   `json:"_route"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("sdktest: decode torana meta: %v", err)
	}
	return meta.Block, meta.Respond, meta.Route
}
