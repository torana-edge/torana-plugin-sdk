//go:build !wasip1

package plugin_sdk

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// Non-WASM build: registrations and host calls are driven by sdktest.

//nolint:unused
func alloc(size uint32) uint32 { return 0 }

//nolint:unused
func dealloc(ptr uint32, size uint32)   {}
func ReadBytes(ptr, size uint32) []byte { return nil }
func WriteResult(data []byte) uint64    { return 0 }

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
	if h := testHostOf(); h != nil && h.Log != nil {
		h.Log(msg, level)
	}
}

func EmitMetric(name string, metricType int32, value float64, labels map[string]string) {
	if h := testHostOf(); h != nil && h.Metric != nil {
		h.Metric(name, metricType, value, labels)
	}
}

func hostCallRawImpl(cmd string, args []byte) ([]byte, error) {
	h := testHostOf()
	if h == nil || h.HostCall == nil {
		return nil, nil
	}
	return h.HostCall(cmd, args)
}

func PluginConfig() string {
	res, err := hostCallString("env.plugin_config", "")
	if err != nil || res == "" || isPermissionDenied(res) {
		return "{}"
	}
	return res
}

// DispatchHook runs the registered handler for in and returns the framed
// result bytes (nil for pass-through). Used by sdktest; mirrors run_hook.
func DispatchHook(in *pbv2.HookInput) ([]byte, error) {
	if in == nil {
		return nil, fmt.Errorf("hook input is nil")
	}
	if err := in.Validate(); err != nil {
		return nil, err
	}
	hook := in.HookOf()
	ctx := withRequestID(context.Background(), in.RequestId)

	var (
		hr  *pbv2.HookResult
		err error
	)
	switch hook {
	case pbv2.Hook_HOOK_BEFORE_REQUEST:
		if beforeRequestHandler == nil {
			return nil, nil
		}
		res, herr := beforeRequestHandler(ctx, in.GetChatRequest())
		if herr != nil {
			return nil, herr
		}
		hr, err = res.hookResult()
	case pbv2.Hook_HOOK_AFTER_RESPONSE:
		if afterResponseHandler == nil {
			return nil, nil
		}
		ar := in.GetAfterResponse()
		res, herr := afterResponseHandler(ctx, ar.GetResponse(), ar.GetMutable())
		if herr != nil {
			return nil, herr
		}
		hr, err = res.hookResult()
	case pbv2.Hook_HOOK_ON_STREAM_CHUNK:
		if streamChunkHandler == nil {
			return nil, nil
		}
		res, herr := streamChunkHandler(ctx, in.GetStreamEvent())
		if herr != nil {
			return nil, herr
		}
		hr, err = res.hookResult()
	case pbv2.Hook_HOOK_ON_HTTP_REQUEST:
		if httpRequestHandler == nil {
			return nil, nil
		}
		res, herr := httpRequestHandler(ctx, in.GetHttpRequest())
		if herr != nil {
			return nil, herr
		}
		hr, err = res.hookResult()
	case pbv2.Hook_HOOK_ON_TICK:
		if tickHandler == nil {
			return nil, nil
		}
		res, herr := tickHandler(ctx, in.GetTickRequest())
		if herr != nil {
			return nil, herr
		}
		hr, err = res.hookResult()
	default:
		return nil, fmt.Errorf("unhandled hook %v", hook)
	}
	if err != nil {
		return nil, err
	}
	if hr == nil {
		return nil, nil
	}
	if err := hr.ValidateFor(hook); err != nil {
		return nil, err
	}
	return proto.Marshal(hr)
}
