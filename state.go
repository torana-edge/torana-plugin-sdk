package plugin_sdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"google.golang.org/protobuf/proto"
	"strconv"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

// Durable plugin state
//
// A plugin has four places to keep things, and choosing wrongly is a common
// source of silent misbehaviour:
//
//   - Meta (env.meta_*): per request and private to this plugin. Gone when the
//     request ends. For carrying fragment buffers or tool-call tracking between
//     hooks of one request.
//   - Cache (env.cache_*): across requests, TTL'd, and private to this plugin.
//     For reusable data whose lifetime may depend on the configured backend.
//   - Shared (env.shared_cache_*): across requests, TTL'd, and shared by every
//     plugin granted access. For deliberate producer/consumer protocols;
//     prefix keys to avoid collisions.
//   - State (env.state_*): across requests and restarts, private to this plugin,
//     and without expiry. For data a plugin must retain after redeployment.
//
// State is the only scope designed to retain data across restarts. Cache
// persistence is backend- and TTL-dependent: an in-memory cache does not
// survive a process restart, while a Redis-backed cache may. Private versus
// shared describes namespace visibility, not persistence: Meta, Cache, and
// State are plugin-private; only Shared is a cross-plugin namespace.
//
// It requires the env.state_get / env.state_set / env.state_keys permissions.
// Nothing expires on its own: a plugin that writes per-conversation keys must
// delete them itself, or it grows without bound until it hits the store's caps.

// ErrStateUnavailable is returned when the host has no durable state
// configured. Plugins must tolerate this rather than assuming persistence — a
// proxy without a data directory has nowhere to put it.
//
// It is wrapped by the JSON convenience helpers (StateGetJSON, StateSetJSON),
// so errors.Is works there. The raw typed helpers (StateGet, StateSet,
// StateDelete, StateKeys) return a *HostError instead and let the caller
// classify — check for ErrorCode_ERROR_CODE_NOT_CONFIGURED.
var ErrStateUnavailable = errors.New("torana: durable plugin state is not available")

// StateGet reads one of this plugin's durable keys.
//
// A key that was never written returns a NOT_FOUND HostError; a key holding an
// empty string returns "" with no HostError. Branch with IsNotFound rather than
// testing the value — the same rule as MetaGet and CacheGet.
func StateGet(key string) (string, bool, error) {
	raw, herr, err := hostCallChecked("env.state_get", &pbv1.StateGetArgs{Key: key})
	if err != nil {
		return "", false, err
	}
	if herr != nil {
		if IsNotFound(herr) {
			return "", false, nil
		}
		return "", false, stateError(key, herr)
	}
	return string(raw), true, nil
}

// StateSet writes one of this plugin's durable keys.
//
// An empty value stores an empty value. It does not delete; use StateDelete to
// release a key.
func StateSet(key, value string) error {
	_, herr, err := hostCallChecked("env.state_set", &pbv1.StateSetArgs{Key: key, Value: value})
	if err != nil {
		return err
	}
	if herr != nil {
		return stateError(key, herr)
	}
	return nil
}

// StateDelete releases one durable key.
//
// Deleting a key that does not exist succeeds: the caller wants the key gone,
// and reporting NOT_FOUND would make every cleanup path branch on a condition
// it does not care about.
//
// This is a distinct command rather than StateSet(key, "") so deletion is not a
// magic value. It is authorised by the EXISTING env.state_set grant — deletion
// mutates a namespace the plugin can already overwrite, so a fourth durable-
// state capability would add approval ceremony without drawing a new line.
// The host maps the command to pbv1.StateDeletePermission; deriving the
// permission from the command string would look for a capability that does not
// exist.
func StateDelete(key string) error {
	_, herr, err := hostCallChecked(pbv1.StateDeleteCommand, &pbv1.StateDeleteArgs{Key: key})
	if err != nil {
		return err
	}
	if herr != nil {
		return stateError(key, herr)
	}
	return nil
}

// StateKeys lists this plugin's durable keys, sorted. Useful when a plugin
// stores one key per conversation and must enumerate them on a tick.
func StateKeys() ([]string, error) {
	raw, _, err := checkedHostCallValue("env.state_keys", nil)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var keys []string
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, fmt.Errorf("torana: decode state keys: %w", err)
	}
	return keys, nil
}

// StateGetJSON reads a key and decodes it into v.
//
// found is false when the key does not exist, and v is left untouched. Absence
// is reported by NOT_FOUND rather than inferred from an empty value.
//
// Any refusal other than absence is returned as an error: a plugin that treats
// a denied capability as "not stored yet" will quietly rewrite state it could
// not read.
func StateGetJSON(key string, v any) (found bool, err error) {
	raw, found, err := StateGet(key)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	// No value-based absence check. A key stored with StateSet(key, "") is
	// PRESENT, and reporting it as absent would contradict both the state
	// contract and this function's own documentation. Empty bytes are not
	// valid JSON, so that case falls through to a decode error — which is the
	// truth: something is stored and it is not a JSON document.
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return false, fmt.Errorf("torana: decode state %q: %w", key, err)
	}
	return true, nil
}

// stateError converts a classified refusal into an error that satisfies BOTH
// classification contracts at once:
//
//   - errors.As recovers the typed *HostCallRefusalError (Code, Reason,
//     Message) for EVERY framed refusal, so a caller can branch on the class
//     without string matching — advisory (NOT_CONFIGURED/UNAVAILABLE) versus
//     contract/protocol (PERMISSION_DENIED/INVALID_ARGUMENT/INTERNAL) versus
//     absence (NOT_FOUND);
//   - errors.Is(err, ErrStateUnavailable) stays true for NOT_CONFIGURED.
//
// A malformed or empty host reply is a protocol defect and deliberately does
// NOT produce a refusal: nothing was classified, so errors.As must not match.
func stateError(key string, herr *pbv1.HostError) error {
	if herr == nil {
		return nil
	}
	refusal := classifiedRefusal(herr)
	wrapped := fmt.Errorf("torana: state %q: %w", key, refusal)
	if herr.Code == pbv1.ErrorCode_ERROR_CODE_NOT_CONFIGURED {
		// Join both so errors.Is(ErrStateUnavailable) and errors.As(refusal)
		// hold simultaneously — one contract must not replace the other.
		return errors.Join(wrapped, ErrStateUnavailable)
	}
	return wrapped
}

// StateSetJSON encodes v and stores it.
func StateSetJSON(key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("torana: encode state %q: %w", key, err)
	}
	return StateSet(key, string(b))
}

// StateGetVersioned reads a value together with its opaque concurrency version.
func StateGetVersioned(key string) (*pbv1.StateValue, bool, error) {
	raw, found, err := checkedHostCallValue("env.state_get_versioned", &pbv1.StateGetArgs{Key: key})
	if err != nil || !found {
		return nil, found, err
	}
	var value pbv1.StateValue
	if err := pbv1.ValidateWire(raw, value.ProtoReflect().Descriptor()); err != nil {
		return nil, false, fmt.Errorf("torana: versioned state wire: %w", err)
	}
	if err := proto.Unmarshal(raw, &value); err != nil {
		return nil, false, fmt.Errorf("torana: decode versioned state: %w", err)
	}
	if err := value.Validate(); err != nil {
		return nil, false, err
	}
	return &value, true, nil
}

func StateCompareAndSet(key, value string, expectedVersion *string) (*pbv1.StateMutationResult, error) {
	raw, _, err := checkedHostCallValue("env.state_compare_and_set", &pbv1.StateCompareAndSetArgs{Key: key, Value: value, ExpectedVersion: expectedVersion})
	if err != nil {
		return nil, err
	}
	var result pbv1.StateMutationResult
	if err := pbv1.ValidateWire(raw, result.ProtoReflect().Descriptor()); err != nil {
		return nil, fmt.Errorf("torana: state mutation wire: %w", err)
	}
	if err := proto.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("torana: decode state mutation: %w", err)
	}
	return &result, result.Validate()
}

func StateCompareAndDelete(key, expectedVersion string) (*pbv1.StateMutationResult, error) {
	raw, _, err := checkedHostCallValue("env.state_compare_and_delete", &pbv1.StateCompareAndDeleteArgs{Key: key, ExpectedVersion: expectedVersion})
	if err != nil {
		return nil, err
	}
	var result pbv1.StateMutationResult
	if err := pbv1.ValidateWire(raw, result.ProtoReflect().Descriptor()); err != nil {
		return nil, fmt.Errorf("torana: state mutation wire: %w", err)
	}
	if err := proto.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("torana: decode state mutation: %w", err)
	}
	return &result, result.Validate()
}

func StateScan(prefix, cursor string, limit uint32) (*pbv1.StateScanResult, error) {
	raw, _, err := checkedHostCallValue("env.state_scan", &pbv1.StateScanArgs{Prefix: prefix, Cursor: cursor, Limit: limit})
	if err != nil {
		return nil, err
	}
	var result pbv1.StateScanResult
	if err := pbv1.ValidateWire(raw, result.ProtoReflect().Descriptor()); err != nil {
		return nil, fmt.Errorf("torana: state scan wire: %w", err)
	}
	if err := proto.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("torana: decode state scan: %w", err)
	}
	return &result, result.Validate()
}

// Now returns the host's wall-clock time in Unix milliseconds.
//
// This permission-gated clock is controllable through sdktest.SetNow, so plugin
// tests can exercise time-dependent behaviour deterministically. Plugins that
// reason about elapsed time (cache lifetimes, deadlines, rate windows) need
// this; those that do not should not request it.
//
// Requires the env.now permission, and returns an error when it is not granted
// or when the host clock cannot be read.
//
// DANGER: never write this value, or anything derived from it, into a request.
// Doing so makes the plugin's output differ between two identical requests,
// which invalidates the provider's prompt cache on every single turn and
// multiplies the operator's token spend. Torana's determinism test exists to
// catch exactly this. Use it to decide *whether* to act, never as content.
func Now() (int64, error) {
	raw, herr, err := hostCallChecked("env.now", nil)
	if err != nil {
		return 0, err
	}
	if herr != nil {
		// The classification survives: errors.As recovers the typed refusal,
		// exactly like the state helpers. An empty or malformed success is a
		// protocol defect and is NOT a refusal.
		return 0, fmt.Errorf("torana: clock is unavailable: %w", classifiedRefusal(herr))
	}
	if len(raw) == 0 {
		return 0, errors.New("torana: clock returned no reading")
	}
	ms, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("torana: invalid clock reading: %w", err)
	}
	return ms, nil
}
