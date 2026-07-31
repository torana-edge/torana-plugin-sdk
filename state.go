package plugin_sdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// Durable plugin state
//
// A plugin has three places to keep things, and choosing wrongly is a common
// source of silent misbehaviour:
//
//	Meta   (env.meta_*)    per request, private to this plugin. Gone when the
//	                       request ends. For carrying data between hooks of one
//	                       request — fragment buffers, tool-call tracking.
//	Cache  (env.cache_*)   across requests, TTL'd, and SHARED by every plugin.
//	                       The channel plugins cooperate through. Prefix your
//	                       keys; anyone can read them.
//	State  (env.state_*)   across requests AND across restarts, private to this
//	                       plugin, no expiry. For things a plugin must still
//	                       have after the proxy is redeployed.
//
// State is the only one that survives a restart, and the only one where a
// plugin's neighbours cannot read its keys.
//
// It requires the env.state_get / env.state_set / env.state_keys permissions.
// Nothing expires on its own: a plugin that writes per-conversation keys must
// delete them itself, or it grows without bound until it hits the store's caps.

// ErrStateUnavailable is returned when the host has no durable state
// configured. Plugins must tolerate this rather than assuming persistence — a
// proxy without a data directory has nowhere to put it.
var ErrStateUnavailable = errors.New("torana: durable plugin state is not available")

// StateGet reads one of this plugin's durable keys.
//
// A key that was never written returns a NOT_FOUND HostError; a key holding an
// empty string returns "" with no HostError. Branch with IsNotFound rather than
// testing the value — the same rule as MetaGet and CacheGet.
//
// v1 could not express this. The reply shared a namespace with the stored value,
// so a JSON object with a "status" field was ambiguous between a host error and
// the plugin's own data, and the heuristic that guessed between them is gone.
func StateGet(key string) (string, *pbv2.HostError, error) {
	raw, herr, err := HostCall("env.state_get", &pbv2.StateGetArgs{Key: key})
	if err != nil || herr != nil {
		return "", herr, err
	}
	return string(raw), nil, nil
}

// StateSet writes one of this plugin's durable keys.
//
// An empty value STORES an empty value. It does not delete — that was the v1
// behaviour, and it made storing an empty string unexpressible while
// contradicting the meta and cache stores where empty is ordinary data. An
// author who learned the rule for those two would have been wrong here.
func StateSet(key, value string) (*pbv2.HostError, error) {
	_, herr, err := HostCall("env.state_set", &pbv2.StateSetArgs{Key: key, Value: value})
	return herr, err
}

// StateDelete releases one durable key.
//
// Deleting a key that does not exist succeeds: the caller wants the key gone,
// and reporting NOT_FOUND would make every cleanup path branch on a condition
// it does not care about.
//
// This is a distinct command rather than StateSet(key, "") so deletion is not a
// magic value. MIGRATION B: the host must implement env.state_delete; until it
// does, this is refused like any unimplemented command.
func StateDelete(key string) (*pbv2.HostError, error) {
	_, herr, err := HostCall("env.state_delete", &pbv2.StateDeleteArgs{Key: key})
	return herr, err
}

// StateKeys lists this plugin's durable keys, sorted. Useful when a plugin
// stores one key per conversation and must enumerate them on a tick.
func StateKeys() ([]string, *pbv2.HostError, error) {
	raw, herr, err := HostCall("env.state_keys", nil)
	if err != nil || herr != nil {
		return nil, herr, err
	}
	if len(raw) == 0 {
		return nil, nil, nil
	}
	var keys []string
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, nil, fmt.Errorf("torana: decode state keys: %w", err)
	}
	return keys, nil, nil
}

// StateGetJSON reads a key and decodes it into v.
//
// found is false when the key does not exist, and v is left untouched. This is
// now answered by NOT_FOUND rather than by the value being empty, so a stored
// empty JSON document is no longer mistaken for absence.
//
// Any refusal other than absence is returned as an error: a plugin that treats
// a denied capability as "not stored yet" will quietly rewrite state it could
// not read.
func StateGetJSON(key string, v any) (found bool, err error) {
	raw, herr, err := StateGet(key)
	if err != nil {
		return false, err
	}
	if IsNotFound(herr) {
		return false, nil
	}
	if herr != nil {
		return false, fmt.Errorf("torana: state %q: %s: %s", key, hostErrorReason(herr), herr.Message)
	}
	if raw == "" {
		return false, nil
	}
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return false, fmt.Errorf("torana: decode state %q: %w", key, err)
	}
	return true, nil
}

// StateSetJSON encodes v and stores it.
func StateSetJSON(key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("torana: encode state %q: %w", key, err)
	}
	herr, err := StateSet(key, string(b))
	if err != nil {
		return err
	}
	if herr != nil {
		return fmt.Errorf("torana: state %q: %s: %s", key, hostErrorReason(herr), herr.Message)
	}
	return nil
}

// Now returns the host's wall-clock time in Unix milliseconds.
//
// WASI preview1 gives a plugin no clock, deliberately — a sandbox withholds
// ambient authority, and time is ambient authority. Plugins that reason about
// elapsed time (cache lifetimes, deadlines, rate windows) need this; those that
// do not should not request it.
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
	raw, herr, err := HostCall("env.now", nil)
	if err != nil {
		return 0, err
	}
	if herr != nil {
		return 0, fmt.Errorf("torana: clock is unavailable (%s): %s",
			hostErrorReason(herr), herr.Message)
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
