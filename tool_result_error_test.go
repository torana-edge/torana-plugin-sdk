package plugin_sdk

import (
	"testing"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

func TestToolResultsPreserveErrorPresenceAndCopy(t *testing.T) {
	for _, flag := range []*bool{nil, new(bool), func() *bool { v := true; return &v }()} {
		tr := &pbv1.RequestToolResultBlock{IsError: flag}
		msg := &pbv1.Message{Blocks: []*pbv1.RequestBlock{{Kind: &pbv1.RequestBlock_ToolResult{ToolResult: tr}}}}
		view := ToolResults(msg)[0]
		if (view.IsError == nil) != (flag == nil) || (flag != nil && *view.IsError != *flag) {
			t.Fatal("tool result view lost error presence or value")
		}
		if got, want := view.MustStayExact("read", "diagnostic data"), flag != nil && *flag; got != want {
			t.Fatalf("MustStayExact = %v, want %v", got, want)
		}
		if flag != nil {
			*view.IsError = !*flag
			if *view.IsError == *flag {
				t.Fatal("view aliases source error flag")
			}
		}
	}
	if !(ToolResultView{}).MustStayExact("write_file", "done") {
		t.Fatal("explicit failure handling bypassed mutation-tool protection")
	}
}
