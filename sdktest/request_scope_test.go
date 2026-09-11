package sdktest

import (
	"testing"

	sdk "github.com/torana-edge/torana-plugin-sdk"
)

func TestRequestMetadataIsScopedToExplicitRequest(t *testing.T) {
	h := New(t)
	first := h.NewRequest()
	second := h.NewRequest()
	first.with(func() {
		if herr, err := sdk.MetaSet("key", "first"); err != nil || herr != nil {
			t.Fatalf("MetaSet: err=%v herr=%v", err, herr)
		}
	})
	first.with(func() {
		got, herr, err := sdk.MetaGet("key")
		if err != nil || herr != nil || got != "first" {
			t.Fatalf("same request lost metadata: got=%q err=%v herr=%v", got, err, herr)
		}
	})
	second.with(func() {
		_, herr, err := sdk.MetaGet("key")
		if err != nil || herr == nil || !sdk.IsNotFound(herr) {
			t.Fatalf("metadata leaked into a new request: err=%v herr=%v", err, herr)
		}
	})
}
