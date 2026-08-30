package sdktest_test

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"github.com/torana-edge/torana-plugin-sdk/sdktest"
)

// The two public host-call paths must stay disjoint.
//
// If HostCallExtension accepted env.* commands it would be an untyped back door
// to verdicts, metadata, cache and state — every typed contract in v1 could be
// routed around by a plugin that found this function first. The guest-side
// check is not the security boundary (the host gates on the grant), but it is
// what stops an author reaching for it by accident.
func TestExtensionPathRejectsCoreCommands(t *testing.T) {
	sdktest.New(t).Run(func() {
		for _, cmd := range []string{
			"env.meta_set", "env.meta_get", "env.cache_set", "env.state_set",
			"env.block_request", "env.respond_request", "env.meta_append",
		} {
			_, _, err := sdk.HostCallExtension(cmd, []byte(`{}`))
			if err == nil {
				t.Errorf("HostCallExtension(%q) was accepted; it is a core command", cmd)
				continue
			}
			if !strings.Contains(err.Error(), "core host call") {
				t.Errorf("HostCallExtension(%q) failed for the wrong reason: %v", cmd, err)
			}
		}
	})
}

func TestExtensionPathRejectsAnEmptyCommand(t *testing.T) {
	sdktest.New(t).Run(func() {
		if _, _, err := sdk.HostCallExtension("", []byte(`{}`)); err == nil {
			t.Fatal("an empty extension command was accepted")
		}
	})
}

// A rejected command must not reach the host at all.
func TestRejectedExtensionCallNeverCrossesTheBoundary(t *testing.T) {
	h := sdktest.New(t)
	h.Run(func() {
		_, _, _ = sdk.HostCallExtension("env.meta_set", []byte(`{}`))
	})
	for _, c := range h.Calls() {
		if c.Command == "env.meta_set" {
			t.Fatalf("a rejected extension call still reached the host: %+v", c)
		}
	}
}

// An extension command the harness cannot emulate answers with a FRAMED
// classified error, not a decode failure. A plugin's degrade path is then
// exercised by the harness rather than only in production.
func TestUnstubbedExtensionCommandIsFramedNotConfigured(t *testing.T) {
	sdktest.New(t).Run(func() {
		v, herr, err := sdk.HostCallExtension("torana_plugin_counter", []byte(`{"counter":"c","delta":1}`))
		if err != nil {
			t.Fatalf("an unstubbed extension command failed to decode: %v", err)
		}
		if herr == nil {
			t.Fatal("an unstubbed extension command reported success")
		}
		if herr.Code != pbv1.ErrorCode_ERROR_CODE_NOT_CONFIGURED {
			t.Fatalf("got %v, want NOT_CONFIGURED", herr.Code)
		}
		if len(v) != 0 {
			t.Fatalf("a refused call returned a value: %q", v)
		}
	})
}

// A stubbed extension command round-trips its opaque body untouched. The body
// is []byte because JSON is what these commands happen to use today, not an
// ABI rule — nothing in the path may assume it.
func TestStubbedExtensionCommandRoundTripsItsBody(t *testing.T) {
	h := sdktest.New(t)
	h.StubHostCall("torana_plugin_counter", func(args string) (string, error) {
		var got struct {
			Counter string `json:"counter"`
			Delta   int64  `json:"delta"`
		}
		if err := json.Unmarshal([]byte(args), &got); err != nil {
			t.Fatalf("the opaque body did not arrive intact: %v", err)
		}
		if got.Counter != "tier_decisions" || got.Delta != 3 {
			t.Fatalf("body = %+v, want counter=tier_decisions delta=3", got)
		}
		return sdktest.HostResultValue([]byte(`{"accepted":true}`)), nil
	})
	h.Run(func() {
		payload, _ := json.Marshal(map[string]any{"counter": "tier_decisions", "delta": 3})
		v, herr, err := sdk.HostCallExtension("torana_plugin_counter", payload)
		if err != nil || herr != nil {
			t.Fatalf("err=%v herr=%v", err, herr)
		}
		if string(v) != `{"accepted":true}` {
			t.Fatalf("value = %q, want the stubbed payload", v)
		}
	})
}

// Non-JSON bodies must survive too, or the "opaque" claim is false.
func TestExtensionBodyNeedNotBeJSON(t *testing.T) {
	h := sdktest.New(t)
	raw := []byte{0x00, 0xff, 0x10, 0x42}
	h.StubHostCall("torana_db_query", func(args string) (string, error) {
		if args != string(raw) {
			t.Fatalf("binary body was altered: got %q, want %q", args, raw)
		}
		return sdktest.HostResultValue(raw), nil
	})
	h.Run(func() {
		v, herr, err := sdk.HostCallExtension("torana_db_query", raw)
		if err != nil || herr != nil {
			t.Fatalf("err=%v herr=%v", err, herr)
		}
		if string(v) != string(raw) {
			t.Fatalf("binary value was altered: %q", v)
		}
	})
}

// The boundary must reject in BOTH directions, and reject before crossing.
//
// It rejected env.* on the extension path but accepted anything on the core
// path, so one operation had two ways to be invoked and only one was the
// contract.
func TestCorePathRejectsExtensionCommands(t *testing.T) {
	h := sdktest.New(t)
	h.Run(func() {
		for _, cmd := range []string{
			"torana_plugin_counter", "torana_offload_completion", "verify_virtual_key",
		} {
			_, _, err := sdk.HostCall(cmd, &pbv1.MetaGetArgs{Key: "k"})
			if err == nil {
				t.Errorf("HostCall(%q) was accepted; it is a host-feature command", cmd)
				continue
			}
			if !strings.Contains(err.Error(), "HostCallExtension") {
				t.Errorf("HostCall(%q) did not name the alternative: %v", cmd, err)
			}
		}
	})
	for _, c := range h.Calls() {
		if !strings.HasPrefix(c.Command, "env.") {
			t.Fatalf("a rejected core call still reached the host: %+v", c)
		}
	}
}

// The extension set is closed in this SDK version, so an unknown token is a
// typo or an unsupported command. Failing at the call site beats discovering it
// at host dispatch in production.
func TestExtensionPathRejectsUnknownAndMalformedTokens(t *testing.T) {
	h := sdktest.New(t)
	h.Run(func() {
		for _, cmd := range []string{
			"torana_typo",                         // not in the allowlist
			"torana plugin counter",               // whitespace
			"env.host_call.torana_plugin_counter", // the permission, not the token
			"TORANA_PLUGIN_COUNTER",               // wrong case
		} {
			if _, _, err := sdk.HostCallExtension(cmd, []byte(`{}`)); err == nil {
				t.Errorf("HostCallExtension(%q) was accepted", cmd)
			}
		}
	})
	if len(h.Calls()) != 0 {
		t.Fatalf("a rejected extension call reached the host: %+v", h.Calls())
	}
}
