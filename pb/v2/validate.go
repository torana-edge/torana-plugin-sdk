package v2

import "fmt"

// Validation for the v2 hook envelopes.
//
// The payload oneof is the sole discriminator, so a frame cannot claim one hook
// while carrying another's payload — that contradiction is unrepresentable
// rather than merely detectable. What still needs checking is everything a
// oneof cannot express: that a payload is present at all, that a disposition
// was stated, and that the disposition and payload agree.
//
// These are normative. The host must reject a frame that fails them rather than
// interpreting it, because every alternative is a guess about what a
// misbehaving plugin meant.

// Validate reports whether a stream event carries an actual event.
//
// A StreamEvent with no oneof variant set is a well-formed protobuf message
// that says nothing. Nothing downstream can act on it, and a list of them is
// indistinguishable in effect from an empty list — so it is a third spelling of
// SUPPRESS, and refused.
func (x *StreamEvent) Validate() error {
	if x == nil {
		return fmt.Errorf("stream event is nil")
	}
	if x.Event == nil {
		return fmt.Errorf("stream event carries no event")
	}
	if b, ok := x.Event.(*StreamEvent_ContentBlockStart); ok {
		if b == nil || b.ContentBlockStart == nil {
			return fmt.Errorf("content block start is nil")
		}
		if err := b.ContentBlockStart.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Validate reports whether a content block start names a kind, and carries
// whatever that kind needs to be assembled.
//
// The oneof makes "text carrying tool metadata" unrepresentable, but it does
// not stop a tool-call block carrying EMPTY metadata. A block that cannot say
// which tool it opens cannot be assembled — its deltas have nothing to attach
// to and its results have nothing to correlate against — so it is refused here
// rather than surfacing later as a tool call with no name.
func (x *ContentBlockStart) Validate() error {
	if x == nil {
		return fmt.Errorf("content block start is nil")
	}
	switch b := x.Block.(type) {
	case nil:
		return fmt.Errorf("content block start at index %d names no block kind", x.Index)

	case *ContentBlockStart_ToolCall:
		if b == nil || b.ToolCall == nil {
			return fmt.Errorf("tool-call block at index %d carries no tool call", x.Index)
		}
		if b.ToolCall.Id == "" {
			return fmt.Errorf("tool-call block at index %d has no id, so its result "+
				"cannot be correlated back to it", x.Index)
		}
		if b.ToolCall.Name == "" {
			return fmt.Errorf("tool-call block at index %d has no tool name", x.Index)
		}

	case *ContentBlockStart_Provider:
		if b == nil || b.Provider == nil {
			return fmt.Errorf("provider block at index %d carries no block", x.Index)
		}
		if b.Provider.Kind == "" {
			return fmt.Errorf("provider block at index %d names no kind, which is the "+
				"only thing that makes it actionable", x.Index)
		}
	}
	return nil
}

// HookOf reports which hook an input belongs to, derived from its payload.
// Returns HOOK_UNSPECIFIED when no payload is set.
//
// A oneof wrapper carrying a nil message still satisfies `Payload != nil`, so
// this reports the hook for one — the frame does name a hook. Whether it names
// anything USABLE is Validate's job.
func (x *HookInput) HookOf() Hook {
	if x == nil {
		return Hook_HOOK_UNSPECIFIED
	}
	switch x.Payload.(type) {
	case *HookInput_ChatRequest:
		return Hook_HOOK_BEFORE_REQUEST
	case *HookInput_ChatResponse:
		return Hook_HOOK_AFTER_RESPONSE
	case *HookInput_StreamEvent:
		return Hook_HOOK_ON_STREAM_CHUNK
	case *HookInput_HttpRequest:
		return Hook_HOOK_ON_HTTP_REQUEST
	case *HookInput_TickRequest:
		return Hook_HOOK_ON_TICK
	}
	return Hook_HOOK_UNSPECIFIED
}

// payloadPresent reports whether the oneof wrapper carries an actual message.
//
// A wrapper with a nil message is a well-formed protobuf frame that says a hook
// and then hands over nothing. Checking only that the wrapper exists let all of
// those through — so a handwritten guest could submit a frame the normative
// validator accepted and the host then dereferenced.
// A oneof wrapper can also be a TYPED nil — Payload: (*HookInput_ChatRequest)(nil).
// The interface is non-nil because it carries a type, so a bare `p.ChatRequest`
// dereferences nothing and panics. A validator that crashes on malformed input
// is worse than one that misses it: the host runs this on bytes a guest
// controls, so the guest picks when the host dies.
func (x *HookInput) payloadPresent() bool {
	switch p := x.Payload.(type) {
	case *HookInput_ChatRequest:
		return p != nil && p.ChatRequest != nil
	case *HookInput_ChatResponse:
		return p != nil && p.ChatResponse != nil
	case *HookInput_StreamEvent:
		return p != nil && p.StreamEvent != nil
	case *HookInput_HttpRequest:
		return p != nil && p.HttpRequest != nil
	case *HookInput_TickRequest:
		return p != nil && p.TickRequest != nil
	}
	return false
}

// Validate reports whether an input is well formed on its own terms: that it
// carries a payload at all.
//
// It is NOT enough by itself. The WASM export a plugin was called through is a
// second discriminator, and nothing in the envelope forces the two to agree —
// a tick payload delivered to run_before_request is a well-formed input to the
// wrong hook. Guests must use ValidateFor.
func (x *HookInput) Validate() error {
	if x == nil {
		return fmt.Errorf("hook input is nil")
	}
	if x.HookOf() == Hook_HOOK_UNSPECIFIED {
		return fmt.Errorf("hook input carries no payload, so there is no hook to dispatch")
	}
	if !x.payloadPresent() {
		return fmt.Errorf("hook input names %v but its payload is nil", x.HookOf())
	}
	if ev, ok := x.Payload.(*HookInput_StreamEvent); ok {
		if err := ev.StreamEvent.Validate(); err != nil {
			return fmt.Errorf("hook input: %w", err)
		}
	}
	return nil
}

// ValidateFor reports whether an input is a well-formed dispatch to hook.
//
// Every guest export must call this with its own hook. Removing the hook field
// from the envelope made a frame unable to contradict itself, but the export
// that was invoked still carries hook identity, and that pairing is exactly
// where a misdispatch shows up.
func (x *HookInput) ValidateFor(hook Hook) error {
	if err := x.Validate(); err != nil {
		return err
	}
	if got := x.HookOf(); got != hook {
		return fmt.Errorf("%v was dispatched a %v payload", hook, got)
	}
	return nil
}

// payloadPresent reports whether the oneof wrapper carries an actual message.
// See HookInput.payloadPresent — the same hole existed here.
func (x *HookResult) payloadPresent() bool {
	switch p := x.Payload.(type) {
	case *HookResult_ChatRequest:
		return p != nil && p.ChatRequest != nil
	case *HookResult_ChatResponse:
		return p != nil && p.ChatResponse != nil
	case *HookResult_StreamEvents:
		return p != nil && p.StreamEvents != nil
	case *HookResult_HttpResponse:
		return p != nil && p.HttpResponse != nil
	case *HookResult_TickOutcome:
		return p != nil && p.TickOutcome != nil
	}
	return false
}

// HookOf reports which hook a result belongs to, derived from its payload.
// A PASS or SUPPRESS result carries none, so this returns HOOK_UNSPECIFIED for
// them; use ValidateFor to check a result against the hook that was dispatched.
func (x *HookResult) HookOf() Hook {
	if x == nil {
		return Hook_HOOK_UNSPECIFIED
	}
	switch x.Payload.(type) {
	case *HookResult_ChatRequest:
		return Hook_HOOK_BEFORE_REQUEST
	case *HookResult_ChatResponse:
		return Hook_HOOK_AFTER_RESPONSE
	case *HookResult_StreamEvents:
		return Hook_HOOK_ON_STREAM_CHUNK
	case *HookResult_HttpResponse:
		return Hook_HOOK_ON_HTTP_REQUEST
	case *HookResult_TickOutcome:
		return Hook_HOOK_ON_TICK
	}
	return Hook_HOOK_UNSPECIFIED
}

// ValidateFor reports whether a result is a well-formed answer to hook.
//
// A hook that changed nothing returns zero bytes at the ABI level and never
// reaches here. Anything that does reach here is a frame the plugin chose to
// build, so it has to mean something specific.
func (x *HookResult) ValidateFor(hook Hook) error {
	if x == nil {
		return fmt.Errorf("hook result is nil")
	}

	switch x.Disposition {
	case Disposition_DISPOSITION_UNSPECIFIED:
		return fmt.Errorf("hook result states no disposition; " +
			"return zero bytes to pass through, or say REPLACE or SUPPRESS")

	case Disposition_DISPOSITION_PASS:
		if x.Payload != nil {
			return fmt.Errorf("hook result says PASS but carries a %v payload; "+
				"PASS means the host uses its own input, so the payload would be silently dropped",
				x.HookOf())
		}
		return nil

	case Disposition_DISPOSITION_SUPPRESS:
		if hook != Hook_HOOK_ON_STREAM_CHUNK {
			return fmt.Errorf("hook result says SUPPRESS, which only %v may do; "+
				"%v has nothing to suppress", Hook_HOOK_ON_STREAM_CHUNK, hook)
		}
		if x.Payload != nil {
			return fmt.Errorf("hook result says SUPPRESS but carries a payload; " +
				"use REPLACE with the events to emit instead")
		}
		return nil

	case Disposition_DISPOSITION_REPLACE:
		if x.Payload == nil {
			return fmt.Errorf("hook result says REPLACE but carries no payload; " +
				"return zero bytes to pass through instead")
		}
		if got := x.HookOf(); got != hook {
			return fmt.Errorf("hook result for %v carries a %v payload", hook, got)
		}
		if !x.payloadPresent() {
			return fmt.Errorf("hook result says REPLACE for %v but its payload is nil", hook)
		}
		// REPLACE with no events emits nothing, which is what SUPPRESS means.
		// Two encodings of one action is the ambiguity this contract exists to
		// remove, so only one of them is legal.
		if ev, ok := x.Payload.(*HookResult_StreamEvents); ok {
			if ev.StreamEvents == nil || len(ev.StreamEvents.Events) == 0 {
				return fmt.Errorf("hook result says REPLACE with no events, which emits " +
					"nothing; say SUPPRESS instead")
			}
			// A list of empty or nil events emits nothing either. Checking the
			// length alone left two more spellings of SUPPRESS.
			for i, e := range ev.StreamEvents.Events {
				if err := e.Validate(); err != nil {
					return fmt.Errorf("hook result REPLACE event %d: %w", i, err)
				}
			}
		}
		return nil
	}

	return fmt.Errorf("hook result states an unknown disposition %d", int32(x.Disposition))
}
