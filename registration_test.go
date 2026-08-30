package plugin_sdk

import (
	"context"
	"strings"
	"testing"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

func TestDuplicateRegistrationPanics(t *testing.T) {
	register := map[string]func(){
		HookBeforeRequest: func() {
			OnBeforeRequest(func(context.Context, *pbv1.ChatRequest) (RequestResult, error) {
				return PassRequest(), nil
			})
		},
		HookAfterResponse: func() {
			OnAfterResponse(func(context.Context, *pbv1.ChatResponse, bool) (ResponseResult, error) {
				return PassResponse(), nil
			})
		},
		HookStreamChunk: func() {
			OnStreamChunk(func(context.Context, *pbv1.StreamEvent) (StreamResult, error) {
				return PassEvent(), nil
			})
		},
		HookHTTPRequest: func() {
			OnHTTPRequest(func(context.Context, *pbv1.HttpRequest) (HTTPResult, error) {
				return PassHTTP(), nil
			})
		},
		HookTick: func() {
			OnTick(func(context.Context, *pbv1.TickRequest) (TickResult, error) {
				return TickIdle(), nil
			})
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

	OnBeforeRequest(func(context.Context, *pbv1.ChatRequest) (RequestResult, error) {
		return PassRequest(), nil
	})
	OnAfterResponse(func(context.Context, *pbv1.ChatResponse, bool) (ResponseResult, error) {
		return PassResponse(), nil
	})
	OnStreamChunk(func(context.Context, *pbv1.StreamEvent) (StreamResult, error) {
		return PassEvent(), nil
	})
	OnHTTPRequest(func(context.Context, *pbv1.HttpRequest) (HTTPResult, error) {
		return PassHTTP(), nil
	})
	OnTick(func(context.Context, *pbv1.TickRequest) (TickResult, error) {
		return TickIdle(), nil
	})

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
