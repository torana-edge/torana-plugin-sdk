package plugin_sdk

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// validator is implemented by host-call argument messages with structural rules.
type validator interface {
	Validate() error
}

// HostCall invokes a host command with a protobuf argument body.
//
// args may be nil (no body). On success it returns the HostCallResult.value
// bytes (possibly empty). On a classified host failure it returns a non-nil
// *HostError. Empty command, invalid args, empty/malformed replies, and
// transport failures return a Go error — callers that must not fail open
// (verdicts) panic on those.
func HostCall(cmd string, args proto.Message) ([]byte, *pbv2.HostError, error) {
	if cmd == "" {
		return nil, nil, fmt.Errorf("torana: host-call command is required")
	}
	if v, ok := args.(validator); ok {
		if err := v.Validate(); err != nil {
			return nil, nil, fmt.Errorf("torana: host-call args: %w", err)
		}
	}
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
		return nil, nil, fmt.Errorf("torana: host-call returned an empty reply; " +
			"HostCallResult requires a result arm")
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

// mustHostCall is for fire-and-forget verdicts: classified host refusals are
// discarded (the host logs them), but local/protocol failures trap the guest.
func mustHostCall(cmd string, args proto.Message) {
	_, _, err := HostCall(cmd, args)
	if err != nil {
		panic("torana plugin: " + cmd + ": " + err.Error())
	}
}

func hostCallRaw(cmd string, args []byte) ([]byte, error) {
	return hostCallRawImpl(cmd, args)
}

// hostCallString is the transitional JSON/string host-call path used by
// cache/state/meta helpers that still speak the v1 argument shapes.
func hostCallString(cmd, args string) (string, error) {
	raw, err := hostCallRaw(cmd, []byte(args))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
