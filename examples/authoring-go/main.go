package main

import (
	"context"
	"errors"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

func main() {}

// updateCursor is the complete read/version/write pattern for concurrent state.
func updateCursor(next string) error {
	current, found, err := sdk.StateGetVersioned("cursor")
	if err != nil {
		return err
	}
	var expected *string
	if found {
		expected = &current.Version
	}
	result, err := sdk.StateCompareAndSet("cursor", next, expected)
	if err != nil {
		return err
	}
	if !result.Applied {
		return errors.New("cursor changed concurrently; retry from a fresh read")
	}
	return nil
}

// classify asks an operator-bound model for strict structured output.
func classify(text string) (string, error) {
	max := uint32(128)
	strict := true
	return sdk.ModelCompleteText(&pbv1.ModelCompleteArgs{
		Service: "classifier",
		Messages: []*pbv1.Message{{Role: "user", Blocks: []*pbv1.RequestBlock{{
			Kind: &pbv1.RequestBlock_Text{Text: &pbv1.RequestTextBlock{Text: text}},
		}}}},
		MaxTokens:    &max,
		OutputFormat: &pbv1.OutputFormat{Mode: pbv1.OutputFormat_MODE_JSON_SCHEMA, Name: "classification", SchemaJson: []byte(`{"type":"object","properties":{"safe":{"type":"boolean"}},"required":["safe"],"additionalProperties":false}`), Strict: &strict},
	})
}

func init() {
	sdk.OnBeforeRequest(func(ctx context.Context, req *pbv1.ChatRequest) (sdk.RequestResult, error) {
		if info := sdk.Execution(ctx); info != nil {
			sdk.Debug("provider=" + info.Provider)
		}
		if err := sdk.BlockRequest(403, "example", "blocked by authoring example"); err != nil {
			return sdk.RequestResult{}, err
		}
		return sdk.PassRequest(), nil
	})
	sdk.OnAfterResponse(func(context.Context, *pbv1.ChatResponse, bool) (sdk.ResponseResult, error) {
		return sdk.PassResponse(), nil
	})
	sdk.OnStreamChunk(func(context.Context, *pbv1.StreamEvent) (sdk.StreamResult, error) { return sdk.PassEvent(), nil })
	sdk.OnHTTPRequest(func(context.Context, *pbv1.HttpRequest) (sdk.HTTPResult, error) { return sdk.PassHTTP(), nil })
	sdk.OnTick(func(context.Context, *pbv1.TickRequest) (sdk.TickResult, error) { return sdk.TickIdle(), nil })
}
