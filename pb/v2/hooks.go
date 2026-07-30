package v2

import (
	"fmt"
	"strings"
)

// MaxHookBit is the highest Hook enum value that can appear in a
// supported_hooks() u32 bitmap. Bit 0 is reserved (HOOK_UNSPECIFIED); bits
// 1..31 are usable. A 32nd hook would need a wider export.
const MaxHookBit = 31

// HookBitmap is the value returned by the supported_hooks() WASM export.
//
// Bit N is set when Hook value N is implemented. Bit 0 is unused because
// HOOK_UNSPECIFIED is not a hook. See the Hook enum comment in torana.proto.
type HookBitmap uint32

// Bit returns the single-hook mask for h, or 0 if h is not a named hook in
// 1..MaxHookBit. Derived from the generated enum map so a new Hook value in
// range automatically participates; values outside the u32 bit capacity or
// unknown numbers return 0.
func (h Hook) Bit() HookBitmap {
	n := int32(h)
	if n < 1 || n > MaxHookBit {
		return 0
	}
	if _, ok := Hook_name[n]; !ok {
		return 0
	}
	return HookBitmap(1) << uint(n)
}

// KnownHooksMask is the union of Bit() for every named Hook in 1..MaxHookBit.
// Bits outside this mask on a guest bitmap are malformed.
func KnownHooksMask() HookBitmap {
	var b HookBitmap
	for n := range Hook_name {
		if n >= 1 && n <= MaxHookBit {
			b |= HookBitmap(1) << uint(n)
		}
	}
	return b
}

// BitmapOf returns the supported_hooks bitmap for the given hooks.
// Values with Bit() == 0 are skipped; use ExpectedBitmap when validating a
// manifest declaration list that must refuse unknowns.
func BitmapOf(hooks ...Hook) HookBitmap {
	var b HookBitmap
	for _, h := range hooks {
		b |= h.Bit()
	}
	return b
}

// Has reports whether the bitmap claims support for h.
func (b HookBitmap) Has(h Hook) bool {
	bit := h.Bit()
	return bit != 0 && b&bit == bit
}

// ExpectedBitmap builds the bitmap a guest must return for declared.
// Refuses HOOK_UNSPECIFIED, unknown enum values, values outside u32 capacity,
// and duplicates.
func ExpectedBitmap(declared []Hook) (HookBitmap, error) {
	var b HookBitmap
	seen := make(map[Hook]struct{}, len(declared))
	for _, h := range declared {
		if h == Hook_HOOK_UNSPECIFIED {
			return 0, fmt.Errorf("manifest declares HOOK_UNSPECIFIED, which is not a hook")
		}
		n := int32(h)
		if n < 1 || n > MaxHookBit {
			return 0, fmt.Errorf("manifest declares hook %v (%d), outside the u32 bit capacity 1..%d",
				h, n, MaxHookBit)
		}
		bit := h.Bit()
		if bit == 0 {
			return 0, fmt.Errorf("manifest declares unknown hook %v (%d)", h, n)
		}
		if _, dup := seen[h]; dup {
			return 0, fmt.Errorf("manifest declares hook %v more than once", h)
		}
		seen[h] = struct{}{}
		b |= bit
	}
	return b, nil
}

// ValidateManifestHooks reports whether the guest's supported_hooks bitmap
// exactly matches the hooks declared in the manifest.
//
// Missing bits mean a declared hook has no handler. Unexpected bits mean the
// guest registered a hook the manifest does not declare. Reserved bit 0 and
// bits outside KnownHooksMask are always refused.
func ValidateManifestHooks(bitmap HookBitmap, declared []Hook) error {
	expected, err := ExpectedBitmap(declared)
	if err != nil {
		return err
	}

	known := KnownHooksMask()
	if bad := bitmap &^ known; bad != 0 {
		return fmt.Errorf("guest supported_hooks bitmap %#x has unexpected bits %#x "+
			"(reserved bit 0 or unknown high bits)", uint32(bitmap), uint32(bad))
	}

	missing := expected &^ bitmap
	unexpected := bitmap &^ expected
	if missing == 0 && unexpected == 0 {
		return nil
	}

	var parts []string
	if missing != 0 {
		parts = append(parts, fmt.Sprintf("missing %v", hooksIn(missing)))
	}
	if unexpected != 0 {
		parts = append(parts, fmt.Sprintf("unexpected %v", hooksIn(unexpected)))
	}
	return fmt.Errorf("guest supported_hooks bitmap %#x does not match manifest %#x: %s",
		uint32(bitmap), uint32(expected), strings.Join(parts, "; "))
}

func hooksIn(b HookBitmap) []Hook {
	var out []Hook
	for n := 1; n <= MaxHookBit; n++ {
		h := Hook(n)
		if b.Has(h) {
			out = append(out, h)
		}
	}
	return out
}

// RequestScoped reports whether a caller request is in flight for this hook.
//
// False for ticks: there is no caller. Their request_id is still a valid
// execution-scope id for plugin-private ephemeral metadata (env.meta_*), which
// is discarded when the tick ends — that is why the host allocates a unique
// synthetic id rather than reusing a live request's. Do not use RequestScoped
// as a blanket gate for meta_*; use it for caller-content host calls
// (original_request, original_response, credentials, …). False for
// HOOK_UNSPECIFIED too.
func (h Hook) RequestScoped() bool {
	switch h {
	case Hook_HOOK_BEFORE_REQUEST,
		Hook_HOOK_AFTER_RESPONSE,
		Hook_HOOK_ON_STREAM_CHUNK,
		Hook_HOOK_ON_HTTP_REQUEST:
		return true
	default:
		return false
	}
}
