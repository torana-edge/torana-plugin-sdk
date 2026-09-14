package sdktest_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"github.com/torana-edge/torana-plugin-sdk/sdktest"
)

func TestTypedModelResourceStubsOwnFraming(t *testing.T) {
	h := sdktest.New(t)
	h.StubModelComplete(func(args *pbv1.ModelCompleteArgs) (*pbv1.ModelCompleteResult, *pbv1.HostError, error) {
		if args.Service != "judge" || len(args.Messages) != 1 {
			t.Fatalf("args = %+v", args)
		}
		return &pbv1.ModelCompleteResult{Message: &pbv1.ResponseMessage{Blocks: []*pbv1.ResponseBlock{{Kind: &pbv1.ResponseBlock_Text{Text: &pbv1.ResponseTextBlock{Text: "yes"}}}}}}, nil, nil
	})
	h.StubModelPricing(func(args *pbv1.ModelPricingGetArgs) (*pbv1.ModelPricing, *pbv1.HostError, error) {
		if args.Resource != "request" {
			t.Fatalf("args = %+v", args)
		}
		free := 0.0
		return &pbv1.ModelPricing{InputUsdPerMtok: &free}, nil, nil
	})
	h.StubPromptCachePolicy(func(args *pbv1.PromptCachePolicyGetArgs) (*pbv1.PromptCachePolicy, *pbv1.HostError, error) {
		if args.Resource != "request-cache" {
			t.Fatalf("args = %+v", args)
		}
		return &pbv1.PromptCachePolicy{Tiers: []*pbv1.PromptCacheTier{{TtlSeconds: 300, MarkerJson: []byte(`{}`)}}}, nil, nil
	})
	h.Run(func() {
		result, err := sdk.ModelComplete(&pbv1.ModelCompleteArgs{Service: "judge", Messages: []*pbv1.Message{{Role: "user", Blocks: []*pbv1.RequestBlock{{Kind: &pbv1.RequestBlock_Text{Text: &pbv1.RequestTextBlock{Text: "question"}}}}}}})
		if err != nil || result.Message == nil || result.Message.Blocks[0].GetText().Text != "yes" {
			t.Fatalf("completion = %+v, %v", result, err)
		}
		pricing, err := sdk.GetModelPricing("request")
		if err != nil || pricing.InputUsdPerMtok == nil || *pricing.InputUsdPerMtok != 0 {
			t.Fatalf("pricing = %+v, %v", pricing, err)
		}
		policy, err := sdk.GetPromptCachePolicy("request-cache")
		if err != nil || len(policy.Tiers) != 1 || policy.Tiers[0].TtlSeconds != 300 {
			t.Fatalf("cache policy = %+v, %v", policy, err)
		}
	})
}

func TestMandatoryValueHelpersPreserveUnexpectedNotFound(t *testing.T) {
	tests := []struct {
		name, command string
		call          func() error
	}{
		{"credential", "env.credential_get", func() error { _, err := sdk.GetCredential("api"); return err }},
		{"file", "env.file_read", func() error { _, err := sdk.ReadFile("events.log"); return err }},
		{"pricing", "env.model_pricing", func() error { _, err := sdk.GetModelPricing("target"); return err }},
		{"cache policy", "env.cache_policy", func() error { _, err := sdk.GetPromptCachePolicy("target"); return err }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := sdktest.New(t)
			h.StubHostCall(tc.command, func(string) (string, error) {
				return sdktest.HostResultError(pbv1.ErrorCode_ERROR_CODE_NOT_FOUND, "unexpected absence"), nil
			})
			h.Run(func() {
				var refusal *sdk.HostCallRefusalError
				if err := tc.call(); !errors.As(err, &refusal) || refusal.Code != pbv1.ErrorCode_ERROR_CODE_NOT_FOUND {
					t.Fatalf("error = %v", err)
				}
			})
		})
	}
}

// Egress refusals are classified: the code survives programmatically so a
// plugin can branch on the class (retry / operator / plugin fix / host
// defect) without matching prose. Every known code is pinned through
// errors.As.
func TestSendRequestRefusalsCarryTheClassifiedCode(t *testing.T) {
	for _, code := range []pbv1.ErrorCode{
		pbv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED,
		pbv1.ErrorCode_ERROR_CODE_NOT_CONFIGURED,
		pbv1.ErrorCode_ERROR_CODE_UNAVAILABLE,
		pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
		pbv1.ErrorCode_ERROR_CODE_NOT_FOUND,
		pbv1.ErrorCode_ERROR_CODE_INTERNAL,
	} {
		t.Run(code.String(), func(t *testing.T) {
			h := sdktest.New(t)
			h.StubHostCall("torana_send_request", func(string) (string, error) {
				return sdktest.HostResultError(code, "refused"), nil
			})
			h.Run(func() {
				_, err := sdk.SendRequest(&pbv1.ChatRequest{Model: "m"}, sdk.SendRequestOptions{Provider: "anthropic", Path: "/v1/messages"})
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
		got, err := sdk.SendRequest(&pbv1.ChatRequest{Model: "m"}, sdk.SendRequestOptions{Provider: "anthropic", Path: "/v1/messages"})
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if string(got.Body) != "hello" {
			t.Fatalf("body = %q, want hello", got.Body)
		}
	})
}

// HostResultError exists to build a CLASSIFIED refusal. Producing a frame that
// HostCallResult.Validate rejects would surface inside the plugin under test as
// a protocol error, and the author would debug their plugin instead of their
// fixture.
func TestHostResultErrorRejectsAnUnclassifiedCode(t *testing.T) {
	for _, code := range []pbv1.ErrorCode{
		pbv1.ErrorCode_ERROR_CODE_UNSPECIFIED,
		pbv1.ErrorCode(9999),
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
		_, err := sdk.SendRequest(&pbv1.ChatRequest{Model: "m"},
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
		got, err := sdk.SendRequest(&pbv1.ChatRequest{Model: "m"},
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
				_, err := sdk.SendRequest(&pbv1.ChatRequest{Model: "m"},
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
			_, err := sdk.SendRequest(&pbv1.ChatRequest{Model: "m"},
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

func TestSendRequestMalformedValueIsADecodeError(t *testing.T) {
	h := sdktest.New(t)
	h.StubHostCall("torana_send_request", func(string) (string, error) {
		return sdktest.HostResultValue([]byte("not json")), nil
	})
	h.Run(func() {
		_, err := sdk.SendRequest(&pbv1.ChatRequest{Model: "m"},
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
