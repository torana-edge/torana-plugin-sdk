package plugin_sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// StreamHandler is the public, callback-first stream API.
//
// Multiple interests live in one handler; registering OnStreamChunk separately
// after Handle is claimed panics as a duplicate. Assembly state is stored via
// host metadata keyed by block index — never on this struct — because the WASM
// pool may run successive events on different module instances.

// ToolCall is a fully assembled tool call presented to OnToolCall.
type ToolCall struct {
	Index     int32
	ID        string
	Name      string
	Signature string
	Arguments string // complete JSON object
}

// ToolCallAction is what OnToolCall returns.
type ToolCallAction struct {
	pass       bool
	replace    string
	suppress   bool
	err        error
	hasReplace bool
}

// PassToolCall leaves the assembled arguments unchanged (re-emits original).
func PassToolCall() ToolCallAction { return ToolCallAction{pass: true} }

// ReplaceToolArguments substitutes args JSON for the tool call's arguments.
func ReplaceToolArguments(args string) ToolCallAction {
	if !json.Valid([]byte(args)) {
		return ToolCallAction{err: fmt.Errorf("ReplaceToolArguments: arguments are not valid JSON")}
	}
	return ToolCallAction{hasReplace: true, replace: args}
}

// SuppressToolCall drops the tool call entirely.
func SuppressToolCall() ToolCallAction { return ToolCallAction{suppress: true} }

// TextAction is what OnTextDelta returns.
type TextAction struct {
	pass       bool
	replace    string
	suppress   bool
	hasReplace bool
}

// PassText leaves the delta unchanged.
func PassText() TextAction { return TextAction{pass: true} }

// ReplaceText substitutes text for the delta.
func ReplaceText(text string) TextAction {
	return TextAction{hasReplace: true, replace: text}
}

// SuppressText drops the delta.
func SuppressText() TextAction { return TextAction{suppress: true} }

type toolCallHeader struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Signature string `json:"sig"`
}

func blockHeaderKey(index int32) string {
	return "stream:block:" + strconv.FormatInt(int64(index), 10) + ":hdr"
}

// StreamHandler routes stream events to semantic callbacks.
type StreamHandler struct {
	onToolCall func(context.Context, ToolCall) (ToolCallAction, error)
	onText     func(context.Context, string) (TextAction, error)
}

// NewStreamHandler builds an empty handler. Call On* then Register.
func NewStreamHandler() *StreamHandler { return &StreamHandler{} }

// OnToolCall registers the tool-call callback. Only plugins that register one
// pay for fragment buffering.
func (s *StreamHandler) OnToolCall(fn func(context.Context, ToolCall) (ToolCallAction, error)) *StreamHandler {
	if s.onToolCall != nil {
		panic("torana sdk: StreamHandler.OnToolCall registered more than once")
	}
	if fn == nil {
		panic("torana sdk: StreamHandler.OnToolCall nil callback")
	}
	s.onToolCall = fn
	return s
}

// OnTextDelta registers the text-delta callback.
func (s *StreamHandler) OnTextDelta(fn func(context.Context, string) (TextAction, error)) *StreamHandler {
	if s.onText != nil {
		panic("torana sdk: StreamHandler.OnTextDelta registered more than once")
	}
	if fn == nil {
		panic("torana sdk: StreamHandler.OnTextDelta nil callback")
	}
	s.onText = fn
	return s
}

// Handle is the OnStreamChunk handler.
func (s *StreamHandler) Handle(ctx context.Context, ev *pbv2.StreamEvent) StreamResult {
	if ev == nil {
		return StreamResult{err: fmt.Errorf("StreamHandler: nil event")}
	}
	switch e := ev.Event.(type) {
	case *pbv2.StreamEvent_ContentBlockStart:
		start := e.ContentBlockStart
		if start == nil {
			return StreamResult{err: fmt.Errorf("StreamHandler: nil content block start")}
		}
		if tc := start.GetToolCall(); tc != nil && s.onToolCall != nil {
			hdr, err := json.Marshal(toolCallHeader{ID: tc.Id, Name: tc.Name, Signature: tc.Signature})
			if err != nil {
				return StreamResult{err: err}
			}
			if err := metaSetString(blockHeaderKey(start.Index), string(hdr)); err != nil {
				return StreamResult{err: err}
			}
			return SuppressEvent()
		}
		return PassEvent()

	case *pbv2.StreamEvent_ToolCallDelta:
		if s.onToolCall == nil {
			return PassEvent()
		}
		d := e.ToolCallDelta
		if d == nil {
			return StreamResult{err: fmt.Errorf("StreamHandler: nil tool call delta")}
		}
		if _, herr, err := MetaAppend(d.Index, []byte(d.ArgumentsDelta)); err != nil {
			return StreamResult{err: err}
		} else if herr != nil {
			return StreamResult{err: fmt.Errorf("meta_append: %s", herr.Message)}
		}
		return SuppressEvent()

	case *pbv2.StreamEvent_ContentBlockStop:
		stop := e.ContentBlockStop
		if stop == nil {
			return StreamResult{err: fmt.Errorf("StreamHandler: nil content block stop")}
		}
		if s.onToolCall == nil {
			return PassEvent()
		}
		hdrJSON, err := metaGetString(blockHeaderKey(stop.Index))
		if err != nil {
			return StreamResult{err: err}
		}
		if hdrJSON == "" {
			return PassEvent()
		}
		var hdr toolCallHeader
		if err := json.Unmarshal([]byte(hdrJSON), &hdr); err != nil {
			return StreamResult{err: fmt.Errorf("tool-call header: %w", err)}
		}
		buf, herr, err := MetaAppend(stop.Index, nil)
		if err != nil {
			return StreamResult{err: err}
		}
		if herr != nil {
			return StreamResult{err: fmt.Errorf("meta_append read: %s", herr.Message)}
		}
		call := ToolCall{
			Index:     stop.Index,
			ID:        hdr.ID,
			Name:      hdr.Name,
			Signature: hdr.Signature,
			Arguments: string(buf),
		}
		_ = metaSetString(blockHeaderKey(stop.Index), "")
		action, cbErr := s.onToolCall(ctx, call)
		if cbErr != nil || action.err != nil {
			return emitAssembledToolCall(call, buf)
		}
		if action.suppress {
			return SuppressEvent()
		}
		args := call.Arguments
		if action.hasReplace {
			args = action.replace
		}
		call.Arguments = args
		return emitAssembledToolCall(call, []byte(args))

	case *pbv2.StreamEvent_TextDelta:
		if s.onText == nil {
			return PassEvent()
		}
		action, cbErr := s.onText(ctx, e.TextDelta)
		if cbErr != nil {
			return PassEvent()
		}
		if action.suppress {
			return SuppressEvent()
		}
		if action.hasReplace {
			return EmitEvents(&pbv2.StreamEvent{
				Event: &pbv2.StreamEvent_TextDelta{TextDelta: action.replace},
			})
		}
		return PassEvent()

	default:
		return PassEvent()
	}
}

func emitAssembledToolCall(call ToolCall, args []byte) StreamResult {
	start := &pbv2.StreamEvent{Event: &pbv2.StreamEvent_ContentBlockStart{
		ContentBlockStart: &pbv2.ContentBlockStart{
			Index: call.Index,
			Block: &pbv2.ContentBlockStart_ToolCall{ToolCall: &pbv2.ToolCallRef{
				Id: call.ID, Name: call.Name, Signature: call.Signature,
			}},
		},
	}}
	delta := &pbv2.StreamEvent{Event: &pbv2.StreamEvent_ToolCallDelta{
		ToolCallDelta: &pbv2.ToolCallDelta{
			Index:          call.Index,
			ArgumentsDelta: string(args),
		},
	}}
	stop := &pbv2.StreamEvent{Event: &pbv2.StreamEvent_ContentBlockStop{
		ContentBlockStop: &pbv2.ContentBlockStop{Index: call.Index},
	}}
	return EmitEvents(start, delta, stop)
}

// Register installs Handle as the stream-chunk hook.
func (s *StreamHandler) Register() {
	OnStreamChunk(s.Handle)
}

// StreamAssembler is the advanced escape hatch. It keeps no cross-call state.
type StreamAssembler struct{}

// NewStreamAssembler returns an assembler with no local buffers.
func NewStreamAssembler() *StreamAssembler { return &StreamAssembler{} }

// Feed returns the events to emit for one inbound event. Default is pass-through.
func (a *StreamAssembler) Feed(ev *pbv2.StreamEvent) ([]*pbv2.StreamEvent, error) {
	if ev == nil {
		return nil, fmt.Errorf("StreamAssembler.Feed: nil event")
	}
	if err := ev.Validate(); err != nil {
		return nil, err
	}
	return []*pbv2.StreamEvent{ev}, nil
}

func metaSetString(key, value string) error {
	payload, err := json.Marshal(map[string]any{"key": key, "value": value})
	if err != nil {
		return err
	}
	res, err := hostCallString("env.meta_set", string(payload))
	if err != nil {
		return err
	}
	if isPermissionDenied(res) {
		return fmt.Errorf("torana: meta_set permission denied")
	}
	if res != "" && !strings.Contains(res, `"status":"ok"`) && strings.Contains(res, `"status":"error"`) {
		return fmt.Errorf("torana: meta_set: %s", res)
	}
	return nil
}

func metaGetString(key string) (string, error) {
	res, err := hostCallString("env.meta_get", key)
	if err != nil {
		return "", err
	}
	if isPermissionDenied(res) {
		return "", fmt.Errorf("torana: meta_get permission denied")
	}
	return res, nil
}
