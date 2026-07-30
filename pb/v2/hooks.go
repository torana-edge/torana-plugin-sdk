package v2

import "fmt"

// HookBitmap is the value returned by the supported_hooks() WASM export.
//
// Bit N is set when Hook value N is implemented. Bit 0 is unused because
// HOOK_UNSPECIFIED is not a hook. See the Hook enum comment in torana.proto.
type HookBitmap uint32

// Bit returns the single-hook mask for h, or 0 if h is not a real hook.
func (h Hook) Bit() HookBitmap {
	switch h {
	case Hook_HOOK_BEFORE_REQUEST,
		Hook_HOOK_AFTER_RESPONSE,
		Hook_HOOK_ON_STREAM_CHUNK,
		Hook_HOOK_ON_HTTP_REQUEST,
		Hook_HOOK_ON_TICK:
		return HookBitmap(1) << uint(h)
	default:
		return 0
	}
}

// BitmapOf returns the supported_hooks bitmap for the given hooks.
// HOOK_UNSPECIFIED and unknown values are ignored.
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

// Missing reports which of required are absent from b.
func (b HookBitmap) Missing(required ...Hook) []Hook {
	var missing []Hook
	for _, h := range required {
		if h.Bit() == 0 {
			continue
		}
		if !b.Has(h) {
			missing = append(missing, h)
		}
	}
	return missing
}

// RequestScoped reports whether HookInput.request_id names a caller request.
//
// False for ticks: their request_id is a synthetic execution-scope id so host
// scratch stays isolated. Request-scoped host calls remain unavailable on tick
// regardless of the field's value. False for HOOK_UNSPECIFIED too — there is
// no request.
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

// ValidateManifestHooks reports whether every declared hook is present in the
// guest's supported_hooks bitmap. unknown names are refused rather than
// ignored — a typo in the manifest must not pass load.
func ValidateManifestHooks(bitmap HookBitmap, declared []Hook) error {
	missing := bitmap.Missing(declared...)
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("guest supported_hooks bitmap %#x is missing declared hooks %v",
		uint32(bitmap), missing)
}
