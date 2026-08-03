package outboundpolicy

import (
	"strings"
	"testing"

	plugin_sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// signedScope is a REFERENCE scope diff, deliberately test-only.
//
// The package ships vocabulary and inventory, not a verifier — an approximate
// public helper would become the thing hosts use instead of implementing the
// policy. But the fixtures are only a contract if something proves they are
// self-consistent, so this reconstructs the signed scope the way a correct
// verifier must: per block index, id and name from the start event, arguments
// concatenated across every delta sharing that index.
//
// Fragment boundaries are transport and must not appear here. Deltas from other
// indexes must not be pooled in.
type signedScope struct {
	found     bool
	signature string
	id        string
	name      string
	arguments string
}

func scopeOf(events []*pbv2.StreamEvent, index int32) signedScope {
	var s signedScope
	for _, ev := range events {
		switch e := ev.Event.(type) {
		case *pbv2.StreamEvent_ContentBlockStart:
			cbs := e.ContentBlockStart
			if cbs.Index != index {
				continue
			}
			tc, ok := cbs.Block.(*pbv2.ContentBlockStart_ToolCall)
			if !ok {
				continue
			}
			s.found = true
			s.signature = tc.ToolCall.Signature
			s.id = tc.ToolCall.Id
			s.name = tc.ToolCall.Name
		case *pbv2.StreamEvent_ToolCallDelta:
			if e.ToolCallDelta.Index == index {
				s.arguments += e.ToolCallDelta.ArgumentsDelta
			}
		}
	}
	return s
}

// contentChanged compares everything the ToolCallRef.signature binding covers:
// id, name, and the assembled arguments. Not the signature itself.
func contentChanged(a, b signedScope) bool {
	return a.id != b.id || a.name != b.name || a.arguments != b.arguments
}

// The fixtures are the cross-repo contract, so they must hold up when the
// boolean is COMPUTED from the streams rather than supplied. An earlier version
// handed BoundContentChanged to the classifier as fixture input, which let any
// verifier reproduce every case without correlating blocks or assembling
// fragments correctly.
func TestSignatureStreamFixturesClassifyAsDeclared(t *testing.T) {
	fx := SignatureStreamFixtures()
	if len(fx) == 0 {
		t.Fatal("no fixtures, so this asserts nothing")
	}
	seen := map[SignatureMutation]bool{}

	for _, f := range fx {
		t.Run(f.Name, func(t *testing.T) {
			before := scopeOf(f.Accepted, f.Index)
			after := scopeOf(f.Returned, f.Index)
			if !before.found || !after.found {
				t.Fatalf("fixture has no tool block at index %d on both sides, "+
					"so it would prove nothing", f.Index)
			}
			got := ClassifySignatureMutation(
				before.signature, after.signature, contentChanged(before, after))
			if got != f.Want {
				t.Fatalf("got %v, want %v\n%s", got, f.Want, f.Why)
			}
		})
		seen[f.Want] = true
	}

	for _, m := range []SignatureMutation{
		SignatureIntact, SignatureCleared, SignatureDropped,
		SignatureStale, SignatureForged, SignatureAdded,
	} {
		if !seen[m] {
			t.Errorf("no fixture covers %v", m)
		}
	}
}

// Fixtures must actually exercise the traps they claim to. A suite of
// single-block, single-delta cases would pass under a verifier that pools
// deltas across indexes or compares fragments pairwise.
func TestSignatureStreamFixturesExerciseTheHardCases(t *testing.T) {
	var multiDelta, multiBlock, differingFraming int
	for _, f := range SignatureStreamFixtures() {
		if len(f.Accepted) > 3 || len(f.Returned) > 3 {
			multiDelta++
		}
		if scopeOf(f.Accepted, 1).found {
			multiBlock++
		}
		if len(f.Accepted) != len(f.Returned) {
			differingFraming++
		}
	}
	if multiDelta == 0 {
		t.Error("no fixture splits arguments across deltas")
	}
	if multiBlock < 2 {
		t.Error("fewer than two fixtures use a second sequential block index; " +
			"one is not enough to catch a verifier that correlates wrongly")
	}
	if differingFraming == 0 {
		t.Error("no fixture changes fragment framing while keeping content identical")
	}
}

// Truth table for the rule itself, independent of stream shape.
func TestClassifySignatureMutationTruthTable(t *testing.T) {
	for _, tc := range []struct {
		accepted, returned string
		changed            bool
		want               SignatureMutation
	}{
		{"sig", "sig", false, SignatureIntact},
		{"", "", false, SignatureIntact},
		{"sig", "", true, SignatureCleared},
		{"sig", "", false, SignatureDropped},
		{"sig", "sig", true, SignatureStale},
		{"sig", "other", false, SignatureForged},
		{"sig", "other", true, SignatureForged},
		{"", "other", false, SignatureAdded},
		{"", "other", true, SignatureAdded},
	} {
		got := ClassifySignatureMutation(tc.accepted, tc.returned, tc.changed)
		if got != tc.want {
			t.Errorf("(%q→%q changed=%v): got %v, want %v",
				tc.accepted, tc.returned, tc.changed, got, tc.want)
		}
	}
}

func TestOnlyIntactAndClearedAreAllowed(t *testing.T) {
	allowed := map[SignatureMutation]bool{SignatureIntact: true, SignatureCleared: true}
	for _, m := range []SignatureMutation{
		SignatureIntact, SignatureCleared, SignatureDropped,
		SignatureStale, SignatureForged, SignatureAdded,
	} {
		if m.Allowed() != allowed[m] {
			t.Errorf("%v.Allowed() = %v, want %v", m, m.Allowed(), allowed[m])
		}
	}
}

// The SDK's emit path and the policy must agree. They live in different
// packages, written from different reasoning, and the review found them
// contradicting. Deriving both sides from the real functions is what stops that
// recurring.
func TestEmitAssembledToolCallSatisfiesThePolicy(t *testing.T) {
	const (
		sig      = "provider-token"
		origArgs = `{"path":"/etc/passwd"}`
		newArgs  = `{"path":"/tmp/safe"}`
	)
	call := plugin_sdk.ToolCall{
		Index: 3, ID: "call_1", Name: "read_file",
		Signature: sig, Arguments: origArgs,
	}
	accepted := toolBlock(3, call.ID, call.Name, sig, origArgs)

	for _, tc := range []struct {
		name string
		args string
		want SignatureMutation
	}{
		{"pass re-emits identically", origArgs, SignatureIntact},
		{"replacement clears the token", newArgs, SignatureCleared},
	} {
		t.Run(tc.name, func(t *testing.T) {
			returned := plugin_sdk.EmitAssembledToolCall(call, tc.args)
			before, after := scopeOf(accepted, 3), scopeOf(returned, 3)
			got := ClassifySignatureMutation(
				before.signature, after.signature, contentChanged(before, after))
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			if !got.Allowed() {
				t.Fatalf("the SDK emits output its own policy rejects: %v", got)
			}
		})
	}
}

// One token, one scope. Deleting from the tracking map is idempotent, so a
// duplicate binding used to pass Validate silently and give a token two
// definitions of what it covers — a verifier iterating the exported bindings
// would have no unique contract to implement.
func TestValidateRejectsDuplicateBinding(t *testing.T) {
	original := signatureBindings
	t.Cleanup(func() { signatureBindings = original })

	// A conflicting duplicate: same field, different signed content.
	conflicting := SignatureBinding{
		Domain:         SignatureDomainOutbound,
		Message:        "torana.v2.ToolCallRef",
		SignatureField: "signature",
		Content: []SignatureContentRef{
			{Scope: SignatureScopeSameMessage, Field: "id"},
		},
	}
	signatureBindings = append(append([]SignatureBinding{}, original...), conflicting)

	err := Validate()
	if err == nil {
		t.Fatal("a duplicate binding was accepted; one token would have two scopes")
	}
	if !strings.Contains(err.Error(), "ToolCallRef.signature") {
		t.Fatalf("error does not name the offending field: %v", err)
	}
}

func TestValidateRejectsDuplicateContentRef(t *testing.T) {
	original := signatureBindings
	t.Cleanup(func() { signatureBindings = original })

	dup := make([]SignatureBinding, len(original))
	copy(dup, original)
	for i := range dup {
		if dup[i].Message == "torana.v2.ToolCallRef" {
			dup[i].Content = append(append([]SignatureContentRef{}, dup[i].Content...), dup[i].Content[0])
		}
	}
	signatureBindings = dup

	if err := Validate(); err == nil {
		t.Fatal("a duplicated content ref was accepted; the signed scope is ambiguous")
	}
}

func TestValidateRejectsDuplicateRequestBinding(t *testing.T) {
	original := signatureBindings
	t.Cleanup(func() { signatureBindings = original })

	conflicting := SignatureBinding{
		Domain:         SignatureDomainRequest,
		Message:        "torana.v2.Message",
		SignatureField: "thinking_signature",
		Content: []SignatureContentRef{
			{Scope: SignatureScopeSameMessage, Field: "thinking"},
		},
	}
	signatureBindings = append(append([]SignatureBinding{}, original...), conflicting)

	err := Validate()
	if err == nil {
		t.Fatal("a duplicate request-domain binding was accepted; one token would have two scopes")
	}
	if !strings.Contains(err.Error(), "Message.thinking_signature") {
		t.Fatalf("error does not name the offending field: %v", err)
	}
}

func TestValidateRejectsDuplicateRequestContentRef(t *testing.T) {
	original := signatureBindings
	t.Cleanup(func() { signatureBindings = original })

	dup := make([]SignatureBinding, len(original))
	copy(dup, original)
	for i := range dup {
		if dup[i].Domain == SignatureDomainRequest &&
			dup[i].Message == "torana.v2.RequestThinkingBlock" && dup[i].SignatureField == "signature" {
			dup[i].Content = append(append([]SignatureContentRef{}, dup[i].Content...), dup[i].Content[0])
		}
	}
	signatureBindings = dup

	if err := Validate(); err == nil {
		t.Fatal("a duplicated request-domain content ref was accepted; the signed scope is ambiguous")
	}
}

// The fixtures are only worth exporting if a WRONG verifier fails them. The
// previous set could be reproduced by any implementation, because the answer
// was handed over as fixture input. These are the specific mistakes named in
// review; each must be caught by at least one case.
func TestWrongVerifiersFailTheFixtures(t *testing.T) {
	// Pools every delta regardless of block index.
	poolsIndexes := func(events []*pbv2.StreamEvent, index int32) signedScope {
		s := scopeOf(events, index)
		s.arguments = ""
		for _, ev := range events {
			if d, ok := ev.Event.(*pbv2.StreamEvent_ToolCallDelta); ok {
				s.arguments += d.ToolCallDelta.ArgumentsDelta
			}
		}
		return s
	}
	// Signs only the arguments, omitting id and name from the scope.
	argsOnly := func(a, b signedScope) bool { return a.arguments != b.arguments }
	// Compares the first fragment instead of the assembled arguments.
	firstFragment := func(events []*pbv2.StreamEvent, index int32) signedScope {
		s := scopeOf(events, index)
		s.arguments = ""
		for _, ev := range events {
			if d, ok := ev.Event.(*pbv2.StreamEvent_ToolCallDelta); ok && d.ToolCallDelta.Index == index {
				s.arguments = d.ToolCallDelta.ArgumentsDelta
				break
			}
		}
		return s
	}

	for _, w := range []struct {
		name    string
		scope   func([]*pbv2.StreamEvent, int32) signedScope
		changed func(a, b signedScope) bool
	}{
		{"pools deltas across block indexes", poolsIndexes, contentChanged},
		{"omits id and name from the signed scope", scopeOf, argsOnly},
		{"compares only the first fragment", firstFragment, contentChanged},
	} {
		t.Run(w.name, func(t *testing.T) {
			for _, f := range SignatureStreamFixtures() {
				before, after := w.scope(f.Accepted, f.Index), w.scope(f.Returned, f.Index)
				got := ClassifySignatureMutation(
					before.signature, after.signature, w.changed(before, after))
				if got != f.Want {
					return // caught, as it must be
				}
			}
			t.Fatalf("a verifier that %s reproduced every fixture; "+
				"the suite does not discriminate on this axis", w.name)
		})
	}
}

// The concurrent-tool shape must classify BOTH blocks: a fixture that only
// asks about index 0 lets a verifier record the first open tool correctly
// while ignoring or overwriting the second concurrently opened one. This
// guard pins both expectations so a later cleanup cannot silently remove one.
func TestConcurrentToolFixtureCoversBothIndexes(t *testing.T) {
	var concurrent []StreamFixture
	for _, f := range SignatureStreamFixtures() {
		if len(f.Accepted) == 0 || len(f.Returned) == 0 {
			continue
		}
		// The concurrent shape is the one whose accepted stream opens two tool
		// blocks before either closes.
		var sawStarts []int32
		for _, ev := range f.Accepted {
			if s, ok := ev.Event.(*pbv2.StreamEvent_ContentBlockStart); ok && s.ContentBlockStart.GetToolCall() != nil {
				sawStarts = append(sawStarts, s.ContentBlockStart.Index)
			}
		}
		if len(sawStarts) >= 2 {
			concurrent = append(concurrent, f)
		}
	}
	if len(concurrent) < 2 {
		t.Fatalf("expected the concurrent shape to carry at least two fixtures (one per block), got %d", len(concurrent))
	}
	wants := map[int32]SignatureMutation{}
	for _, f := range concurrent {
		wants[f.Index] = f.Want
	}
	if wants[0] != SignatureIntact || wants[1] != SignatureIntact {
		t.Fatalf("concurrent shape must classify BOTH blocks intact, got %v", wants)
	}
}
