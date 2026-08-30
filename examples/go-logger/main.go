package main

import (
	"context"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

func main() {}

func init() {
	sdk.OnBeforeRequest(func(_ context.Context, request *pbv1.ChatRequest) (sdk.RequestResult, error) {
		sdk.Log("received request for model "+request.Model, sdk.LogLevelInfo)
		return sdk.PassRequest(), nil
	})
}
