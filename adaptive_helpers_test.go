package plugin_sdk_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"github.com/torana-edge/torana-plugin-sdk/sdktest"
)

func TestAdaptiveHostHelpers(t *testing.T) {
	h := sdktest.New(t)
	h.StubHostCall("env.suggest", func(string) (string, error) {
		value, err := proto.Marshal(&pbv1.SuggestResult{SuggestionId: "sg_1"})
		if err != nil {
			t.Fatal(err)
		}
		return sdktest.HostResultValue(value), nil
	})
	h.StubHostCall("env.model_capabilities", func(string) (string, error) {
		value, err := proto.Marshal(&pbv1.ModelCapabilities{Format: "anthropic", EffortLevels: []pbv1.Effort{pbv1.Effort_EFFORT_HIGH}})
		if err != nil {
			t.Fatal(err)
		}
		return sdktest.HostResultValue(value), nil
	})
	h.Run(func() {
		result, err := sdk.Suggest(&pbv1.SuggestArgs{Kind: "model_switch", DedupeKey: "up", Title: "Try a stronger model", Body: "Repeated tool failures"})
		if err != nil || result.SuggestionId != "sg_1" {
			t.Fatalf("suggest = %+v, %v", result, err)
		}
		caps, err := sdk.GetModelCapabilities("anthropic", "m")
		if err != nil || caps.Format != "anthropic" {
			t.Fatalf("capabilities = %+v, %v", caps, err)
		}
		if err := sdk.RouteRequestWithEffort("anthropic", "m", pbv1.Effort_EFFORT_HIGH); err != nil {
			t.Fatal(err)
		}
	})
	var route *pbv1.RouteRequestArgs
	for _, call := range h.Calls() {
		if call.Command == "env.route_request" {
			route = new(pbv1.RouteRequestArgs)
			if err := proto.Unmarshal([]byte(call.Args), route); err != nil {
				t.Fatal(err)
			}
		}
	}
	if route == nil || route.Effort != pbv1.Effort_EFFORT_HIGH {
		t.Fatalf("route = %+v", route)
	}
}

func TestAdaptiveMetadataReaders(t *testing.T) {
	req := new(pbv1.ChatRequest)
	if err := sdktest.SetRequestMeta(req, map[string]any{"_suggestions": []map[string]string{{"id": "sg_1", "status": "accepted", "action": "accept", "via": "directive"}}, "_torana_mcp": "absent"}); err != nil {
		t.Fatal(err)
	}
	outcomes, err := sdk.Suggestions(req)
	if err != nil || len(outcomes) != 1 || outcomes[0].Status != "accepted" {
		t.Fatalf("outcomes = %+v, %v", outcomes, err)
	}
	status, ok, err := sdk.ToranaMCP(req)
	if err != nil || !ok || status != "absent" {
		t.Fatalf("MCP = %q, %v, %v", status, ok, err)
	}
	if err := sdktest.SetRequestMeta(req, map[string]any{"_suggestions": "bad"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sdk.Suggestions(req); err == nil {
		t.Fatal("malformed outcomes accepted")
	}
}
