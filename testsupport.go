//go:build !wasip1

package plugin_sdk

import (
	"context"

	"github.com/torana-edge/torana-plugin-sdk/pb"
)

// TestHost is the seam the sdktest package drives. It is deliberately not part
// of the plugin-authoring API: this file is excluded from wasip1 builds, so
// none of it exists in a compiled plugin.
//
// Plugin authors should use the sdktest package rather than these directly.
type TestHost struct {
	HostCall func(cmd, args string) (string, error)
	Log      func(msg string, level int32)
	Metric   func(name string, metricType int32, value float64, labels map[string]string)
}

var testHost *TestHost

// InstallTestHost routes Log, EmitMetric and HostCall to h. Passing nil
// restores the inert behaviour.
func InstallTestHost(h *TestHost) { testHost = h }

// The registered-handler accessors below let sdktest dispatch a plugin's hooks
// in-process. They return nil when the plugin did not register that hook,
// which is what makes "declared a hook but never registered a handler"
// detectable from a test — in production that combination silently does
// nothing forever.

func RegisteredBeforeRequest() func(context.Context, *pb.ChatRequest) (*pb.ChatRequest, error) {
	return chatRequestHandler
}

func RegisteredAfterResponse() func(context.Context, *pb.ChatRequest) (*pb.ChatRequest, error) {
	return chatResponseHandler
}

func RegisteredStreamChunk() func(context.Context, *pb.StreamEvent) (*pb.StreamEventResult, error) {
	return streamChunkHandler
}

func RegisteredHTTPRequest() func(context.Context, *pb.HttpRequest) (*pb.HttpResponse, error) {
	return httpRequestHandler
}

func RegisteredTick() func(context.Context, *pb.TickRequest) (*pb.TickResult, error) {
	return tickHandler
}

// RegisteredHooks names the hooks the plugin registered a handler for, in ABI
// order. sdktest uses it to cross-check against plugin.json.
func RegisteredHooks() []string {
	var out []string
	if chatRequestHandler != nil {
		out = append(out, "run_before_request")
	}
	if chatResponseHandler != nil {
		out = append(out, "run_after_response")
	}
	if streamChunkHandler != nil {
		out = append(out, "run_on_stream_chunk")
	}
	if httpRequestHandler != nil {
		out = append(out, "run_on_http_request")
	}
	if tickHandler != nil {
		out = append(out, "run_on_tick")
	}
	return out
}
