package main

import (
	"context"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

func main() {}

func init() {
	sdk.OnBeforeRequest(func(_ context.Context, request *pbv2.ChatRequest) sdk.RequestResult {
		sdk.Log("received request for model "+request.Model, sdk.LogLevelInfo)
		return sdk.PassRequest()
	})
}
