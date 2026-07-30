package v2

// Normative host-call command / permission strings and buffer semantics that
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

// ApplyMetaAppend is the normative buffer update for env.meta_append.
//
// existing is the buffer currently stored for the block_index key; present is
// whether that key exists. The returned complete slice is what
// HostCallResult.value must carry on success. A false presentOut means the key
// must remain absent (only possible when the fragment was empty and the key
// was already absent).
//
// Semantics:
//
//	absent + empty fragment  → complete empty, key stays absent
//	present + empty fragment → complete = existing (no-op/read)
//	absent + non-empty       → complete = fragment (create)
//	present + non-empty      → complete = existing∥fragment
func ApplyMetaAppend(existing []byte, present bool, fragment []byte) (complete []byte, presentOut bool) {
	if len(fragment) == 0 {
		if !present {
			return []byte{}, false
		}
		out := make([]byte, len(existing))
		copy(out, existing)
		return out, true
	}
	if !present {
		out := make([]byte, len(fragment))
		copy(out, fragment)
		return out, true
	}
	out := make([]byte, 0, len(existing)+len(fragment))
	out = append(out, existing...)
	out = append(out, fragment...)
	return out, true
}
