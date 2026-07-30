package plugin_sdk

import (
	"context"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// Handler registrations and the context key for request_id. Shared by the
// wasip1 trampoline and the non-WASM test build.

type requestIDCtxKey struct{}

// RequestID returns the host request id from ctx, or 0 when absent.
func RequestID(ctx context.Context) uint64 {
	id, _ := ctx.Value(requestIDCtxKey{}).(uint64)
	return id
}

func withRequestID(ctx context.Context, id uint64) context.Context {
	return context.WithValue(ctx, requestIDCtxKey{}, id)
}

var (
	beforeRequestHandler func(context.Context, *pbv2.ChatRequest) RequestResult
	afterResponseHandler func(context.Context, *pbv2.ChatResponse, bool) ResponseResult
	streamChunkHandler   func(context.Context, *pbv2.StreamEvent) StreamResult
	httpRequestHandler   func(context.Context, *pbv2.HttpRequest) HTTPResult
	tickHandler          func(context.Context, *pbv2.TickRequest) TickResult
)

// OnBeforeRequest registers the before-request handler.
func OnBeforeRequest(handler func(context.Context, *pbv2.ChatRequest) RequestResult) {
	claimHook(HookBeforeRequest, handler)
	beforeRequestHandler = handler
}

// OnAfterResponse registers the after-response handler.
// mutable is false for observational dispatches (streamed or errored responses).
func OnAfterResponse(handler func(context.Context, *pbv2.ChatResponse, bool) ResponseResult) {
	claimHook(HookAfterResponse, handler)
	afterResponseHandler = handler
}

// OnStreamChunk registers the stream-chunk handler.
// Prefer StreamHandler for tool-call assembly and multiple stream interests.
func OnStreamChunk(handler func(context.Context, *pbv2.StreamEvent) StreamResult) {
	claimHook(HookStreamChunk, handler)
	streamChunkHandler = handler
}

// OnHTTPRequest registers the plugin-served HTTP handler.
func OnHTTPRequest(handler func(context.Context, *pbv2.HttpRequest) HTTPResult) {
	claimHook(HookHTTPRequest, handler)
	httpRequestHandler = handler
}

// OnTick registers the background-tick handler.
func OnTick(handler func(context.Context, *pbv2.TickRequest) TickResult) {
	claimHook(HookTick, handler)
	tickHandler = handler
}
