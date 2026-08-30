package plugin_sdk

import (
	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"google.golang.org/protobuf/proto"
)

// OriginalRequest returns the pristine request as the caller sent it, BEFORE
// any plugin in the chain mutated it. Plugins are chained — each hook receives
// its predecessor's output — so this host call is the only way to see the
// caller's actual request (audit, diffing, DLP).
//
// Requires the env.original_request permission grant. Returns ok=false when
// the grant is missing, the call runs outside a request, or decoding fails.
func OriginalRequest() (*pbv1.ChatRequest, bool) {
	// ok=false covers every unavailable case deliberately: the caller's only
	// sensible response to "no original" is to skip whatever needed it, so a
	// classified error would be ceremony. The framed path still matters — the
	// byte-exact permission-denied string it used to compare against was a
	// wire constant that silently broke if the host ever reworded it.
	//
	// Absence comes from the ERROR arm only, never from the value's length. An
	// all-default ChatRequest marshals to zero bytes and unmarshals cleanly, so
	// treating an empty value as absence would report a real captured request
	// as missing — the same absence-versus-emptiness confusion the envelope
	// exists to prevent.
	raw, herr, err := HostCall("env.original_request", nil)
	if err != nil || herr != nil {
		return nil, false
	}
	var req pbv1.ChatRequest
	if proto.Unmarshal(raw, &req) != nil {
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
	// As with OriginalRequest, absence is the error arm. An upstream body can
	// legitimately be empty (a 204, or a provider that returns nothing), and
	// reporting that as "no original captured" would send a plugin looking for
	// a missing grant.
	raw, herr, err := HostCall("env.original_response", nil)
	if err != nil || herr != nil {
		return nil, false
	}
	return raw, true
}
