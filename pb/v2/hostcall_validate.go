package v2

import "fmt"

// Validation for host-call envelopes and their normative argument schemas.
//
// These are what the host must reject rather than interpret: a missing oneof
// arm, an unspecified error code, a typed-nil nested error, or argument
// frames that cannot be acted on. Guests use the same helpers so handwritten
// plugins fail closed the same way the host would.

// knownErrorCodes are the ErrorCode values this build can act on. An unknown
// numeric code (from a newer host/guest) is refused rather than treated as a
// successful classification the plugin cannot branch on.
var knownErrorCodes = map[ErrorCode]bool{
	ErrorCode_ERROR_CODE_PERMISSION_DENIED: true,
	ErrorCode_ERROR_CODE_NOT_FOUND:         true,
	ErrorCode_ERROR_CODE_NOT_CONFIGURED:    true,
	ErrorCode_ERROR_CODE_UNAVAILABLE:       true,
	ErrorCode_ERROR_CODE_INVALID_ARGUMENT:  true,
	ErrorCode_ERROR_CODE_INTERNAL:          true,
}

// Validate reports whether a HostError carries a classified failure.
//
// UNSPECIFIED is the zero value and means "no code was set", not a real
// classification — plugins must not branch on it. Unknown numeric codes from
// a newer ABI are likewise refused.
func (x *HostError) Validate() error {
	if x == nil {
		return fmt.Errorf("host error is nil")
	}
	if x.Code == ErrorCode_ERROR_CODE_UNSPECIFIED {
		return fmt.Errorf("host error has no code; UNSPECIFIED is not a classification")
	}
	if !knownErrorCodes[x.Code] {
		return fmt.Errorf("host error code %v is not recognised by this build", x.Code)
	}
	return nil
}

// Validate reports whether a HostCallResult is a usable host-call reply.
//
// The result oneof must be set. Empty bytes on the value arm are success with
// no payload. The error arm must carry a validated HostError. Unknown top-level
// bytes are an unrecognised (future) result arm and are refused.
//
// This envelope is host-produced. Multiple known arms on the wire follow
// protobuf last-known-arm-wins after unmarshal; Validate cannot and does not
// detect that case. Guest-controlled HookResult uses DecodeHookResult instead.
func (x *HostCallResult) Validate() error {
	if x == nil {
		return fmt.Errorf("host call result is nil")
	}
	if len(x.ProtoReflect().GetUnknown()) != 0 {
		return fmt.Errorf("host call result carries a result arm this build does not recognise")
	}
	switch r := x.Result.(type) {
	case nil:
		return fmt.Errorf("host call result carries no result; empty is not a valid reply")
	case *HostCallResult_Value:
		// A typed-nil wrapper is a frame that claims value but hands over
		// nothing usable — refuse it the same way HookInput refuses typed-nil
		// payloads. Empty (non-nil) bytes are success with no payload.
		if r == nil {
			return fmt.Errorf("host call result names value but the wrapper is nil")
		}
		// r.Value may be nil or empty; both mean success with no bytes.
		return nil
	case *HostCallResult_Error:
		if r == nil || r.Error == nil {
			return fmt.Errorf("host call result names error but carries no HostError")
		}
		if err := r.Error.Validate(); err != nil {
			return fmt.Errorf("host call result: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("host call result carries an unhandled result arm")
	}
}

// Validate reports whether BlockRequestArgs can be acted on as a block verdict.
func (x *BlockRequestArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("block request args are nil")
	}
	if !blockRequestStatusOK(x.Status) {
		return fmt.Errorf("block request args status %d is outside 400–599", x.Status)
	}
	if x.Code == "" {
		return fmt.Errorf("block request args need a code")
	}
	return nil
}

// Validate reports whether RespondRequestArgs can be acted on.
// Empty content is allowed.
func (x *RespondRequestArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("respond request args are nil")
	}
	return nil
}

// Validate reports whether RouteRequestArgs names a route change.
func (x *RouteRequestArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("route request args are nil")
	}
	if x.Provider == "" && x.Model == "" {
		return fmt.Errorf("route request args need a provider and/or model")
	}
	return nil
}

// Validate reports whether SetIdentityArgs names an identity.
func (x *SetIdentityArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("set identity args are nil")
	}
	if x.Identity == "" {
		return fmt.Errorf("set identity args need a non-empty identity")
	}
	return nil
}

// Validate reports whether MetaAppendArgs can be accepted.
// Empty fragment is valid — it is the no-op/read path (see MetaAppendSuccessValue).
func (x *MetaAppendArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("meta append args are nil")
	}
	if x.BlockIndex < 0 {
		return fmt.Errorf("meta append args block_index %d is negative", x.BlockIndex)
	}
	return nil
}

// Validation for the metadata and cache argument bodies.
//
// A key is required in all four. An empty key is never a meaningful address:
// on the meta side the host namespaces it per plugin, so an empty key names
// the namespace itself rather than a value in it, and on the cache side it
// names a slot every plugin would collide on. Refusing it here means the guest
// learns at the call rather than reading back a value that silently was not
// stored.
//
// Values are deliberately not required. Empty is a legitimate value; deletion
// uses its explicit command.

func (x *MetaGetArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("meta get args are nil")
	}
	if x.Key == "" {
		return fmt.Errorf("meta get args have no key")
	}
	return nil
}

func (x *MetaSetArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("meta set args are nil")
	}
	if x.Key == "" {
		return fmt.Errorf("meta set args have no key")
	}
	return nil
}

func (x *CacheGetArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("cache get args are nil")
	}
	if x.Key == "" {
		return fmt.Errorf("cache get args have no key")
	}
	return nil
}

func (x *CacheSetArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("cache set args are nil")
	}
	if x.Key == "" {
		return fmt.Errorf("cache set args have no key")
	}
	return nil
}

// Durable-state argument bodies. A key is required for the same reason as meta
// and cache: an empty key names the plugin's namespace rather than a value in
// it. Values are not required — empty is a value, and env.state_delete is how a
// key is released.

func (x *StateGetArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("state get args are nil")
	}
	if x.Key == "" {
		return fmt.Errorf("state get args have no key")
	}
	return nil
}

func (x *StateSetArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("state set args are nil")
	}
	if x.Key == "" {
		return fmt.Errorf("state set args have no key")
	}
	return nil
}

func (x *StateDeleteArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("state delete args are nil")
	}
	if x.Key == "" {
		return fmt.Errorf("state delete args have no key")
	}
	return nil
}
