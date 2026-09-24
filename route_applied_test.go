package plugin_sdk_test

import (
	"testing"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

func TestRouteApplied(t *testing.T) {
	resp := &pbv1.ChatResponse{}
	if _, ok, err := sdk.RouteApplied(resp); err != nil || ok {
		t.Fatalf("missing route: ok=%v err=%v", ok, err)
	}
	resp.ToranaMetaJson = []byte(`{"_route_applied":{"provider":"a","model":"m","verdict_plugin":"router","refused":null,"served_by":"b","served_model":"m","failover":true}}`)
	route, ok, err := sdk.RouteApplied(resp)
	if err != nil || !ok || route.Provider != "a" || route.ServedBy != "b" || !route.Failover || route.Refused != nil {
		t.Fatalf("route=%+v ok=%v err=%v", route, ok, err)
	}
}
