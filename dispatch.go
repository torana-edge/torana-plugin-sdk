package plugin_sdk

import (
	"context"
	"fmt"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"google.golang.org/protobuf/proto"
)

// DispatchHook runs the registered handler for in and returns the framed
// result bytes (nil for pass-through). Both sdktest and the WASI run_hook
// wrapper execute this implementation.
func DispatchHook(in *pbv1.HookInput) ([]byte, error) {
	if in == nil {
		return nil, fmt.Errorf("hook input is nil")
	}
	if err := in.Validate(); err != nil {
		return nil, err
	}
	hook := in.HookOf()
	ctx := withRequestID(context.Background(), in.RequestId)

	var (
		hr  *pbv1.HookResult
		err error
	)
	switch hook {
	case pbv1.Hook_HOOK_BEFORE_REQUEST:
		if beforeRequestHandler == nil {
			return nil, nil
		}
		res, herr := beforeRequestHandler(ctx, in.GetChatRequest())
		if herr != nil {
			return nil, herr
		}
		hr, err = res.hookResult()
	case pbv1.Hook_HOOK_AFTER_RESPONSE:
		if afterResponseHandler == nil {
			return nil, nil
		}
		ar := in.GetAfterResponse()
		res, herr := afterResponseHandler(ctx, ar.GetResponse(), ar.GetMutable())
		if herr != nil {
			return nil, herr
		}
		hr, err = res.hookResult()
	case pbv1.Hook_HOOK_ON_STREAM_CHUNK:
		if streamChunkHandler == nil {
			return nil, nil
		}
		res, herr := streamChunkHandler(ctx, in.GetStreamEvent())
		if herr != nil {
			return nil, herr
		}
		hr, err = res.hookResult()
	case pbv1.Hook_HOOK_ON_HTTP_REQUEST:
		if httpRequestHandler == nil {
			return nil, nil
		}
		res, herr := httpRequestHandler(ctx, in.GetHttpRequest())
		if herr != nil {
			return nil, herr
		}
		hr, err = res.hookResult()
	case pbv1.Hook_HOOK_ON_TICK:
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

// PluginConfig returns this plugin's config JSON blob, or "{}" when unset or
// denied.
//
// "{}" rather than an error because every caller unmarshals the result, and an
// absent config genuinely means "no operator settings" — the plugin should run
// on its defaults. Returning an error would make every plugin write the same
// fallback.
func PluginConfig() string {
	raw, herr, err := HostCall("env.plugin_config", nil)
	if err != nil || herr != nil || len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}
