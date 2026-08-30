//go:build !wasip1

package plugin_sdk

import (
	"testing"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

// The four argument bodies must reject an empty key.
//
// An empty key is never a meaningful address. On the meta side the host
// namespaces it per plugin, so an empty key names the namespace rather than a
// value in it; on the cache side it names a slot every plugin collides on.
// Without this the guest reads back an empty value and cannot tell it from a
// key that was simply never written.
func TestKeyIsRequired(t *testing.T) {
	for _, tc := range []struct {
		name string
		args interface{ Validate() error }
	}{
		{"MetaGetArgs", &pbv1.MetaGetArgs{}},
		{"MetaSetArgs", &pbv1.MetaSetArgs{Value: "v"}},
		{"CacheGetArgs", &pbv1.CacheGetArgs{}},
		{"CacheSetArgs", &pbv1.CacheSetArgs{Value: "v"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.args.Validate(); err == nil {
				t.Fatal("an empty key was accepted")
			}
		})
	}
}

// An empty VALUE is legitimate and must not be refused. Treating it as a delete
// is what left v1 unable to distinguish "store an empty string" from "remove
// this key".
func TestEmptyValueIsAllowed(t *testing.T) {
	for _, tc := range []struct {
		name string
		args interface{ Validate() error }
	}{
		{"MetaSetArgs", &pbv1.MetaSetArgs{Key: "k"}},
		{"CacheSetArgs", &pbv1.CacheSetArgs{Key: "k"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.args.Validate(); err != nil {
				t.Fatalf("an empty value was refused: %v", err)
			}
		})
	}
}

// A nil body must be refused rather than dereferenced. The host validates
// bytes a guest controls, so the guest picks when this path runs.
func TestNilArgsAreRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		args interface{ Validate() error }
	}{
		{"MetaGetArgs", (*pbv1.MetaGetArgs)(nil)},
		{"MetaSetArgs", (*pbv1.MetaSetArgs)(nil)},
		{"CacheGetArgs", (*pbv1.CacheGetArgs)(nil)},
		{"CacheSetArgs", (*pbv1.CacheSetArgs)(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.args.Validate(); err == nil {
				t.Fatal("nil args were accepted")
			}
		})
	}
}

// HostCall validates the body before sending, so a helper called with an empty
// key fails locally instead of costing a boundary crossing and returning an
// empty value the guest would read as a miss.
func TestHelpersRejectAnEmptyKeyWithoutCallingTheHost(t *testing.T) {
	if _, _, err := MetaGet(""); err == nil {
		t.Error("MetaGet(\"\") was accepted")
	}
	if _, err := MetaSet("", "v"); err == nil {
		t.Error("MetaSet(\"\", …) was accepted")
	}
	if _, _, err := CacheGet(""); err == nil {
		t.Error("CacheGet(\"\") was accepted")
	}
	if _, err := CacheSet("", "v"); err == nil {
		t.Error("CacheSet(\"\", …) was accepted")
	}
	if _, _, err := SharedCacheGet(""); err == nil {
		t.Error("SharedCacheGet(\"\") was accepted")
	}
	if _, err := SharedCacheSet("", "v"); err == nil {
		t.Error("SharedCacheSet(\"\", …) was accepted")
	}
}
