package plugin_sdk

import "testing"

// The host returns this exact JSON when a capability is refused. Every wrapper
// that can be denied must recognise it: a plugin that mistakes the refusal for
// a normal result carries on as though the call had succeeded.
//
// PluginConfig did exactly that. It returned the envelope AS the plugin's
// config blob, so a plugin missing env.plugin_config parsed
// {"status":"error","message":"permission denied"} — an object with none of its
// expected fields — and silently ran on defaults rather than reporting a
// missing grant. Its own doc comment already claimed the correct behaviour.
const hostDenialEnvelope = `{"status":"error","message":"permission denied"}`

func TestIsPermissionDeniedMatchesTheHostEnvelope(t *testing.T) {
	if !isPermissionDenied(hostDenialEnvelope) {
		t.Fatalf("the SDK no longer recognises the host's refusal envelope %s.\n"+
			"Every denied host call would be treated as an ordinary result.", hostDenialEnvelope)
	}
}

func TestIsPermissionDeniedDoesNotOverMatch(t *testing.T) {
	// A plugin's own data must never be mistaken for a refusal — that would
	// discard a legitimate result.
	for _, notDenial := range []string{
		"",
		"{}",
		`{"status":"ok"}`,
		`{"status":"error","message":"something else"}`,
		`{"message":"permission denied"}`,
		`prefix{"status":"error","message":"permission denied"}`,
	} {
		if isPermissionDenied(notDenial) {
			t.Errorf("treated %q as a permission denial", notDenial)
		}
	}
}
