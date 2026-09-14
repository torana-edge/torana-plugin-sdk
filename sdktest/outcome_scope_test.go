package sdktest_test

import (
	"context"
	"errors"
	sdk "github.com/torana-edge/torana-plugin-sdk"
	pb "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"github.com/torana-edge/torana-plugin-sdk/sdktest"
	"testing"
)

func TestFailedInvocationDiscardsOnlyTransientVerdicts(t *testing.T) {
	for _, trap := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "panic"}[trap], func(t *testing.T) {
			sdktest.Reset()
			defer sdktest.Reset()
			h := sdktest.New(t).WithPermissions([]string{"env.block_request", "env.respond_request", "env.route_request"})
			for _, cmd := range []string{"env.block_request", "env.respond_request", "env.route_request"} {
				h.StubHostCall(cmd, func(string) (string, error) { return sdktest.HostResultValue(nil), nil })
			}
			sdk.OnBeforeRequest(func(context.Context, *pb.ChatRequest) (sdk.RequestResult, error) {
				if err := sdk.BlockRequest(403, "denied", "blocked"); err != nil {
					return sdk.RequestResult{}, err
				}
				if err := sdk.RespondText("cached"); err != nil {
					return sdk.RequestResult{}, err
				}
				if err := sdk.RouteRequest("provider", "model"); err != nil {
					return sdk.RequestResult{}, err
				}
				if trap {
					panic("callback failed")
				}
				return sdk.RequestResult{}, errors.New("callback failed")
			})
			r := h.NewRequest()
			result := r.BeforeRequest(&pb.ChatRequest{Model: "m"})
			if result.Err == nil {
				t.Fatal("callback failure lost")
			}
			if len(r.Calls()) != 3 || len(r.AcceptedCalls()) != 3 || len(r.EffectiveBlockCalls()) != 1 || len(r.EffectiveRespondCalls()) != 0 || len(r.EffectiveRouteCalls()) != 0 {
				t.Fatalf("incorrect outcomes: %+v", r.AcceptedCalls())
			}
			sdk.OnAfterResponse(func(context.Context, *pb.ChatResponse, bool) (sdk.ResponseResult, error) {
				return sdk.PassResponse(), nil
			})
			if result := r.AfterResponse(&pb.ChatResponse{}, false); result.Err != nil {
				t.Fatal(result.Err)
			}
			if len(r.EffectiveRespondCalls())+len(r.EffectiveRouteCalls()) != 0 {
				t.Fatal("later success resurrected discarded verdict")
			}
			for _, entry := range h.AcceptedCalls() {
				if entry.Command != "env.block_request" && entry.Effective {
					t.Fatalf("aggregate outcome stale: %+v", entry)
				}
			}
			if len(h.NewRequest().Calls()) != 0 {
				t.Fatal("calls leaked into another request")
			}
		})
	}
}

func TestManifestModeRefusesCrossHookVerdictsBeforeStubs(t *testing.T) {
	sdktest.Reset()
	defer sdktest.Reset()
	h := sdktest.New(t).WithPermissions([]string{"env.block_request", "env.background_tick", "env.serve_http"})
	calls := 0
	h.StubHostCall("env.block_request", func(string) (string, error) { calls++; return sdktest.HostResultValue(nil), nil })
	check := func() error {
		err := sdk.BlockRequest(403, "denied", "blocked")
		var refusal *sdk.HostCallRefusalError
		if !errors.As(err, &refusal) || refusal.Code != pb.ErrorCode_ERROR_CODE_PERMISSION_DENIED {
			t.Fatalf("wrong denial: %v", err)
		}
		return nil
	}
	sdk.OnTick(func(context.Context, *pb.TickRequest) (sdk.TickResult, error) { return sdk.TickIdle(), check() })
	sdk.OnHTTPRequest(func(context.Context, *pb.HttpRequest) (sdk.HTTPResult, error) { return sdk.PassHTTP(), check() })
	r := h.NewRequest()
	if result := r.Tick(&pb.TickRequest{}); result.Err != nil {
		t.Fatal(result.Err)
	}
	if result := r.HTTPRequest(&pb.HttpRequest{Method: "GET", Path: "/"}); result.Err != nil {
		t.Fatal(result.Err)
	}
	if calls != 0 || len(r.Calls()) != 2 || len(r.AcceptedCalls()) != 0 {
		t.Fatalf("stub calls=%d attempted=%d accepted=%d", calls, len(r.Calls()), len(r.AcceptedCalls()))
	}
}

func TestMalformedHostRepliesNeverCountAsAccepted(t *testing.T) {
	for _, raw := range [][]byte{{0x0a, 0, 0x0a, 0}, {0x0a, 0, 0x18, 1}} {
		h := sdktest.New(t)
		h.StubHostCall("env.cache_get", func(string) (string, error) { return string(raw), nil })
		r := h.NewRequest()
		r.Run(func() {
			if _, _, err := sdk.CacheGet("k"); err == nil {
				t.Fatal("malformed reply accepted")
			}
		})
		if len(r.AcceptedCalls()) != 0 || len(r.Calls()) != 1 {
			t.Fatal("malformed call counted as accepted")
		}
	}
}

func TestNativeVerdictPriorityAndIdentityRollback(t *testing.T) {
	sdktest.Reset()
	defer sdktest.Reset()
	h := sdktest.New(t)
	sdk.OnBeforeRequest(func(context.Context, *pb.ChatRequest) (sdk.RequestResult, error) {
		for _, status := range []int32{401, 403} {
			if err := sdk.BlockRequest(status, "denied", "blocked"); err != nil {
				return sdk.RequestResult{}, err
			}
		}
		for _, text := range []string{"first", "second"} {
			if err := sdk.RespondText(text); err != nil {
				return sdk.RequestResult{}, err
			}
		}
		for _, id := range []string{"first", "second"} {
			if err := sdk.SetIdentity(id); err != nil {
				return sdk.RequestResult{}, err
			}
		}
		return sdk.PassRequest(), nil
	})
	r := h.NewRequest()
	if result := r.BeforeRequest(&pb.ChatRequest{}); result.Err != nil {
		t.Fatal(result.Err)
	}
	if len(r.AcceptedCalls()) != 6 || len(r.EffectiveBlockCalls()) != 1 || len(r.EffectiveRespondCalls()) != 0 || len(r.EffectiveIdentityCalls()) != 1 {
		t.Fatalf("priority not applied: %+v", r.AcceptedCalls())
	}
	respondAccepted := 0
	for _, call := range r.AcceptedCalls() {
		if call.Command == "env.respond_request" {
			respondAccepted++
		}
	}
	if respondAccepted != 2 {
		t.Fatalf("retained respond calls = %d, want 2", respondAccepted)
	}
	if got := h.EffectiveBlockCalls(); len(got) != 1 {
		t.Fatalf("aggregate effective blocks = %d, want one winner: %+v", len(got), got)
	}
	sdk.OnAfterResponse(func(context.Context, *pb.ChatResponse, bool) (sdk.ResponseResult, error) {
		return sdk.ResponseResult{}, errors.New("later invocation failed")
	})
	if result := r.AfterResponse(&pb.ChatResponse{}, false); result.Err == nil {
		t.Fatal("expected failure")
	}
	if len(r.EffectiveIdentityCalls())+len(r.EffectiveRespondCalls()) != 0 || len(r.EffectiveBlockCalls()) != 1 {
		t.Fatalf("rollback priority: %+v", r.AcceptedCalls())
	}
}
