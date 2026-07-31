package sdktest_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
	"github.com/torana-edge/torana-plugin-sdk/sdktest"
)

// GetCachePricing and SendRequest changed transport AND error semantics when
// they moved onto HostCallExtension: they used to pattern-match a
// permission-denied JSON string, and now branch on a framed code. The direct
// HostCallExtension tests do not cover them, so a regression in either would
// have been invisible — these are the two helpers real plugins actually call.

func TestGetCachePricingDecodesAFramedValue(t *testing.T) {
	h := sdktest.New(t)
	h.StubHostCall("torana_cache_pricing", func(string) (string, error) {
		return sdktest.HostResultValue([]byte(`{"status":"ok","cache_read_multiplier":0.1}`)), nil
	})
	h.Run(func() {
		got, err := sdk.GetCachePricing("anthropic", "claude-opus-5")
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if got.Status != "ok" {
			t.Fatalf("status = %q, want ok", got.Status)
		}
	})
}

// Pricing is advisory, so a refusal must degrade rather than fail — but the
// REASON has to survive, because a missing grant and an unconfigured backend
// need different fixes and read identically otherwise.
func TestGetCachePricingDegradesWithTheReason(t *testing.T) {
	for _, tc := range []struct {
		code       pbv2.ErrorCode
		wantReason string
	}{
		{pbv2.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "permission_denied"},
		{pbv2.ErrorCode_ERROR_CODE_NOT_CONFIGURED, "not_configured"},
		{pbv2.ErrorCode_ERROR_CODE_UNAVAILABLE, "unavailable"},
	} {
		t.Run(tc.wantReason, func(t *testing.T) {
			h := sdktest.New(t)
			h.StubHostCall("torana_cache_pricing", func(string) (string, error) {
				return sdktest.HostResultError(tc.code, "refused"), nil
			})
			h.Run(func() {
				got, err := sdk.GetCachePricing("anthropic", "m")
				if err != nil {
					t.Fatalf("a refusal became an error: %v", err)
				}
				if got.Status != "unavailable" {
					t.Fatalf("status = %q, want unavailable", got.Status)
				}
				if got.Reason != tc.wantReason {
					t.Fatalf("reason = %q, want %q", got.Reason, tc.wantReason)
				}
			})
		})
	}
}

func TestGetCachePricingHandlesEmptyAndMalformedValues(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		h := sdktest.New(t)
		h.StubHostCall("torana_cache_pricing", func(string) (string, error) {
			return sdktest.HostResultValue(nil), nil
		})
		h.Run(func() {
			got, err := sdk.GetCachePricing("p", "m")
			if err != nil {
				t.Fatalf("an empty value became an error: %v", err)
			}
			if got.Status != "unavailable" || got.Reason != "no_result" {
				t.Fatalf("got %+v, want unavailable/no_result", got)
			}
		})
	})
	t.Run("malformed", func(t *testing.T) {
		h := sdktest.New(t)
		h.StubHostCall("torana_cache_pricing", func(string) (string, error) {
			return sdktest.HostResultValue([]byte("not json")), nil
		})
		h.Run(func() {
			if _, err := sdk.GetCachePricing("p", "m"); err == nil {
				t.Fatal("a malformed pricing body was accepted")
			}
		})
	})
}

// Egress is all-or-nothing for the caller, so every refusal maps to the
// sentinel — but errors.Is must keep working, or existing callers silently
// stop recognising it.
func TestSendRequestRefusalsStayErrEgressUnavailable(t *testing.T) {
	for _, code := range []pbv2.ErrorCode{
		pbv2.ErrorCode_ERROR_CODE_PERMISSION_DENIED,
		pbv2.ErrorCode_ERROR_CODE_NOT_CONFIGURED,
		pbv2.ErrorCode_ERROR_CODE_UNAVAILABLE,
	} {
		t.Run(code.String(), func(t *testing.T) {
			h := sdktest.New(t)
			h.StubHostCall("torana_send_request", func(string) (string, error) {
				return sdktest.HostResultError(code, "refused"), nil
			})
			h.Run(func() {
				_, err := sdk.SendRequest(&pbv2.ChatRequest{Model: "m"}, sdk.SendRequestOptions{Provider: "anthropic", Path: "/v1/messages"})
				if err == nil {
					t.Fatal("a refusal succeeded")
				}
				if !errors.Is(err, sdk.ErrEgressUnavailable) {
					t.Fatalf("errors.Is(ErrEgressUnavailable) is false: %v", err)
				}
			})
		})
	}
}

func TestSendRequestDecodesAFramedValue(t *testing.T) {
	h := sdktest.New(t)
	h.StubHostCall("torana_send_request", func(string) (string, error) {
		body, _ := json.Marshal(map[string]any{
			"status":      "ok",
			"http_status": 200,
			"body":        base64.StdEncoding.EncodeToString([]byte("hello")),
		})
		return sdktest.HostResultValue(body), nil
	})
	h.Run(func() {
		got, err := sdk.SendRequest(&pbv2.ChatRequest{Model: "m"}, sdk.SendRequestOptions{Provider: "anthropic", Path: "/v1/messages"})
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if string(got.Body) != "hello" {
			t.Fatalf("body = %q, want hello", got.Body)
		}
	})
}

// The reason tokens are a stable contract: callers branch on them, so the
// message text cannot be the thing they read. Every known code is pinned, and
// an unknown one must not collapse onto a known token.
func TestHostErrorReasonCoversEveryKnownCode(t *testing.T) {
	for _, tc := range []struct {
		code pbv2.ErrorCode
		want string
	}{
		{pbv2.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "permission_denied"},
		{pbv2.ErrorCode_ERROR_CODE_NOT_FOUND, "not_found"},
		{pbv2.ErrorCode_ERROR_CODE_NOT_CONFIGURED, "not_configured"},
		{pbv2.ErrorCode_ERROR_CODE_UNAVAILABLE, "unavailable"},
		{pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid_argument"},
		{pbv2.ErrorCode_ERROR_CODE_INTERNAL, "internal"},
	} {
		t.Run(tc.code.String(), func(t *testing.T) {
			h2 := sdktest.New(t)
			h2.StubHostCall("torana_cache_pricing", func(string) (string, error) {
				return sdktest.HostResultError(tc.code, "x"), nil
			})
			h2.Run(func() {
				got, _ := sdk.GetCachePricing("p", "m")
				if got.Reason != tc.want {
					t.Fatalf("reason = %q, want %q", got.Reason, tc.want)
				}
			})
		})
	}
}

// HostResultError exists to build a CLASSIFIED refusal. Producing a frame that
// HostCallResult.Validate rejects would surface inside the plugin under test as
// a protocol error, and the author would debug their plugin instead of their
// fixture.
func TestHostResultErrorRejectsAnUnclassifiedCode(t *testing.T) {
	for _, code := range []pbv2.ErrorCode{
		pbv2.ErrorCode_ERROR_CODE_UNSPECIFIED,
		pbv2.ErrorCode(9999),
	} {
		t.Run(code.String(), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("HostResultError(%v) built a frame the host would reject", code)
				}
			}()
			_ = sdktest.HostResultError(code, "x")
		})
	}
}
