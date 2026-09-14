//go:build !wasip1

package sdktest_test

import (
	"errors"
	"testing"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	"github.com/torana-edge/torana-plugin-sdk/sdktest"
)

func TestCanonicalStateCASScanAndCacheSemantics(t *testing.T) {
	h := sdktest.New(t).SetNow(1000)
	h.Run(func() {
		if err := sdk.StateSet("a/1", "one"); err != nil {
			t.Fatal(err)
		}
		if err := sdk.StateSet("a/2", "two"); err != nil {
			t.Fatal(err)
		}
		v, found, err := sdk.StateGetVersioned("a/1")
		if err != nil || !found {
			t.Fatalf("versioned get: %v %v", found, err)
		}
		if _, err := sdk.StateCompareAndSet("a/1", "changed", &v.Version); err != nil {
			t.Fatal(err)
		}
		stale, err := sdk.StateCompareAndSet("a/1", "stale", &v.Version)
		if err != nil || stale.Applied {
			t.Fatalf("stale CAS: %+v %v", stale, err)
		}
		r, err := sdk.StateScan("a/", "", 1)
		if err != nil || len(r.Entries) != 1 || r.NextCursor == "" {
			t.Fatalf("scan page: %+v %v", r, err)
		}
		r, err = sdk.StateScan("a/", r.NextCursor, 10)
		if err != nil || len(r.Entries) != 1 {
			t.Fatalf("scan continuation: %+v %v", r, err)
		}
		if err := sdk.CacheSetTTL("k", "v", 10); err != nil {
			t.Fatal(err)
		}
		h.SetNow(1010)
		if _, ok, err := sdk.CacheGet("k"); err != nil || ok {
			t.Fatalf("deadline cache: %v %v", ok, err)
		}
		if err := sdk.SharedCacheSetTTL("s", "v", 10); err != nil {
			t.Fatal(err)
		}
		if err := sdk.SharedCacheDelete("s"); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := sdk.SharedCacheGet("s"); err != nil || ok {
			t.Fatalf("deleted shared cache: %v %v", ok, err)
		}
	})
	if _, ok := h.State("a/1"); !ok {
		t.Fatal("state was unexpectedly reset")
	}
}

func TestCanonicalHarnessResetAndDeniedCalls(t *testing.T) {
	h := sdktest.New(t)
	h.DenyPermission("env.block_request")
	h.Run(func() {
		err := sdk.BlockRequest(400, "test", "reason")
		var refusal *sdk.HostCallRefusalError
		if !errors.As(err, &refusal) {
			t.Fatalf("denied block error = %v", err)
		}
	})
	if len(h.EffectiveBlockCalls()) != 0 || len(h.Calls()) == 0 {
		t.Fatal("denied call was counted as effective")
	}
}

func TestPermissionModeRefusesBeforeStubAndScopesOutcomes(t *testing.T) {
	h := sdktest.New(t).WithPermissions([]string{"env.block_request"})
	h.StubHostCall("env.block_request", func(string) (string, error) { return sdktest.HostResultValue([]byte("ok")), nil })
	called := false
	h.StubHostCall("env.cache_get", func(string) (string, error) { called = true; return sdktest.HostResultValue([]byte("bad")), nil })
	r := h.NewRequest()
	r.Run(func() { _, _, _ = sdk.CacheGet("x") })
	if called {
		t.Fatal("permission refusal invoked stub")
	}
	if len(r.Calls()) != 1 {
		t.Fatal("permission-refused call should be observed")
	}
	r.Run(func() {
		if err := sdk.BlockRequest(400, "x", "y"); err != nil {
			t.Fatal(err)
		}
	})
	if len(r.EffectiveBlockCalls()) != 1 {
		t.Fatal("accepted block missing from request scope")
	}
}
