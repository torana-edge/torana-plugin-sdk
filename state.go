package plugin_sdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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

// StateGet reads one of this plugin's durable keys. A missing key returns "".
func StateGet(key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("torana: state key is required")
	}
	res, err := HostCall("env.state_get", key)
	if err != nil {
		return "", err
	}
	if isPermissionDenied(res) {
		return "", ErrStateUnavailable
	}
	return res, nil
}

// StateSet writes one of this plugin's durable keys. An empty value deletes it.
func StateSet(key, value string) error {
	if key == "" {
		return fmt.Errorf("torana: state key is required")
	}
	payload, err := json.Marshal(map[string]any{"key": key, "value": value})
	if err != nil {
		return err
	}
	res, err := HostCall("env.state_set", string(payload))
	if err != nil {
		return err
	}
	return stateStatus(res)
}

// StateKeys lists this plugin's durable keys, sorted. Useful when a plugin
// stores one key per conversation and must enumerate them on a tick.
func StateKeys() ([]string, error) {
	res, err := HostCall("env.state_keys", "")
	if err != nil {
		return nil, err
	}
	if isPermissionDenied(res) {
		return nil, ErrStateUnavailable
	}
	if res == "" {
		return nil, nil
	}
	var keys []string
	if err := json.Unmarshal([]byte(res), &keys); err != nil {
		return nil, fmt.Errorf("torana: decode state keys: %w", err)
	}
	return keys, nil
}

// StateGetJSON reads a key and decodes it into v. A missing key leaves v
// untouched and reports found=false, so callers can distinguish "absent" from
// "present but zero".
func StateGetJSON(key string, v any) (found bool, err error) {
	raw, err := StateGet(key)
	if err != nil || raw == "" {
		return false, err
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
	return StateSet(key, string(b))
}

// stateStatus turns the host's JSON envelope into an error.
func stateStatus(res string) error {
	if res == "" {
		return nil
	}
	var envelope struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(res), &envelope); err != nil {
		return nil // not an envelope; treat as success
	}
	if envelope.Status == "error" {
		if envelope.Message == "permission denied" {
			return ErrStateUnavailable
		}
		return fmt.Errorf("torana: %s", envelope.Message)
	}
	return nil
}

func isPermissionDenied(res string) bool {
	return res == `{"status":"error","message":"permission denied"}`
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
	res, err := HostCall("env.now", "")
	if err != nil {
		return 0, err
	}
	if res == "" || isPermissionDenied(res) {
		return 0, errors.New("torana: clock is unavailable (requires env.now permission)")
	}
	ms, err := strconv.ParseInt(res, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("torana: invalid clock reading: %w", err)
	}
	return ms, nil
}
