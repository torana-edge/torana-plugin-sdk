//go:build !wasip1

package plugin_sdk

import (
	"encoding/binary"
	"strconv"
	"strings"
	"sync"
	"testing"

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

func TestToolFrameRoundTripPreservesUnknownFields(t *testing.T) {
	ref := &pbv2.ToolCallRef{Id: "id", Name: "name", Signature: "sig"}
	// Simulate an unknown field via proto marshaling of an extended message is
	// hard without the field number; pin length-prefix framing instead.
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
