package plugin_sdk

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The hook signature is written out in two places that both read as normative:
// ABI.md and docs/WASM_PLUGIN_GUIDE.md. They disagreed — ABI.md said i64/i32
// while the guide said u64/u32 in one paragraph and i64/i32 in another, so the
// guide contradicted both the spec and itself.
//
// That is not cosmetic. A pointer above 2 GiB is reachable inside a 4 GiB wasm
// memory and has its high bit set; interpreted as signed it is negative, and
// packing it into the return value sign-extends and corrupts the high half. A
// guest written to the signed reading works until a plugin allocates enough
// memory, then fails in a way that looks like memory corruption rather than a
// spec misreading.
//
// Checked rather than trusted because this exact drift has now been introduced
// twice: once between the two documents, and once between one document and
// itself.
func TestHookSignatureNotationIsConsistent(t *testing.T) {
	signature := regexp.MustCompile(`\((?:request_id|reqID): ([iu])64, ptr: ([iu])32,\s*(?:len|size): ([iu])32\)`)

	var found int
	for _, path := range []string{"ABI.md", "docs/WASM_PLUGIN_GUIDE.md"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, m := range signature.FindAllStringSubmatch(strings.ReplaceAll(string(raw), "\n", " "), -1) {
			found++
			for i, kind := range m[1:] {
				if kind != "u" {
					t.Errorf("%s writes the hook signature with a SIGNED type in position %d: %q\n"+
						"The ABI is unsigned. A pointer above 2GiB read as signed is negative, and "+
						"packing it sign-extends and corrupts the high half of the return value.",
						path, i+1, strings.TrimSpace(m[0]))
				}
			}
		}
	}
	if found < 2 {
		t.Errorf("found %d hook signatures across both documents, want at least 2 — "+
			"the pattern no longer matches, so this check is vacuous", found)
	}
}
