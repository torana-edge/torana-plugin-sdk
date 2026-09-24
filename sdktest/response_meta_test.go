//go:build !wasip1

package sdktest_test

import (
	"testing"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"github.com/torana-edge/torana-plugin-sdk/sdktest"
)

func TestSetResponseMetaForRouteObservation(t *testing.T) {
	resp := &pbv1.ChatResponse{}
	if err := sdktest.SetResponseMeta(resp, map[string]any{
		"_route_applied": map[string]any{
			"provider": "fast", "model": "m", "verdict_plugin": "router",
			"refused": nil, "served_by": "fast", "served_model": "m", "failover": false,
		},
	}); err != nil {
		t.Fatal(err)
	}
	route, ok, err := sdk.RouteApplied(resp)
	if err != nil || !ok || route.Provider != "fast" || route.Failover {
		t.Fatalf("route=%+v ok=%v err=%v", route, ok, err)
	}
}
