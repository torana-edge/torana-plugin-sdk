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
	// developer is OpenAI's rename of system, and adapters pass roles through
	// verbatim rather than normalising, so it reaches plugins as itself.
	roles := []string{"user", "assistant", "system", "tool", "developer"}

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

	// Every message-role grant must be reachable, so the vocabulary carries
	// nothing an author cannot actually use. The catch-all is reachable only
	// from roles Torana does not model, so it is checked that way.
	if s, _ := MessageWriteSection("a_role_torana_does_not_model"); s == SectionMessagesOther {
		granted[SectionMessagesOther] = true
	}
	for _, p := range WritePermissions {
		if !strings.HasPrefix(p, "ir.messages.write.") {
			continue
		}
		if !granted[WriteSection(p)] {
			t.Errorf("%q is offered but no role maps to it", p)
		}
	}
}

// A role Torana does not model must still be writable BY GRANT, and must never
// be unrestricted.
//
// Format adapters cast provider role strings straight into the IR, so a role
// added by any provider tomorrow arrives with no code change. Mapping it to
// nothing would make such a message permanently unmutatable — a plugin that
// legitimately edited it would have its entire output rejected — and mapping it
// to "allowed" would make it an unguarded write path.
func TestUnmodelledRoleFallsToTheCatchAllGrant(t *testing.T) {
	for _, role := range []string{"", "USER", "tool_result", "some_future_role"} {
		section, modelled := MessageWriteSection(role)
		if modelled {
			t.Errorf("role %q reported as modelled by name", role)
		}
		if section != SectionMessagesOther {
			t.Errorf("role %q mapped to %q, want the catch-all %q",
				role, section, SectionMessagesOther)
		}
		if !IsWritePermission(string(section)) {
			t.Errorf("the catch-all %q is not a requestable grant, so nothing could ever "+
				"write an unmodelled role", section)
		}
	}
}

// Roles Torana models by name must NOT fall to the catch-all, or the narrow
// grants would be pointless: one plugin holding ir.messages.write.other could
// rewrite user prompts.
func TestModelledRolesDoNotFallToTheCatchAll(t *testing.T) {
	for _, role := range []string{"user", "assistant", "system", "tool", "developer"} {
		section, modelled := MessageWriteSection(role)
		if !modelled {
			t.Errorf("role %q is not reported as modelled", role)
		}
		if section == SectionMessagesOther {
			t.Errorf("role %q fell to the catch-all", role)
		}
	}
}

// Permissions must CONTAIN the write grants, not sit beside them.
//
// Hosts build their allowlist from Permissions, while `torana plugin lint`
// checks IsPermission. An earlier draft kept the write grants in a separate
// list with a union helper, so a plugin passed lint and was then refused at
// load — build succeeded, install succeeded, the plugin never ran. Two lists
// that must agree will eventually not.
func TestPermissionsContainsTheWriteGrants(t *testing.T) {
	in := map[string]bool{}
	for _, p := range Permissions {
		in[p] = true
	}
	for _, w := range WritePermissions {
		if !in[w] {
			t.Errorf("%q is a write grant but is absent from Permissions, which is what "+
				"hosts build their allowlist from — a plugin requesting it would pass "+
				"lint and be refused at load", w)
		}
		if !IsPermission(w) {
			t.Errorf("%q is a write grant but IsPermission rejects it", w)
		}
	}
}

// No duplicates, or an operator sees the same capability twice at approval.
func TestPermissionsHasNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Permissions {
		if seen[p] {
			t.Errorf("%q appears twice in Permissions", p)
		}
		seen[p] = true
	}
}

// Sorted, so a reader can find one and a diff shows a real change rather than a
// reordering.
func TestWritePermissionsAreSorted(t *testing.T) {
	if !sort.StringsAreSorted(WritePermissions) {
		t.Errorf("WritePermissions is not sorted: %v", WritePermissions)
	}
}
