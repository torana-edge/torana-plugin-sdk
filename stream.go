package plugin_sdk

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/proto"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

// StreamAssembler is the advanced, host-backed stream state machine.
// Feed never stores fragments on this Go value — successive WASM calls may
// land on different module instances — so all durable state is request-scoped
// host metadata keyed by block index via env.meta_append only.
//
// Call pattern for a buffered tool call (one typed host crossing each):
//
//	start → MetaAppend(index, length-prefixed ToolCallRef)
//	delta → MetaAppend(index, arguments_delta)   // empty delta is a no-op
//	stop  → MetaAppend(index, nil)               // read framed buffer
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
	Emit []*pbv1.StreamEvent
	// Suppress is true when the inbound event must not be forwarded as-is.
	Suppress bool
	// Complete is set when a tool-call block finished assembling.
	Complete *ToolCall
	// Err is a coherent failure (corrupt/missing metadata after buffering
	// began). Callers must not pass the current fragment alone.
	Err error
}

// Feed processes one stream event. When tool assembly is enabled, tool-call
// start/deltas are suppressed and stored; on stop, Complete carries the
// assembled call and Emit is empty until the caller decides pass/replace/
// suppress (see EmitAssembledToolCall).
func (a *StreamAssembler) Feed(ev *pbv1.StreamEvent) FeedResult {
	if ev == nil {
		return FeedResult{Err: fmt.Errorf("StreamAssembler.Feed: nil event")}
	}
	if err := ev.Validate(); err != nil {
		return FeedResult{Err: err}
	}
	if !a.assembleTools {
		return FeedResult{Emit: []*pbv1.StreamEvent{ev}}
	}

	switch e := ev.Event.(type) {
	case *pbv1.StreamEvent_ContentBlockStart:
		start := e.ContentBlockStart
		tc := start.GetToolCall()
		if tc == nil {
			return FeedResult{Emit: []*pbv1.StreamEvent{ev}}
		}
		frame, err := encodeToolFrameHeader(tc)
		if err != nil {
			return FeedResult{Err: err}
		}
		if _, herr, err := MetaAppend(start.Index, frame); err != nil {
			return FeedResult{Err: err}
		} else if herr != nil {
			return FeedResult{Err: fmt.Errorf("meta_append: %s", herr.Message)}
		}
		return FeedResult{Suppress: true}

	case *pbv1.StreamEvent_ToolCallDelta:
		d := e.ToolCallDelta
		// Empty fragment is the meta_append read path — skip the host call.
		if len(d.ArgumentsDelta) == 0 {
			return FeedResult{Suppress: true}
		}
		if _, herr, err := MetaAppend(d.Index, []byte(d.ArgumentsDelta)); err != nil {
			return FeedResult{Err: err}
		} else if herr != nil {
			return FeedResult{Err: fmt.Errorf("meta_append: %s", herr.Message)}
		}
		return FeedResult{Suppress: true}

	case *pbv1.StreamEvent_ContentBlockStop:
		stop := e.ContentBlockStop
		buf, herr, err := MetaAppend(stop.Index, nil)
		if err != nil {
			return FeedResult{Err: err}
		}
		if herr != nil {
			return FeedResult{Err: fmt.Errorf("meta_append read: %s", herr.Message)}
		}
		if len(buf) == 0 {
			// No buffer for this index — not a tool block we started.
			return FeedResult{Emit: []*pbv1.StreamEvent{ev}}
		}
		ref, args, err := decodeToolFrame(buf)
		if err != nil {
			return FeedResult{Err: fmt.Errorf("tool-call frame corrupt: %w", err)}
		}
		return FeedResult{
			Suppress: true,
			Complete: &ToolCall{
				Index:     stop.Index,
				ID:        ref.Id,
				Name:      ref.Name,
				Signature: ref.Signature,
				Arguments: args,
				ref:       ref,
			},
		}

	case *pbv1.StreamEvent_Error:
		// Terminal mid-block: incomplete buffers stay in meta and are unused.
		return FeedResult{Emit: []*pbv1.StreamEvent{ev}}

	default:
		return FeedResult{Emit: []*pbv1.StreamEvent{ev}}
	}
}

// reemitRef rebuilds the ContentBlockStart ref for an assembled tool call.
//
// When the assembler decoded a ref, that ref is cloned and only the fields the
// action deliberately changes are touched, so anything a newer host added
// travels through unaltered. Rebuilding from scratch would drop it, which is
// the opposite of what pass and fail-open promise.
//
// A caller that constructed the ToolCall itself has no ref to preserve, so one
// is built from the known fields.
func reemitRef(call ToolCall, sig string) *pbv1.ToolCallRef {
	if call.ref == nil {
		return &pbv1.ToolCallRef{Id: call.ID, Name: call.Name, Signature: sig}
	}
	out, _ := proto.Clone(call.ref).(*pbv1.ToolCallRef)
	out.Id = call.ID
	out.Name = call.Name
	out.Signature = sig
	return out
}

// encodeToolFrameHeader builds the initial meta_append fragment for a tool
// block: big-endian uint32 length + protobuf ToolCallRef. Argument bytes are
// appended by later MetaAppend calls; decodeToolFrame splits them on read.
func encodeToolFrameHeader(ref *pbv1.ToolCallRef) ([]byte, error) {
	if ref == nil {
		return nil, fmt.Errorf("tool-call frame: nil ToolCallRef")
	}
	raw, err := proto.Marshal(ref)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 4+len(raw))
	binary.BigEndian.PutUint32(out, uint32(len(raw)))
	copy(out[4:], raw)
	return out, nil
}

func decodeToolFrame(buf []byte) (*pbv1.ToolCallRef, string, error) {
	if len(buf) < 4 {
		return nil, "", fmt.Errorf("shorter than length prefix")
	}
	n := binary.BigEndian.Uint32(buf[:4])
	if uint64(4)+uint64(n) > uint64(len(buf)) {
		return nil, "", fmt.Errorf("header length %d exceeds buffer %d", n, len(buf))
	}
	ref := &pbv1.ToolCallRef{}
	if err := proto.Unmarshal(buf[4:4+n], ref); err != nil {
		return nil, "", err
	}
	return ref, string(buf[4+n:]), nil
}

// EmitAssembledToolCall builds start+delta+stop for an assembled tool call.
//
// When args differs from call.Arguments, Signature is cleared: the provider
// binding covers id/name/arguments, and a stale signature must not ship.
// Pass and fail-open re-emission keep call.Arguments (and thus Signature)
// byte-identical so the host can verify the buffered tool block transactionally
// — temporary suppress-then-reemit is not deletion/forgery.
func EmitAssembledToolCall(call ToolCall, args string) []*pbv1.StreamEvent {
	sig := call.Signature
	if args != call.Arguments {
		sig = ""
	}
	return []*pbv1.StreamEvent{
		{Event: &pbv1.StreamEvent_ContentBlockStart{
			ContentBlockStart: &pbv1.ContentBlockStart{
				Index: call.Index,
				Block: &pbv1.ContentBlockStart_ToolCall{ToolCall: reemitRef(call, sig)},
			},
		}},
		{Event: &pbv1.StreamEvent_ToolCallDelta{
			ToolCallDelta: &pbv1.ToolCallDelta{
				Index:          call.Index,
				ArgumentsDelta: args,
			},
		}},
		{Event: &pbv1.StreamEvent_ContentBlockStop{
			ContentBlockStop: &pbv1.ContentBlockStop{Index: call.Index},
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

	// ref is the ToolCallRef the host actually sent, kept whole so re-emission
	// can preserve fields this build does not know about.
	//
	// Copying the four scalars out and rebuilding a fresh ref silently dropped
	// any additive field a newer host added — on pass and on callback-error
	// fail-open, the two paths whose entire purpose is to leave the call
	// untouched. Forward compatibility that survives only until a plugin
	// buffers a tool call is not forward compatibility.
	//
	// Nil when the caller constructed the ToolCall itself rather than
	// receiving it from the assembler; EmitAssembledToolCall builds a fresh
	// ref in that case.
	ref *pbv1.ToolCallRef
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

func PassText() TextAction               { return TextAction{pass: true} }
func ReplaceText(text string) TextAction { return TextAction{hasReplace: true, replace: text} }
func SuppressText() TextAction           { return TextAction{suppress: true} }

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
func (s *StreamHandler) Handle(ctx context.Context, ev *pbv1.StreamEvent) (StreamResult, error) {
	if ev == nil {
		return StreamResult{}, fmt.Errorf("StreamHandler: nil event")
	}

	// Text deltas can be rewritten without assembly.
	if td, ok := ev.Event.(*pbv1.StreamEvent_TextDelta); ok && s.onText != nil {
		action, cbErr := s.onText(ctx, td.TextDelta)
		if cbErr != nil {
			return PassEvent(), nil
		}
		if action.suppress {
			return SuppressEvent(), nil
		}
		if action.hasReplace {
			return EmitEvents(&pbv1.StreamEvent{
				Event: &pbv1.StreamEvent_TextDelta{TextDelta: action.replace},
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
