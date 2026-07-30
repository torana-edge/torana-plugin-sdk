// Command go-allhooks is a conformance guest that registers EVERY hook.
package main

import (
	"context"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

func main() {}

func init() {
	sdk.OnBeforeRequest(func(context.Context, *pbv2.ChatRequest) (sdk.RequestResult, error) {
		return sdk.PassRequest(), nil
	})
	sdk.OnAfterResponse(func(context.Context, *pbv2.ChatResponse, bool) (sdk.ResponseResult, error) {
		return sdk.PassResponse(), nil
	})
	sdk.OnStreamChunk(func(context.Context, *pbv2.StreamEvent) (sdk.StreamResult, error) {
		return sdk.PassEvent(), nil
	})
	sdk.OnHTTPRequest(func(context.Context, *pbv2.HttpRequest) (sdk.HTTPResult, error) {
		return sdk.ServeHTTP(&pbv2.HttpResponse{Status: 204}), nil
	})
	sdk.OnTick(func(context.Context, *pbv2.TickRequest) (sdk.TickResult, error) {
		return sdk.TickIdle(), nil
	})
}
