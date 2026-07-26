package main

import (
	"context"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	"github.com/torana-edge/torana-plugin-sdk/pb"
)

func main() {}

func init() {
	sdk.OnBeforeRequest(func(_ context.Context, request *pb.ChatRequest) (*pb.ChatRequest, error) {
		sdk.Log("received request for model "+request.Model, sdk.LogLevelInfo)
		return nil, nil // nil means pass-through.
	})
}
