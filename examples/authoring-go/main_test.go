package main

import (
	"testing"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"github.com/torana-edge/torana-plugin-sdk/sdktest"
)

func TestAllDocumentedHooksDispatchAndVerdictIsRecorded(t *testing.T) {
	h := sdktest.New(t).
		WithPermissions([]string{"env.block_request", "env.log", "env.background_tick", "env.serve_http"}).
		WithHooks([]string{"run_before_request", "run_after_response", "run_on_stream_chunk", "run_on_http_request", "run_on_tick"})
	r := h.NewRequest()
	if got := r.BeforeRequest(&pbv1.ChatRequest{Model: "example"}); got.Err != nil {
		t.Fatal(got.Err)
	}
	if len(r.EffectiveBlockCalls()) != 1 {
		t.Fatal("checked block verdict was not effective")
	}
	if got := r.AfterResponse(&pbv1.ChatResponse{}, false); got.Err != nil {
		t.Fatal(got.Err)
	}
	if got := r.StreamChunk(&pbv1.StreamEvent{Event: &pbv1.StreamEvent_MessageStart{MessageStart: &pbv1.MessageStart{}}}); got.Err != nil {
		t.Fatal(got.Err)
	}
	if got := r.HTTPRequest(&pbv1.HttpRequest{Method: "GET", Path: "/"}); got.Err != nil {
		t.Fatal(got.Err)
	}
	if got := r.Tick(&pbv1.TickRequest{}); got.Err != nil {
		t.Fatal(got.Err)
	}
}

func TestStateCASAndCanonicalModelRecipe(t *testing.T) {
	h := sdktest.New(t).WithPermissions([]string{"env.state_get", "env.state_set", "env.model_complete"})
	h.StubModelComplete(func(*pbv1.ModelCompleteArgs) (*pbv1.ModelCompleteResult, *pbv1.HostError, error) {
		return &pbv1.ModelCompleteResult{Message: &pbv1.ResponseMessage{Blocks: []*pbv1.ResponseBlock{{Kind: &pbv1.ResponseBlock_Text{Text: &pbv1.ResponseTextBlock{Text: `{"safe":true}`}}}}}, FinishReason: "stop"}, nil, nil
	})
	r := h.NewRequest()
	r.Run(func() {
		if err := updateCursor("page-2"); err != nil {
			t.Fatal(err)
		}
		got, err := classify("hello")
		if err != nil || got != `{"safe":true}` {
			t.Fatalf("classify = %q, %v", got, err)
		}
	})
	if got, ok := h.State("cursor"); !ok || got != "page-2" {
		t.Fatalf("cursor = %q, %v", got, ok)
	}
}
