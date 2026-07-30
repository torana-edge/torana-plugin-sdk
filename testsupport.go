//go:build !wasip1

package plugin_sdk

import (
	"context"
	"sync"
	"sync/atomic"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// TestHost is the seam the sdktest package drives. Excluded from wasip1 builds.
type TestHost struct {
	HostCall func(cmd string, args []byte) ([]byte, error)
	Log      func(msg string, level int32)
	Metric   func(name string, metricType int32, value float64, labels map[string]string)
}

var (
	testHostPtr atomic.Pointer[TestHost]
	dispatchMu  sync.Mutex
)

func testHostOf() *TestHost { return testHostPtr.Load() }

func ResetRegistrations() { resetRegistrations() }

func WithTestHost(h *TestHost, fn func()) {
	dispatchMu.Lock()
	defer dispatchMu.Unlock()

	prev := testHostPtr.Load()
	testHostPtr.Store(h)
	defer testHostPtr.Store(prev)

	fn()
}

func RegisteredBeforeRequest() func(context.Context, *pbv2.ChatRequest) (RequestResult, error) {
	return beforeRequestHandler
}

func RegisteredAfterResponse() func(context.Context, *pbv2.ChatResponse, bool) (ResponseResult, error) {
	return afterResponseHandler
}

func RegisteredStreamChunk() func(context.Context, *pbv2.StreamEvent) (StreamResult, error) {
	return streamChunkHandler
}

func RegisteredHTTPRequest() func(context.Context, *pbv2.HttpRequest) (HTTPResult, error) {
	return httpRequestHandler
}

func RegisteredTick() func(context.Context, *pbv2.TickRequest) (TickResult, error) {
	return tickHandler
}

func RegisteredHooks() []string {
	var out []string
	if beforeRequestHandler != nil {
		out = append(out, HookBeforeRequest)
	}
	if afterResponseHandler != nil {
		out = append(out, HookAfterResponse)
	}
	if streamChunkHandler != nil {
		out = append(out, HookStreamChunk)
	}
	if httpRequestHandler != nil {
		out = append(out, HookHTTPRequest)
	}
	if tickHandler != nil {
		out = append(out, HookTick)
	}
	return out
}

// RegisteredHookBitmap is the supported_hooks value derived from registrations.
func RegisteredHookBitmap() pbv2.HookBitmap { return registeredHookBitmap() }
