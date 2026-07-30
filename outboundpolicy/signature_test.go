package outboundpolicy

import (
	"testing"

	plugin_sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

func TestSignatureFixturesClassifyAsDeclared(t *testing.T) {
	fx := SignatureFixtures()
	if len(fx) == 0 {
		t.Fatal("no fixtures, so this asserts nothing")
	}
	seen := map[SignatureMutation]bool{}
	for _, f := range fx {
		got := ClassifySignatureMutation(f.AcceptedSignature, f.ReturnedSignature, f.BoundContentChanged)
		if got != f.Want {
			t.Errorf("%s: got %v, want %v\n%s", f.Name, got, f.Want, f.Why)
		}
		seen[f.Want] = true
	}
	// Every outcome the rule can produce needs a case, or a whole branch goes
	// unexercised while the suite still passes.
	for _, m := range []SignatureMutation{
		SignatureIntact, SignatureCleared, SignatureStale, SignatureForged, SignatureAdded,
	} {
		if !seen[m] {
			t.Errorf("no fixture covers %v", m)
		}
	}
}

// The SDK's emit path and the policy must agree. They are written in different
// packages by different reasoning, and the review found them contradicting:
// EmitAssembledToolCall cleared the token while the registry called it
// unconditionally immutable. Deriving both sides of this test from the real
// functions is what stops that recurring.
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

	for _, tc := range []struct {
		name    string
		args    string
		changed bool
		want    SignatureMutation
	}{
		{"pass re-emits identically", origArgs, false, SignatureIntact},
		{"replacement clears the token", newArgs, true, SignatureCleared},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := plugin_sdk.EmitAssembledToolCall(call, tc.args)
			got := signatureOfToolBlock(t, events)

			if m := ClassifySignatureMutation(sig, got, tc.changed); m != tc.want {
				t.Fatalf("emitted signature %q classifies as %v, want %v", got, m, tc.want)
			} else if !m.Allowed() {
				t.Fatalf("the SDK emits output its own policy rejects: %v", m)
			}
		})
	}
}

// A plugin that keeps the token while changing arguments must be rejected —
// the case the policy exists for. The SDK cannot produce it, so it is built by
// hand to prove the rule bites rather than merely describing SDK behaviour.
func TestStaleSignatureIsRejected(t *testing.T) {
	events := ToolBlock(3, "call_1", "read_file", "provider-token", `{"path":"/tmp/evil"}`)
	got := signatureOfToolBlock(t, events)
	m := ClassifySignatureMutation("provider-token", got, true)
	if m.Allowed() {
		t.Fatalf("a stale signature over rewritten arguments was allowed (%v)", m)
	}
	if m != SignatureStale {
		t.Fatalf("got %v, want %v", m, SignatureStale)
	}
}

func signatureOfToolBlock(t *testing.T, events []*pbv2.StreamEvent) string {
	t.Helper()
	for _, ev := range events {
		if s, ok := ev.Event.(*pbv2.StreamEvent_ContentBlockStart); ok {
			tc, ok := s.ContentBlockStart.Block.(*pbv2.ContentBlockStart_ToolCall)
			if !ok {
				t.Fatal("block start is not a tool call")
			}
			return tc.ToolCall.Signature
		}
	}
	t.Fatal("no content block start in the emitted events")
	return ""
}
