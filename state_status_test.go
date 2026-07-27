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

// TestStateStatusDiscriminatesFromTheOldImplementation covers the cases that
// actually CHANGED. The rest of this file characterises behaviour that was
// already correct — useful as documentation, useless as a regression guard,
// which review caught by running the previous state.go under these tests and
// finding every one still green.
//
// Each case below returns a different result under that implementation.
func TestStateStatusDiscriminatesFromTheOldImplementation(t *testing.T) {
	t.Run("malformed envelope is an error, not success", func(t *testing.T) {
		// Before: the struct decode failed on the non-string message and the
		// error was swallowed, so a real host error was reported as success.
		if err := stateStatus(`{"status":"error","message":123}`); err == nil {
			t.Fatal("a malformed error envelope must not read as success")
		}
	})

	t.Run("status of the wrong type is an error", func(t *testing.T) {
		if err := stateStatus(`{"status":{"nested":true}}`); err == nil {
			t.Fatal("a non-string status must not read as success")
		}
	})

	t.Run("error with no message names the envelope", func(t *testing.T) {
		// Before: fmt.Errorf("torana: %s", "") produced the useless "torana: ".
		err := stateStatus(`{"status":"error"}`)
		if err == nil {
			t.Fatal("expected an error")
		}
		if err.Error() == "torana: " {
			t.Error("error message is empty; the plugin author learns nothing")
		}
	})

	t.Run("case-sensitive, one rule for the whole function", func(t *testing.T) {
		// Before: the map probe missed "Status" but encoding/json matched it
		// case-insensitively, so the two halves of one function disagreed.
		// JSON is case-sensitive and the host emits lowercase.
		if err := stateStatus(`{"Status":"error","Message":"permission denied"}`); err != nil {
			t.Errorf("a capitalised key is data, not an envelope: %v", err)
		}
	})
}

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
