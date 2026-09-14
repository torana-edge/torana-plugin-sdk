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
// Reads return (value, found, error); writes return error. A missing key is
// ("", false, nil). Permission denials, unavailable services, malformed
// replies, and transport failures are errors. Classified refusals wrap
// *HostCallRefusalError so callers can use errors.As and its stable Code.
//
// ABSENCE IS NOT EMPTINESS. A key holding an empty string returns ("", true,
// nil). Branch on found, not the value.

// IsNotFound reports whether a raw HostError represents ordinary absence.
// Most plugin code should use the found result returned by MetaGet, CacheGet,
// SharedCacheGet, StateGet, or StateGetVersioned instead.
func IsNotFound(herr *pbv1.HostError) bool {
	return herr != nil && herr.Code == pbv1.ErrorCode_ERROR_CODE_NOT_FOUND
}

// MetaGet reads one of this plugin's request-scoped keys.
//
// A key that was never written returns ("", false, nil). A key holding an
// empty string returns ("", true, nil). Other host refusals return an error.
func MetaGet(key string) (string, bool, error) {
	raw, found, err := checkedHostCallValue("env.meta_get", &pbv1.MetaGetArgs{Key: key})
	return string(raw), found, err
}

// MetaSet writes one of this plugin's request-scoped keys.
//
// An empty value stores an empty value; it is not a delete. After
// MetaSet(k, ""), MetaGet(k) succeeds with an empty value rather than reporting
// NOT_FOUND.
func MetaSet(key, value string) error {
	return checkedHostCall("env.meta_set", &pbv1.MetaSetArgs{Key: key, Value: value})
}

// CacheGet reads a key from this plugin's private cross-request cache.
//
// A miss returns ("", false, nil); a cached empty string returns ("", true,
// nil). Other host refusals return an error.
func CacheGet(key string) (string, bool, error) {
	raw, found, err := checkedHostCallValue("env.cache_get", &pbv1.CacheGetArgs{Key: key})
	return string(raw), found, err
}

// CacheSet writes a key to this plugin's private cross-request cache without
// an explicit TTL. An empty value is stored, not deleted.
func CacheSet(key, value string) error {
	return checkedHostCall("env.cache_set", &pbv1.CacheSetArgs{Key: key, Value: value})
}

// CacheSetTTL writes a private cache entry with a positive bounded TTL in milliseconds.
func CacheSetTTL(key, value string, ttlMS uint64) error {
	return checkedHostCall("env.cache_set", &pbv1.CacheSetArgs{Key: key, Value: value, TtlMs: &ttlMS})
}

// CacheDelete removes a private cache entry. Missing entries succeed.
func CacheDelete(key string) error {
	return checkedHostCall("env.cache_delete", &pbv1.CacheDeleteArgs{Key: key})
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
func SharedCacheSet(key, value string) error {
	return checkedHostCall("env.shared_cache_set", &pbv1.CacheSetArgs{Key: key, Value: value})
}

// SharedCacheSetTTL writes a shared entry with a positive bounded TTL in milliseconds.
func SharedCacheSetTTL(key, value string, ttlMS uint64) error {
	return checkedHostCall("env.shared_cache_set", &pbv1.CacheSetArgs{Key: key, Value: value, TtlMs: &ttlMS})
}

// SharedCacheDelete removes a shared entry. Missing entries succeed.
func SharedCacheDelete(key string) error {
	return checkedHostCall("env.shared_cache_delete", &pbv1.CacheDeleteArgs{Key: key})
}
