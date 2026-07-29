package plugin_sdk

import (
	"sort"
	"strings"
	"testing"
)

// Every message role Torana models must have a write grant, and every write
// grant for a role must map back. A role with no grant is a role a plugin can
// rewrite with no capability — which is the hole this vocabulary closes.
func TestEveryMessageRoleHasAWriteGrant(t *testing.T) {
	roles := []string{"user", "assistant", "system", "tool"}

	granted := map[WriteSection]bool{}
	for _, role := range roles {
		section, ok := MessageWriteSection(role)
		if !ok {
			t.Fatalf("role %q has no write grant", role)
		}
		if !IsWritePermission(string(section)) {
			t.Errorf("%q is not in WritePermissions, so a manifest requesting it is rejected", section)
		}
		granted[section] = true
	}

	// And each message-role grant is reachable from some role, so the
	// vocabulary carries nothing an author cannot actually use.
	for _, p := range WritePermissions {
		if !strings.HasPrefix(p, "ir.messages.write.") {
			continue
		}
		if !granted[WriteSection(p)] {
			t.Errorf("%q is offered but no role maps to it", p)
		}
	}
}

// An unmodelled role must not silently become unrestricted.
func TestUnknownRoleHasNoWriteGrant(t *testing.T) {
	for _, role := range []string{"", "developer", "USER", "tool_result"} {
		if _, ok := MessageWriteSection(role); ok {
			t.Errorf("role %q resolved to a grant; an unmodelled role must be treated as "+
				"ungrantable rather than unrestricted", role)
		}
	}
}

// A host validates manifests against IsPermission, so a write grant it does not
// recognise makes every plugin using one unloadable.
func TestWriteGrantsAreRecognisedPermissions(t *testing.T) {
	for _, p := range WritePermissions {
		if !IsPermission(p) {
			t.Errorf("%q is a write grant but IsPermission rejects it", p)
		}
	}
	for _, p := range Permissions {
		if IsWritePermission(p) {
			t.Errorf("%q appears in both lists", p)
		}
	}
}

// AllPermissions must be exactly the union, with nothing lost or duplicated.
func TestAllPermissionsIsTheUnion(t *testing.T) {
	all := AllPermissions()
	if len(all) != len(Permissions)+len(WritePermissions) {
		t.Fatalf("AllPermissions has %d entries, want %d", len(all), len(Permissions)+len(WritePermissions))
	}
	seen := map[string]bool{}
	for _, p := range all {
		if seen[p] {
			t.Errorf("%q appears twice", p)
		}
		seen[p] = true
	}
	for _, p := range append(append([]string{}, Permissions...), WritePermissions...) {
		if !seen[p] {
			t.Errorf("%q is missing from AllPermissions", p)
		}
	}
}

// Sorted, so a reader can find one and a diff shows a real change rather than a
// reordering.
func TestWritePermissionsAreSorted(t *testing.T) {
	if !sort.StringsAreSorted(WritePermissions) {
		t.Errorf("WritePermissions is not sorted: %v", WritePermissions)
	}
}
