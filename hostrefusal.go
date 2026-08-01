package plugin_sdk

import (
	"fmt"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// HostCallRefusalError is a CLASSIFIED host-call refusal, carried through the
// public helper APIs so a plugin can decide what to do without string matching.
//
// The host frames every refusal with a classification code; helpers must not
// collapse that into a single catch-all sentinel, because the right reaction
// differs by class:
//
//   - ERROR_CODE_PERMISSION_DENIED  — operator: the plugin's manifest lacks the
//     grant. No code change helps; enable the permission.
//   - ERROR_CODE_NOT_CONFIGURED     — operator: the feature exists but is not
//     configured (no budget, unknown provider, backend disabled). Fix the
//     configuration, not the plugin.
//   - ERROR_CODE_UNAVAILABLE        — retry later: the capability is configured
//     but unusable right now (budget exhausted, transient transport failure).
//   - ERROR_CODE_INVALID_ARGUMENT   — plugin: the request itself was malformed.
//     Retrying cannot help; fix the caller.
//   - ERROR_CODE_NOT_FOUND          — plugin: the call named something that does
//     not exist. Fix the caller.
//   - ERROR_CODE_INTERNAL           — host defect: the host violated its own
//     contract. Report it; nothing the plugin does can fix it.
//   - any other code                — protocol defect from a newer host; treat
//     like INTERNAL until the SDK catches up.
//
// Match with errors.As:
//
//	var refusal *plugin_sdk.HostCallRefusalError
//	if errors.As(err, &refusal) {
//	    switch refusal.Code { ... }
//	}
type HostCallRefusalError struct {
	Code    pbv2.ErrorCode // stable classification; never UNSPECIFIED
	Reason  string         // stable snake_case token (see hostErrorReason)
	Message string         // human message for logs; never branch on it
}

func (e *HostCallRefusalError) Error() string {
	return fmt.Sprintf("torana: host refused (%s): %s", e.Reason, e.Message)
}

// classifiedRefusal builds a HostCallRefusalError from a framed host error.
func classifiedRefusal(herr *pbv2.HostError) *HostCallRefusalError {
	if herr == nil {
		return nil
	}
	return &HostCallRefusalError{
		Code:    herr.Code,
		Reason:  hostErrorReason(herr),
		Message: herr.Message,
	}
}
