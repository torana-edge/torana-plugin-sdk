package sdktest_test

import (
	"context"
	"sync"
	"testing"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
	"github.com/torana-edge/torana-plugin-sdk/sdktest"
)

func TestBeforeRequestReplaceAndPass(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	sdk.OnBeforeRequest(func(_ context.Context, req *pbv2.ChatRequest) sdk.RequestResult {
		req.Model = "rewritten"
		return sdk.ReplaceRequest(req)
	})
	res := sdktest.New(t).BeforeRequest(&pbv2.ChatRequest{Model: "original"})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if res.PassedThrough || res.Request == nil || res.Request.Model != "rewritten" {
		t.Fatalf("got %+v", res)
	}

	sdktest.Reset()
	sdk.OnBeforeRequest(func(context.Context, *pbv2.ChatRequest) sdk.RequestResult {
		return sdk.PassRequest()
	})
	res = sdktest.New(t).BeforeRequest(&pbv2.ChatRequest{Model: "m"})
	if !res.PassedThrough {
		t.Fatal("expected pass-through")
	}
}

func TestBlockRequestIsHostCall(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	sdk.OnBeforeRequest(func(context.Context, *pbv2.ChatRequest) sdk.RequestResult {
		sdk.BlockRequest(422, "pii", "redacted")
		return sdk.PassRequest()
	})
	h := sdktest.New(t)
	h.BeforeRequest(&pbv2.ChatRequest{})
	calls := h.BlockCalls()
	if len(calls) != 1 {
		t.Fatalf("want 1 block call, got %d", len(calls))
	}
	args := sdktest.DecodeBlockArgs(t, calls[0].Args)
	if args.Status != 422 || args.Code != "pii" {
		t.Fatalf("args %+v", args)
	}
}

func TestPluginConfigAndDeny(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	var got string
	sdk.OnBeforeRequest(func(context.Context, *pbv2.ChatRequest) sdk.RequestResult {
		got = sdk.PluginConfig()
		return sdk.PassRequest()
	})
	sdktest.New(t).SetConfig(`{"threshold":42}`).BeforeRequest(&pbv2.ChatRequest{})
	if got != `{"threshold":42}` {
		t.Fatalf("config %q", got)
	}
}

func TestHostCallStringPathStillWorks(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	sdk.OnBeforeRequest(func(context.Context, *pbv2.ChatRequest) sdk.RequestResult {
		_ = sdk.StateSet("k", "v")
		v, _ := sdk.StateGet("k")
		if v != "v" {
			t.Errorf("state get %q", v)
		}
		return sdk.PassRequest()
	})
	sdktest.New(t).BeforeRequest(&pbv2.ChatRequest{})
}

func TestStreamHandlerAssemblesToolCall(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	h := sdktest.New(t)
	stream := sdk.NewStreamHandler()
	stream.OnToolCall(func(_ context.Context, call sdk.ToolCall) (sdk.ToolCallAction, error) {
		if call.Arguments != `{"a":1}` {
			t.Errorf("args %q", call.Arguments)
		}
		return sdk.ReplaceToolArguments(`{"a":2}`), nil
	})
	stream.Register()

	start := &pbv2.StreamEvent{Event: &pbv2.StreamEvent_ContentBlockStart{
		ContentBlockStart: &pbv2.ContentBlockStart{
			Index: 0,
			Block: &pbv2.ContentBlockStart_ToolCall{ToolCall: &pbv2.ToolCallRef{
				Id: "1", Name: "write",
			}},
		},
	}}
	delta := &pbv2.StreamEvent{Event: &pbv2.StreamEvent_ToolCallDelta{
		ToolCallDelta: &pbv2.ToolCallDelta{Index: 0, ArgumentsDelta: `{"a":1}`},
	}}
	stop := &pbv2.StreamEvent{Event: &pbv2.StreamEvent_ContentBlockStop{
		ContentBlockStop: &pbv2.ContentBlockStop{Index: 0},
	}}

	if r := h.StreamChunk(start); !r.Suppressed {
		t.Fatal("start should suppress while assembling")
	}
	if r := h.StreamChunk(delta); !r.Suppressed {
		t.Fatal("delta should suppress")
	}
	r := h.StreamChunk(stop)
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if len(r.Events) != 3 {
		t.Fatalf("want start+delta+stop, got %d events", len(r.Events))
	}
	if got := r.Events[1].GetToolCallDelta().GetArgumentsDelta(); got != `{"a":2}` {
		t.Fatalf("replacement args %q", got)
	}
}

func TestParallelDispatchSerializes(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	sdk.OnBeforeRequest(func(_ context.Context, req *pbv2.ChatRequest) sdk.RequestResult {
		_ = req.Model
		return sdk.PassRequest()
	})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sdktest.New(t).BeforeRequest(&pbv2.ChatRequest{})
		}()
	}
	wg.Wait()
}

func TestHTTPAndTick(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	sdk.OnHTTPRequest(func(context.Context, *pbv2.HttpRequest) sdk.HTTPResult {
		return sdk.ServeHTTP(&pbv2.HttpResponse{Status: 204})
	})
	sdk.OnTick(func(context.Context, *pbv2.TickRequest) sdk.TickResult {
		return sdk.TickDid(1, "ok")
	})
	h := sdktest.New(t)
	hr := h.HTTPRequest(&pbv2.HttpRequest{Method: "GET", Path: "/"})
	if hr.Response == nil || hr.Response.Status != 204 {
		t.Fatalf("%+v", hr)
	}
	tr := h.Tick(&pbv2.TickRequest{TickId: 1})
	if tr.Outcome == nil || tr.Outcome.Actions != 1 {
		t.Fatalf("%+v", tr)
	}
}
