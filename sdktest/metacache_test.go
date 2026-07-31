package sdktest_test

import (
	"testing"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
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
		if herr, err := sdk.MetaSet("k", "v"); err != nil || herr != nil {
			t.Fatalf("MetaSet: err=%v herr=%v", err, herr)
		}
		got, herr, err := sdk.MetaGet("k")
		if err != nil || herr != nil {
			t.Fatalf("MetaGet: err=%v herr=%v", err, herr)
		}
		if got != "v" {
			t.Fatalf("MetaGet = %q, want %q", got, "v")
		}
	})
}

func TestCacheSetThenGetReturnsTheStoredValue(t *testing.T) {
	sdktest.New(t).Run(func() {
		if herr, err := sdk.CacheSet("k", "v"); err != nil || herr != nil {
			t.Fatalf("CacheSet: err=%v herr=%v", err, herr)
		}
		got, herr, err := sdk.CacheGet("k")
		if err != nil || herr != nil {
			t.Fatalf("CacheGet: err=%v herr=%v", err, herr)
		}
		if got != "v" {
			t.Fatalf("CacheGet = %q, want %q", got, "v")
		}
	})
}

// The distinction this change exists to preserve. A missing key and a stored
// empty string must not produce the same answer, or a plugin cannot tell
// "nothing stored" from "I stored nothing" — the v1 ambiguity v2 removes.
func TestAbsenceIsNotEmptiness(t *testing.T) {
	for _, store := range []struct {
		name string
		set  func(k, v string) (*pbv2.HostError, error)
		get  func(k string) (string, *pbv2.HostError, error)
	}{
		{"meta", sdk.MetaSet, sdk.MetaGet},
		{"cache", sdk.CacheSet, sdk.CacheGet},
	} {
		t.Run(store.name, func(t *testing.T) {
			sdktest.New(t).Run(func() {
				// Never written.
				_, herr, err := store.get("absent")
				if err != nil {
					t.Fatalf("get(absent): transport error %v", err)
				}
				if herr == nil {
					t.Fatal("a missing key succeeded; it is indistinguishable from a stored empty value")
				}
				if !sdk.IsNotFound(herr) {
					t.Fatalf("a missing key reported %v, want NOT_FOUND", herr.Code)
				}

				// Explicitly stored empty.
				if herr, err := store.set("empty", ""); err != nil || herr != nil {
					t.Fatalf("set(empty, \"\"): err=%v herr=%v", err, herr)
				}
				got, herr, err := store.get("empty")
				if err != nil {
					t.Fatalf("get(empty): transport error %v", err)
				}
				if herr != nil {
					t.Fatalf("a stored empty value reported %v; empty is a value, not a delete", herr.Code)
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
		if _, err := sdk.MetaSet("", "v"); err == nil {
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
		_, herr, err := sdk.MetaGet("k")
		if err != nil {
			t.Fatalf("transport error: %v", err)
		}
		if herr == nil {
			t.Fatal("a denied permission succeeded")
		}
		if sdk.IsNotFound(herr) {
			t.Fatal("a permission denial was reported as NOT_FOUND; " +
				"a plugin would treat a refused capability as an ordinary miss")
		}
		if herr.Code != pbv2.ErrorCode_ERROR_CODE_PERMISSION_DENIED {
			t.Fatalf("got %v, want PERMISSION_DENIED", herr.Code)
		}
	})
}

// The command name and the decoded argument fields are asserted, so a typo or
// a swapped Args message cannot leave the suite green while sending nonsense.
func TestCommandNamesAndArgumentsAreExact(t *testing.T) {
	h := sdktest.New(t)
	h.Run(func() {
		if _, err := sdk.MetaSet("mk", "mv"); err != nil {
			t.Fatal(err)
		}
		if _, err := sdk.CacheSet("ck", "cv"); err != nil {
			t.Fatal(err)
		}
	})

	seen := map[string]bool{}
	for _, c := range h.Calls() {
		seen[c.Command] = true
		switch c.Command {
		case "env.meta_set":
			var a pbv2.MetaSetArgs
			if err := proto.Unmarshal([]byte(c.Args), &a); err != nil {
				t.Fatalf("env.meta_set args do not decode as MetaSetArgs: %v", err)
			}
			if a.Key != "mk" || a.Value != "mv" {
				t.Fatalf("env.meta_set args = %+v, want key=mk value=mv", &a)
			}
		case "env.cache_set":
			var a pbv2.CacheSetArgs
			if err := proto.Unmarshal([]byte(c.Args), &a); err != nil {
				t.Fatalf("env.cache_set args do not decode as CacheSetArgs: %v", err)
			}
			if a.Key != "ck" || a.Value != "cv" {
				t.Fatalf("env.cache_set args = %+v, want key=ck value=cv", &a)
			}
		}
	}
	for _, cmd := range []string{"env.meta_set", "env.cache_set"} {
		if !seen[cmd] {
			t.Errorf("%s was never called", cmd)
		}
	}
}

// meta and cache are separate stores. Sharing one map in the harness would let
// a plugin's test pass while the real host kept them apart.
func TestMetaAndCacheAreSeparateStores(t *testing.T) {
	sdktest.New(t).Run(func() {
		if _, err := sdk.MetaSet("same", "from-meta"); err != nil {
			t.Fatal(err)
		}
		if _, herr, _ := sdk.CacheGet("same"); herr == nil {
			t.Fatal("a meta write was visible through CacheGet")
		}
		if _, err := sdk.CacheSet("same", "from-cache"); err != nil {
			t.Fatal(err)
		}
		got, _, _ := sdk.MetaGet("same")
		if got != "from-meta" {
			t.Fatalf("a cache write overwrote meta: MetaGet = %q", got)
		}
	})
}
