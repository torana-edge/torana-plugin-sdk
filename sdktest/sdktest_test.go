package sdktest_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
	"github.com/torana-edge/torana-plugin-sdk/sdktest"
)

func TestBeforeRequestReplaceAndPass(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	sdk.OnBeforeRequest(func(_ context.Context, req *pbv2.ChatRequest) (sdk.RequestResult, error) {
		req.Model = "rewritten"
		return sdk.ReplaceRequest(req), nil
	})
	res := sdktest.New(t).BeforeRequest(&pbv2.ChatRequest{Model: "original"})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if res.PassedThrough || res.Request == nil || res.Request.Model != "rewritten" {
		t.Fatalf("got %+v", res)
	}

	sdktest.Reset()
	sdk.OnBeforeRequest(func(context.Context, *pbv2.ChatRequest) (sdk.RequestResult, error) {
		return sdk.PassRequest(), nil
	})
	res = sdktest.New(t).BeforeRequest(&pbv2.ChatRequest{Model: "m"})
	if !res.PassedThrough {
		t.Fatal("expected pass-through")
	}
}

func TestHandlerErrorPropagates(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	sdk.OnBeforeRequest(func(context.Context, *pbv2.ChatRequest) (sdk.RequestResult, error) {
		return sdk.PassRequest(), context.Canceled
	})
	res := sdktest.New(t).BeforeRequest(&pbv2.ChatRequest{})
	if res.Err == nil {
		t.Fatal("expected handler error")
	}
	if res.PassedThrough {
		t.Fatal("handler error must not look like pass-through; failure_mode decides")
	}
}

func TestAfterResponseReplace(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	sdk.OnAfterResponse(func(_ context.Context, resp *pbv2.ChatResponse, _ bool) (sdk.ResponseResult, error) {
		resp.Id = "rewritten"
		return sdk.ReplaceResponse(resp), nil
	})
	res := sdktest.New(t).AfterResponse(&pbv2.ChatResponse{Id: "orig"}, true)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if res.PassedThrough || res.Applied == nil || res.Applied.Id != "rewritten" {
		t.Fatalf("mutable apply got %+v", res)
	}
	if res.Replacement == nil || res.Replacement.Id != "rewritten" {
		t.Fatalf("replacement %+v", res.Replacement)
	}

	obs := sdktest.New(t).AfterResponse(&pbv2.ChatResponse{Id: "orig"}, false)
	if obs.Err != nil {
		t.Fatal(obs.Err)
	}
	if obs.Applied != nil {
		t.Fatal("mutable=false must not report Applied; host discards the proposal")
	}
	if obs.Replacement == nil || obs.Replacement.Id != "rewritten" {
		t.Fatalf("guest proposal still visible as Replacement: %+v", obs)
	}
}

func TestBlockRequestIsHostCall(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	sdk.OnBeforeRequest(func(context.Context, *pbv2.ChatRequest) (sdk.RequestResult, error) {
		sdk.BlockRequest(422, "pii", "redacted")
		return sdk.PassRequest(), nil
	})
	h := sdktest.New(t)
	h.BeforeRequest(&pbv2.ChatRequest{})
	calls := h.BlockCalls()
	if len(calls) != 1 {
		t.Fatalf("want 1 block call, got %d", len(calls))
	}
	args := sdktest.DecodeBlockArgs(t, calls[0].Args)
	if args.Status != 422 || args.Code == "" || args.Code != "pii" {
		t.Fatalf("args %+v", args)
	}
}

func TestInvalidBlockPanics(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	sdk.OnBeforeRequest(func(context.Context, *pbv2.ChatRequest) (sdk.RequestResult, error) {
		sdk.BlockRequest(200, "bad", "must not succeed")
		return sdk.PassRequest(), nil
	})
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("invalid block must panic")
		}
		if msg, ok := r.(string); !ok || !strings.Contains(msg, "block_request") {
			t.Fatalf("panic %v", r)
		}
	}()
	sdktest.New(t).BeforeRequest(&pbv2.ChatRequest{})
}

func TestEmptyBlockReplyPanics(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	sdk.OnBeforeRequest(func(context.Context, *pbv2.ChatRequest) (sdk.RequestResult, error) {
		sdk.BlockRequest(403, "denied", "no")
		return sdk.PassRequest(), nil
	})
	h := sdktest.New(t)
	h.StubHostCall("env.block_request", func(string) (string, error) {
		return "", nil
	})
	defer func() {
		if recover() == nil {
			t.Fatal("empty host reply must panic")
		}
	}()
	h.BeforeRequest(&pbv2.ChatRequest{})
}

func TestMalformedBlockReplyPanics(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	sdk.OnBeforeRequest(func(context.Context, *pbv2.ChatRequest) (sdk.RequestResult, error) {
		sdk.BlockRequest(403, "denied", "no")
		return sdk.PassRequest(), nil
	})
	h := sdktest.New(t)
	h.StubHostCall("env.block_request", func(string) (string, error) {
		return "not-a-protobuf", nil
	})
	defer func() {
		if recover() == nil {
			t.Fatal("malformed host reply must panic")
		}
	}()
	h.BeforeRequest(&pbv2.ChatRequest{})
}

func TestTypedPermissionDeniedIsQuiet(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	var hostErr *pbv2.HostError
	sdk.OnBeforeRequest(func(context.Context, *pbv2.ChatRequest) (sdk.RequestResult, error) {
		_, hostErr, _ = sdk.HostCall("env.block_request", &pbv2.BlockRequestArgs{
			Status: 403, Code: "x", Message: "y",
		})
		sdk.BlockRequest(403, "x", "y") // classified refusal must not panic
		return sdk.PassRequest(), nil
	})
	h := sdktest.New(t)
	h.DenyPermission("env.block_request")
	h.BeforeRequest(&pbv2.ChatRequest{})
	if hostErr == nil || hostErr.Code != pbv2.ErrorCode_ERROR_CODE_PERMISSION_DENIED {
		t.Fatalf("want typed permission denied, got %+v", hostErr)
	}
}

func TestPluginConfigAndDeny(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	var got string
	sdk.OnBeforeRequest(func(context.Context, *pbv2.ChatRequest) (sdk.RequestResult, error) {
		got = sdk.PluginConfig()
		return sdk.PassRequest(), nil
	})
	sdktest.New(t).SetConfig(`{"threshold":42}`).BeforeRequest(&pbv2.ChatRequest{})
	if got != `{"threshold":42}` {
		t.Fatalf("config %q", got)
	}
}

func TestStateRoundTripsThroughTheFramedPath(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	sdk.OnBeforeRequest(func(context.Context, *pbv2.ChatRequest) (sdk.RequestResult, error) {
		if herr, err := sdk.StateSet("k", "v"); err != nil || herr != nil {
			t.Errorf("state set: err=%v herr=%v", err, herr)
		}
		v, herr, err := sdk.StateGet("k")
		if err != nil || herr != nil {
			t.Errorf("state get: err=%v herr=%v", err, herr)
		}
		if v != "v" {
			t.Errorf("state get %q", v)
		}
		return sdk.PassRequest(), nil
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

func TestStreamHandlerPassSuppressFailOpen(t *testing.T) {
	cases := []struct {
		name   string
		action func(sdk.ToolCall) (sdk.ToolCallAction, error)
		check  func(*testing.T, sdktest.StreamResult)
	}{
		{
			name: "pass",
			action: func(call sdk.ToolCall) (sdk.ToolCallAction, error) {
				return sdk.PassToolCall(), nil
			},
			check: func(t *testing.T, r sdktest.StreamResult) {
				if len(r.Events) != 3 || r.Events[1].GetToolCallDelta().GetArgumentsDelta() != `{"x":1}` {
					t.Fatalf("%+v", r)
				}
			},
		},
		{
			name: "suppress",
			action: func(sdk.ToolCall) (sdk.ToolCallAction, error) {
				return sdk.SuppressToolCall(), nil
			},
			check: func(t *testing.T, r sdktest.StreamResult) {
				if !r.Suppressed {
					t.Fatalf("%+v", r)
				}
			},
		},
		{
			name: "callback-error-reemit",
			action: func(sdk.ToolCall) (sdk.ToolCallAction, error) {
				return sdk.ToolCallAction{}, context.Canceled
			},
			check: func(t *testing.T, r sdktest.StreamResult) {
				if r.Err != nil || len(r.Events) != 3 {
					t.Fatalf("%+v", r)
				}
				if r.Events[1].GetToolCallDelta().GetArgumentsDelta() != `{"x":1}` {
					t.Fatalf("fail-open must re-emit original args")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sdktest.Reset()
			t.Cleanup(sdktest.Reset)
			h := sdktest.New(t)
			s := sdk.NewStreamHandler()
			s.OnToolCall(func(_ context.Context, call sdk.ToolCall) (sdk.ToolCallAction, error) {
				return tc.action(call)
			})
			s.Register()
			feedToolCall(t, h, 0, `{"x":1}`)
			r := h.StreamChunk(&pbv2.StreamEvent{Event: &pbv2.StreamEvent_ContentBlockStop{
				ContentBlockStop: &pbv2.ContentBlockStop{Index: 0},
			}})
			tc.check(t, r)
		})
	}
}

func TestStreamHandlerTextAndNoMetaWhenTextOnly(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	h := sdktest.New(t)
	s := sdk.NewStreamHandler()
	s.OnTextDelta(func(_ context.Context, text string) (sdk.TextAction, error) {
		return sdk.ReplaceText(strings.ToUpper(text)), nil
	})
	s.Register()

	r := h.StreamChunk(&pbv2.StreamEvent{Event: &pbv2.StreamEvent_TextDelta{TextDelta: "hi"}})
	if r.Err != nil || len(r.Events) != 1 || r.Events[0].GetTextDelta() != "HI" {
		t.Fatalf("%+v", r)
	}
	for _, c := range h.Calls() {
		if strings.Contains(c.Command, "meta") {
			t.Fatalf("text-only handler must not touch meta: %+v", c)
		}
	}
}

func TestStreamAssemblerFeedSplitFragments(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	h := sdktest.New(t)
	var got string
	s := sdk.NewStreamHandler()
	s.OnToolCall(func(_ context.Context, call sdk.ToolCall) (sdk.ToolCallAction, error) {
		got = call.Arguments
		return sdk.PassToolCall(), nil
	})
	s.Register()

	h.StreamChunk(toolStart(1, "t"))
	h.StreamChunk(toolDelta(1, `{"a":`))
	h.StreamChunk(toolDelta(1, `1}`))
	r := h.StreamChunk(toolStop(1))
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if got != `{"a":1}` {
		t.Fatalf("assembled %q", got)
	}
}

func TestStreamMetaAppendDeniedFailsClosed(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	h := sdktest.New(t)
	s := sdk.NewStreamHandler()
	s.OnToolCall(func(context.Context, sdk.ToolCall) (sdk.ToolCallAction, error) {
		return sdk.PassToolCall(), nil
	})
	s.Register()
	h.DenyPermission(pbv2.MetaAppendCommand)

	r := h.StreamChunk(toolStart(0, "t"))
	if r.Err == nil {
		t.Fatal("permission-denied meta_append must fail closed, not pass fragment")
	}
	if r.PassedThrough {
		t.Fatal("assembly error must not look like pass-through")
	}
}

func TestStreamCorruptStopDoesNotPassFragmentAlone(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	s := sdk.NewStreamHandler()
	s.OnToolCall(func(context.Context, sdk.ToolCall) (sdk.ToolCallAction, error) {
		return sdk.PassToolCall(), nil
	})
	s.Register()
	h := sdktest.New(t)
	// After a successful start, force the stop read to return garbage.
	started := false
	h.StubHostCall(pbv2.MetaAppendCommand, func(args string) (string, error) {
		var a pbv2.MetaAppendArgs
		if err := proto.Unmarshal([]byte(args), &a); err != nil {
			t.Fatal(err)
		}
		if len(a.Fragment) != 0 {
			started = true
			raw, _ := proto.Marshal(&pbv2.HostCallResult{
				Result: &pbv2.HostCallResult_Value{Value: nil},
			})
			return string(raw), nil
		}
		if !started {
			t.Fatal("expected start before stop read")
		}
		raw, _ := proto.Marshal(&pbv2.HostCallResult{
			Result: &pbv2.HostCallResult_Value{Value: []byte("!!!")},
		})
		return string(raw), nil
	})
	if r := h.StreamChunk(toolStart(0, "t")); r.Err != nil {
		t.Fatal(r.Err)
	}
	r := h.StreamChunk(toolStop(0))
	if r.Err == nil {
		t.Fatal("corrupt frame must fail closed")
	}
	if r.PassedThrough || r.Suppressed || len(r.Events) > 0 {
		t.Fatalf("must not emit stop alone: %+v", r)
	}
}

func TestSignedToolCallContract(t *testing.T) {
	cases := []struct {
		name string
		act  func(sdk.ToolCall) (sdk.ToolCallAction, error)
		sig  string
		args string
	}{
		{
			name: "pass-keeps-signature",
			act:  func(sdk.ToolCall) (sdk.ToolCallAction, error) { return sdk.PassToolCall(), nil },
			sig:  "provider-sig",
			args: `{"x":1}`,
		},
		{
			name: "fail-open-keeps-signature",
			act: func(sdk.ToolCall) (sdk.ToolCallAction, error) {
				return sdk.ToolCallAction{}, context.Canceled
			},
			sig:  "provider-sig",
			args: `{"x":1}`,
		},
		{
			name: "replace-clears-signature",
			act: func(sdk.ToolCall) (sdk.ToolCallAction, error) {
				return sdk.ReplaceToolArguments(`{"x":2}`), nil
			},
			sig:  "",
			args: `{"x":2}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sdktest.Reset()
			t.Cleanup(sdktest.Reset)
			h := sdktest.New(t)
			s := sdk.NewStreamHandler()
			s.OnToolCall(func(_ context.Context, call sdk.ToolCall) (sdk.ToolCallAction, error) {
				return tc.act(call)
			})
			s.Register()
			start := &pbv2.StreamEvent{Event: &pbv2.StreamEvent_ContentBlockStart{
				ContentBlockStart: &pbv2.ContentBlockStart{
					Index: 0,
					Block: &pbv2.ContentBlockStart_ToolCall{ToolCall: &pbv2.ToolCallRef{
						Id: "1", Name: "tool", Signature: "provider-sig",
					}},
				},
			}}
			h.StreamChunk(start)
			h.StreamChunk(toolDelta(0, `{"x":1}`))
			r := h.StreamChunk(toolStop(0))
			if r.Err != nil {
				t.Fatal(r.Err)
			}
			if len(r.Events) != 3 {
				t.Fatalf("%+v", r)
			}
			gotSig := r.Events[0].GetContentBlockStart().GetToolCall().GetSignature()
			gotArgs := r.Events[1].GetToolCallDelta().GetArgumentsDelta()
			if gotSig != tc.sig || gotArgs != tc.args {
				t.Fatalf("sig=%q args=%q want sig=%q args=%q", gotSig, gotArgs, tc.sig, tc.args)
			}
		})
	}
}

func TestParallelDispatchSerializes(t *testing.T) {
	sdktest.Reset()
	t.Cleanup(sdktest.Reset)

	sdk.OnBeforeRequest(func(_ context.Context, req *pbv2.ChatRequest) (sdk.RequestResult, error) {
		_ = req.Model
		return sdk.PassRequest(), nil
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

	sdk.OnHTTPRequest(func(context.Context, *pbv2.HttpRequest) (sdk.HTTPResult, error) {
		return sdk.ServeHTTP(&pbv2.HttpResponse{Status: 204}), nil
	})
	sdk.OnTick(func(context.Context, *pbv2.TickRequest) (sdk.TickResult, error) {
		return sdk.TickDid(1, "ok"), nil
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

func feedToolCall(t *testing.T, h *sdktest.Harness, index int32, args string) {
	t.Helper()
	if r := h.StreamChunk(toolStart(index, "tool")); !r.Suppressed {
		t.Fatal(r)
	}
	if r := h.StreamChunk(toolDelta(index, args)); !r.Suppressed {
		t.Fatal(r)
	}
}

func toolStart(index int32, name string) *pbv2.StreamEvent {
	return &pbv2.StreamEvent{Event: &pbv2.StreamEvent_ContentBlockStart{
		ContentBlockStart: &pbv2.ContentBlockStart{
			Index: index,
			Block: &pbv2.ContentBlockStart_ToolCall{ToolCall: &pbv2.ToolCallRef{
				Id: "1", Name: name,
			}},
		},
	}}
}

func toolDelta(index int32, args string) *pbv2.StreamEvent {
	return &pbv2.StreamEvent{Event: &pbv2.StreamEvent_ToolCallDelta{
		ToolCallDelta: &pbv2.ToolCallDelta{Index: index, ArgumentsDelta: args},
	}}
}

func toolStop(index int32) *pbv2.StreamEvent {
	return &pbv2.StreamEvent{Event: &pbv2.StreamEvent_ContentBlockStop{
		ContentBlockStop: &pbv2.ContentBlockStop{Index: index},
	}}
}

// ExampleBeforeRequest is the compile-checked version of the package
// documentation snippet: a plugin test drives the hook in-process with the
// ordered message body.
func ExampleHarness_BeforeRequest() {
	req := &pbv2.ChatRequest{Messages: []*pbv2.Message{
		{Role: "user", Blocks: []*pbv2.RequestBlock{{
			Kind: &pbv2.RequestBlock_Text{Text: &pbv2.RequestTextBlock{Text: "hi"}},
		}}},
		{Role: "tool", Blocks: []*pbv2.RequestBlock{{
			Kind: &pbv2.RequestBlock_ToolResult{ToolResult: &pbv2.RequestToolResultBlock{
				ToolCallId: "t1",
				Content: []*pbv2.ToolResultContentBlock{{
					Kind: &pbv2.ToolResultContentBlock_Text{Text: &pbv2.ToolResultTextBlock{Text: "contact: someone@example.com"}},
				}},
			}},
		}}},
	}}
	if err := req.ValidateReplacement(); err != nil {
		panic(err)
	}
	// Output:
}
