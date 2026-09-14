package plugin_sdk

import (
	"context"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

// Handler registrations and invocation context shared by the wasip1 trampoline
// and native test build. Dispatch accepts only the exact ABI-v1 contract
// revision; unsupported revisions fail before a handler runs.

type requestIDCtxKey struct{}
type executionCtxKey struct{}

// RequestID returns the host request id from ctx, or 0 when absent.
func RequestID(ctx context.Context) uint64 {
	id, _ := ctx.Value(requestIDCtxKey{}).(uint64)
	return id
}

func withRequestID(ctx context.Context, id uint64) context.Context {
	return context.WithValue(ctx, requestIDCtxKey{}, id)
}

// Execution returns the host-supplied snapshot for the current hook. It may
// contain effective provider/model, optional conversation identity and
// deadline, synthetic-response status, and hard memory/response/stream-buffer
// limits. It never contains credentials or endpoint secrets. Treat the value
// as immutable; nil means the host did not provide a snapshot.
func Execution(ctx context.Context) *pbv1.ExecutionInfo {
	value, _ := ctx.Value(executionCtxKey{}).(*pbv1.ExecutionInfo)
	return value
}

func withExecution(ctx context.Context, info *pbv1.ExecutionInfo) context.Context {
	if info == nil {
		return ctx
	}
	return context.WithValue(ctx, executionCtxKey{}, info)
}

var (
	beforeRequestHandler func(context.Context, *pbv1.ChatRequest) (RequestResult, error)
	afterResponseHandler func(context.Context, *pbv1.ChatResponse, bool) (ResponseResult, error)
	streamChunkHandler   func(context.Context, *pbv1.StreamEvent) (StreamResult, error)
	httpRequestHandler   func(context.Context, *pbv1.HttpRequest) (HTTPResult, error)
	tickHandler          func(context.Context, *pbv1.TickRequest) (TickResult, error)
)

// OnBeforeRequest registers the before-request handler.
// A non-nil error traps the guest so the host applies failure_mode.
func OnBeforeRequest(handler func(context.Context, *pbv1.ChatRequest) (RequestResult, error)) {
	claimHook(HookBeforeRequest, handler)
	beforeRequestHandler = handler
}

// OnAfterResponse registers the after-response handler.
// mutable is false for observational dispatches (streamed or errored responses).
func OnAfterResponse(handler func(context.Context, *pbv1.ChatResponse, bool) (ResponseResult, error)) {
	claimHook(HookAfterResponse, handler)
	afterResponseHandler = handler
}

// OnStreamChunk registers the stream-chunk handler.
// Prefer StreamHandler for tool-call assembly and multiple stream interests.
// Errors from a raw handler or StreamHandler callback trap so the host applies
// failure_mode.
func OnStreamChunk(handler func(context.Context, *pbv1.StreamEvent) (StreamResult, error)) {
	claimHook(HookStreamChunk, handler)
	streamChunkHandler = handler
}

// OnHTTPRequest registers the plugin-served HTTP handler.
func OnHTTPRequest(handler func(context.Context, *pbv1.HttpRequest) (HTTPResult, error)) {
	claimHook(HookHTTPRequest, handler)
	httpRequestHandler = handler
}

// OnTick registers the background-tick handler.
func OnTick(handler func(context.Context, *pbv1.TickRequest) (TickResult, error)) {
	claimHook(HookTick, handler)
	tickHandler = handler
}
