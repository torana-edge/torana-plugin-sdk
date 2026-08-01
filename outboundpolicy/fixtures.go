package outboundpolicy

import (
	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// Executable before/after fixtures for the bound-signature rule.
//
// These exist because the rule was previously stated only in prose, in a
// Migration B checklist, where nothing could hold the implementation to it.
//
// They deliberately do NOT expose whether bound content changed. That boolean
// is the answer a verifier must COMPUTE — transactionally, over the binding's
// scope, correlating by block index. An earlier version of these fixtures
// handed it over as input, which meant a verifier could compare per event,
// correlate the wrong index, omit id/name or a fragment from the scope, or
// simply trust the field, and still reproduce every case. A fixture that
// supplies the answer tests nothing.
//
// A verifier is correct on this axis exactly when, given Accepted and Returned,
// its own scope diff plus ClassifySignatureMutation yields Want for the block
// at Index.
//
// Exported because Migration B lives in torana-edge and must run the SDK's own
// cases rather than reimplement its reading of them.

// StreamFixture is one accepted→returned stream and the verdict a verifier must
// reach for the signed block at Index.
type StreamFixture struct {
	// Name identifies the case in failures.
	Name string
	// Accepted is the event sequence the host handed the plugin.
	Accepted []*pbv2.StreamEvent
	// Returned is the sequence the plugin produced.
	Returned []*pbv2.StreamEvent
	// Index is the content block the expectation is about. Fixtures with more
	// than one block exist precisely to catch verifiers that correlate wrongly.
	//
	// Sequential fixtures stay valid: the contract permits interleaving only
	// among TOOL blocks (non-tool content remains exclusive), so the interleaved
	// fixture below exercises exactly that concurrent-tool topology.
	Index int32
	// Want is the classification for that block's signature.
	Want SignatureMutation
	// Why states the consequence of getting this case wrong.
	Why string
}

const (
	sigA = "provider-token-a"
	sigB = "provider-token-b"
)

// SignatureStreamFixtures returns the cross-repo transactional contract.
func SignatureStreamFixtures() []StreamFixture {
	return []StreamFixture{
		{
			Name:     "suppress then byte-identical re-emission",
			Accepted: toolBlock(0, "call_1", "read_file", sigA, `{"path":"/a"}`),
			Returned: toolBlock(0, "call_1", "read_file", sigA, `{"path":"/a"}`),
			Index:    0,
			Want:     SignatureIntact,
			Why: "StreamHandler suppresses fragments and replays them. A verifier that " +
				"judges at the suppression, rather than across the block, rejects every " +
				"buffering assembler.",
		},
		{
			Name:     "arguments split across deltas, reassembled identically",
			Accepted: toolBlockDeltas(0, "call_1", "read_file", sigA, `{"pa`, `th":"`, `/a"}`),
			Returned: toolBlockDeltas(0, "call_1", "read_file", sigA, `{"path":"/a"}`),
			Index:    0,
			Want:     SignatureIntact,
			Why: "Fragment boundaries are transport, not content. A verifier comparing " +
				"deltas pairwise sees a change where there is none and rejects a pass.",
		},
		{
			Name:     "arguments changed across multiple deltas, token cleared",
			Accepted: toolBlockDeltas(0, "call_1", "read_file", sigA, `{"pa`, `th":"`, `/a"}`),
			Returned: toolBlockDeltas(0, "call_1", "read_file", "", `{"path":"/b"}`),
			Index:    0,
			Want:     SignatureCleared,
			Why: "What ReplaceToolArguments produces. A verifier that only inspects the " +
				"first delta misses the change and misreads this as a dropped token.",
		},
		{
			Name:     "arguments changed in a later delta only, token kept",
			Accepted: toolBlockDeltas(0, "call_1", "read_file", sigA, `{"path":`, `"/a"}`),
			Returned: toolBlockDeltas(0, "call_1", "read_file", sigA, `{"path":`, `"/evil"}`),
			Index:    0,
			Want:     SignatureStale,
			Why: "The dangerous case, hidden behind a matching first fragment: a valid " +
				"provider token over content the provider never signed.",
		},
		{
			Name:     "start id changed, token kept",
			Accepted: toolBlock(0, "call_1", "read_file", sigA, `{"path":"/a"}`),
			Returned: toolBlock(0, "call_2", "read_file", sigA, `{"path":"/a"}`),
			Index:    0,
			Want:     SignatureStale,
			Why: "id is inside the signed scope. A verifier that signs only the arguments " +
				"lets the call be re-pointed under a valid signature.",
		},
		{
			Name:     "start name changed, token kept",
			Accepted: toolBlock(0, "call_1", "read_file", sigA, `{"path":"/a"}`),
			Returned: toolBlock(0, "call_1", "delete_file", sigA, `{"path":"/a"}`),
			Index:    0,
			Want:     SignatureStale,
			Why: "name is inside the signed scope, and swapping it is the highest-impact " +
				"mutation available: same arguments, different tool.",
		},
		{
			Name:     "token swapped for another",
			Accepted: toolBlock(0, "call_1", "read_file", sigA, `{"path":"/a"}`),
			Returned: toolBlock(0, "call_1", "read_file", sigB, `{"path":"/a"}`),
			Index:    0,
			Want:     SignatureForged,
			Why:      "A plugin cannot mint a provider signature, changed content or not.",
		},
		{
			Name:     "token appears where there was none",
			Accepted: toolBlock(0, "call_1", "read_file", "", `{"path":"/a"}`),
			Returned: toolBlock(0, "call_1", "read_file", sigB, `{"path":"/a"}`),
			Index:    0,
			Want:     SignatureAdded,
			Why:      "Forgery reached by addition rather than replacement.",
		},
		{
			Name:     "token dropped while content is untouched",
			Accepted: toolBlock(0, "call_1", "read_file", sigA, `{"path":"/a"}`),
			Returned: toolBlock(0, "call_1", "read_file", "", `{"path":"/a"}`),
			Index:    0,
			Want:     SignatureDropped,
			Why: "Stripping provenance from content the provider did sign. Providers can " +
				"require the token on a later turn, so this breaks replay rather than " +
				"degrading, and it is indistinguishable from laundering.",
		},
		{
			Name:     "unsigned block passes through",
			Accepted: toolBlock(0, "call_1", "read_file", "", `{"path":"/a"}`),
			Returned: toolBlock(0, "call_1", "read_file", "", `{"path":"/a"}`),
			Index:    0,
			Want:     SignatureIntact,
			Why:      "Most tool calls carry no signature and must not need special handling.",
		},
		{
			Name: "two sequential blocks, only the second changed",
			Accepted: append(
				toolBlockDeltas(0, "call_1", "read_file", sigA, `{"path":`, `"/a"}`),
				toolBlockDeltas(1, "call_2", "write_file", sigB, `{"path":`, `"/b"}`)...),
			Returned: append(
				toolBlockDeltas(0, "call_1", "read_file", sigA, `{"path":`, `"/a"}`),
				toolBlockDeltas(1, "call_2", "write_file", "", `{"path":`, `"/c"}`)...),
			Index: 0,
			Want:  SignatureIntact,
			Why: "Block 0 is untouched. A verifier that pools deltas across indexes sees " +
				"block 1's change here and rejects an innocent block.",
		},
		{
			Name: "two sequential blocks, the changed one is judged",
			Accepted: append(
				toolBlockDeltas(0, "call_1", "read_file", sigA, `{"path":`, `"/a"}`),
				toolBlockDeltas(1, "call_2", "write_file", sigB, `{"path":`, `"/b"}`)...),
			Returned: append(
				toolBlockDeltas(0, "call_1", "read_file", sigA, `{"path":`, `"/a"}`),
				toolBlockDeltas(1, "call_2", "write_file", "", `{"path":`, `"/c"}`)...),
			Index: 1,
			Want:  SignatureCleared,
			Why: "Same streams as above, different block. A verifier keying on anything " +
				"but the block index cannot produce both answers.",
		},
		{
			Name:     "two concurrently open tool blocks, interleaved deltas, both intact",
			Accepted: interleavedToolBlocks(),
			Returned: interleavedToolBlocks(),
			Index:    0,
			Want:     SignatureIntact,
			Why: "OpenAI Chat shape: block 1 opens and its deltas interleave before " +
				"block 0 closes. Tokens travel with their blocks — a verifier that " +
				"tracks a single open block or pools fragments across indexes " +
				"misattributes block 1's content to block 0.",
		},
	}
}

// toolBlock renders one signed tool block with a single arguments delta.
func toolBlock(index int32, id, name, signature, args string) []*pbv2.StreamEvent {
	return toolBlockDeltas(index, id, name, signature, args)
}

// toolBlockDeltas renders one signed tool block whose arguments arrive as the
// given fragments, so fixtures can vary framing independently of content.
func toolBlockDeltas(index int32, id, name, signature string, args ...string) []*pbv2.StreamEvent {
	out := []*pbv2.StreamEvent{
		{Event: &pbv2.StreamEvent_ContentBlockStart{
			ContentBlockStart: &pbv2.ContentBlockStart{
				Index: index,
				Block: &pbv2.ContentBlockStart_ToolCall{ToolCall: &pbv2.ToolCallRef{
					Id: id, Name: name, Signature: signature,
				}},
			},
		}},
	}
	for _, a := range args {
		out = append(out, &pbv2.StreamEvent{
			Event: &pbv2.StreamEvent_ToolCallDelta{
				ToolCallDelta: &pbv2.ToolCallDelta{Index: index, ArgumentsDelta: a},
			},
		})
	}
	return append(out, &pbv2.StreamEvent{
		Event: &pbv2.StreamEvent_ContentBlockStop{
			ContentBlockStop: &pbv2.ContentBlockStop{Index: index},
		},
	})
}

// interleavedToolBlocks renders two tool-call blocks that are open
// CONCURRENTLY, with their arguments deltas interleaved by index — the OpenAI
// Chat parallel-tool shape. Block 1 starts before block 0 stops, so only an
// index-keyed assembler/verifier can keep the two argument buffers apart.
func interleavedToolBlocks() []*pbv2.StreamEvent {
	return []*pbv2.StreamEvent{
		{Event: &pbv2.StreamEvent_ContentBlockStart{
			ContentBlockStart: &pbv2.ContentBlockStart{
				Index: 0,
				Block: &pbv2.ContentBlockStart_ToolCall{ToolCall: &pbv2.ToolCallRef{
					Id: "call_1", Name: "read_file", Signature: sigA,
				}},
			},
		}},
		{Event: &pbv2.StreamEvent_ToolCallDelta{
			ToolCallDelta: &pbv2.ToolCallDelta{Index: 0, ArgumentsDelta: `{"path":`},
		}},
		{Event: &pbv2.StreamEvent_ContentBlockStart{
			ContentBlockStart: &pbv2.ContentBlockStart{
				Index: 1,
				Block: &pbv2.ContentBlockStart_ToolCall{ToolCall: &pbv2.ToolCallRef{
					Id: "call_2", Name: "write_file", Signature: sigB,
				}},
			},
		}},
		{Event: &pbv2.StreamEvent_ToolCallDelta{
			ToolCallDelta: &pbv2.ToolCallDelta{Index: 1, ArgumentsDelta: `{"path":`},
		}},
		{Event: &pbv2.StreamEvent_ToolCallDelta{
			ToolCallDelta: &pbv2.ToolCallDelta{Index: 0, ArgumentsDelta: `"/a"}`},
		}},
		{Event: &pbv2.StreamEvent_ToolCallDelta{
			ToolCallDelta: &pbv2.ToolCallDelta{Index: 1, ArgumentsDelta: `"/b"}`},
		}},
		{Event: &pbv2.StreamEvent_ContentBlockStop{
			ContentBlockStop: &pbv2.ContentBlockStop{Index: 0},
		}},
		{Event: &pbv2.StreamEvent_ContentBlockStop{
			ContentBlockStop: &pbv2.ContentBlockStop{Index: 1},
		}},
	}
}
