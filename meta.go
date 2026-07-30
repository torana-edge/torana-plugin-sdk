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
// All four return (value, *HostError, error). The HostError is the host
// refusing — most often a missing permission — and is distinct from the error,
// which means the call itself could not be made. v1 collapsed these, so ~30
// `if err != nil` checks across the official plugins were dead code and a
// refusal was indistinguishable from an empty value.

// MetaGet reads one of this plugin's request-scoped keys.
//
// A key that was never set reads back as empty with no error: absence is an
// ordinary state for request metadata, not a failure.
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
func MetaSet(key, value string) (*pbv2.HostError, error) {
	_, herr, err := HostCall("env.meta_set", &pbv2.MetaSetArgs{Key: key, Value: value})
	return herr, err
}

// CacheGet reads a key from the shared cache. A miss returns empty with no
// error.
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
