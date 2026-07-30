//go:build wasip1

package plugin_sdk

import (
	"context"
	"encoding/json"
	"sync"
	"unsafe"

	"github.com/torana-edge/torana-plugin-sdk/pb"
	"google.golang.org/protobuf/proto"
)

var (
	pinned   = make(map[uint32][]byte)
	pinMutex sync.Mutex
)

//go:wasmexport alloc
func alloc(size uint32) uint32 {
	if size == 0 {
		return 0
	}
	buf := make([]byte, size)
	ptr := uint32(uintptr(unsafe.Pointer(&buf[0])))
	pinMutex.Lock()
	pinned[ptr] = buf
	pinMutex.Unlock()
	return ptr
}

//go:wasmexport dealloc
func dealloc(ptr uint32, size uint32) {
	pinMutex.Lock()
	delete(pinned, ptr)
	pinMutex.Unlock()
}

// ReadBytes reads from a pointer returned by alloc.
func ReadBytes(ptr, size uint32) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), int(size))
}

// WriteResult allocates memory, copies data, returns packed ptr|len.
func WriteResult(data []byte) uint64 {
	p := alloc(uint32(len(data)))
	copy(ReadBytes(p, uint32(len(data))), data)
	return uint64(p)<<32 | uint64(len(data))
}

var chatRequestHandler func(ctx context.Context, req *pb.ChatRequest) (*pb.ChatRequest, error)

// OnBeforeRequest registers the handler for chat requests.
func OnBeforeRequest(handler func(ctx context.Context, req *pb.ChatRequest) (*pb.ChatRequest, error)) {
	claimHook(HookBeforeRequest, handler)
	chatRequestHandler = handler
}

//go:wasmexport run_before_request
func run_before_request(reqID uint64, ptr, size uint32) uint64 {
	if chatRequestHandler == nil {
		return 0
	}
	inputBytes := ReadBytes(ptr, size)
	var req pb.ChatRequest
	if err := proto.Unmarshal(inputBytes, &req); err != nil {
		panic("torana sdk: decode run_before_request: " + err.Error())
	}

	out, err := chatRequestHandler(context.WithValue(context.Background(), "reqID", reqID), &req)
	if err != nil {
		panic("torana plugin: run_before_request: " + err.Error())
	}
	if out == nil {
		return 0
	}

	outBytes, err := proto.Marshal(out)
	if err != nil {
		panic("torana sdk: encode run_before_request: " + err.Error())
	}
	if len(outBytes) == 0 {
		// A degenerate all-defaults request marshals to nothing. Pass through
		// rather than trap: zero is the ABI's pass-through signal, and the
		// host will send the caller's original bytes.
		return 0
	}
	return WriteResult(outBytes)
}

var chatResponseHandler func(ctx context.Context, resp *pb.ChatRequest) (*pb.ChatRequest, error)

// OnAfterResponse registers the handler for a completed, non-streaming response.
//
// The parameter is a pb.ChatRequest, and that is deliberate rather than a
// mistake — there is no pb.ChatResponse in the v1 contract.
//
// Torana normalises a provider's reply into the SAME message shape it uses for
// a request: the assistant's turn arrives as Messages, its tool calls as
// ToolCalls, and provider metadata under ToranaMetaJson["_response"]. One shape
// means a plugin that rewrites message content works identically on the way out
// and on the way in, and the host's four provider adapters have one target
// instead of two.
//
// The host marshals a pb.ChatRequest for this hook and unmarshals the reply as
// one, so the types match end to end. Returning nil is pass-through.
func OnAfterResponse(handler func(ctx context.Context, resp *pb.ChatRequest) (*pb.ChatRequest, error)) {
	claimHook(HookAfterResponse, handler)
	chatResponseHandler = handler
}

//go:wasmexport run_after_response
func run_after_response(reqID uint64, ptr, size uint32) uint64 {
	if chatResponseHandler == nil {
		return 0
	}
	inputBytes := ReadBytes(ptr, size)
	var resp pb.ChatRequest
	if err := proto.Unmarshal(inputBytes, &resp); err != nil {
		// Trap, do not return 0. ABI.md reserves zero for a deliberate
		// pass-through, so reporting a codec failure that way told the host the
		// plugin had chosen not to act: the instance was kept, failure_mode was
		// never consulted, and a corrupt payload looked like a healthy no-op.
		// The other three hooks and every Rust hook already trap here.
		panic("torana sdk: decode run_after_response: " + err.Error())
	}

	out, err := chatResponseHandler(context.WithValue(context.Background(), "reqID", reqID), &resp)
	if err != nil {
		panic("torana plugin: run_after_response: " + err.Error())
	}
	if out == nil {
		return 0
	}

	outBytes, err := proto.Marshal(out)
	if err != nil {
		panic("torana sdk: encode run_after_response: " + err.Error())
	}
	if len(outBytes) == 0 {
		// See run_before_request: zero means pass-through, not failure.
		return 0
	}
	return WriteResult(outBytes)
}

var streamChunkHandler func(ctx context.Context, chunk *pb.StreamEvent) (*pb.StreamEventResult, error)

// OnStreamChunk registers the handler for stream chunks.
//
// The handler returns a *pb.StreamEventResult describing what should replace
// the input event: use Pass() (or nil) to forward it unchanged, Suppress()
// to drop it, Replace(ev) to substitute it, or Emit(evs...) to fan out
// multiple events in its place.
func OnStreamChunk(handler func(ctx context.Context, chunk *pb.StreamEvent) (*pb.StreamEventResult, error)) {
	claimHook(HookStreamChunk, handler)
	streamChunkHandler = handler
}

// Pass forwards the input event unchanged (equivalent to returning nil).
func Pass() *pb.StreamEventResult { return nil }

// Suppress drops the input event from the stream.
func Suppress() *pb.StreamEventResult {
	return &pb.StreamEventResult{Handled: true}
}

// Replace substitutes the input event with ev.
func Replace(ev *pb.StreamEvent) *pb.StreamEventResult {
	return &pb.StreamEventResult{Handled: true, Events: []*pb.StreamEvent{ev}}
}

// Emit replaces the input event with the given events (fan-out).
func Emit(evs ...*pb.StreamEvent) *pb.StreamEventResult {
	return &pb.StreamEventResult{Handled: true, Events: evs}
}

//go:wasmexport run_on_stream_chunk
func run_on_stream_chunk(reqID uint64, ptr, size uint32) uint64 {
	if streamChunkHandler == nil {
		return 0
	}
	inputBytes := ReadBytes(ptr, size)
	var chunk pb.StreamEvent
	if err := proto.Unmarshal(inputBytes, &chunk); err != nil {
		// Trap, do not return 0. ABI.md reserves zero for a deliberate
		// pass-through, so reporting a codec failure that way told the host the
		// plugin had chosen not to act: the instance was kept, failure_mode was
		// never consulted, and a corrupt payload looked like a healthy no-op.
		// The other three hooks and every Rust hook already trap here.
		panic("torana sdk: decode run_on_stream_chunk: " + err.Error())
	}

	out, err := streamChunkHandler(context.WithValue(context.Background(), "reqID", reqID), &chunk)
	if err != nil {
		panic("torana plugin: run_on_stream_chunk: " + err.Error())
	}
	if out == nil || !out.Handled {
		return 0
	}

	outBytes, err := proto.Marshal(out)
	if err != nil {
		panic("torana sdk: encode run_on_stream_chunk: " + err.Error())
	}
	if len(outBytes) == 0 {
		return 0
	}
	return WriteResult(outBytes)
}

var httpRequestHandler func(ctx context.Context, req *pb.HttpRequest) (*pb.HttpResponse, error)

// OnHTTPRequest registers the handler for the run_on_http_request hook.
// The plugin serves HTTP under /_torana/plugin/<name>/* when it also declares
// the env.serve_http permission in its manifest. The returned *pb.HttpResponse
// MUST have Handled=true for the host to deliver the response to the caller.
func OnHTTPRequest(handler func(ctx context.Context, req *pb.HttpRequest) (*pb.HttpResponse, error)) {
	claimHook(HookHTTPRequest, handler)
	httpRequestHandler = handler
}

//go:wasmexport run_on_http_request
func run_on_http_request(reqID uint64, ptr, size uint32) uint64 {
	if httpRequestHandler == nil {
		return 0
	}
	inputBytes := ReadBytes(ptr, size)
	var req pb.HttpRequest
	if err := proto.Unmarshal(inputBytes, &req); err != nil {
		panic("torana sdk: decode run_on_http_request: " + err.Error())
	}

	out, err := httpRequestHandler(context.WithValue(context.Background(), "reqID", reqID), &req)
	if err != nil {
		panic("torana plugin: run_on_http_request: " + err.Error())
	}
	if out == nil || !out.Handled {
		return 0
	}

	outBytes, err := proto.Marshal(out)
	if err != nil {
		panic("torana sdk: encode run_on_http_request: " + err.Error())
	}
	if len(outBytes) == 0 {
		return 0
	}
	return WriteResult(outBytes)
}

var tickHandler func(ctx context.Context, req *pb.TickRequest) (*pb.TickResult, error)

// OnTick registers the handler for the run_on_tick hook, which the host fires
// periodically with no request in flight. It requires the env.background_tick
// permission in the manifest.
//
// This is the only hook that runs when nothing is happening, which makes it the
// only place a plugin can act on elapsed time. Note what is NOT available here:
// there is no request, so env.original_request, env.original_response and
// env.meta_* have nothing to read, and the caller's credential does not exist.
// Anything a tick needs must come from the plugin's own durable state or from a
// host call that resolves its own configuration.
//
// The returned *pb.TickResult MUST have Handled=true for the host to record it;
// an all-defaults message is indistinguishable from doing nothing. Returning nil
// is the correct way to say "nothing to do this tick".
func OnTick(handler func(ctx context.Context, req *pb.TickRequest) (*pb.TickResult, error)) {
	claimHook(HookTick, handler)
	tickHandler = handler
}

//go:wasmexport run_on_tick
func run_on_tick(reqID uint64, ptr, size uint32) uint64 {
	if tickHandler == nil {
		return 0
	}
	inputBytes := ReadBytes(ptr, size)
	var req pb.TickRequest
	if err := proto.Unmarshal(inputBytes, &req); err != nil {
		panic("torana sdk: decode run_on_tick: " + err.Error())
	}

	out, err := tickHandler(context.WithValue(context.Background(), "reqID", reqID), &req)
	if err != nil {
		panic("torana plugin: run_on_tick: " + err.Error())
	}
	if out == nil || !out.Handled {
		return 0
	}

	outBytes, err := proto.Marshal(out)
	if err != nil {
		panic("torana sdk: encode run_on_tick: " + err.Error())
	}
	if len(outBytes) == 0 {
		return 0
	}
	return WriteResult(outBytes)
}

//go:wasmimport env log
func hostLog(level int32, ptr uint32, length uint32)

const (
	LogLevelDebug = 0
	LogLevelInfo  = 1
)

func Log(msg string, level int32) {
	b := []byte(msg)
	if len(b) == 0 {
		return
	}
	ptr := alloc(uint32(len(b)))
	copy(ReadBytes(ptr, uint32(len(b))), b)
	hostLog(level, ptr, uint32(len(b)))
	dealloc(ptr, uint32(len(b)))
}

//go:wasmimport env emit_metric
func hostEmitMetric(metricType int32, ptr uint32, length uint32, value float64, labelsPtr uint32, labelsLen uint32)

// Metric types accepted by EmitMetric (mirrors the host's OTel mapping).
const (
	MetricCounter   = 0
	MetricHistogram = 1
	MetricGauge     = 2
)

// EmitMetric records a named metric, with optional labels, via the host's OTel
// exporter. Requires the env.emit_metric permission in the plugin manifest.
func EmitMetric(name string, metricType int32, value float64, labels map[string]string) {
	b := []byte(name)
	if len(b) == 0 {
		return
	}
	ptr := alloc(uint32(len(b)))
	copy(ReadBytes(ptr, uint32(len(b))), b)
	defer dealloc(ptr, uint32(len(b)))

	var lPtr, lLen uint32
	if len(labels) > 0 {
		if lb, err := json.Marshal(labels); err == nil {
			lPtr = alloc(uint32(len(lb)))
			copy(ReadBytes(lPtr, uint32(len(lb))), lb)
			lLen = uint32(len(lb))
			defer dealloc(lPtr, lLen)
		}
	}
	hostEmitMetric(metricType, ptr, uint32(len(b)), value, lPtr, lLen)
}

//go:wasmimport env host_call
func hostCall(cmdPtr uint32, cmdLen uint32, argsPtr uint32, argsLen uint32) uint64

// PluginConfig returns this plugin's config JSON blob (plugins.config.<name>
// from the Torana config), or "{}" when none is set or the env.plugin_config
// permission is missing. Callers should tolerate absent/zero fields.
func PluginConfig() string {
	res, err := HostCall("env.plugin_config", "")
	// Without the isPermissionDenied check this returned the host's refusal
	// envelope AS the config blob, so a plugin missing env.plugin_config parsed
	// {"status":"error","message":"permission denied"} and saw a config with no
	// recognised fields — silently running on defaults instead of reporting a
	// missing grant. Every other host-call wrapper checks it; the doc comment
	// above already promised this behaviour.
	if err != nil || res == "" || isPermissionDenied(res) {
		return "{}"
	}
	return res
}

// HostCall invokes a registered host function by name.
func HostCall(cmd string, args string) (string, error) {
	cb := []byte(cmd)
	ab := []byte(args)
	if len(cb) == 0 {
		return "", nil
	}

	cPtr := alloc(uint32(len(cb)))
	copy(ReadBytes(cPtr, uint32(len(cb))), cb)
	defer dealloc(cPtr, uint32(len(cb)))

	var aPtr uint32
	if len(ab) > 0 {
		aPtr = alloc(uint32(len(ab)))
		copy(ReadBytes(aPtr, uint32(len(ab))), ab)
		defer dealloc(aPtr, uint32(len(ab)))
	}

	ret := hostCall(cPtr, uint32(len(cb)), aPtr, uint32(len(ab)))
	if ret == 0 {
		return "", nil
	}

	outPtr := uint32(ret >> 32)
	outLen := uint32(ret & 0xFFFFFFFF)
	res := string(ReadBytes(outPtr, outLen))
	dealloc(outPtr, outLen)

	return res, nil
}
