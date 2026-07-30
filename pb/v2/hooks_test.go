package v2_test

import (
	"testing"

	v2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

func TestHookBitmapBitsMatchEnumValues(t *testing.T) {
	// Bit N ↔ Hook value N; bit 0 unused.
	cases := []struct {
		h    v2.Hook
		bit  uint32
		name string
	}{
		{v2.Hook_HOOK_BEFORE_REQUEST, 1 << 1, "before_request"},
		{v2.Hook_HOOK_AFTER_RESPONSE, 1 << 2, "after_response"},
		{v2.Hook_HOOK_ON_STREAM_CHUNK, 1 << 3, "stream"},
		{v2.Hook_HOOK_ON_HTTP_REQUEST, 1 << 4, "http"},
		{v2.Hook_HOOK_ON_TICK, 1 << 5, "tick"},
	}
	for _, tc := range cases {
		if got := uint32(tc.h.Bit()); got != tc.bit {
			t.Errorf("%s: Bit() = %#x, want %#x", tc.name, got, tc.bit)
		}
		if v2.Hook_HOOK_UNSPECIFIED.Bit() != 0 {
			t.Fatal("HOOK_UNSPECIFIED must not set a bit")
		}
	}

	all := v2.BitmapOf(
		v2.Hook_HOOK_BEFORE_REQUEST,
		v2.Hook_HOOK_AFTER_RESPONSE,
		v2.Hook_HOOK_ON_STREAM_CHUNK,
		v2.Hook_HOOK_ON_HTTP_REQUEST,
		v2.Hook_HOOK_ON_TICK,
	)
	want := uint32((1 << 1) | (1 << 2) | (1 << 3) | (1 << 4) | (1 << 5))
	if uint32(all) != want {
		t.Fatalf("BitmapOf all hooks = %#x, want %#x", uint32(all), want)
	}
}

func TestValidateManifestHooksCatchesMissingRegistration(t *testing.T) {
	// Declares five, registers one — the load-time failure single run_hook
	// would otherwise defer until customer traffic.
	guest := v2.BitmapOf(v2.Hook_HOOK_BEFORE_REQUEST)
	declared := []v2.Hook{
		v2.Hook_HOOK_BEFORE_REQUEST,
		v2.Hook_HOOK_AFTER_RESPONSE,
		v2.Hook_HOOK_ON_STREAM_CHUNK,
		v2.Hook_HOOK_ON_HTTP_REQUEST,
		v2.Hook_HOOK_ON_TICK,
	}
	err := v2.ValidateManifestHooks(guest, declared)
	if err == nil {
		t.Fatal("a guest missing four declared hooks must fail load-time validation")
	}
	missing := guest.Missing(declared...)
	if len(missing) != 4 {
		t.Fatalf("Missing = %v, want four hooks", missing)
	}
	if !guest.Has(v2.Hook_HOOK_BEFORE_REQUEST) {
		t.Fatal("the one registered hook must still be reported present")
	}

	if err := v2.ValidateManifestHooks(v2.BitmapOf(declared...), declared); err != nil {
		t.Fatalf("full agreement must pass: %v", err)
	}
}

func TestTickRequestIdIsNotRequestScoped(t *testing.T) {
	for _, h := range []v2.Hook{
		v2.Hook_HOOK_BEFORE_REQUEST,
		v2.Hook_HOOK_AFTER_RESPONSE,
		v2.Hook_HOOK_ON_STREAM_CHUNK,
		v2.Hook_HOOK_ON_HTTP_REQUEST,
	} {
		if !h.RequestScoped() {
			t.Errorf("%v must be request-scoped", h)
		}
	}
	if v2.Hook_HOOK_ON_TICK.RequestScoped() {
		t.Fatal("ticks fire with no request in flight: request_id is a synthetic " +
			"execution-scope id, not a caller request")
	}
	if v2.Hook_HOOK_UNSPECIFIED.RequestScoped() {
		t.Fatal("HOOK_UNSPECIFIED is not request-scoped")
	}

	// A tick HookInput may carry a non-zero request_id (the synthetic scope).
	// That must not flip RequestScoped — the hook kind decides, not the value.
	in := &v2.HookInput{
		RequestId: 99,
		Payload: &v2.HookInput_TickRequest{TickRequest: &v2.TickRequest{TickId: 1}},
	}
	if in.HookOf().RequestScoped() {
		t.Fatal("a tick envelope with a synthetic scope id is still not request-scoped")
	}
	if err := in.Validate(); err != nil {
		t.Fatalf("well-formed tick input rejected: %v", err)
	}
}
