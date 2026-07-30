package plugin_sdk

import (
	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
	"google.golang.org/protobuf/proto"
)

// OriginalRequest returns the pristine request as the caller sent it, BEFORE
// any plugin in the chain mutated it. Plugins are chained — each hook receives
// its predecessor's output — so this host call is the only way to see the
// caller's actual request (audit, diffing, DLP).
//
// Requires the env.original_request permission grant. Returns ok=false when
// the grant is missing, the call runs outside a request, or decoding fails.
func OriginalRequest() (*pbv2.ChatRequest, bool) {
	res, err := hostCallString("env.original_request", "")
	if err != nil || res == "" || res == `{"status":"error","message":"permission denied"}` {
		return nil, false
	}
	var req pbv2.ChatRequest
	if proto.Unmarshal([]byte(res), &req) != nil {
		return nil, false
	}
	return &req, true
}

// OriginalResponse returns the raw upstream response body exactly as the
// provider sent it, before any response hook mutated it. Available on the
// non-streaming JSON path only — streamed bodies are never buffered — and
// only from run_after_response (the body doesn't exist earlier).
//
// Requires the env.original_response permission grant. Returns ok=false when
// unavailable.
func OriginalResponse() ([]byte, bool) {
	res, err := hostCallString("env.original_response", "")
	if err != nil || res == "" || res == `{"status":"error","message":"permission denied"}` {
		return nil, false
	}
	return []byte(res), true
}
