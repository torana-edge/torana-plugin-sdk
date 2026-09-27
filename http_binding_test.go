package plugin_sdk

import (
	"strings"
	"testing"

	pb "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

func TestHTTPConversation(t *testing.T) {
	for _, tc := range []struct {
		raw            string
		present, bound bool
	}{
		{"", false, false}, {`{}`, false, false},
		{`{"X-Torana-MCP-Binding":["unbound"]}`, true, false},
		{`{"x-torana-mcp-binding":["bound"],"X-Torana-Conversation-Id":["session"],"X-Torana-Tool-Use-Id":["call"]}`, true, true},
	} {
		binding, present, err := HTTPConversation(&pb.HttpRequest{HeadersJson: []byte(tc.raw)})
		if err != nil || present != tc.present || binding.Bound != tc.bound {
			t.Fatalf("binding=%+v present=%v err=%v", binding, present, err)
		}
		if tc.bound && (binding.ConversationID != "session" || binding.CallID != "call") {
			t.Fatal("lost identities")
		}
	}
	if _, present, err := HTTPConversation(nil); present || err != nil {
		t.Fatal("nil request is absent")
	}
}

func TestHTTPConversationRejectsAmbiguousOrUnboundIdentity(t *testing.T) {
	for _, raw := range []string{
		`null`, `[]`, `{`, `{} {}`, `{"X-Torana-MCP-Binding":["bound"],"X-Torana-MCP-Binding":["unbound"]}`,
		`{"X-Torana-MCP-Binding":["unbound"],"x-torana-mcp-binding":["unbound"]}`,
		`{"X-Torana-MCP-Binding":[]}`, `{"X-Torana-MCP-Binding":"bound"}`, `{"X-Torana-MCP-Binding":["bound","unbound"]}`,
		`{"X-Torana-MCP-Binding":["unknown"]}`, `{"X-Torana-MCP-Binding":["bound"]}`,
		`{"X-Torana-Conversation-Id":["private-session"]}`,
		`{"X-Torana-MCP-Binding":["unbound"],"X-Torana-Tool-Use-Id":["private-call"]}`,
		`{"X-Torana-MCP-Binding":["bound"],"X-Torana-Conversation-Id":["session\n"],"X-Torana-Tool-Use-Id":["call"]}`,
		`{"X-Torana-MCP-Binding":["bound"],"X-Torana-Conversation-Id":["` + strings.Repeat("a", 257) + `"],"X-Torana-Tool-Use-Id":["call"]}`,
	} {
		binding, present, err := HTTPConversation(&pb.HttpRequest{HeadersJson: []byte(raw)})
		if err == nil || present || binding != (HTTPConversationBinding{}) {
			t.Fatal("unsafe binding accepted")
		}
		if strings.Contains(err.Error(), "private-") {
			t.Fatal("diagnostic leaked an identity")
		}
	}
}
