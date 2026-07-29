//go:build !wasip1

package plugin_sdk

import (
	"context"

	"github.com/torana-edge/torana-plugin-sdk/pb"
)

// This is the non-WASM build of the SDK. It exists so a plugin module compiles
// and tests under `go test`, where GOOS is the host rather than wasip1.
//
// It used to no-op everything — registrations were dropped on the floor and
// every host call returned an empty string. That made a plugin's own hooks
// unreachable from its tests: `init()` registered nothing, so there was no way
// to invoke a handler, and authors were forced to restructure plugins so the
// hook body lived in separately-testable pure functions. It also meant a test
// suite could stay green while the hook it was meant to cover was broken.
//
// So this build now records what a plugin registers and routes host calls to a
// pluggable fake. The sdktest package drives it. Nothing here is compiled into
// a real plugin: wasip1 builds use sdk.go instead.

//nolint:unused
func alloc(size uint32) uint32 { return 0 }

//nolint:unused
func dealloc(ptr uint32, size uint32)   {}
func ReadBytes(ptr, size uint32) []byte { return nil }
func WriteResult(data []byte) uint64    { return 0 }

var (
	chatRequestHandler  func(ctx context.Context, req *pb.ChatRequest) (*pb.ChatRequest, error)
	chatResponseHandler func(ctx context.Context, resp *pb.ChatRequest) (*pb.ChatRequest, error)
	streamChunkHandler  func(ctx context.Context, chunk *pb.StreamEvent) (*pb.StreamEventResult, error)
	httpRequestHandler  func(ctx context.Context, req *pb.HttpRequest) (*pb.HttpResponse, error)
	tickHandler         func(ctx context.Context, req *pb.TickRequest) (*pb.TickResult, error)
)

func OnBeforeRequest(handler func(ctx context.Context, req *pb.ChatRequest) (*pb.ChatRequest, error)) {
	chatRequestHandler = handler
}

func OnAfterResponse(handler func(ctx context.Context, resp *pb.ChatRequest) (*pb.ChatRequest, error)) {
	chatResponseHandler = handler
}

func OnStreamChunk(handler func(ctx context.Context, chunk *pb.StreamEvent) (*pb.StreamEventResult, error)) {
	streamChunkHandler = handler
}

func OnHTTPRequest(handler func(ctx context.Context, req *pb.HttpRequest) (*pb.HttpResponse, error)) {
	httpRequestHandler = handler
}

func OnTick(handler func(ctx context.Context, req *pb.TickRequest) (*pb.TickResult, error)) {
	tickHandler = handler
}

func Pass() *pb.StreamEventResult     { return nil }
func Suppress() *pb.StreamEventResult { return &pb.StreamEventResult{Handled: true} }
func Replace(ev *pb.StreamEvent) *pb.StreamEventResult {
	return &pb.StreamEventResult{Handled: true, Events: []*pb.StreamEvent{ev}}
}
func Emit(evs ...*pb.StreamEvent) *pb.StreamEventResult {
	return &pb.StreamEventResult{Handled: true, Events: evs}
}

const (
	LogLevelDebug = 0
	LogLevelInfo  = 1
)

const (
	MetricCounter   = 0
	MetricHistogram = 1
	MetricGauge     = 2
)

func Log(msg string, level int32) {
	if h := testHost; h != nil && h.Log != nil {
		h.Log(msg, level)
	}
}

func EmitMetric(name string, metricType int32, value float64, labels map[string]string) {
	if h := testHost; h != nil && h.Metric != nil {
		h.Metric(name, metricType, value, labels)
	}
}

// HostCall routes to the installed test host. With no host installed it
// returns an empty string, matching the previous behaviour so a plugin that is
// compiled but not exercised under sdktest still builds and runs.
//
// The error is always nil, mirroring the wasip1 implementation exactly
// (sdk.go:371) — a divergence here would let a test pass on an error path that
// cannot occur in production.
func HostCall(cmd string, args string) (string, error) {
	if h := testHost; h != nil && h.HostCall != nil {
		return h.HostCall(cmd, args)
	}
	return "", nil
}

func PluginConfig() string {
	res, _ := HostCall("env.plugin_config", "")
	if res == "" || isPermissionDenied(res) {
		return "{}"
	}
	return res
}
