package sdktest_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
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
		// torana_cache_pricing is a QUERY: the host must return either a
		// pricing envelope or a refusal. An empty success value is a
		// protocol/host defect, not "no pricing".
		h := sdktest.New(t)
		h.StubHostCall("torana_cache_pricing", func(string) (string, error) {
			return sdktest.HostResultValue(nil), nil
		})
		h.Run(func() {
			_, err := sdk.GetCachePricing("p", "m")
			if err == nil {
				t.Fatal("an empty pricing value was treated as advisory data")
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

// Egress refusals are classified: the code survives programmatically so a
// plugin can branch on the class (retry / operator / plugin fix / host
// defect) without matching prose. Every known code is pinned through
// errors.As.
func TestSendRequestRefusalsCarryTheClassifiedCode(t *testing.T) {
	for _, code := range []pbv2.ErrorCode{
		pbv2.ErrorCode_ERROR_CODE_PERMISSION_DENIED,
		pbv2.ErrorCode_ERROR_CODE_NOT_CONFIGURED,
		pbv2.ErrorCode_ERROR_CODE_UNAVAILABLE,
		pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
		pbv2.ErrorCode_ERROR_CODE_NOT_FOUND,
		pbv2.ErrorCode_ERROR_CODE_INTERNAL,
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
				var refusal *sdk.HostCallRefusalError
				if !errors.As(err, &refusal) {
					t.Fatalf("not a *HostCallRefusalError: %v", err)
				}
				if refusal.Code != code {
					t.Fatalf("code = %v, want %v", refusal.Code, code)
				}
				if refusal.Reason == "" {
					t.Fatal("reason token is empty")
				}
			})
		})
	}
}

func TestSendRequestDecodesAFramedValue(t *testing.T) {
	h := sdktest.New(t)
	h.StubHostCall("torana_send_request", func(string) (string, error) {
		body, _ := json.Marshal(map[string]any{
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

// The reason tokens are a stable contract for the DEGRADE path (advisory
// pricing refusals): callers branch on them, so the message text cannot be the
// thing they read. Every degrade-eligible code is pinned; the error-path codes
// are pinned by TestGetCachePricingClassifiesRefusals.
func TestHostErrorReasonCoversEveryDegradeCode(t *testing.T) {
	for _, tc := range []struct {
		code pbv2.ErrorCode
		want string
	}{
		{pbv2.ErrorCode_ERROR_CODE_NOT_CONFIGURED, "not_configured"},
		{pbv2.ErrorCode_ERROR_CODE_UNAVAILABLE, "unavailable"},
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

// Round 2 covered empty and malformed values for pricing but not egress. The
// implementations were already correct; this is the missing coverage.
// send_request always returns the outcome envelope on success; a success with
// NO body is a host defect, not "no result" — and must not look like a
// classified refusal a caller could react to.
func TestSendRequestEmptyValueIsAProtocolError(t *testing.T) {
	h := sdktest.New(t)
	h.StubHostCall("torana_send_request", func(string) (string, error) {
		return sdktest.HostResultValue(nil), nil
	})
	h.Run(func() {
		_, err := sdk.SendRequest(&pbv2.ChatRequest{Model: "m"},
			sdk.SendRequestOptions{Provider: "anthropic", Path: "/v1/messages"})
		if err == nil {
			t.Fatal("an empty framed value succeeded")
		}
		var refusal *sdk.HostCallRefusalError
		if errors.As(err, &refusal) {
			t.Fatalf("an empty body was reported as a classified refusal: %v", err)
		}
	})
}

// A host-built body that is not valid base64 is a protocol/host defect, and
// must not be silently dropped into a successful result with an empty body.
func TestSendRequestMalformedBase64BodyIsADecodeError(t *testing.T) {
	h := sdktest.New(t)
	h.StubHostCall("torana_send_request", func(string) (string, error) {
		body, _ := json.Marshal(map[string]any{"http_status": 200, "body": "!!!not-base64!!!"})
		return sdktest.HostResultValue(body), nil
	})
	h.Run(func() {
		got, err := sdk.SendRequest(&pbv2.ChatRequest{Model: "m"},
			sdk.SendRequestOptions{Provider: "anthropic", Path: "/v1/messages"})
		if err == nil {
			t.Fatalf("a malformed base64 body succeeded with %d bytes", len(got.Body))
		}
	})
}

// The SDK mirrors the host's cheap input checks so an author gets immediate
// feedback instead of a host refusal — and a malformed request must never
// cross the boundary at all. The adversarial matrix is the SHARED reference
// table (sdktest.EgressPathCases): the host pins the same rows, so the two
// predicates cannot quietly diverge.
func TestSendRequestMirrorsInputValidation(t *testing.T) {
	for _, tc := range sdktest.EgressPathCases {
		t.Run(pathCaseName(tc.Path), func(t *testing.T) {
			called := false
			h := sdktest.New(t)
			h.StubHostCall("torana_send_request", func(string) (string, error) {
				called = true
				// A valid outcome envelope so a VALID path completes cleanly; an
				// invalid path never reaches this stub.
				return sdktest.HostResultValue([]byte(`{"http_status":200}`)), nil
			})
			h.Run(func() {
				_, err := sdk.SendRequest(&pbv2.ChatRequest{Model: "m"},
					sdk.SendRequestOptions{Provider: "anthropic", Path: tc.Path})
				if tc.Valid && err != nil {
					t.Fatalf("a valid path was rejected: %v", err)
				}
				if !tc.Valid && err == nil {
					t.Fatalf("an invalid path %q was accepted", tc.Path)
				}
				if !tc.Valid && called {
					t.Fatalf("an invalid path crossed the host boundary")
				}
				if tc.Valid && !called {
					t.Fatalf("a valid path did not reach the host")
				}
			})
		})
	}
	t.Run("negative timeout", func(t *testing.T) {
		called := false
		h := sdktest.New(t)
		h.StubHostCall("torana_send_request", func(string) (string, error) {
			called = true
			return sdktest.HostResultValue(nil), nil
		})
		h.Run(func() {
			_, err := sdk.SendRequest(&pbv2.ChatRequest{Model: "m"},
				sdk.SendRequestOptions{Provider: "anthropic", Path: "/v1/messages", TimeoutMS: -1})
			if err == nil {
				t.Fatal("a negative timeout was accepted")
			}
			if called {
				t.Fatal("a negative timeout crossed the host boundary")
			}
		})
	})
}

// pathCaseName renders a path (which may contain control characters) into a
// stable, readable subtest name.
func pathCaseName(p string) string {
	n := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, p)
	if n == "" {
		return "(empty)"
	}
	return n
}

// Pricing degrades for expected advisory refusals but surfaces caller and host
// defects as errors — a plugin bug must not hide as an ordinary "unavailable".
func TestGetCachePricingClassifiesRefusals(t *testing.T) {
	// Only operator/transient states degrade: NOT_CONFIGURED (operator gap) and
	// UNAVAILABLE (retry later). PERMISSION_DENIED is NOT advisory — approvals
	// are all-or-nothing, so a permission refusal is an author or host defect.
	degrade := []pbv2.ErrorCode{
		pbv2.ErrorCode_ERROR_CODE_NOT_CONFIGURED,
		pbv2.ErrorCode_ERROR_CODE_UNAVAILABLE,
	}
	for _, code := range degrade {
		t.Run("degrades/"+code.String(), func(t *testing.T) {
			h := sdktest.New(t)
			h.StubHostCall("torana_cache_pricing", func(string) (string, error) {
				return sdktest.HostResultError(code, "x"), nil
			})
			h.Run(func() {
				got, err := sdk.GetCachePricing("p", "m")
				if err != nil {
					t.Fatalf("an advisory refusal errored: %v", err)
				}
				if got.Status != "unavailable" || got.Reason == "" {
					t.Fatalf("status=%q reason=%q, want unavailable with a reason", got.Status, got.Reason)
				}
			})
		})
	}
	for _, code := range []pbv2.ErrorCode{
		pbv2.ErrorCode_ERROR_CODE_PERMISSION_DENIED,
		pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
		pbv2.ErrorCode_ERROR_CODE_NOT_FOUND,
		pbv2.ErrorCode_ERROR_CODE_INTERNAL,
	} {
		t.Run("errors/"+code.String(), func(t *testing.T) {
			h := sdktest.New(t)
			h.StubHostCall("torana_cache_pricing", func(string) (string, error) {
				return sdktest.HostResultError(code, "x"), nil
			})
			h.Run(func() {
				_, err := sdk.GetCachePricing("p", "m")
				if err == nil {
					t.Fatalf("a %v refusal degraded instead of erroring", code)
				}
				var refusal *sdk.HostCallRefusalError
				if !errors.As(err, &refusal) || refusal.Code != code {
					t.Fatalf("err=%v, want a %v refusal", err, code)
				}
			})
		})
	}
}

func TestSendRequestMalformedValueIsADecodeError(t *testing.T) {
	h := sdktest.New(t)
	h.StubHostCall("torana_send_request", func(string) (string, error) {
		return sdktest.HostResultValue([]byte("not json")), nil
	})
	h.Run(func() {
		_, err := sdk.SendRequest(&pbv2.ChatRequest{Model: "m"},
			sdk.SendRequestOptions{Provider: "anthropic", Path: "/v1/messages"})
		if err == nil {
			t.Fatal("a malformed egress body was accepted")
		}
		var refusal *sdk.HostCallRefusalError
		if errors.As(err, &refusal) {
			t.Fatal("a malformed body was reported as a classified refusal; " +
				"a caller would react to the wrong class")
		}
	})
}
