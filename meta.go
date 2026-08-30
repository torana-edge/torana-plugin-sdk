package plugin_sdk

import (
	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

// Request-scoped metadata plus private and explicitly shared caches.
//
// Meta is scoped to one request and private to the calling plugin — the host
// namespaces the key, so two plugins using "state" do not collide. It is the
// right place for anything that must survive between hooks of the same request
// but must not outlive it.
//
// CacheGet/CacheSet are shared across requests but namespaced to the exact
// executing plugin. SharedCacheGet/SharedCacheSet are the separate,
// separately-granted cross-plugin channel. For durable per-plugin storage use
// State*.
//
// Reads return (value, *HostError, error); writes return (*HostError, error).
//
// HostError is a classified host-side non-success: NOT_FOUND is ordinary
// absence for reads, while other codes report a refusal — most often a missing
// permission — or a host failure. error means the call itself could not be
// made, or its reply was invalid.
//
// v1 collapsed these, so ~30 `if err != nil` checks across the official plugins
// were dead code and a refusal was indistinguishable from an empty value.
//
// ABSENCE IS NOT EMPTINESS. A key that does not exist returns a HostError with
// code NOT_FOUND; a key holding an empty string returns success with an empty
// value. An earlier draft of this file allowed storing an empty value AND
// documented a miss as returning empty, which made the two indistinguishable —
// exactly the v1 ambiguity v1 exists to remove. Use IsNotFound to branch.

// IsNotFound reports whether a HostError means the key does not exist, as
// opposed to any other refusal such as a missing permission.
//
// Without this, distinguishing a miss from a denial means comparing enum
// constants at every call site, and the easy mistake — treating every
// HostError as a miss — silently swallows permission failures.
func IsNotFound(herr *pbv1.HostError) bool {
	return herr != nil && herr.Code == pbv1.ErrorCode_ERROR_CODE_NOT_FOUND
}

// MetaGet reads one of this plugin's request-scoped keys.
//
// A key that was never written returns a NOT_FOUND HostError. A key holding an
// empty string returns "" with no HostError. Callers that treat absence as a
// default should branch with IsNotFound rather than testing the value.
func MetaGet(key string) (string, *pbv1.HostError, error) {
	raw, herr, err := HostCall("env.meta_get", &pbv1.MetaGetArgs{Key: key})
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
func MetaSet(key, value string) (*pbv1.HostError, error) {
	_, herr, err := HostCall("env.meta_set", &pbv1.MetaSetArgs{Key: key, Value: value})
	return herr, err
}

// CacheGet reads a key from this plugin's private cross-request cache.
//
// A miss returns a NOT_FOUND HostError, not an empty value — the same
// distinction as MetaGet, and the reason a cached empty string is usable at
// all.
func CacheGet(key string) (string, *pbv1.HostError, error) {
	raw, herr, err := HostCall("env.cache_get", &pbv1.CacheGetArgs{Key: key})
	if err != nil || herr != nil {
		return "", herr, err
	}
	return string(raw), nil, nil
}

// CacheSet writes a key to this plugin's private cross-request cache.
func CacheSet(key, value string) (*pbv1.HostError, error) {
	_, herr, err := HostCall("env.cache_set", &pbv1.CacheSetArgs{Key: key, Value: value})
	return herr, err
}

// SharedCacheGet reads a key from the explicit cross-plugin cache namespace.
// Most plugins should use CacheGet. Shared cache capabilities are appropriate
// only when two separately approved plugins intentionally exchange data under
// a documented key contract.
func SharedCacheGet(key string) (string, *pbv1.HostError, error) {
	raw, herr, err := HostCall("env.shared_cache_get", &pbv1.CacheGetArgs{Key: key})
	if err != nil || herr != nil {
		return "", herr, err
	}
	return string(raw), nil, nil
}

// SharedCacheSet writes a key to the explicit cross-plugin cache namespace.
// Possessing private env.cache_set never authorizes this operation.
func SharedCacheSet(key, value string) (*pbv1.HostError, error) {
	_, herr, err := HostCall("env.shared_cache_set", &pbv1.CacheSetArgs{Key: key, Value: value})
	return herr, err
}
