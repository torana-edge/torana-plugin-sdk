package plugin_sdk

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
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
func HostCall(cmd string, args proto.Message) ([]byte, *pbv1.HostError, error) {
	if cmd == "" {
		return nil, nil, fmt.Errorf("torana: host-call command is required")
	}
	if !strings.HasPrefix(cmd, "env.") {
		// Otherwise there are two ways to invoke one operation: a caller could
		// send a proto to torana_plugin_counter and an opaque body to the same
		// command, and only one of them is the contract. One route per
		// operation is the point of splitting the paths at all.
		return nil, nil, fmt.Errorf("torana: %q is a host-feature command, not a core "+
			"host call; use HostCallExtension with an opaque body", cmd)
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
	return dispatchHostCall(cmd, argBytes)
}

// HostCallExtension invokes a host FEATURE command with an opaque body.
//
// Core env.* operations are ABI surface: their shapes are part of the plugin
// contract, so they take typed protobuf arguments through HostCall. The
// torana_* commands are host features — their payloads are defined by the
// feature, not by the ABI. Putting every one of them in the ABI proto would
// turn it into a catalogue of unrelated RPCs and couple ABI releases to edge
// features, so their bodies stay opaque here.
//
// The body is []byte rather than string because JSON is the encoding those
// commands happen to use today, not an ABI rule. Pass json.Marshal output.
//
// The RESULT envelope is not opaque. Replies are HostCallResult exactly as for
// HostCall: a refusal is the framed error arm (INVALID_ARGUMENT,
// NOT_CONFIGURED, UNAVAILABLE, PERMISSION_DENIED), and a Go error means the
// call could not be made or its reply was invalid. v1's `{"status":"error"}`
// convention is deliberately NOT preserved — it is the ambiguous error channel
// v1 exists to remove. A domain result may still carry a status field where
// status is real data, such as a pricing decision.
//
// The two paths are disjoint on purpose. This one refuses env.-prefixed
// commands so it cannot become an untyped back door to verdicts, metadata,
// cache or state. Pass the canonical command token — "torana_plugin_counter",
// never the permission string "env.host_call.torana_plugin_counter".
//
// Authorisation is the host's job: it gates each call on the exact
// env.host_call.<command> grant, where the calling module and the approved
// manifest are authoritative. The SDK never sees the manifest and cannot check
// it honestly.
//
// Extension commands are NOT open-ended today. sdk.Permissions is a closed
// allowlist and hosts must not invent names; a third-party extension registry
// would be a separate platform feature.
func HostCallExtension(cmd string, args []byte) ([]byte, *pbv1.HostError, error) {
	if cmd == "" {
		return nil, nil, fmt.Errorf("torana: extension host-call command is required")
	}
	if strings.HasPrefix(cmd, "env.") {
		return nil, nil, fmt.Errorf("torana: %q is a core host call, not an extension; "+
			"use HostCall with its typed arguments — routing it here would bypass "+
			"the typed contract for verdicts, metadata, cache and state", cmd)
	}
	// The extension set is closed today, so an unrecognised token is a typo or
	// a command this SDK does not support. Catching it here fails at the call
	// site instead of at host dispatch in production, and it enforces the
	// canonical form: the capability is env.host_call.<cmd>, so passing the
	// permission string itself does not accidentally resolve.
	if !IsPermission("env.host_call." + cmd) {
		return nil, nil, fmt.Errorf("torana: %q is not a supported extension command; "+
			"pass the canonical token (for example \"torana_plugin_counter\", not "+
			"\"env.host_call.torana_plugin_counter\"). Supported extensions are a "+
			"closed set in this SDK version", cmd)
	}
	return dispatchHostCall(cmd, args)
}

// dispatchHostCall is the one place a host reply is decoded. HostCall and
// HostCallExtension differ only in how the REQUEST body is produced; sharing
// this means the two cannot drift on how a refusal or a malformed frame is
// reported.
func dispatchHostCall(cmd string, argBytes []byte) ([]byte, *pbv1.HostError, error) {
	raw, err := hostCallRaw(cmd, argBytes)
	if err != nil {
		return nil, nil, err
	}
	if len(raw) == 0 {
		return nil, nil, fmt.Errorf("torana: host-call returned an empty reply; " +
			"HostCallResult requires a result arm")
	}
	var res pbv1.HostCallResult
	if err := proto.Unmarshal(raw, &res); err != nil {
		return nil, nil, fmt.Errorf("torana: decode host-call result: %w", err)
	}
	if err := res.Validate(); err != nil {
		return nil, nil, fmt.Errorf("torana: host-call result: %w", err)
	}
	switch r := res.Result.(type) {
	case *pbv1.HostCallResult_Value:
		return r.Value, nil, nil
	case *pbv1.HostCallResult_Error:
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

// hostErrorReason renders a HostError code as a stable snake_case token.
//
// Helpers that degrade rather than fail (pricing, egress) surface a reason to
// their callers, and that reason must not be the human-readable message: the
// message is for logs and can change, while a caller branching on it needs
// something stable.
func hostErrorReason(herr *pbv1.HostError) string {
	if herr == nil {
		return ""
	}
	switch herr.Code {
	case pbv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED:
		return "permission_denied"
	case pbv1.ErrorCode_ERROR_CODE_NOT_CONFIGURED:
		return "not_configured"
	case pbv1.ErrorCode_ERROR_CODE_UNAVAILABLE:
		return "unavailable"
	case pbv1.ErrorCode_ERROR_CODE_NOT_FOUND:
		return "not_found"
	case pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT:
		return "invalid_argument"
	case pbv1.ErrorCode_ERROR_CODE_INTERNAL:
		return "internal"
	}
	// Only UNSPECIFIED or a code from a newer build reaches here. Both mean
	// "this build cannot classify it", which is different from any known code
	// and must not be silently folded into one of them.
	return "unclassified"
}
