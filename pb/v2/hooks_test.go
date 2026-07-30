package v2_test

import (
	"strings"
	"testing"

	v2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

func TestHookBitmapBitsMatchDescriptor(t *testing.T) {
	// Enumerate every Hook enum value from the descriptor. Every non-zero
	// value in 1..MaxHookBit must map to 1<<value; bit 0 stays unused; this
	// ABI has only 31 usable hook bits.
	ed := v2.Hook(0).Descriptor()
	if ed == nil {
		t.Fatal("Hook has no enum descriptor")
	}
	var seenNonZero int
	var wantMask v2.HookBitmap
	for i := 0; i < ed.Values().Len(); i++ {
		v := ed.Values().Get(i)
		n := int32(v.Number())
		h := v2.Hook(n)
		switch {
		case n == 0:
			if h.Bit() != 0 {
				t.Fatal("HOOK_UNSPECIFIED must not set a bit")
			}
		case n < 1 || n > v2.MaxHookBit:
			t.Errorf("Hook %s = %d is outside 1..%d; supported_hooks is u32 and "+
				"only has 31 usable bits", v.Name(), n, v2.MaxHookBit)
			if h.Bit() != 0 {
				t.Errorf("Hook %d outside capacity must not set a bit", n)
			}
		default:
			seenNonZero++
			want := v2.HookBitmap(1) << uint(n)
			if got := h.Bit(); got != want {
				t.Errorf("Hook %s (%d): Bit() = %#x, want %#x", v.Name(), n, uint32(got), uint32(want))
			}
			wantMask |= want
		}
	}
	if seenNonZero == 0 {
		t.Fatal("descriptor listed no named hooks")
	}
	if v2.MaxHookBit != 31 {
		t.Fatalf("MaxHookBit = %d, want 31 (u32 has 31 usable bits after reserved 0)", v2.MaxHookBit)
	}
	if got := v2.KnownHooksMask(); got != wantMask {
		t.Fatalf("KnownHooksMask = %#x, want %#x", uint32(got), uint32(wantMask))
	}

	// Unknown and out-of-range numbers must not set bits.
	if v2.Hook(99).Bit() != 0 {
		t.Fatal("unknown Hook(99) must not set a bit")
	}
	if v2.Hook(32).Bit() != 0 {
		t.Fatal("Hook(32) is outside u32 bit capacity")
	}

	// Mutating the generated Hook_name map must not invent a contract hook.
	v2.Hook_name[30] = "HOOK_FAKE"
	t.Cleanup(func() { delete(v2.Hook_name, 30) })
	if v2.Hook(30).Bit() != 0 {
		t.Fatal("Hook.Bit must not honour a mutated Hook_name entry")
	}
	if v2.KnownHooksMask().Has(v2.Hook(30)) {
		t.Fatal("KnownHooksMask must not honour a mutated Hook_name entry")
	}
}

func TestValidateManifestHooksExactAgreement(t *testing.T) {
	before := v2.Hook_HOOK_BEFORE_REQUEST
	after := v2.Hook_HOOK_AFTER_RESPONSE

	t.Run("exact agreement", func(t *testing.T) {
		declared := []v2.Hook{before, after}
		if err := v2.ValidateManifestHooks(v2.BitmapOf(declared...), declared); err != nil {
			t.Fatalf("exact agreement must pass: %v", err)
		}
	})

	t.Run("empty declaration", func(t *testing.T) {
		err := v2.ValidateManifestHooks(0, nil)
		if err == nil || !strings.Contains(err.Error(), "no hooks") {
			t.Fatalf("want empty-declaration error, got %v", err)
		}
	})

	t.Run("zero bitmap with declared hooks", func(t *testing.T) {
		err := v2.ValidateManifestHooks(0, []v2.Hook{before})
		if err == nil || !strings.Contains(err.Error(), "missing") {
			t.Fatalf("want missing-hook error for zero bitmap, got %v", err)
		}
	})

	t.Run("missing hook", func(t *testing.T) {
		err := v2.ValidateManifestHooks(v2.BitmapOf(before), []v2.Hook{before, after})
		if err == nil || !strings.Contains(err.Error(), "missing") {
			t.Fatalf("want missing-hook error, got %v", err)
		}
	})

	t.Run("extra registered hook", func(t *testing.T) {
		err := v2.ValidateManifestHooks(v2.BitmapOf(before, after), []v2.Hook{before})
		if err == nil || !strings.Contains(err.Error(), "unexpected") {
			t.Fatalf("want unexpected-hook error, got %v", err)
		}
	})

	t.Run("unknown declared enum", func(t *testing.T) {
		// 30 is inside u32 bit capacity but not a named Hook today.
		err := v2.ValidateManifestHooks(0, []v2.Hook{v2.Hook(30)})
		if err == nil || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("want unknown-hook error, got %v", err)
		}
	})

	t.Run("HOOK_UNSPECIFIED declared", func(t *testing.T) {
		err := v2.ValidateManifestHooks(0, []v2.Hook{v2.Hook_HOOK_UNSPECIFIED})
		if err == nil || !strings.Contains(err.Error(), "HOOK_UNSPECIFIED") {
			t.Fatalf("want HOOK_UNSPECIFIED error, got %v", err)
		}
	})

	t.Run("duplicate declared", func(t *testing.T) {
		err := v2.ValidateManifestHooks(v2.BitmapOf(before), []v2.Hook{before, before})
		if err == nil || !strings.Contains(err.Error(), "more than once") {
			t.Fatalf("want duplicate error, got %v", err)
		}
	})

	t.Run("reserved bit 0", func(t *testing.T) {
		err := v2.ValidateManifestHooks(v2.HookBitmap(1)|v2.BitmapOf(before), []v2.Hook{before})
		if err == nil || !strings.Contains(err.Error(), "unexpected bits") {
			t.Fatalf("want reserved-bit error, got %v", err)
		}
	})

	t.Run("unknown high bit", func(t *testing.T) {
		// Bit 30 is within u32 but not a named Hook today.
		high := v2.HookBitmap(1) << 30
		err := v2.ValidateManifestHooks(high|v2.BitmapOf(before), []v2.Hook{before})
		if err == nil || !strings.Contains(err.Error(), "unexpected bits") {
			t.Fatalf("want high-bit error, got %v", err)
		}
	})

	t.Run("out of capacity declared", func(t *testing.T) {
		err := v2.ValidateManifestHooks(0, []v2.Hook{v2.Hook(32)})
		if err == nil || !strings.Contains(err.Error(), "outside the u32 bit capacity") {
			t.Fatalf("want capacity error, got %v", err)
		}
	})
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
		t.Fatal("ticks fire with no caller request: request_id is a synthetic " +
			"execution-scope id for plugin-private metadata, not a caller request")
	}
	if v2.Hook_HOOK_UNSPECIFIED.RequestScoped() {
		t.Fatal("HOOK_UNSPECIFIED is not request-scoped")
	}

	// A tick HookInput may carry a non-zero request_id (the synthetic scope for
	// env.meta_*). That must not flip RequestScoped — the hook kind decides.
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
