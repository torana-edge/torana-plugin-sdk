package v2

// Normative host-call command / permission strings and reply semantics that
// Migration B and StreamHandler must implement identically. Argument message
// schemas live in torana.proto; Validate lives in hostcall_validate.go.

// MetaAppendCommand is the host-call command for atomic request-metadata append.
//
// The dispatcher MUST map this command to MetaAppendPermission. Deriving the
// permission from the command string would look for "env.meta_append", which is
// not an operator-facing capability.
const MetaAppendCommand = "env.meta_append"

// MetaAppendPermission is the operator-facing capability that authorises
// MetaAppendCommand. There is no separate env.meta_append permission.
const MetaAppendPermission = "env.meta_set"

// StateDeleteCommand is the host-call command for releasing one durable key.
//
// The dispatcher MUST map this command to StateDeletePermission. Deriving the
// permission from the command string would look for "env.state_delete", which
// is not an operator-facing capability.
const StateDeleteCommand = "env.state_delete"

// StateDeletePermission is the operator-facing capability that authorises
// StateDeleteCommand. There is no separate env.state_delete permission.
//
// Deletion is a mutation of the same plugin-private namespace a plugin can
// already overwrite, so a fourth durable-state capability would add manifest
// and approval ceremony without drawing a new security line. v1's StateDelete
// was env.state_set for the same reason: it was a set with an empty value.
const StateDeletePermission = "env.state_set"

// MetaAppendSuccessValue returns the HostCallResult.value bytes for a
// successful env.meta_append.
//
// Storage mutation is the host's job: under the request lock, append the
// fragment with amortized/in-place growth (bytes.Buffer, slice grow, etc.).
// This helper only defines the constant-size acknowledgement vs read-back
// split so ordinary deltas do not copy and return the cumulative buffer.
//
//	non-empty fragment → empty success value (ack; buffer stays host-side)
//	empty fragment     → complete current buffer (absent → empty bytes; key
//	                     stays absent). This is the ContentBlockStop /
//	                     fail-open read path.
func MetaAppendSuccessValue(fragment, current []byte, present bool) []byte {
	if len(fragment) != 0 {
		return []byte{}
	}
	if !present {
		return []byte{}
	}
	out := make([]byte, len(current))
	copy(out, current)
	return out
}
