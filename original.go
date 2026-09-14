package plugin_sdk

import (
	"fmt"
	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

// OriginalRequest returns the pristine request as the caller sent it, BEFORE
// any plugin in the chain mutated it. Plugins are chained — each hook receives
// its predecessor's output — so this host call is the only way to see the
// caller's actual request (audit, diffing, DLP).
//
// Requires the env.original_request permission grant. Returns ok=false only
// when the host reports NOT_FOUND because no request was captured.
func OriginalRequest() (*pbv1.ChatRequest, bool, error) {
	// Absence comes from the ERROR arm only, never from the value's length. An
	// all-default ChatRequest marshals to zero bytes and unmarshals cleanly, so
	// treating an empty value as absence would report a real captured request
	// as missing — the same absence-versus-emptiness confusion the envelope
	// exists to prevent.
	raw, herr, err := hostCallChecked("env.original_request", nil)
	if err != nil {
		return nil, false, err
	}
	if herr != nil {
		if herr.Code == pbv1.ErrorCode_ERROR_CODE_NOT_FOUND {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("torana: env.original_request: %w", classifiedRefusal(herr))
	}
	req, err := pbv1.DecodeChatRequest(raw)
	if err != nil {
		return nil, false, fmt.Errorf("torana: decode original request: %w", err)
	}
	return req, true, nil
}

// OriginalResponse returns the raw upstream response body exactly as the
// provider sent it, before any response hook mutated it. Available on the
// non-streaming JSON path only — streamed bodies are never buffered — and
// only from run_after_response (the body doesn't exist earlier).
//
// Requires the env.original_response permission grant. Returns ok=false when
// unavailable.
func OriginalResponse() ([]byte, bool, error) {
	// As with OriginalRequest, absence is the error arm. An upstream body can
	// legitimately be empty (a 204, or a provider that returns nothing), and
	// reporting that as "no original captured" would send a plugin looking for
	// a missing grant.
	raw, herr, err := hostCallChecked("env.original_response", nil)
	if err != nil {
		return nil, false, err
	}
	if herr != nil {
		if herr.Code == pbv1.ErrorCode_ERROR_CODE_NOT_FOUND {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("torana: env.original_response: %w", classifiedRefusal(herr))
	}
	return raw, true, nil
}
