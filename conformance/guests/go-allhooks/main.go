// Command go-allhooks is a conformance guest that registers EVERY hook.
package main

import (
	"context"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

func main() {}

func init() {
	sdk.OnBeforeRequest(func(context.Context, *pbv2.ChatRequest) sdk.RequestResult {
		return sdk.PassRequest()
	})
	sdk.OnAfterResponse(func(context.Context, *pbv2.ChatResponse, bool) sdk.ResponseResult {
		return sdk.PassResponse()
	})
	sdk.OnStreamChunk(func(context.Context, *pbv2.StreamEvent) sdk.StreamResult {
		return sdk.PassEvent()
	})
	sdk.OnHTTPRequest(func(context.Context, *pbv2.HttpRequest) sdk.HTTPResult {
		return sdk.ServeHTTP(&pbv2.HttpResponse{Status: 204})
	})
	sdk.OnTick(func(context.Context, *pbv2.TickRequest) sdk.TickResult {
		return sdk.TickIdle()
	})
}
