package plugin_sdk

import (
	"context"
	"strings"
	"testing"

	"github.com/torana-edge/torana-plugin-sdk/pb"
)

// Registering a hook twice is a programming error and must fail loudly at
// registration, not silently replace the first handler.
//
// Silent overwrite was the previous behaviour: a plugin that grew a second file
// — or copied a snippet into the wrong init() — lost a handler with no error
// anywhere, and the symptom was a hook that simply never ran. That is the same
// shape as registering in main() instead of init().
//
// Every hook is covered, not just streams: the footgun is in the mechanism, so
// testing one hook would leave four untested paths to the same failure.
func TestDuplicateRegistrationPanics(t *testing.T) {
	register := map[string]func(){
		HookBeforeRequest: func() {
			OnBeforeRequest(func(context.Context, *pb.ChatRequest) (*pb.ChatRequest, error) { return nil, nil })
		},
		HookAfterResponse: func() {
			OnAfterResponse(func(context.Context, *pb.ChatRequest) (*pb.ChatRequest, error) { return nil, nil })
		},
		HookStreamChunk: func() {
			OnStreamChunk(func(context.Context, *pb.StreamEvent) (*pb.StreamEventResult, error) { return nil, nil })
		},
		HookHTTPRequest: func() {
			OnHTTPRequest(func(context.Context, *pb.HttpRequest) (*pb.HttpResponse, error) { return nil, nil })
		},
		HookTick: func() {
			OnTick(func(context.Context, *pb.TickRequest) (*pb.TickResult, error) { return nil, nil })
		},
	}

	for hook, reg := range register {
		t.Run(hook, func(t *testing.T) {
			resetRegistrations()
			t.Cleanup(resetRegistrations)

			reg() // first registration: fine

			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("registering twice did not panic — the second handler would " +
						"have silently replaced the first")
				}
				msg, ok := r.(string)
				if !ok {
					t.Fatalf("panicked with %T, want a string message", r)
				}
				// The message must name the hook. "registered more than once"
				// alone sends the reader through every file looking for it.
				if !strings.Contains(msg, hook) {
					t.Errorf("panic message does not name the hook:\n%s", msg)
				}
			}()
			reg() // second: must panic
		})
	}
}

// One registration per hook must still work, or the guard is just a way to
// break every plugin.
func TestSingleRegistrationSucceeds(t *testing.T) {
	resetRegistrations()
	t.Cleanup(resetRegistrations)

	OnBeforeRequest(func(context.Context, *pb.ChatRequest) (*pb.ChatRequest, error) { return nil, nil })
	OnAfterResponse(func(context.Context, *pb.ChatRequest) (*pb.ChatRequest, error) { return nil, nil })
	OnStreamChunk(func(context.Context, *pb.StreamEvent) (*pb.StreamEventResult, error) { return nil, nil })
	OnHTTPRequest(func(context.Context, *pb.HttpRequest) (*pb.HttpResponse, error) { return nil, nil })
	OnTick(func(context.Context, *pb.TickRequest) (*pb.TickResult, error) { return nil, nil })

	for _, hook := range []string{
		HookBeforeRequest, HookAfterResponse, HookStreamChunk, HookHTTPRequest, HookTick,
	} {
		if !registered[hook] {
			t.Errorf("%s did not record its registration", hook)
		}
	}
}

// Reset must clear the handlers, not only the bookkeeping.
//
// Clearing only the map would let a later registration succeed while the
// previous handler stayed installed, so a test asserting "no handler" would
// still find one and the reset would look like it worked.
func TestResetClearsHandlersNotJustBookkeeping(t *testing.T) {
	resetRegistrations()
	t.Cleanup(resetRegistrations)

	OnBeforeRequest(func(context.Context, *pb.ChatRequest) (*pb.ChatRequest, error) { return nil, nil })
	if chatRequestHandler == nil {
		t.Fatal("precondition: the handler should be installed")
	}

	resetRegistrations()

	if registered[HookBeforeRequest] {
		t.Error("reset left the registration recorded")
	}
	if chatRequestHandler != nil {
		t.Error("reset left the handler installed, so the next registration would " +
			"succeed while the old handler still ran")
	}
}

// After a reset, registering again must work — that is the whole point for
// tests, which register per-case.
func TestRegistrationSucceedsAfterReset(t *testing.T) {
	resetRegistrations()
	t.Cleanup(resetRegistrations)

	OnTick(func(context.Context, *pb.TickRequest) (*pb.TickResult, error) { return nil, nil })
	resetRegistrations()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("registering after a reset panicked: %v", r)
		}
	}()
	OnTick(func(context.Context, *pb.TickRequest) (*pb.TickResult, error) { return nil, nil })
}
