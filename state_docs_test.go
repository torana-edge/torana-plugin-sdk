package plugin_sdk

import (
	"os"
	"strings"
	"testing"
)

func TestStorageSummaryDoesNotPromiseAbsoluteCacheLifetime(t *testing.T) {
	raw, err := os.ReadFile("state.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "State is the only one that survives a restart") {
		t.Fatal("storage summary still makes cache persistence an absolute claim")
	}
	for _, contract := range []string{
		"designed to retain data across restarts",
		"backend- and TTL-dependent",
		"Redis-backed cache may",
		"namespace visibility, not persistence",
	} {
		if !strings.Contains(text, contract) {
			t.Errorf("storage summary does not state %q", contract)
		}
	}
}
