package sdktest_test

import (
	"errors"
	"reflect"
	"testing"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"github.com/torana-edge/torana-plugin-sdk/sdktest"
	"google.golang.org/protobuf/proto"
)

// These make real calls through a real Harness, on purpose.
//
// The first version of the meta/cache tests exercised only argument validation
// and never made a valid call. All four helpers were completely unusable under
// sdktest — the commands fell through to the legacy JSON dispatcher and failed
// to decode as HostCallResult — and the suite stayed green. A test that never
// calls the thing it is testing is not coverage.

func TestMetaSetThenGetReturnsTheStoredValue(t *testing.T) {
	sdktest.New(t).Run(func() {
		if err := sdk.MetaSet("k", "v"); err != nil {
			t.Fatalf("MetaSet: err=%v", err)
		}
		got, found, err := sdk.MetaGet("k")
		if err != nil || !found {
			t.Fatalf("MetaGet: err=%v found=%v", err, found)
		}
		if got != "v" {
			t.Fatalf("MetaGet = %q, want %q", got, "v")
		}
	})
}

func TestCacheSetThenGetReturnsTheStoredValue(t *testing.T) {
	sdktest.New(t).Run(func() {
		if err := sdk.CacheSet("k", "v"); err != nil {
			t.Fatalf("CacheSet: err=%v", err)
		}
		got, found, err := sdk.CacheGet("k")
		if err != nil || !found {
			t.Fatalf("CacheGet: err=%v found=%v", err, found)
		}
		if got != "v" {
			t.Fatalf("CacheGet = %q, want %q", got, "v")
		}
	})
}

func TestSharedCacheSetThenGetUsesExplicitCommands(t *testing.T) {
	h := sdktest.New(t)
	h.Run(func() {
		if err := sdk.SharedCacheSet("contract:key", "v"); err != nil {
			t.Fatalf("SharedCacheSet: err=%v", err)
		}
		got, found, err := sdk.SharedCacheGet("contract:key")
		if err != nil || !found || got != "v" {
			t.Fatalf("SharedCacheGet = %q, found=%v err=%v", got, found, err)
		}
	})
	commands := []string{h.Calls()[0].Command, h.Calls()[1].Command}
	if !reflect.DeepEqual(commands, []string{"env.shared_cache_set", "env.shared_cache_get"}) {
		t.Fatalf("commands = %v", commands)
	}
}

// The distinction this change exists to preserve. A missing key and a stored
// empty string must not produce the same answer, or a plugin cannot tell
// "nothing stored" from "I stored nothing" — the ambiguity the typed result
// contract removes.
func TestAbsenceIsNotEmptiness(t *testing.T) {
	for _, store := range []struct {
		name string
		set  func(k, v string) error
		get  func(k string) (string, bool, error)
	}{
		{"meta", sdk.MetaSet, sdk.MetaGet},
		{"cache", sdk.CacheSet, sdk.CacheGet},
		{"shared cache", sdk.SharedCacheSet, sdk.SharedCacheGet},
	} {
		t.Run(store.name, func(t *testing.T) {
			sdktest.New(t).Run(func() {
				// Never written.
				_, found, err := store.get("absent")
				if err != nil {
					t.Fatalf("get(absent): error %v", err)
				}
				if found {
					t.Fatal("a missing key succeeded; it is indistinguishable from a stored empty value")
				}

				// Explicitly stored empty.
				if err := store.set("empty", ""); err != nil {
					t.Fatalf("set(empty, \"\"): err=%v", err)
				}
				got, found, err := store.get("empty")
				if err != nil {
					t.Fatalf("get(empty): transport error %v", err)
				}
				if !found {
					t.Fatal("a stored empty value reported absence; empty is a value, not a delete")
				}
				if got != "" {
					t.Fatalf("get(empty) = %q, want empty", got)
				}
			})
		})
	}
}

// An empty key is refused before anything is stored. A harness that mutated
// anyway would hide the failure from the test asserting it.
func TestInvalidKeyIsRejectedWithoutMutating(t *testing.T) {
	h := sdktest.New(t)
	h.Run(func() {
		if err := sdk.MetaSet("", "v"); err == nil {
			t.Fatal("MetaSet with an empty key was accepted")
		}
	})
	for _, c := range h.Calls() {
		if c.Command == "env.meta_set" {
			t.Fatalf("a rejected MetaSet still reached the host: %+v", c)
		}
	}
}

func TestPermissionDenialIsDistinctFromNotFound(t *testing.T) {
	h := sdktest.New(t)
	h.DenyPermission("env.meta_get")
	h.Run(func() {
		_, _, err := sdk.MetaGet("k")
		if err == nil {
			t.Fatal("a denied permission succeeded")
		}
	})
}

// The command name and the decoded argument fields are asserted, so a typo or
// a swapped Args message cannot leave the suite green while sending nonsense.
func TestCommandNamesAndArgumentsAreExact(t *testing.T) {
	h := sdktest.New(t)
	h.Run(func() {
		if err := sdk.MetaSet("mk", "mv"); err != nil {
			t.Fatal(err)
		}
		if err := sdk.CacheSet("ck", "cv"); err != nil {
			t.Fatal(err)
		}
		// The getters need distinct keys of their own. Asserting only the
		// setters would not prove a getter forwards its CALLER'S key rather
		// than a constant or the wrong field.
		if _, _, err := sdk.MetaGet("mget"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := sdk.CacheGet("cget"); err != nil {
			t.Fatal(err)
		}
	})

	seen := map[string]bool{}
	for _, c := range h.Calls() {
		seen[c.Command] = true
		switch c.Command {
		case "env.meta_set":
			var a pbv1.MetaSetArgs
			if err := proto.Unmarshal([]byte(c.Args), &a); err != nil {
				t.Fatalf("env.meta_set args do not decode as MetaSetArgs: %v", err)
			}
			if a.Key != "mk" || a.Value != "mv" {
				t.Fatalf("env.meta_set args = %+v, want key=mk value=mv", &a)
			}
		case "env.cache_set":
			var a pbv1.CacheSetArgs
			if err := proto.Unmarshal([]byte(c.Args), &a); err != nil {
				t.Fatalf("env.cache_set args do not decode as CacheSetArgs: %v", err)
			}
			if a.Key != "ck" || a.Value != "cv" {
				t.Fatalf("env.cache_set args = %+v, want key=ck value=cv", &a)
			}
		case "env.meta_get":
			var a pbv1.MetaGetArgs
			if err := proto.Unmarshal([]byte(c.Args), &a); err != nil {
				t.Fatalf("env.meta_get args do not decode as MetaGetArgs: %v", err)
			}
			if a.Key != "mget" {
				t.Fatalf("env.meta_get args = %+v, want key=mget", &a)
			}
		case "env.cache_get":
			var a pbv1.CacheGetArgs
			if err := proto.Unmarshal([]byte(c.Args), &a); err != nil {
				t.Fatalf("env.cache_get args do not decode as CacheGetArgs: %v", err)
			}
			if a.Key != "cget" {
				t.Fatalf("env.cache_get args = %+v, want key=cget", &a)
			}
		}
	}
	for _, cmd := range []string{"env.meta_set", "env.cache_set", "env.meta_get", "env.cache_get"} {
		if !seen[cmd] {
			t.Errorf("%s was never called", cmd)
		}
	}
}

// meta and cache are separate stores. Sharing one map in the harness would let
// a plugin's test pass while the real host kept them apart.
func TestMetaAndCacheAreSeparateStores(t *testing.T) {
	sdktest.New(t).Run(func() {
		if err := sdk.MetaSet("same", "from-meta"); err != nil {
			t.Fatal(err)
		}
		if _, found, _ := sdk.CacheGet("same"); found {
			t.Fatal("a meta write was visible through CacheGet")
		}
		if err := sdk.CacheSet("same", "from-cache"); err != nil {
			t.Fatal(err)
		}
		got, _, _ := sdk.MetaGet("same")
		if got != "from-meta" {
			t.Fatalf("a cache write overwrote meta: MetaGet = %q", got)
		}
	})
}

func TestPluginAndSharedCacheAreSeparateStores(t *testing.T) {
	sdktest.New(t).Run(func() {
		if err := sdk.CacheSet("same", "private"); err != nil {
			t.Fatalf("CacheSet: err=%v", err)
		}
		if _, found, err := sdk.SharedCacheGet("same"); err != nil || found {
			t.Fatalf("private cache leaked into shared cache: err=%v found=%v", err, found)
		}
		if err := sdk.SharedCacheSet("same", "shared"); err != nil {
			t.Fatalf("SharedCacheSet: err=%v", err)
		}
		got, found, err := sdk.CacheGet("same")
		if err != nil || !found || got != "private" {
			t.Fatalf("shared cache overwrote private cache: got=%q found=%v err=%v", got, found, err)
		}
	})
}

// A transport failure must reach the caller as an error, not be flattened into
// NOT_FOUND or an empty success. Those wrappers each collapse three channels
// into fewer, and a plugin that reads a transport fault as a cache miss will
// happily recompute and carry on while the boundary is broken.
func TestTransportFailureIsNotAMiss(t *testing.T) {
	h := sdktest.New(t)
	h.StubHostCall("env.meta_get", func(string) (string, error) {
		return "", errors.New("guest/host boundary failed")
	})
	h.Run(func() {
		v, _, err := sdk.MetaGet("k")
		if err == nil {
			t.Fatal("a transport failure was not reported as an error")
		}
		if v != "" {
			t.Fatalf("a failed read returned a value: %q", v)
		}
	})
}

// A reply that is not a valid HostCallResult is a protocol fault and must not
// be mistaken for either a miss or a successfully read value.
func TestMalformedReplyIsNotAValue(t *testing.T) {
	h := sdktest.New(t)
	h.StubHostCall("env.cache_get", func(string) (string, error) {
		return "\xff\xfe not a HostCallResult", nil
	})
	h.Run(func() {
		v, _, err := sdk.CacheGet("k")
		if err == nil {
			t.Fatal("a malformed reply was accepted")
		}
		if v != "" {
			t.Fatalf("a malformed reply produced a value: %q", v)
		}
	})
}

// The harness must be able to seed and read everything the SDK lets a plugin
// use. The private and shared caches are separate stores, and for one release
// only the private one could be seeded — so a plugin calling SharedCacheGet
// could not be tested at all. It broke an official plugin's suite four tests
// at a time, and each failure looked like the plugin declining to use a cached
// value rather than never having been shown one.
func TestSharedCacheIsSeedableAndReadable(t *testing.T) {
	h := sdktest.New(t)
	h.SeedSharedCache("intent:call_1", "find the bug")

	// The private store must NOT satisfy a shared read: they are different
	// stores, and seeding the wrong one should stay visibly wrong.
	if _, ok := h.Cache("intent:call_1"); ok {
		t.Error("SeedSharedCache wrote into the private cache; the two stores are not separate")
	}

	h.Run(func() {
		got, found, err := sdk.SharedCacheGet("intent:call_1")
		if err != nil || !found {
			t.Fatalf("SharedCacheGet on a seeded key: err=%v found=%v", err, found)
		}
		if got != "find the bug" {
			t.Fatalf("SharedCacheGet = %q, want %q", got, "find the bug")
		}
		if err := sdk.SharedCacheSet("derived:call_1", got+"/derived"); err != nil {
			t.Fatalf("SharedCacheSet: err=%v", err)
		}
	})

	got, ok := h.SharedCache("derived:call_1")
	if !ok {
		t.Fatal("what the plugin published to the shared cache is not readable back")
	}
	if got != "find the bug/derived" {
		t.Errorf("SharedCache = %q, want %q", got, "find the bug/derived")
	}
}
