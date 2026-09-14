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
		return nil, fmt.Errorf("validate hook input: input is nil")
	}
	if err := in.Validate(); err != nil {
		return nil, fmt.Errorf("%s: validate input: %w", in.HookOf(), err)
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
			return nil, fmt.Errorf("%s: handler: %w", hook, herr)
		}
		hr, err = res.hookResult()
	case pbv1.Hook_HOOK_AFTER_RESPONSE:
		if afterResponseHandler == nil {
			return nil, nil
		}
		ar := in.GetAfterResponse()
		res, herr := afterResponseHandler(ctx, ar.GetResponse(), ar.GetMutable())
		if herr != nil {
			return nil, fmt.Errorf("%s: handler: %w", hook, herr)
		}
		hr, err = res.hookResult()
	case pbv1.Hook_HOOK_ON_STREAM_CHUNK:
		if streamChunkHandler == nil {
			return nil, nil
		}
		res, herr := streamChunkHandler(ctx, in.GetStreamEvent())
		if herr != nil {
			return nil, fmt.Errorf("%s: handler: %w", hook, herr)
		}
		hr, err = res.hookResult()
	case pbv1.Hook_HOOK_ON_HTTP_REQUEST:
		if httpRequestHandler == nil {
			return nil, nil
		}
		res, herr := httpRequestHandler(ctx, in.GetHttpRequest())
		if herr != nil {
			return nil, fmt.Errorf("%s: handler: %w", hook, herr)
		}
		hr, err = res.hookResult()
	case pbv1.Hook_HOOK_ON_TICK:
		if tickHandler == nil {
			return nil, nil
		}
		res, herr := tickHandler(ctx, in.GetTickRequest())
		if herr != nil {
			return nil, fmt.Errorf("%s: handler: %w", hook, herr)
		}
		hr, err = res.hookResult()
	default:
		return nil, fmt.Errorf("unhandled hook %v", hook)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: construct result: %w", hook, err)
	}
	if hr == nil {
		return nil, nil
	}
	if err := hr.ValidateFor(hook); err != nil {
		return nil, fmt.Errorf("%s: validate result: %w", hook, err)
	}
	out, err := proto.Marshal(hr)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal result: %w", hook, err)
	}
	return out, nil
}

// PluginConfig returns the config JSON, defaulting to "{}" when unset or
// unavailable. Use PluginConfigStrict for policy decisions that must distinguish
// missing configuration from a refused call or malformed host reply.
func PluginConfig() string {
	config, herr, err := PluginConfigStrict()
	if err != nil || herr != nil {
		return "{}"
	}
	return config
}

// PluginConfigStrict reads config without suppressing failures. A successful
// empty value means no operator settings and returns "{}". Classified refusals
// and local/protocol errors remain separate, as in HostCall.
func PluginConfigStrict() (string, *pbv1.HostError, error) {
	raw, herr, err := HostCall("env.plugin_config", nil)
	if err != nil || herr != nil {
		return "", herr, err
	}
	if len(raw) == 0 {
		return "{}", nil, nil
	}
	return string(raw), nil, nil
}
