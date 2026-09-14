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
// ABSENCE IS NOT EMPTINESS. A key that does not exist returns a HostError with
// code NOT_FOUND; a key holding an empty string returns success with an empty
// value. Use IsNotFound to branch.

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
func MetaGet(key string) (string, bool, error) {
	raw, found, err := checkedHostCallValue("env.meta_get", &pbv1.MetaGetArgs{Key: key})
	return string(raw), found, err
}

// MetaSet writes one of this plugin's request-scoped keys.
//
// An empty value stores an empty value; it is not a delete. After
// MetaSet(k, ""), MetaGet(k) succeeds with an empty value rather than reporting
// NOT_FOUND.
func MetaSet(key, value string) error { return checkedHostCall("env.meta_set", &pbv1.MetaSetArgs{Key: key, Value: value})
}

// CacheGet reads a key from this plugin's private cross-request cache.
//
// A miss returns a NOT_FOUND HostError, not an empty value — the same
// distinction as MetaGet, and the reason a cached empty string is usable at
// all.
func CacheGet(key string) (string, bool, error) {
	raw, found, err := checkedHostCallValue("env.cache_get", &pbv1.CacheGetArgs{Key: key})
	return string(raw), found, err
}

// CacheSet writes a key to this plugin's private cross-request cache.
func CacheSet(key, value string) error { return checkedHostCall("env.cache_set", &pbv1.CacheSetArgs{Key: key, Value: value})
}

// SharedCacheGet reads a key from the explicit cross-plugin cache namespace.
// Most plugins should use CacheGet. Shared cache capabilities are appropriate
// only when two separately approved plugins intentionally exchange data under
// a documented key contract.
func SharedCacheGet(key string) (string, bool, error) {
	raw, found, err := checkedHostCallValue("env.shared_cache_get", &pbv1.CacheGetArgs{Key: key})
	return string(raw), found, err
}

// SharedCacheSet writes a key to the explicit cross-plugin cache namespace.
// Possessing private env.cache_set never authorizes this operation.
func SharedCacheSet(key, value string) error { return checkedHostCall("env.shared_cache_set", &pbv1.CacheSetArgs{Key: key, Value: value})
}
