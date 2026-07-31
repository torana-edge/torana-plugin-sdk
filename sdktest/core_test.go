package sdktest_test

import (
	"strings"
	"testing"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
	"github.com/torana-edge/torana-plugin-sdk/sdktest"
)

// State, clock, config and originals were the LAST core helpers still speaking
// the unframed string path. Nothing tested them through a real harness, which
// is how the stale "only env.state_* remain" comment survived — the coverage
// that would have contradicted it did not exist.

func TestStateAbsenceIsNotEmptiness(t *testing.T) {
	sdktest.New(t).Run(func() {
		_, herr, err := sdk.StateGet("absent")
		if err != nil {
			t.Fatalf("transport error: %v", err)
		}
		if !sdk.IsNotFound(herr) {
			t.Fatalf("a missing state key reported %v, want NOT_FOUND", herr)
		}

		if herr, err := sdk.StateSet("empty", ""); err != nil || herr != nil {
			t.Fatalf("set empty: err=%v herr=%v", err, herr)
		}
		v, herr, err := sdk.StateGet("empty")
		if err != nil || herr != nil {
			t.Fatalf("get empty: err=%v herr=%v", err, herr)
		}
		if v != "" {
			t.Fatalf("got %q, want empty", v)
		}
	})
}

// v1 deleted a key by setting it to "", which made storing an empty value
// impossible and contradicted meta and cache. The two are now separate
// operations and must stay so.
func TestStateSetEmptyDoesNotDelete(t *testing.T) {
	sdktest.New(t).Run(func() {
		if _, err := sdk.StateSet("k", ""); err != nil {
			t.Fatal(err)
		}
		if _, herr, _ := sdk.StateGet("k"); sdk.IsNotFound(herr) {
			t.Fatal("StateSet(k, \"\") deleted the key; empty is a value, not a delete")
		}
		if _, err := sdk.StateDelete("k"); err != nil {
			t.Fatal(err)
		}
		if _, herr, _ := sdk.StateGet("k"); !sdk.IsNotFound(herr) {
			t.Fatal("StateDelete did not remove the key")
		}
	})
}

// Deleting an absent key succeeds: the caller wants it gone, and reporting
// NOT_FOUND would make every cleanup path branch on something it ignores.
func TestStateDeleteIsIdempotent(t *testing.T) {
	sdktest.New(t).Run(func() {
		if herr, err := sdk.StateDelete("never-existed"); err != nil || herr != nil {
			t.Fatalf("deleting an absent key failed: err=%v herr=%v", err, herr)
		}
	})
}

// An unconfigured store is NOT_CONFIGURED, not absence. A plugin that reads
// them as the same thing writes state into a store that is not there and
// believes it succeeded.
func TestUnconfiguredStateIsDistinctFromAbsence(t *testing.T) {
	h := sdktest.New(t)
	h.StateConfigured = false
	h.Run(func() {
		_, herr, err := sdk.StateGet("k")
		if err != nil {
			t.Fatalf("transport error: %v", err)
		}
		if herr == nil || herr.Code != pbv2.ErrorCode_ERROR_CODE_NOT_CONFIGURED {
			t.Fatalf("got %v, want NOT_CONFIGURED", herr)
		}
		if sdk.IsNotFound(herr) {
			t.Fatal("an unconfigured store was reported as a missing key")
		}
		if _, err := sdk.StateSet("k", "v"); err != nil {
			t.Fatalf("transport error: %v", err)
		}
	})
}

// StateGetJSON must distinguish absence from a stored empty document. v1 could
// not: it decided by whether the raw value was "".
func TestStateGetJSONDistinguishesAbsenceFromEmptyDocument(t *testing.T) {
	sdktest.New(t).Run(func() {
		var v map[string]any
		found, err := sdk.StateGetJSON("absent", &v)
		if err != nil {
			t.Fatalf("absent key errored: %v", err)
		}
		if found {
			t.Fatal("absent key reported found")
		}

		if err := sdk.StateSetJSON("present", map[string]any{}); err != nil {
			t.Fatal(err)
		}
		found, err = sdk.StateGetJSON("present", &v)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			t.Fatal("a stored empty JSON document was reported as absent")
		}
	})
}

// A refusal that is not absence must reach the caller. A plugin treating a
// denied capability as "not stored yet" quietly rewrites state it could not
// read.
func TestStateGetJSONSurfacesRefusals(t *testing.T) {
	h := sdktest.New(t)
	h.DenyPermission("env.state_get")
	h.Run(func() {
		var v map[string]any
		found, err := sdk.StateGetJSON("k", &v)
		if err == nil {
			t.Fatal("a permission denial was swallowed")
		}
		if found {
			t.Fatal("a denied read reported found")
		}
		if !strings.Contains(err.Error(), "permission_denied") {
			t.Fatalf("error does not name the reason: %v", err)
		}
	})
}

func TestStateKeysReadsFramedValues(t *testing.T) {
	sdktest.New(t).Run(func() {
		for _, k := range []string{"b", "a"} {
			if _, err := sdk.StateSet(k, "v"); err != nil {
				t.Fatal(err)
			}
		}
		keys, herr, err := sdk.StateKeys()
		if err != nil || herr != nil {
			t.Fatalf("err=%v herr=%v", err, herr)
		}
		if len(keys) != 2 || keys[0] != "a" || keys[1] != "b" {
			t.Fatalf("keys = %v, want [a b] sorted", keys)
		}
	})
}

func TestNowReadsAFramedValue(t *testing.T) {
	h := sdktest.New(t)
	h.SetNow(1234567)
	h.Run(func() {
		got, err := sdk.Now()
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if got != 1234567 {
			t.Fatalf("Now = %d, want 1234567", got)
		}
	})
}

// A denied clock must be an error naming the reason, not a zero timestamp — a
// plugin comparing against 0 would think every deadline had passed.
func TestNowRefusalIsAnErrorNotZero(t *testing.T) {
	h := sdktest.New(t)
	h.DenyPermission("env.now")
	h.Run(func() {
		got, err := sdk.Now()
		if err == nil {
			t.Fatal("a denied clock returned success")
		}
		if got != 0 {
			t.Fatalf("a failed clock returned %d", got)
		}
		if !strings.Contains(err.Error(), "permission_denied") {
			t.Fatalf("error does not name the reason: %v", err)
		}
	})
}

func TestPluginConfigReadsAFramedValue(t *testing.T) {
	h := sdktest.New(t)
	h.SetConfig(`{"mode":"strict"}`)
	h.Run(func() {
		if got := sdk.PluginConfig(); got != `{"mode":"strict"}` {
			t.Fatalf("PluginConfig = %q", got)
		}
	})
}

// A denied config falls back to "{}" rather than returning the refusal AS the
// config. v1 returned the denial envelope, so a plugin parsed an object with
// none of its fields and silently ran on defaults — the failure this fallback
// has to be careful not to reintroduce in a new form.
func TestPluginConfigRefusalFallsBackToEmptyObject(t *testing.T) {
	h := sdktest.New(t)
	h.DenyPermission("env.plugin_config")
	h.Run(func() {
		if got := sdk.PluginConfig(); got != "{}" {
			t.Fatalf("PluginConfig = %q, want {} — a refusal must not become the config", got)
		}
	})
}

func TestOriginalsReadFramedValues(t *testing.T) {
	h := sdktest.New(t)
	h.Run(func() {
		if _, ok := sdk.OriginalRequest(); ok {
			t.Fatal("an uncaptured original request reported ok")
		}
		if _, ok := sdk.OriginalResponse(); ok {
			t.Fatal("an uncaptured original response reported ok")
		}
	})
}
