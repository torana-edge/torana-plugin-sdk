//go:build !wasip1

package plugin_sdk

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// metaHost is a minimal host-backed meta store for StreamAssembler Feed tests.
type metaHost struct {
	mu   sync.Mutex
	meta map[string]string
}

func newMetaHost() *metaHost { return &metaHost{meta: map[string]string{}} }

func (m *metaHost) handle(cmd string, args []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch cmd {
	case "env.meta_set":
		var kv struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(args, &kv); err != nil {
			return []byte(`{"status":"error"}`), nil
		}
		m.meta[kv.Key] = kv.Value
		return []byte(`{"status":"ok"}`), nil
	case "env.meta_get":
		return []byte(m.meta[string(args)]), nil
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
					Id: "c", Name: "n",
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
	})
}

func TestStreamAssemblerFeedMetaAppendDenied(t *testing.T) {
	WithTestHost(&TestHost{
		HostCall: func(cmd string, args []byte) ([]byte, error) {
			if cmd == "env.meta_set" {
				return []byte(`{"status":"ok"}`), nil
			}
			if cmd == "env.meta_get" {
				return []byte(`{"id":"1","name":"n","sig":""}`), nil
			}
			if cmd == pbv2.MetaAppendCommand {
				return marshalHostErr(pbv2.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "permission denied"), nil
			}
			return nil, nil
		},
	}, func() {
		asm := NewStreamAssembler().WithToolAssembly()
		fr := asm.Feed(&pbv2.StreamEvent{Event: &pbv2.StreamEvent_ToolCallDelta{
			ToolCallDelta: &pbv2.ToolCallDelta{Index: 0, ArgumentsDelta: "{}"},
		}})
		if fr.Err == nil || !strings.Contains(fr.Err.Error(), "meta_append") {
			t.Fatalf("%+v", fr)
		}
		if len(fr.Emit) != 0 {
			t.Fatal("must not emit fragment after buffering failure")
		}
	})
}

func TestStreamAssemblerFeedCorruptHeaderOnStop(t *testing.T) {
	WithTestHost(&TestHost{
		HostCall: func(cmd string, args []byte) ([]byte, error) {
			if cmd == "env.meta_get" {
				return []byte("not-json"), nil
			}
			return nil, nil
		},
	}, func() {
		asm := NewStreamAssembler().WithToolAssembly()
		stop := &pbv2.StreamEvent{Event: &pbv2.StreamEvent_ContentBlockStop{
			ContentBlockStop: &pbv2.ContentBlockStop{Index: 0},
		}}
		fr := asm.Feed(stop)
		if fr.Err == nil {
			t.Fatal("corrupt header must error")
		}
		if len(fr.Emit) != 0 || fr.Complete != nil {
			t.Fatalf("must not pass stop alone: %+v", fr)
		}
	})
}

func TestEmitAssembledToolCallRoundTrip(t *testing.T) {
	evs := EmitAssembledToolCall(ToolCall{Index: 2, ID: "i", Name: "n", Arguments: "{}"}, `{"a":1}`)
	if len(evs) != 3 {
		t.Fatal(len(evs))
	}
	if evs[1].GetToolCallDelta().GetArgumentsDelta() != `{"a":1}` {
		t.Fatal(evs[1])
	}
}
