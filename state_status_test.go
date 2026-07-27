package plugin_sdk

import "testing"

// stateStatus decides whether a host response is an error envelope. It used to
// unmarshal ANY response into the envelope struct and treat a decode failure as
// success — which meant a bare value the host legitimately returned was probed
// for error fields, and succeeded with an empty Status by accident rather than
// by decision.
//
// The behaviour is unchanged for every case that matters; the reasoning is now
// explicit, which is the point. The residual ambiguity is pinned below so it is
// a known limitation rather than a surprise.

func TestStateStatusRecognisesEnvelopes(t *testing.T) {
	for name, tc := range map[string]struct {
		res     string
		wantErr bool
	}{
		"empty":                 {"", false},
		"permission denied":     {`{"status":"error","message":"permission denied"}`, true},
		"other host error":      {`{"status":"error","message":"store unavailable"}`, true},
		"explicit success":      {`{"status":"ok"}`, false},
		"bare string value":     {`"a stored value"`, false},
		"bare number value":     {`42`, false},
		"object without status": {`{"count":3,"label":"x"}`, false},
		"not json at all":       {`not json`, false},
		"array value":           {`[1,2,3]`, false},
	} {
		t.Run(name, func(t *testing.T) {
			err := stateStatus(tc.res)
			if (err != nil) != tc.wantErr {
				t.Errorf("stateStatus(%s) = %v, wantErr=%v", tc.res, err, tc.wantErr)
			}
		})
	}
}

func TestStateStatusMapsDenialToASentinel(t *testing.T) {
	// Callers distinguish "not granted" from "the store failed", so the denial
	// must map to the sentinel rather than a generic error.
	if err := stateStatus(`{"status":"error","message":"permission denied"}`); err != ErrStateUnavailable {
		t.Errorf("got %v, want ErrStateUnavailable", err)
	}
}

// TestStateStatusAmbiguityIsKnown documents the limitation rather than pretending
// it is fixed: the envelope shares a namespace with user data, so a plugin that
// stores an object with its own "status":"error" is indistinguishable from a
// host error. Separating them is an ABI break, not a bug fix.
func TestStateStatusAmbiguityIsKnown(t *testing.T) {
	if err := stateStatus(`{"status":"error","message":"user data"}`); err == nil {
		t.Error("expected the documented ambiguity: user data shaped like an envelope reads as an error")
	}
}
