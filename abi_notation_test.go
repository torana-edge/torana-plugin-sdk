package plugin_sdk

import (
	"os"
	"strings"
	"testing"
)

// The low-level guide writes out the ABI-v1 hook signature for authors who are
// implementing another SDK. Keep it aligned with the compiled Go and Rust
// exports, which use unsigned WASM32 pointers and a packed unsigned result.
//
// That is not cosmetic. A pointer above 2 GiB is reachable inside a 4 GiB wasm
// memory and has its high bit set; interpreted as signed it is negative, and
// packing it into the return value sign-extends and corrupts the high half. A
// guest written to the signed reading works until a plugin allocates enough
// memory, then fails in a way that looks like memory corruption rather than a
// spec misreading.
//
// Checked rather than trusted because signed/unsigned drift has previously
// corrupted the high half of packed return values above 2 GiB.
func TestV1HookSignatureNotationIsUnsigned(t *testing.T) {
	const path = "docs/WASM_PLUGIN_GUIDE.md"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(raw), "\n", " ")
	if !strings.Contains(text, "run_hook(ptr: u32, size: u32) -> u64") {
		t.Fatalf("%s does not contain the canonical unsigned ABI-v1 hook signature", path)
	}
	for _, signed := range []string{
		"run_hook(ptr: i32",
		"size: i32) -> u64",
		"size: u32) -> i64",
	} {
		if strings.Contains(text, signed) {
			t.Fatalf("%s contains signed ABI-v1 hook notation %q", path, signed)
		}
	}
}
