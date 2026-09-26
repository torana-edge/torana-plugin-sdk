package plugin_sdk

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	pb "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"github.com/torana-edge/torana-plugin-sdk/strictjson"
)

// HTTPConversationBinding is host-owned MCP evidence, not caller input.
// ConversationID identifies the session; it does not identify a side thread
// or reproduce a plugin's own state-key derivation.
type HTTPConversationBinding struct {
	Bound          bool
	ConversationID string
	CallID         string
}

// HTTPConversation reads the binding injected by Torana into an HTTP hook.
// present=false means ordinary HTTP, with no MCP observation. present=true
// and Bound=false means explicitly unbound; neither permits scoped state access.
// This validates the envelope, not its provenance: only use the request supplied
// to OnHTTPRequest. Never apply it to model arguments or caller-created headers.
func HTTPConversation(req *pb.HttpRequest) (HTTPConversationBinding, bool, error) {
	var empty HTTPConversationBinding
	if req == nil || len(req.HeadersJson) == 0 {
		return empty, false, nil
	}
	object, err := strictjson.DecodeObject(req.HeadersJson)
	if err != nil || object == nil {
		return empty, false, fmt.Errorf("torana: invalid HTTP header envelope")
	}
	values := map[string]string{}
	for name, raw := range object {
		key := strings.ToLower(name)
		if key != "x-torana-mcp-binding" && key != "x-torana-conversation-id" && key != "x-torana-tool-use-id" {
			continue
		}
		if _, exists := values[key]; exists {
			return empty, false, fmt.Errorf("torana: duplicate HTTP binding header")
		}
		encoded, err := json.Marshal(raw)
		var list []string
		if err != nil || json.Unmarshal(encoded, &list) != nil || len(list) != 1 {
			return empty, false, fmt.Errorf("torana: HTTP binding header needs one value")
		}
		values[key] = list[0]
	}
	status, present := values["x-torana-mcp-binding"]
	conversation, hasConversation := values["x-torana-conversation-id"]
	call, hasCall := values["x-torana-tool-use-id"]
	if !present || status == "unbound" {
		if hasConversation || hasCall {
			return empty, false, fmt.Errorf("torana: unbound HTTP call cannot carry identities")
		}
		return empty, present, nil
	}
	valid := func(value string) bool {
		return value != "" && len(value) <= 256 && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
	}
	if status != "bound" || !valid(conversation) || !valid(call) {
		return empty, false, fmt.Errorf("torana: invalid HTTP conversation binding")
	}
	return HTTPConversationBinding{Bound: true, ConversationID: conversation, CallID: call}, true, nil
}
