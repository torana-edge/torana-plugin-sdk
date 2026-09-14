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
		_, found, err := sdk.StateGet("absent")
		if err != nil || found {
			t.Fatalf("a missing state key reported found=%v err=%v", found, err)
		}

		if err := sdk.StateSet("empty", ""); err != nil {
			t.Fatalf("set empty: err=%v", err)
		}
		v, found, err := sdk.StateGet("empty")
		if err != nil || !found {
			t.Fatalf("get empty: err=%v found=%v", err, found)
		}
		if v != "" {
			t.Fatalf("got %q, want empty", v)
		}
	})
}

// The old state helper deleted a key by setting it to "", which made storing an
// empty value impossible and contradicted meta and cache. The two are separate
// operations and must stay so.
func TestStateSetEmptyDoesNotDelete(t *testing.T) {
	sdktest.New(t).Run(func() {
		if err := sdk.StateSet("k", ""); err != nil {
			t.Fatal(err)
		}
		if _, found, _ := sdk.StateGet("k"); !found {
			t.Fatal("StateSet(k, \"\") deleted the key; empty is a value, not a delete")
		}
		if err := sdk.StateDelete("k"); err != nil {
			t.Fatal(err)
		}
		if _, found, _ := sdk.StateGet("k"); found {
			t.Fatal("StateDelete did not remove the key")
		}
	})
}

// Deleting an absent key succeeds: the caller wants it gone, and reporting
// NOT_FOUND would make every cleanup path branch on something it ignores.
func TestStateDeleteIsIdempotent(t *testing.T) {
	sdktest.New(t).Run(func() {
		if err := sdk.StateDelete("never-existed"); err != nil {
			t.Fatalf("deleting an absent key failed: err=%v", err)
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
		_, _, err := sdk.StateGet("k")
		if err == nil {
			t.Fatal("an unconfigured store reported success")
		}

		// Assert BOTH channels. An earlier version of this test checked only
		// err, so it passed when the write was refused — the exact
		// false-success it claims to prevent.
		if setErr := sdk.StateSet("k", "v"); setErr == nil {
			t.Fatal("a write to an unconfigured store reported success")
		}
	})

	// And prove nothing was stored: a refused write that silently mutated
	// would make the refusal cosmetic.
	h.StateConfigured = true
	h.Run(func() {
		if _, found, _ := sdk.StateGet("k"); found {
			t.Fatal("a refused write mutated the store")
		}
	})
}

// StateGetJSON must distinguish absence from a stored empty document. The old
// untyped reply could not: it decided by whether the raw value was "".
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
			if err := sdk.StateSet(k, "v"); err != nil {
				t.Fatal(err)
			}
		}
		keys, err := sdk.StateKeys()
		if err != nil {
			t.Fatalf("err=%v", err)
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
		if got, err := sdk.PluginConfig(); err != nil || got != `{"mode":"strict"}` {
			t.Fatalf("PluginConfig = %q, err=%v", got, err)
		}
	})
}

// A denied config falls back to "{}" rather than returning the refusal AS the
// config. v1 returned the denial envelope, so a plugin parsed an object with
// none of its fields and silently ran on defaults — the failure this fallback
// has to be careful not to reintroduce in a new form.
func TestPluginConfigRefusalIsObservable(t *testing.T) {
	h := sdktest.New(t)
	h.DenyPermission("env.plugin_config")
	h.Run(func() {
		if got, err := sdk.PluginConfig(); err == nil || got != "" {
			t.Fatalf("PluginConfig = %q, err=%v; want refusal", got, err)
		}
	})
}

func TestPluginConfigRejectsInvalidJSONShapesAndDuplicates(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		ok   bool
	}{
		{name: "valid nested", raw: `{"mode":"strict","limits":{"max":3}}`, ok: true},
		{name: "duplicate top level", raw: `{"mode":"strict","mode":"loose"}`},
		{name: "duplicate nested", raw: `{"limits":{"max":3,"max":4}}`},
		{name: "escaped equal keys", raw: `{"mo\u0064e":"strict","mode":"loose"}`},
		{name: "array", raw: `[]`},
		{name: "null", raw: `null`},
		{name: "trailing", raw: `{"mode":"strict"} {}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := sdktest.New(t)
			h.SetConfig(tc.raw)
			h.Run(func() {
				got, err := sdk.PluginConfig()
				if tc.ok {
					if err != nil || got != tc.raw {
						t.Fatalf("PluginConfig = %q, err=%v", got, err)
					}
				} else if err == nil || got != "" {
					t.Fatalf("PluginConfig = %q, err=%v; want strict rejection", got, err)
				}
			})
		})
	}
}

func TestOriginalsAbsentReportNotOK(t *testing.T) {
	sdktest.New(t).Run(func() {
		if _, ok, _ := sdk.OriginalRequest(); ok {
			t.Fatal("an uncaptured original request reported ok")
		}
		if _, ok, _ := sdk.OriginalResponse(); ok {
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
		req, ok, err := sdk.OriginalRequest()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatal("a captured all-default request reported absent")
		}
		if req == nil {
			t.Fatal("ok=true with a nil request")
		}
		body, ok, err := sdk.OriginalResponse()
		if err != nil {
			t.Fatal(err)
		}
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
		req, ok, _ := sdk.OriginalRequest()
		if !ok || req.Model != "claude-opus-5" {
			t.Fatalf("request round trip: ok=%v req=%+v", ok, req)
		}
		body, ok, _ := sdk.OriginalResponse()
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
		if _, ok, err := sdk.OriginalRequest(); ok || err == nil || !strings.Contains(err.Error(), "decode original request") {
			t.Fatalf("malformed original request: ok=%v err=%v", ok, err)
		}
	})
}

func TestOriginalsPreserveClassifiedRefusals(t *testing.T) {
	for _, command := range []string{"env.original_request", "env.original_response"} {
		t.Run(command, func(t *testing.T) {
			h := sdktest.New(t)
			h.StubHostCall(command, func(string) (string, error) {
				return sdktest.HostResultError(pbv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "denied"), nil
			})
			h.Run(func() {
				var err error
				if command == "env.original_request" {
					_, _, err = sdk.OriginalRequest()
				} else {
					_, _, err = sdk.OriginalResponse()
				}
				var refusal *sdk.HostCallRefusalError
				if !errors.As(err, &refusal) || refusal.Code != pbv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED {
					t.Fatalf("refusal = %v", err)
				}
			})
		})
	}
}

func TestOriginalRequestRejectsUnknownWireFields(t *testing.T) {
	h := sdktest.New(t)
	h.StubHostCall("env.original_request", func(string) (string, error) {
		return sdktest.HostResultValue([]byte{0xa0, 0x06, 0x01}), nil
	})
	h.Run(func() {
		if _, ok, err := sdk.OriginalRequest(); ok || err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("closed decode: ok=%v err=%v", ok, err)
		}
	})
}

func TestStateScanRequiresConfiguredStore(t *testing.T) {
	h := sdktest.New(t)
	h.StateConfigured = false
	h.Run(func() {
		_, err := sdk.StateScan("", "", 1)
		var refusal *sdk.HostCallRefusalError
		if !errors.As(err, &refusal) || refusal.Code != pbv1.ErrorCode_ERROR_CODE_NOT_CONFIGURED {
			t.Fatalf("StateScan refusal = %v", err)
		}
	})
}

// A key stored with an empty value is PRESENT. Reporting it as absent would
// contradict the state contract and StateGetJSON's own documentation; empty
// bytes are not valid JSON, so the truthful answer is a decode error.
func TestStateGetJSONOnAStoredEmptyValue(t *testing.T) {
	sdktest.New(t).Run(func() {
		if err := sdk.StateSet("raw-empty", ""); err != nil {
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
		if err := sdk.StateSet("", "v"); err == nil {
			t.Error("StateSet(\"\", …) was accepted")
		}
		if err := sdk.StateDelete(""); err == nil {
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

func TestPluginConfigRetainsFailureClasses(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		transport   error
		wantCode    pbv1.ErrorCode
		wantErr     bool
		want        string
	}{
		{name: "configured", reply: sdktest.HostResultValue([]byte(`{"mode":"strict"}`)), want: `{"mode":"strict"}`},
		{name: "unset", reply: sdktest.HostResultValue(nil), want: "{}"},
		{name: "denied", reply: sdktest.HostResultError(pbv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "denied"), wantCode: pbv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED},
		{name: "malformed", reply: "not protobuf", wantErr: true},
		{name: "transport", transport: errors.New("transport failed"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := sdktest.New(t)
			h.StubHostCall("env.plugin_config", func(string) (string, error) { return tc.reply, tc.transport })
			h.Run(func() {
				got, err := sdk.PluginConfig()
				if got != tc.want || (err != nil) != (tc.wantErr || tc.wantCode != 0) {
					t.Fatalf("got %q, %v", got, err)
				}
				if tc.transport != nil && !errors.Is(err, tc.transport) {
					t.Fatalf("transport identity lost: %v", err)
				}
			})
		})
	}
}
