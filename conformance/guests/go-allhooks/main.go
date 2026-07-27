// Command go-allhooks is a conformance guest that registers EVERY hook.
//
// The examples register only run_before_request, so an unregistered hook
// returns 0 before it ever decodes anything. That is correct, but it makes the
// examples useless for testing decode behaviour: a hook that ignores a codec
// error and a hook with no handler are indistinguishable from outside.
//
// This guest registers all of them, so a 0 from any hook means the SDK decoded
// nothing and reported it as a deliberate pass-through.
package main

import (
	"context"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	"github.com/torana-edge/torana-plugin-sdk/pb"
)

func main() {}

func init() {
	// Every handler passes through. The hooks exist to be reached, not to do
	// anything — what is under test is what the SDK does before calling them.
	sdk.OnBeforeRequest(func(context.Context, *pb.ChatRequest) (*pb.ChatRequest, error) {
		return nil, nil
	})
	sdk.OnAfterResponse(func(context.Context, *pb.ChatRequest) (*pb.ChatRequest, error) {
		return nil, nil
	})
	sdk.OnStreamChunk(func(context.Context, *pb.StreamEvent) (*pb.StreamEventResult, error) {
		return nil, nil
	})
	sdk.OnHTTPRequest(func(context.Context, *pb.HttpRequest) (*pb.HttpResponse, error) {
		return nil, nil
	})
	sdk.OnTick(func(context.Context, *pb.TickRequest) (*pb.TickResult, error) {
		return nil, nil
	})
}
