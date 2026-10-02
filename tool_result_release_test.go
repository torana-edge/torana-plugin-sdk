package plugin_sdk_test

import (
	"strings"
	"testing"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	"github.com/torana-edge/torana-plugin-sdk/sdktest"
)

func TestToolResultReleaseHelper(t *testing.T) {
	h := sdktest.New(t).StubHostCall("torana_tool_result_release", func(args string) (string, error) {
		if args != `{"message":2,"block":3,"register":true}` {
			t.Fatalf("args=%s", args)
		}
		return sdktest.HostResultValue([]byte(`{"reference":"tr_` + strings.Repeat("a", 64) + `","approved":false}`)), nil
	})
	h.Run(func() {
		value, err := sdk.ToolResultRelease(2, 3, true)
		if err != nil || value.Approved || value.Reference == "" {
			t.Fatalf("release=%+v err=%v", value, err)
		}
	})
}

func TestToolResultReleaseRejectsMalformedResult(t *testing.T) {
	for _, raw := range []string{`{}`, `{"reference":"","approved":true}`, `{"reference":null,"approved":false}`, `{"reference":"","approved":false,"content":"secret"}`} {
		h := sdktest.New(t).StubHostCall("torana_tool_result_release", func(string) (string, error) { return sdktest.HostResultValue([]byte(raw)), nil })
		h.Run(func() {
			if _, err := sdk.ToolResultRelease(0, 0, false); err == nil {
				t.Fatalf("accepted %s", raw)
			}
		})
	}
}
