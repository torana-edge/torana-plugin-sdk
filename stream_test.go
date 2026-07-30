//go:build !wasip1

package plugin_sdk

import (
	"bytes"
	"encoding/binary"
	"strconv"
	"strings"
	"sync"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// metaHost is a minimal host-backed meta_append store for StreamAssembler Feed tests.
type metaHost struct {
	mu    sync.Mutex
	meta  map[string]string
	calls []string
}

func newMetaHost() *metaHost { return &metaHost{meta: map[string]string{}} }

func (m *metaHost) handle(cmd string, args []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, cmd)
	switch cmd {
	case pbv2.MetaAppendCommand:
		var a pbv2.MetaAppendArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return marshalHostErr(pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "bad"), nil
		}
		key := "block:" + strconv.FormatInt(int64(a.BlockIndex), 10)
		existing, present := m.meta[key]
		if len(a.Fragment) != 0 {
			m.meta[key] = existing + string(a.Fragment)
			existing = m.meta[key]
			present = true
		}
		val := pbv2.MetaAppendSuccessValue(a.Fragment, []byte(existing), present)
		raw, _ := proto.Marshal(&pbv2.HostCallResult{
			Result: &pbv2.HostCallResult_Value{Value: val},
		})
		return raw, nil
	default:
		return nil, nil
	}
}

func marshalHostErr(code pbv2.ErrorCode, msg string) []byte {
	raw, _ := proto.Marshal(&pbv2.HostCallResult{
		Result: &pbv2.HostCallResult_Error{Error: &pbv2.HostError{Code: code, Message: msg}},
	})
	return raw
}

func withMetaHost(m *metaHost, fn func()) {
	WithTestHost(&TestHost{HostCall: m.handle}, fn)
}

func TestStreamAssemblerFeedPassThroughWithoutAssembly(t *testing.T) {
	asm := NewStreamAssembler()
	ev := &pbv2.StreamEvent{Event: &pbv2.StreamEvent_TextDelta{TextDelta: "x"}}
	fr := asm.Feed(ev)
	if fr.Err != nil || fr.Suppress || len(fr.Emit) != 1 || fr.Emit[0] != ev {
		t.Fatalf("%+v", fr)
	}
}

func TestStreamAssemblerFeedCrossInstance(t *testing.T) {
	m := newMetaHost()
	withMetaHost(m, func() {
		a1 := NewStreamAssembler().WithToolAssembly()
		start := &pbv2.StreamEvent{Event: &pbv2.StreamEvent_ContentBlockStart{
			ContentBlockStart: &pbv2.ContentBlockStart{
				Index: 3,
				Block: &pbv2.ContentBlockStart_ToolCall{ToolCall: &pbv2.ToolCallRef{
					Id: "c", Name: "n", Signature: "sig",
				}},
			},
		}}
		if fr := a1.Feed(start); !fr.Suppress || fr.Err != nil {
			t.Fatalf("start %+v", fr)
		}
		if fr := a1.Feed(&pbv2.StreamEvent{Event: &pbv2.StreamEvent_ToolCallDelta{
			ToolCallDelta: &pbv2.ToolCallDelta{Index: 3, ArgumentsDelta: `{"p":`},
		}}); !fr.Suppress || fr.Err != nil {
			t.Fatalf("delta1 %+v", fr)
		}

		// New Go object, same request-scoped host meta (cross-dispatch).
		a2 := NewStreamAssembler().WithToolAssembly()
		if fr := a2.Feed(&pbv2.StreamEvent{Event: &pbv2.StreamEvent_ToolCallDelta{
			ToolCallDelta: &pbv2.ToolCallDelta{Index: 3, ArgumentsDelta: `1}`},
		}}); !fr.Suppress || fr.Err != nil {
			t.Fatalf("delta2 %+v", fr)
		}
		fr := a2.Feed(&pbv2.StreamEvent{Event: &pbv2.StreamEvent_ContentBlockStop{
			ContentBlockStop: &pbv2.ContentBlockStop{Index: 3},
		}})
		if fr.Err != nil || fr.Complete == nil || fr.Complete.Arguments != `{"p":1}` {
			t.Fatalf("complete %+v", fr)
		}
		if fr.Complete.ID != "c" || fr.Complete.Name != "n" || fr.Complete.Index != 3 {
			t.Fatalf("header %+v", fr.Complete)
		}
		if fr.Complete.Signature != "sig" {
			t.Fatalf("signature %q", fr.Complete.Signature)
		}
	})
}

func TestStreamAssemblerOneHostCallPerFragment(t *testing.T) {
	const n = 5
	m := newMetaHost()
	withMetaHost(m, func() {
		asm := NewStreamAssembler().WithToolAssembly()
		if fr := asm.Feed(&pbv2.StreamEvent{Event: &pbv2.StreamEvent_ContentBlockStart{
			ContentBlockStart: &pbv2.ContentBlockStart{
				Index: 0,
				Block: &pbv2.ContentBlockStart_ToolCall{ToolCall: &pbv2.ToolCallRef{
					Id: "1", Name: "t",
				}},
			},
		}}); fr.Err != nil {
			t.Fatal(fr.Err)
		}
		for i := 0; i < n; i++ {
			if fr := asm.Feed(&pbv2.StreamEvent{Event: &pbv2.StreamEvent_ToolCallDelta{
				ToolCallDelta: &pbv2.ToolCallDelta{Index: 0, ArgumentsDelta: "x"},
			}}); fr.Err != nil {
				t.Fatal(fr.Err)
			}
		}
		if fr := asm.Feed(&pbv2.StreamEvent{Event: &pbv2.StreamEvent_ContentBlockStop{
			ContentBlockStop: &pbv2.ContentBlockStop{Index: 0},
		}}); fr.Err != nil || fr.Complete == nil {
			t.Fatalf("%+v", fr)
		}
	})
	want := n + 2 // start + N deltas + stop read
	var metaAppend int
	for _, c := range m.calls {
		if c == pbv2.MetaAppendCommand {
			metaAppend++
		}
		if c == "env.meta_set" || c == "env.meta_get" {
			t.Fatalf("legacy string meta path used: %q", c)
		}
	}
	if metaAppend != want {
		t.Fatalf("meta_append calls: got %d want %d (calls=%v)", metaAppend, want, m.calls)
	}
}

func TestStreamAssemblerFeedMetaAppendDenied(t *testing.T) {
	WithTestHost(&TestHost{
		HostCall: func(cmd string, args []byte) ([]byte, error) {
			if cmd == pbv2.MetaAppendCommand {
				return marshalHostErr(pbv2.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "permission denied"), nil
			}
			return nil, nil
		},
	}, func() {
		asm := NewStreamAssembler().WithToolAssembly()
		fr := asm.Feed(&pbv2.StreamEvent{Event: &pbv2.StreamEvent_ContentBlockStart{
			ContentBlockStart: &pbv2.ContentBlockStart{
				Index: 0,
				Block: &pbv2.ContentBlockStart_ToolCall{ToolCall: &pbv2.ToolCallRef{
					Id: "1", Name: "n",
				}},
			},
		}})
		if fr.Err == nil || !strings.Contains(fr.Err.Error(), "meta_append") {
			t.Fatalf("%+v", fr)
		}
		if fr.Suppress || len(fr.Emit) != 0 {
			t.Fatal("must not suppress or emit after failed persist")
		}
	})
}

func TestStreamAssemblerFeedCorruptFrameOnStop(t *testing.T) {
	WithTestHost(&TestHost{
		HostCall: func(cmd string, args []byte) ([]byte, error) {
			if cmd != pbv2.MetaAppendCommand {
				return nil, nil
			}
			var a pbv2.MetaAppendArgs
			_ = proto.Unmarshal(args, &a)
			// Read path: return garbage that is not a framed ToolCallRef.
			if len(a.Fragment) == 0 {
				raw, _ := proto.Marshal(&pbv2.HostCallResult{
					Result: &pbv2.HostCallResult_Value{Value: []byte("!!!")},
				})
				return raw, nil
			}
			raw, _ := proto.Marshal(&pbv2.HostCallResult{
				Result: &pbv2.HostCallResult_Value{Value: nil},
			})
			return raw, nil
		},
	}, func() {
		asm := NewStreamAssembler().WithToolAssembly()
		_ = asm.Feed(&pbv2.StreamEvent{Event: &pbv2.StreamEvent_ContentBlockStart{
			ContentBlockStart: &pbv2.ContentBlockStart{
				Index: 0,
				Block: &pbv2.ContentBlockStart_ToolCall{ToolCall: &pbv2.ToolCallRef{Id: "1", Name: "n"}},
			},
		}})
		fr := asm.Feed(&pbv2.StreamEvent{Event: &pbv2.StreamEvent_ContentBlockStop{
			ContentBlockStop: &pbv2.ContentBlockStop{Index: 0},
		}})
		if fr.Err == nil {
			t.Fatal("corrupt frame must error")
		}
		if len(fr.Emit) != 0 || fr.Complete != nil {
			t.Fatalf("must not pass stop alone: %+v", fr)
		}
	})
}

func TestEmitAssembledToolCallClearsSignatureOnReplace(t *testing.T) {
	call := ToolCall{Index: 2, ID: "i", Name: "n", Signature: "provider-sig", Arguments: `{"a":1}`}
	pass := EmitAssembledToolCall(call, call.Arguments)
	if pass[0].GetContentBlockStart().GetToolCall().GetSignature() != "provider-sig" {
		t.Fatal("pass must keep signature")
	}
	repl := EmitAssembledToolCall(call, `{"a":2}`)
	if repl[0].GetContentBlockStart().GetToolCall().GetSignature() != "" {
		t.Fatal("replace must clear signature")
	}
	if repl[1].GetToolCallDelta().GetArgumentsDelta() != `{"a":2}` {
		t.Fatal(repl[1])
	}
}

func TestToolFrameRoundTripPreservesFraming(t *testing.T) {
	ref := &pbv2.ToolCallRef{Id: "id", Name: "name", Signature: "sig"}
	frame, err := encodeToolFrameHeader(ref)
	if err != nil {
		t.Fatal(err)
	}
	frame = append(frame, []byte(`{"k":1}`)...)
	got, args, err := decodeToolFrame(frame)
	if err != nil {
		t.Fatal(err)
	}
	if got.Id != "id" || got.Name != "name" || got.Signature != "sig" || args != `{"k":1}` {
		t.Fatalf("%+v %q", got, args)
	}
	if binary.BigEndian.Uint32(frame[:4]) != uint32(len(frame)-4-len(args)) {
		t.Fatal("length prefix mismatch")
	}
}

// refWithUnknownField builds a ToolCallRef carrying a field this build does
// not know, the way a newer host would send one.
func refWithUnknownField(t *testing.T, id, name, sig string) *pbv2.ToolCallRef {
	t.Helper()
	raw, err := proto.Marshal(&pbv2.ToolCallRef{Id: id, Name: name, Signature: sig})
	if err != nil {
		t.Fatal(err)
	}
	raw = protowire.AppendTag(raw, 99, protowire.BytesType)
	raw = protowire.AppendBytes(raw, []byte("from-a-newer-host"))

	ref := &pbv2.ToolCallRef{}
	if err := proto.Unmarshal(raw, ref); err != nil {
		t.Fatal(err)
	}
	if len(ref.ProtoReflect().GetUnknown()) == 0 {
		t.Fatal("fixture retained no unknown field, so it would prove nothing")
	}
	return ref
}

func unknownOf(t *testing.T, events []*pbv2.StreamEvent) []byte {
	t.Helper()
	for _, ev := range events {
		s, ok := ev.Event.(*pbv2.StreamEvent_ContentBlockStart)
		if !ok {
			continue
		}
		tc, ok := s.ContentBlockStart.Block.(*pbv2.ContentBlockStart_ToolCall)
		if !ok {
			t.Fatal("block start is not a tool call")
		}
		return tc.ToolCall.ProtoReflect().GetUnknown()
	}
	t.Fatal("no content block start emitted")
	return nil
}

// A field a newer host added must survive the assembler.
//
// The previous test of this name inserted no unknown field at all — its own
// comment said so — so it pinned nothing while reading as coverage. Dropping
// these fields is worst on the two paths that promise to leave the call alone:
// pass, and callback-error fail-open.
func TestAssembledToolCallPreservesUnknownFields(t *testing.T) {
	const origArgs = `{"path":"/a"}`
	ref := refWithUnknownField(t, "call_1", "read_file", "sig")
	want := ref.ProtoReflect().GetUnknown()

	call := ToolCall{
		Index: 2, ID: ref.Id, Name: ref.Name,
		Signature: ref.Signature, Arguments: origArgs, ref: ref,
	}

	for _, tc := range []struct {
		name string
		args string
	}{
		{"pass re-emits unchanged", origArgs},
		{"fail-open re-emits the assembled original", origArgs},
		{"argument replacement keeps unrelated fields", `{"path":"/b"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := unknownOf(t, EmitAssembledToolCall(call, tc.args))
			if !bytes.Equal(got, want) {
				t.Fatalf("unknown field lost: got %q, want %q", got, want)
			}
		})
	}
}

// Replacing arguments must still clear the signature even when cloning a ref,
// or preserving unknown fields would quietly reintroduce a stale token.
func TestClonedRefStillClearsSignatureOnReplace(t *testing.T) {
	ref := refWithUnknownField(t, "call_1", "read_file", "sig")
	call := ToolCall{
		Index: 2, ID: ref.Id, Name: ref.Name,
		Signature: ref.Signature, Arguments: `{"path":"/a"}`, ref: ref,
	}
	events := EmitAssembledToolCall(call, `{"path":"/b"}`)
	for _, ev := range events {
		if s, ok := ev.Event.(*pbv2.StreamEvent_ContentBlockStart); ok {
			tc := s.ContentBlockStart.Block.(*pbv2.ContentBlockStart_ToolCall)
			if tc.ToolCall.Signature != "" {
				t.Fatalf("signature survived an argument replacement: %q", tc.ToolCall.Signature)
			}
		}
	}
}

// Cloning must not alias: mutating the emitted ref must not reach back into
// the buffered call, or a fan-out would corrupt its own source.
func TestReemitDoesNotAliasTheStoredRef(t *testing.T) {
	ref := refWithUnknownField(t, "call_1", "read_file", "sig")
	call := ToolCall{
		Index: 2, ID: ref.Id, Name: ref.Name,
		Signature: ref.Signature, Arguments: `{"path":"/a"}`, ref: ref,
	}
	events := EmitAssembledToolCall(call, `{"path":"/a"}`)
	for _, ev := range events {
		if s, ok := ev.Event.(*pbv2.StreamEvent_ContentBlockStart); ok {
			s.ContentBlockStart.Block.(*pbv2.ContentBlockStart_ToolCall).ToolCall.Id = "mutated"
		}
	}
	if call.ref.Id != "call_1" {
		t.Fatalf("emitted ref aliased the stored one: %q", call.ref.Id)
	}
}
