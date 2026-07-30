package plugin_sdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// The shipped Go logger is the customer-facing template. A compile-only check
// previously let abi_version:v1 ship beside a v2 run_hook guest.
func TestGoLoggerManifestMatchesV2Surface(t *testing.T) {
	manifestPath := filepath.Join("examples", "go-logger", "plugin.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		ABIVersion string `json:"abi_version"`
		Hooks      []struct {
			Name string `json:"name"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m.ABIVersion != "v2" {
		t.Fatalf("%s abi_version=%q, want v2 (Go guests export run_hook)", manifestPath, m.ABIVersion)
	}
	if len(m.Hooks) == 0 {
		t.Fatal("go-logger must declare at least one hook")
	}
	var declared []pbv2.Hook
	for _, h := range m.Hooks {
		hk, ok := ManifestHookName(h.Name)
		if !ok {
			t.Fatalf("unknown hook %q in %s", h.Name, manifestPath)
		}
		declared = append(declared, hk)
	}
	// Exact bitmap the guest must export once registrations match the manifest.
	want, err := pbv2.ExpectedBitmap(declared)
	if err != nil {
		t.Fatal(err)
	}
	if err := pbv2.ValidateManifestHooks(want, declared); err != nil {
		t.Fatal(err)
	}

	src, err := os.ReadFile(filepath.Join("examples", "go-logger", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if !strings.Contains(body, "torana-plugin-sdk/pb/v2") {
		t.Fatal("go-logger must import pb/v2")
	}
	if strings.Contains(body, "github.com/torana-edge/torana-plugin-sdk/pb\"") {
		t.Fatal("go-logger must not import the v1 pb package")
	}
}

func TestRustLoggerRemainsV1UntilMigrationC(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("examples", "rust-logger", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"abi_version": "v1"`) {
		t.Fatal("rust-logger must stay on abi_version v1 until Migration C")
	}
}
