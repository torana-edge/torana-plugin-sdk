package plugin_sdk

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// This test keeps checked-in language inventories honest. The generator is
// intentionally small and deterministic; CI runs it and then this test catches
// any generated artifact that was forgotten in a commit.
func TestCapabilityCatalogGeneratedArtifactsAreCurrent(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "capabilities.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Hooks      []string          `json:"hooks"`
		HookGrants map[string]string `json:"hook_grants"`
		Metadata   []string          `json:"metadata_grants"`
		Writes     []string          `json:"write_permissions"`
		Commands   []CommandSpec     `json:"commands"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"capabilities-reference.md", "capabilities-rust.json"} {
		if b, err := os.ReadFile(filepath.Join(root, name)); err != nil || len(bytes.TrimSpace(b)) == 0 {
			t.Fatalf("missing generated capability artifact %s: %v", name, err)
		}
	}
	if len(c.Commands) == 0 || len(c.Hooks) == 0 || len(c.Writes) == 0 {
		t.Fatal("capability catalog is incomplete")
	}
}
