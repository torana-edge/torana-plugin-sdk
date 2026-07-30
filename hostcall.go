package plugin_sdk

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// HostCall invokes a host command with a protobuf argument body.
//
// args may be nil (no body). On success it returns the HostCallResult.value
// bytes (possibly empty). On a classified host failure it returns a non-nil
//*HostError. Transport/decode failures return a Go error.
//
// Verdict helpers are fire-and-forget: they call HostCall and discard the
// result; a refused permission is logged by the host.
func HostCall(cmd string, args proto.Message) ([]byte, *pbv2.HostError, error) {
	var argBytes []byte
	if args != nil {
		b, err := proto.Marshal(args)
		if err != nil {
			return nil, nil, fmt.Errorf("torana: marshal host-call args: %w", err)
		}
		argBytes = b
	}
	raw, err := hostCallRaw(cmd, argBytes)
	if err != nil {
		return nil, nil, err
	}
	if len(raw) == 0 {
		// Empty reply is not a valid HostCallResult (oneof required). Treat as
		// success with no payload for hosts that still return zero on some
		// fire-and-forget paths during migration — Migration B makes the
		// envelope mandatory on every reply.
		return nil, nil, nil
	}
	var res pbv2.HostCallResult
	if err := proto.Unmarshal(raw, &res); err != nil {
		return nil, nil, fmt.Errorf("torana: decode host-call result: %w", err)
	}
	if err := res.Validate(); err != nil {
		return nil, nil, fmt.Errorf("torana: host-call result: %w", err)
	}
	switch r := res.Result.(type) {
	case *pbv2.HostCallResult_Value:
		return r.Value, nil, nil
	case *pbv2.HostCallResult_Error:
		return nil, r.Error, nil
	default:
		return nil, nil, fmt.Errorf("torana: host-call result has no arm")
	}
}

// hostCallRaw is the platform-specific import / test-host seam.
func hostCallRaw(cmd string, args []byte) ([]byte, error) {
	return hostCallRawImpl(cmd, args)
}

// hostCallString is the transitional JSON/string host-call path used by
// cache/state/meta helpers that still speak the v1 argument shapes. Migration B
// moves those commands onto typed envelopes; until then they share the same
// WASM import but treat the reply as an opaque string (not HostCallResult).
func hostCallString(cmd, args string) (string, error) {
	raw, err := hostCallRaw(cmd, []byte(args))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
