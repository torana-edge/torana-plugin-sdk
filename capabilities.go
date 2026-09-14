package plugin_sdk

import (
	_ "embed"
	"encoding/json"
	"sort"
)

// CommandSpec describes the one canonical route to a supported host operation.
// Result names refer to protobuf messages unless they explicitly name a raw
// representation. HostCallResult always frames the result or typed refusal.
type CommandSpec struct {
	Command     string   `json:"command"`
	Arguments   string   `json:"arguments"`
	Result      string   `json:"result"`
	Permission  string   `json:"permission"`
	Hooks       []string `json:"hooks"`
	Helpers     []string `json:"helpers"`
	Implemented bool     `json:"implemented"`
}

type capabilityCatalog struct {
	Hooks          []string          `json:"hooks"`
	HookGrants     map[string]string `json:"hook_grants"`
	MetadataGrants []string          `json:"metadata_grants"`
	Commands       []CommandSpec     `json:"commands"`
}

//go:embed capabilities.json
var capabilityJSON []byte
var catalog = readCapabilityCatalog()

func readCapabilityCatalog() capabilityCatalog {
	var c capabilityCatalog
	if err := json.Unmarshal(capabilityJSON, &c); err != nil {
		panic(err)
	}
	return c
}

// Hooks and Permissions derive from the same catalog used by host dispatch,
// tooling and SDK reference generation. Requesting a permission never grants it.
var Hooks = append([]string(nil), catalog.Hooks...)
var Permissions = catalogPermissions()

func catalogPermissions() []string {
	set := map[string]bool{}
	for _, c := range catalog.Commands {
		if c.Implemented {
			set[c.Permission] = true
		}
	}
	for p := range catalog.HookGrants {
		set[p] = true
	}
	for _, p := range catalog.MetadataGrants {
		set[p] = true
	}
	for _, p := range WritePermissions {
		set[p] = true
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Commands returns a copy so callers cannot change the host authority catalog.
func Commands() []CommandSpec {
	out := append([]CommandSpec(nil), catalog.Commands...)
	for i := range out {
		out[i].Hooks = append([]string(nil), out[i].Hooks...)
		out[i].Helpers = append([]string(nil), out[i].Helpers...)
	}
	return out
}
func Command(name string) (CommandSpec, bool) {
	for _, c := range Commands() {
		if c.Command == name && c.Implemented {
			return c, true
		}
	}
	return CommandSpec{}, false
}
func CommandPermission(name string) (string, bool) { c, ok := Command(name); return c.Permission, ok }
func HelperPermissions() map[string]string {
	out := map[string]string{}
	for _, c := range catalog.Commands {
		for _, h := range c.Helpers {
			out[h] = c.Permission
		}
	}
	return out
}
func HookGrants() map[string]string {
	out := map[string]string{}
	for p, h := range catalog.HookGrants {
		out[p] = h
	}
	return out
}
func IsHook(name string) bool       { return contains(Hooks, name) }
func IsPermission(name string) bool { return contains(Permissions, name) }
func contains(list []string, name string) bool {
	for _, v := range list {
		if v == name {
			return true
		}
	}
	return false
}
