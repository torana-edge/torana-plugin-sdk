package sdktest_test

import (
	"errors"
	"strings"
	"testing"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
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
		if herr == nil || herr.Code != pbv1.ErrorCode_ERROR_CODE_NOT_CONFIGURED {
			t.Fatalf("got %v, want NOT_CONFIGURED", herr)
		}
		if sdk.IsNotFound(herr) {
			t.Fatal("an unconfigured store was reported as a missing key")
		}

		// Assert BOTH channels. An earlier version of this test checked only
		// err, so it passed when the write was refused — the exact
		// false-success it claims to prevent.
		setHerr, setErr := sdk.StateSet("k", "v")
		if setErr != nil {
			t.Fatalf("transport error: %v", setErr)
		}
		if setHerr == nil {
			t.Fatal("a write to an unconfigured store reported success")
		}
		if setHerr.Code != pbv1.ErrorCode_ERROR_CODE_NOT_CONFIGURED {
			t.Fatalf("write refused with %v, want NOT_CONFIGURED", setHerr.Code)
		}
	})

	// And prove nothing was stored: a refused write that silently mutated
	// would make the refusal cosmetic.
	h.StateConfigured = true
	h.Run(func() {
		if _, herr, _ := sdk.StateGet("k"); !sdk.IsNotFound(herr) {
			t.Fatal("a refused write mutated the store")
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

func TestOriginalsAbsentReportNotOK(t *testing.T) {
	sdktest.New(t).Run(func() {
		if _, ok := sdk.OriginalRequest(); ok {
			t.Fatal("an uncaptured original request reported ok")
		}
		if _, ok := sdk.OriginalResponse(); ok {
			t.Fatal("an uncaptured original response reported ok")
		}
	})
}

// A captured but EMPTY original is present, not absent.
//
// An all-default ChatRequest marshals to zero bytes and unmarshals cleanly, and
// an upstream body can legitimately be empty. Deciding presence by length would
// report a real capture as missing, which is the absence-versus-emptiness
// confusion the envelope exists to prevent.
func TestCapturedEmptyOriginalsArePresent(t *testing.T) {
	h := sdktest.New(t)
	h.SetOriginalRequest(&pbv1.ChatRequest{})
	h.SetOriginalResponse(nil)
	h.Run(func() {
		req, ok := sdk.OriginalRequest()
		if !ok {
			t.Fatal("a captured all-default request reported absent")
		}
		if req == nil {
			t.Fatal("ok=true with a nil request")
		}
		body, ok := sdk.OriginalResponse()
		if !ok {
			t.Fatal("a captured empty response body reported absent")
		}
		if len(body) != 0 {
			t.Fatalf("body = %q, want empty", body)
		}
	})
}

func TestNonEmptyOriginalsRoundTrip(t *testing.T) {
	h := sdktest.New(t)
	h.SetOriginalRequest(&pbv1.ChatRequest{Model: "claude-opus-5"})
	h.SetOriginalResponse([]byte("pristine-upstream"))
	h.Run(func() {
		req, ok := sdk.OriginalRequest()
		if !ok || req.Model != "claude-opus-5" {
			t.Fatalf("request round trip: ok=%v req=%+v", ok, req)
		}
		body, ok := sdk.OriginalResponse()
		if !ok || string(body) != "pristine-upstream" {
			t.Fatalf("response round trip: ok=%v body=%q", ok, body)
		}
	})
}

// A value that is not a ChatRequest must report ok=false rather than a
// half-decoded request.
func TestMalformedOriginalRequestReportsNotOK(t *testing.T) {
	h := sdktest.New(t)
	h.StubHostCall("env.original_request", func(string) (string, error) {
		return sdktest.HostResultValue([]byte{0xff, 0xff, 0xff, 0xff}), nil
	})
	h.Run(func() {
		if _, ok := sdk.OriginalRequest(); ok {
			t.Fatal("a malformed original request decoded as ok")
		}
	})
}

// A key stored with an empty value is PRESENT. Reporting it as absent would
// contradict the state contract and StateGetJSON's own documentation; empty
// bytes are not valid JSON, so the truthful answer is a decode error.
func TestStateGetJSONOnAStoredEmptyValue(t *testing.T) {
	sdktest.New(t).Run(func() {
		if _, err := sdk.StateSet("raw-empty", ""); err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		found, err := sdk.StateGetJSON("raw-empty", &v)
		if found {
			t.Fatal("an empty stored value decoded as a document")
		}
		if err == nil {
			t.Fatal("a stored empty value was reported as absent; " +
				"it is present and simply not JSON")
		}
	})
}

// The sentinel is documented for the JSON convenience helpers, so errors.Is
// must work on both the read and write paths.
func TestStateJSONHelpersWrapErrStateUnavailable(t *testing.T) {
	h := sdktest.New(t)
	h.StateConfigured = false
	h.Run(func() {
		var v map[string]any
		if _, err := sdk.StateGetJSON("k", &v); !errors.Is(err, sdk.ErrStateUnavailable) {
			t.Fatalf("StateGetJSON: errors.Is(ErrStateUnavailable) is false: %v", err)
		}
		if err := sdk.StateSetJSON("k", map[string]any{}); !errors.Is(err, sdk.ErrStateUnavailable) {
			t.Fatalf("StateSetJSON: errors.Is(ErrStateUnavailable) is false: %v", err)
		}
	})
}

// An empty key must fail locally, before costing a boundary crossing.
func TestStateHelpersRejectAnEmptyKeyLocally(t *testing.T) {
	h := sdktest.New(t)
	h.Run(func() {
		if _, _, err := sdk.StateGet(""); err == nil {
			t.Error("StateGet(\"\") was accepted")
		}
		if _, err := sdk.StateSet("", "v"); err == nil {
			t.Error("StateSet(\"\", …) was accepted")
		}
		if _, err := sdk.StateDelete(""); err == nil {
			t.Error("StateDelete(\"\") was accepted")
		}
	})
	for _, c := range h.Calls() {
		t.Fatalf("a rejected state call reached the host: %+v", c)
	}
}

// Deletion is authorised by env.state_set, not a fourth capability. The
// constants exist so the host and the linter special-map it rather than
// deriving a permission that does not exist.
func TestStateDeleteUsesTheStateSetPermission(t *testing.T) {
	if pbv1.StateDeleteCommand != "env.state_delete" {
		t.Fatalf("command = %q", pbv1.StateDeleteCommand)
	}
	if pbv1.StateDeletePermission != "env.state_set" {
		t.Fatalf("permission = %q, want env.state_set", pbv1.StateDeletePermission)
	}
	if sdk.IsPermission(pbv1.StateDeleteCommand) {
		t.Fatal("env.state_delete is in the operator capability vocabulary; " +
			"it must not be — it is a command governed by env.state_set")
	}
	if !sdk.IsPermission(pbv1.StateDeletePermission) {
		t.Fatal("env.state_set is not a known permission")
	}
}
