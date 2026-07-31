package plugin_sdk

import (
	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// Request-scoped metadata and the shared cache.
//
// Meta is scoped to one request and private to the calling plugin — the host
// namespaces the key, so two plugins using "state" do not collide. It is the
// right place for anything that must survive between hooks of the same request
// but must not outlive it.
//
// Cache is shared across requests AND across plugins, and is NOT namespaced.
// Two plugins naming the same key see each other's value. That is what makes it
// a cache rather than plugin state: callers who do not intend sharing must
// namespace the key themselves. For durable per-plugin storage use State*.
//
// Reads return (value, *HostError, error); writes return (*HostError, error).
// The HostError is the host refusing — most often a missing permission — and is distinct from the error,
// which means the call itself could not be made. v1 collapsed these, so ~30
// `if err != nil` checks across the official plugins were dead code and a
// refusal was indistinguishable from an empty value.
//
// ABSENCE IS NOT EMPTINESS. A key that does not exist returns a HostError with
// code NOT_FOUND; a key holding an empty string returns success with an empty
// value. An earlier draft of this file allowed storing an empty value AND
// documented a miss as returning empty, which made the two indistinguishable —
// exactly the v1 ambiguity v2 exists to remove. Use IsNotFound to branch.

// IsNotFound reports whether a HostError means the key does not exist, as
// opposed to any other refusal such as a missing permission.
//
// Without this, distinguishing a miss from a denial means comparing enum
// constants at every call site, and the easy mistake — treating every
// HostError as a miss — silently swallows permission failures.
func IsNotFound(herr *pbv2.HostError) bool {
	return herr != nil && herr.Code == pbv2.ErrorCode_ERROR_CODE_NOT_FOUND
}

// MetaGet reads one of this plugin's request-scoped keys.
//
// A key that was never written returns a NOT_FOUND HostError. A key holding an
// empty string returns "" with no HostError. Callers that treat absence as a
// default should branch with IsNotFound rather than testing the value.
func MetaGet(key string) (string, *pbv2.HostError, error) {
	raw, herr, err := HostCall("env.meta_get", &pbv2.MetaGetArgs{Key: key})
	if err != nil || herr != nil {
		return "", herr, err
	}
	return string(raw), nil, nil
}

// MetaSet writes one of this plugin's request-scoped keys.
//
// An empty value stores an empty value. It is not a delete — conflating the two
// is what left v1 unable to express "I looked, and the answer was nothing".
// After MetaSet(k, ""), MetaGet(k) succeeds with an empty value rather than
// reporting NOT_FOUND.
func MetaSet(key, value string) (*pbv2.HostError, error) {
	_, herr, err := HostCall("env.meta_set", &pbv2.MetaSetArgs{Key: key, Value: value})
	return herr, err
}

// CacheGet reads a key from the shared cache.
//
// A miss returns a NOT_FOUND HostError, not an empty value — the same
// distinction as MetaGet, and the reason a cached empty string is usable at
// all.
func CacheGet(key string) (string, *pbv2.HostError, error) {
	raw, herr, err := HostCall("env.cache_get", &pbv2.CacheGetArgs{Key: key})
	if err != nil || herr != nil {
		return "", herr, err
	}
	return string(raw), nil, nil
}

// CacheSet writes a key to the shared cache.
//
// The cache is shared with every other plugin, so a key that is not namespaced
// is a key another plugin can overwrite.
func CacheSet(key, value string) (*pbv2.HostError, error) {
	_, herr, err := HostCall("env.cache_set", &pbv2.CacheSetArgs{Key: key, Value: value})
	return herr, err
}
