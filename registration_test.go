package plugin_sdk

import (
	"context"
	"strings"
	"testing"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

func TestDuplicateRegistrationPanics(t *testing.T) {
	register := map[string]func(){
		HookBeforeRequest: func() {
			OnBeforeRequest(func(context.Context, *pbv2.ChatRequest) RequestResult { return PassRequest() })
		},
		HookAfterResponse: func() {
			OnAfterResponse(func(context.Context, *pbv2.ChatResponse, bool) ResponseResult { return PassResponse() })
		},
		HookStreamChunk: func() {
			OnStreamChunk(func(context.Context, *pbv2.StreamEvent) StreamResult { return PassEvent() })
		},
		HookHTTPRequest: func() {
			OnHTTPRequest(func(context.Context, *pbv2.HttpRequest) HTTPResult { return PassHTTP() })
		},
		HookTick: func() {
			OnTick(func(context.Context, *pbv2.TickRequest) TickResult { return TickIdle() })
		},
	}

	for hook, reg := range register {
		t.Run(hook, func(t *testing.T) {
			resetRegistrations()
			t.Cleanup(resetRegistrations)

			reg()

			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("registering twice did not panic")
				}
				msg, ok := r.(string)
				if !ok {
					t.Fatalf("panicked with %T, want a string message", r)
				}
				if !strings.Contains(msg, hook) {
					t.Errorf("panic message does not name the hook:\n%s", msg)
				}
			}()
			reg()
		})
	}
}

func TestSingleRegistrationSucceeds(t *testing.T) {
	resetRegistrations()
	t.Cleanup(resetRegistrations)

	OnBeforeRequest(func(context.Context, *pbv2.ChatRequest) RequestResult { return PassRequest() })
	OnAfterResponse(func(context.Context, *pbv2.ChatResponse, bool) ResponseResult { return PassResponse() })
	OnStreamChunk(func(context.Context, *pbv2.StreamEvent) StreamResult { return PassEvent() })
	OnHTTPRequest(func(context.Context, *pbv2.HttpRequest) HTTPResult { return PassHTTP() })
	OnTick(func(context.Context, *pbv2.TickRequest) TickResult { return TickIdle() })

	for _, hook := range []string{
		HookBeforeRequest, HookAfterResponse, HookStreamChunk, HookHTTPRequest, HookTick,
	} {
		if !registered[hook] {
			t.Errorf("%s not marked registered", hook)
		}
	}
}

func TestNilHandlerPanics(t *testing.T) {
	resetRegistrations()
	t.Cleanup(resetRegistrations)
	defer func() {
		if recover() == nil {
			t.Fatal("nil handler must panic")
		}
	}()
	OnBeforeRequest(nil)
}
