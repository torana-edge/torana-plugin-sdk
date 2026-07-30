package outboundpolicy

import (
	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// Executable before/after fixtures for the bound-signature rule.
//
// These exist because the rule was previously stated only in prose, in a
// Migration B checklist, where nothing could hold the implementation to it. A
// verifier is correct on this axis exactly when it reproduces every case here.
//
// They are exported deliberately: Migration B lives in torana-edge and must be
// able to run the SDK's own cases rather than reimplement its reading of them.

// SignatureFixture is one accepted→returned tool block and the verdict the
// verifier must reach.
type SignatureFixture struct {
	// Name identifies the case in failures.
	Name string
	// AcceptedSignature is the token on the stream the host handed the plugin.
	AcceptedSignature string
	// ReturnedSignature is the token on the plugin's output.
	ReturnedSignature string
	// BoundContentChanged is computed over the WHOLE binding scope — for a
	// tool block, start id/name plus every arguments_delta sharing the index.
	// Never per event: see the transactional note in the package comment.
	BoundContentChanged bool
	// Want is the classification ClassifySignatureMutation must return.
	Want SignatureMutation
	// Why states the consequence of getting this case wrong.
	Why string
}

// SignatureFixtures returns every bound-signature case, including the two the
// SDK itself produces (pass and argument replacement).
func SignatureFixtures() []SignatureFixture {
	return []SignatureFixture{
		{
			Name:              "pass through, nothing touched",
			AcceptedSignature: "sig-a",
			ReturnedSignature: "sig-a",
			Want:              SignatureIntact,
			Why: "StreamHandler suppresses fragments and re-emits them byte-identically. " +
				"Judging at the suppression instead of across the block would reject every " +
				"buffering assembler.",
		},
		{
			Name:                "arguments replaced, token cleared",
			AcceptedSignature:   "sig-a",
			ReturnedSignature:   "",
			BoundContentChanged: true,
			Want:                SignatureCleared,
			Why: "Exactly what EmitAssembledToolCall produces via ReplaceToolArguments. " +
				"Rejecting this is the contradiction the bound-signature policy exists to remove.",
		},
		{
			Name:                "arguments replaced, token kept",
			AcceptedSignature:   "sig-a",
			ReturnedSignature:   "sig-a",
			BoundContentChanged: true,
			Want:                SignatureStale,
			Why: "The dangerous case: a valid provider token over content the provider " +
				"never signed. Downstream cannot tell it apart from a genuine signature.",
		},
		{
			Name:              "token swapped for another",
			AcceptedSignature: "sig-a",
			ReturnedSignature: "sig-b",
			Want:              SignatureForged,
			Why:               "A plugin cannot mint a provider signature, changed content or not.",
		},
		{
			Name:                "token swapped while content also changed",
			AcceptedSignature:   "sig-a",
			ReturnedSignature:   "sig-b",
			BoundContentChanged: true,
			Want:                SignatureForged,
			Why:                "Changing the content does not license supplying a different token.",
		},
		{
			Name:              "token appears where there was none",
			AcceptedSignature: "",
			ReturnedSignature: "sig-b",
			Want:              SignatureAdded,
			Why:               "Same forgery, reached by addition rather than replacement.",
		},
		{
			Name:              "unsigned block passes through",
			AcceptedSignature: "",
			ReturnedSignature: "",
			Want:              SignatureIntact,
			Why:               "Most tool calls carry no signature; they must not need special handling.",
		},
		{
			Name:              "token cleared although content did not change",
			AcceptedSignature: "sig-a",
			ReturnedSignature: "",
			Want:              SignatureCleared,
			Why: "Permitted: discarding a fact is not forging one. The host forwards no " +
				"signature and the provider re-derives as it would for any unsigned block.",
		},
	}
}

// ToolBlock renders a fixture side as the stream events a verifier compares,
// so Migration B tests the real event shape rather than two bare strings.
func ToolBlock(index int32, id, name, signature, args string) []*pbv2.StreamEvent {
	return []*pbv2.StreamEvent{
		{Event: &pbv2.StreamEvent_ContentBlockStart{
			ContentBlockStart: &pbv2.ContentBlockStart{
				Index: index,
				Block: &pbv2.ContentBlockStart_ToolCall{ToolCall: &pbv2.ToolCallRef{
					Id: id, Name: name, Signature: signature,
				}},
			},
		}},
		{Event: &pbv2.StreamEvent_ToolCallDelta{
			ToolCallDelta: &pbv2.ToolCallDelta{Index: index, ArgumentsDelta: args},
		}},
		{Event: &pbv2.StreamEvent_ContentBlockStop{
			ContentBlockStop: &pbv2.ContentBlockStop{Index: index},
		}},
	}
}
