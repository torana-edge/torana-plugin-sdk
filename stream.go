package plugin_sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// StreamAssembler is the advanced, host-backed stream state machine.
// Feed never stores fragments on this Go value — successive WASM calls may
// land on different module instances — so all durable state is request-scoped
// host metadata keyed by block index.
type StreamAssembler struct {
	// assembleTools enables tool-call buffering. StreamHandler sets this when
	// an OnToolCall callback is registered so only interested plugins pay.
	assembleTools bool
}

// NewStreamAssembler returns an assembler with no tool buffering (pass-through
// of every event after Validate). Enable assembly with WithToolAssembly.
func NewStreamAssembler() *StreamAssembler { return &StreamAssembler{} }

// WithToolAssembly turns on host-backed tool-call fragment assembly.
func (a *StreamAssembler) WithToolAssembly() *StreamAssembler {
	a.assembleTools = true
	return a
}

// FeedResult is what Feed returns for one inbound event.
type FeedResult struct {
	// Emit is the events to send onward. Nil/empty with Suppress means drop.
	Emit []*pbv2.StreamEvent
	// Suppress is true when the inbound event must not be forwarded as-is.
	Suppress bool
	// Complete is set when a tool-call block finished assembling.
	Complete *ToolCall
	// Err is a coherent failure (corrupt/missing metadata after buffering
	// began). Callers must not pass the current fragment alone.
	Err error
}

type toolCallHeader struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Signature string `json:"sig"`
}

func blockHeaderKey(index int32) string {
	return "stream:block:" + strconv.FormatInt(int64(index), 10) + ":hdr"
}

// Feed processes one stream event. When tool assembly is enabled, tool-call
// start/deltas are suppressed and stored; on stop, Complete carries the
// assembled call and Emit is empty until the caller decides pass/replace/
// suppress (see EmitAssembledToolCall).
func (a *StreamAssembler) Feed(ev *pbv2.StreamEvent) FeedResult {
	if ev == nil {
		return FeedResult{Err: fmt.Errorf("StreamAssembler.Feed: nil event")}
	}
	if err := ev.Validate(); err != nil {
		return FeedResult{Err: err}
	}
	if !a.assembleTools {
		return FeedResult{Emit: []*pbv2.StreamEvent{ev}}
	}

	switch e := ev.Event.(type) {
	case *pbv2.StreamEvent_ContentBlockStart:
		start := e.ContentBlockStart
		tc := start.GetToolCall()
		if tc == nil {
			return FeedResult{Emit: []*pbv2.StreamEvent{ev}}
		}
		hdr, err := json.Marshal(toolCallHeader{ID: tc.Id, Name: tc.Name, Signature: tc.Signature})
		if err != nil {
			return FeedResult{Err: err}
		}
		if err := metaSetString(blockHeaderKey(start.Index), string(hdr)); err != nil {
			return FeedResult{Err: err}
		}
		return FeedResult{Suppress: true}

	case *pbv2.StreamEvent_ToolCallDelta:
		d := e.ToolCallDelta
		hdrJSON, err := metaGetString(blockHeaderKey(d.Index))
		if err != nil {
			return FeedResult{Err: err}
		}
		if hdrJSON == "" {
			// Not assembling this index — pass through.
			return FeedResult{Emit: []*pbv2.StreamEvent{ev}}
		}
		if _, herr, err := MetaAppend(d.Index, []byte(d.ArgumentsDelta)); err != nil {
			return FeedResult{Err: err}
		} else if herr != nil {
			return FeedResult{Err: fmt.Errorf("meta_append: %s", herr.Message)}
		}
		return FeedResult{Suppress: true}

	case *pbv2.StreamEvent_ContentBlockStop:
		stop := e.ContentBlockStop
		hdrJSON, err := metaGetString(blockHeaderKey(stop.Index))
		if err != nil {
			return FeedResult{Err: err}
		}
		if hdrJSON == "" {
			return FeedResult{Emit: []*pbv2.StreamEvent{ev}}
		}
		var hdr toolCallHeader
		if err := json.Unmarshal([]byte(hdrJSON), &hdr); err != nil {
			return FeedResult{Err: fmt.Errorf("tool-call header corrupt: %w", err)}
		}
		buf, herr, err := MetaAppend(stop.Index, nil)
		if err != nil {
			return FeedResult{Err: err}
		}
		if herr != nil {
			return FeedResult{Err: fmt.Errorf("meta_append read: %s", herr.Message)}
		}
		_ = metaSetString(blockHeaderKey(stop.Index), "")
		return FeedResult{
			Suppress: true,
			Complete: &ToolCall{
				Index:     stop.Index,
				ID:        hdr.ID,
				Name:      hdr.Name,
				Signature: hdr.Signature,
				Arguments: string(buf),
			},
		}

	case *pbv2.StreamEvent_Error:
		// Terminal mid-block: abandon headers we can see is not possible without
		// an index; pass the error through. Incomplete buffers stay in meta and
		// are unused.
		return FeedResult{Emit: []*pbv2.StreamEvent{ev}}

	default:
		return FeedResult{Emit: []*pbv2.StreamEvent{ev}}
	}
}

// EmitAssembledToolCall builds start+delta+stop for an assembled tool call.
func EmitAssembledToolCall(call ToolCall, args string) []*pbv2.StreamEvent {
	return []*pbv2.StreamEvent{
		{Event: &pbv2.StreamEvent_ContentBlockStart{
			ContentBlockStart: &pbv2.ContentBlockStart{
				Index: call.Index,
				Block: &pbv2.ContentBlockStart_ToolCall{ToolCall: &pbv2.ToolCallRef{
					Id: call.ID, Name: call.Name, Signature: call.Signature,
				}},
			},
		}},
		{Event: &pbv2.StreamEvent_ToolCallDelta{
			ToolCallDelta: &pbv2.ToolCallDelta{
				Index:          call.Index,
				ArgumentsDelta: args,
			},
		}},
		{Event: &pbv2.StreamEvent_ContentBlockStop{
			ContentBlockStop: &pbv2.ContentBlockStop{Index: call.Index},
		}},
	}
}

// --- StreamHandler (built on StreamAssembler) ---

// ToolCall is a fully assembled tool call presented to OnToolCall.
type ToolCall struct {
	Index     int32
	ID        string
	Name      string
	Signature string
	Arguments string
}

// ToolCallAction is what OnToolCall returns.
type ToolCallAction struct {
	pass       bool
	replace    string
	suppress   bool
	err        error
	hasReplace bool
}

func PassToolCall() ToolCallAction { return ToolCallAction{pass: true} }

func ReplaceToolArguments(args string) ToolCallAction {
	if !json.Valid([]byte(args)) {
		return ToolCallAction{err: fmt.Errorf("ReplaceToolArguments: arguments are not valid JSON")}
	}
	return ToolCallAction{hasReplace: true, replace: args}
}

func SuppressToolCall() ToolCallAction { return ToolCallAction{suppress: true} }

// TextAction is what OnTextDelta returns.
type TextAction struct {
	pass       bool
	replace    string
	suppress   bool
	hasReplace bool
}

func PassText() TextAction             { return TextAction{pass: true} }
func ReplaceText(text string) TextAction { return TextAction{hasReplace: true, replace: text} }
func SuppressText() TextAction         { return TextAction{suppress: true} }

// StreamHandler routes stream events to semantic callbacks via StreamAssembler.
type StreamHandler struct {
	asm        *StreamAssembler
	onToolCall func(context.Context, ToolCall) (ToolCallAction, error)
	onText     func(context.Context, string) (TextAction, error)
}

func NewStreamHandler() *StreamHandler {
	return &StreamHandler{asm: NewStreamAssembler()}
}

func (s *StreamHandler) OnToolCall(fn func(context.Context, ToolCall) (ToolCallAction, error)) *StreamHandler {
	if s.onToolCall != nil {
		panic("torana sdk: StreamHandler.OnToolCall registered more than once")
	}
	if fn == nil {
		panic("torana sdk: StreamHandler.OnToolCall nil callback")
	}
	s.onToolCall = fn
	s.asm.WithToolAssembly()
	return s
}

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

// Handle implements the stream-chunk hook signature. Semantic callback errors
// are consumed for fail-open re-emission; assembler/protocol errors are returned
// so the trampoline traps.
func (s *StreamHandler) Handle(ctx context.Context, ev *pbv2.StreamEvent) (StreamResult, error) {
	if ev == nil {
		return StreamResult{}, fmt.Errorf("StreamHandler: nil event")
	}

	// Text deltas can be rewritten without assembly.
	if td, ok := ev.Event.(*pbv2.StreamEvent_TextDelta); ok && s.onText != nil {
		action, cbErr := s.onText(ctx, td.TextDelta)
		if cbErr != nil {
			return PassEvent(), nil
		}
		if action.suppress {
			return SuppressEvent(), nil
		}
		if action.hasReplace {
			return EmitEvents(&pbv2.StreamEvent{
				Event: &pbv2.StreamEvent_TextDelta{TextDelta: action.replace},
			}), nil
		}
		return PassEvent(), nil
	}

	fr := s.asm.Feed(ev)
	if fr.Err != nil {
		return StreamResult{}, fr.Err
	}
	if fr.Complete != nil {
		call := *fr.Complete
		action, cbErr := s.onToolCall(ctx, call)
		if cbErr != nil || action.err != nil {
			return EmitEvents(EmitAssembledToolCall(call, call.Arguments)...), nil
		}
		if action.suppress {
			return SuppressEvent(), nil
		}
		args := call.Arguments
		if action.hasReplace {
			args = action.replace
		}
		return EmitEvents(EmitAssembledToolCall(call, args)...), nil
	}
	if fr.Suppress {
		return SuppressEvent(), nil
	}
	if len(fr.Emit) == 0 {
		return PassEvent(), nil
	}
	if len(fr.Emit) == 1 && fr.Emit[0] == ev {
		return PassEvent(), nil
	}
	return EmitEvents(fr.Emit...), nil
}

// Register installs Handle as the stream-chunk hook.
func (s *StreamHandler) Register() {
	OnStreamChunk(s.Handle)
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
