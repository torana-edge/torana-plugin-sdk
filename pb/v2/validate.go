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
	case *HookInput_AfterResponse:
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
	case *HookInput_AfterResponse:
		// The wrapper itself must be present; an empty ChatResponse inside it
		// is still a real after-response dispatch (e.g. an error with no body).
		return p != nil && p.AfterResponse != nil
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
// With a single run_hook export, the host cannot deliver a payload to the
// wrong named export — that class of misdispatch is gone. ValidateFor remains
// for SDK-internal dispatch: a trampoline that routes by HookOf still checks
// that a handwritten guest did not hand the after-response handler a tick.
func (x *HookInput) Validate() error {
	if x == nil {
		return fmt.Errorf("hook input is nil")
	}
	if x.HookOf() == Hook_HOOK_UNSPECIFIED {
		// A payload this build cannot name unmarshals with Payload nil and its
		// bytes in unknown fields, the same way an unknown action does on
		// HookResult.
		//
		// The check is narrower here, and deliberately so. HookResult is
		// nothing BUT its oneof, so any unknown top-level field is an action
		// and is refused unconditionally. HookInput also carries top-level
		// scalars — abi_minor, request_id — so an unknown top-level field may
		// instead be a scalar a later minor added. Those are additive and
		// advisory by construction: a guest that ignores one behaves as it did
		// before, which is what abi_minor exists to let it negotiate. Refusing
		// them would make every additive host change a breaking one.
		//
		// So the two cases are distinguished by what is MISSING, not by what is
		// unknown: no payload plus unknown bytes means the payload is the part
		// this build cannot name, and there is no hook to dispatch to.
		if len(x.ProtoReflect().GetUnknown()) != 0 {
			return fmt.Errorf("hook input carries a payload this build does not " +
				"recognise; it was produced by a newer ABI and cannot be dispatched")
		}
		return fmt.Errorf("hook input carries no payload, so there is no hook to dispatch")
	}
	if !x.payloadPresent() {
		return fmt.Errorf("hook input names %v but its payload is nil", x.HookOf())
	}
	if ar, ok := x.Payload.(*HookInput_AfterResponse); ok {
		// AfterResponse with a typed-nil nested response is still "present" as
		// a wrapper, but nothing usable to hand a handler. Refuse it here.
		if ar.AfterResponse.Response == nil {
			return fmt.Errorf("hook input names %v but its response is nil", x.HookOf())
		}
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
// Guests use this when routing a decoded HookInput to a registered handler.
// The WASM export is no longer a second discriminator — there is only
// run_hook — so this catches SDK-level and handwritten miswiring, not host
// export-name mistakes.
func (x *HookInput) ValidateFor(hook Hook) error {
	if err := x.Validate(); err != nil {
		return err
	}
	if got := x.HookOf(); got != hook {
		return fmt.Errorf("%v was dispatched a %v payload", hook, got)
	}
	return nil
}

// actionPresent reports whether the oneof wrapper carries an actual message.
// See HookInput.payloadPresent — the same typed-nil hole existed here.
func (x *HookResult) actionPresent() bool {
	switch a := x.Action.(type) {
	case *HookResult_ReplaceRequest:
		return a != nil && a.ReplaceRequest != nil
	case *HookResult_ReplaceResponse:
		return a != nil && a.ReplaceResponse != nil
	case *HookResult_EmitEvents:
		return a != nil && a.EmitEvents != nil
	case *HookResult_ServeHttp:
		return a != nil && a.ServeHttp != nil
	case *HookResult_TickOutcome:
		return a != nil && a.TickOutcome != nil
	case *HookResult_Suppress:
		return a != nil && a.Suppress != nil
	}
	return false
}

// HookOf reports which hook an action belongs to.
//
// Suppress belongs to the stream hook and nothing else, so it reports that.
// A result with no action reports HOOK_UNSPECIFIED, which is also what an
// empty frame means — and an empty frame is pass-through, not an error.
func (x *HookResult) HookOf() Hook {
	if x == nil {
		return Hook_HOOK_UNSPECIFIED
	}
	switch x.Action.(type) {
	case *HookResult_ReplaceRequest:
		return Hook_HOOK_BEFORE_REQUEST
	case *HookResult_ReplaceResponse:
		return Hook_HOOK_AFTER_RESPONSE
	case *HookResult_EmitEvents, *HookResult_Suppress:
		return Hook_HOOK_ON_STREAM_CHUNK
	case *HookResult_ServeHttp:
		return Hook_HOOK_ON_HTTP_REQUEST
	case *HookResult_TickOutcome:
		return Hook_HOOK_ON_TICK
	}
	return Hook_HOOK_UNSPECIFIED
}

// ValidateFor reports whether a result is a well-formed answer to hook.
//
// A hook that wants nothing done returns zero bytes, which never reaches here.
// A result with no action set marshals to zero bytes too, so it means the same
// thing — there is one encoding of pass-through, not two.
//
// Everything else is a deliberate action, so it has to be one this hook can
// take, and it has to carry something.
func (x *HookResult) ValidateFor(hook Hook) error {
	if x == nil {
		return fmt.Errorf("hook result is nil")
	}
	// Every top-level field of HookResult is a member of the action oneof, so
	// an unknown top-level field is a future action — there is no other kind of
	// field it could be. Honouring the rest of the frame would silently DISCARD
	// what a newer guest asked for. Checking this only when Action == nil caught
	// the case when the future action arrived ALONE; a known action alongside a
	// future one still validated, and the host executed the half it understood.
	//
	// Multiple KNOWN arms are a different case: protobuf unmarshal applies
	// last-known-arm-wins and leaves no unknown bytes, so this method cannot
	// see them. The host must decode guest frames with DecodeHookResult, which
	// inspects the wire before unmarshal.
	//
	// Additive evolution of an EXISTING action is unaffected: protobuf stores
	// unknown fields of a nested message on that message, not here. See
	// HookInput.Validate for why the same rule would be wrong there.
	if len(x.ProtoReflect().GetUnknown()) != 0 {
		return fmt.Errorf("hook result carries an action this build does not recognise")
	}
	if x.Action == nil {
		// Genuinely empty: indistinguishable on the wire from returning
		// nothing, and means the same — leave the input alone.
		return nil
	}
	if !x.actionPresent() {
		return fmt.Errorf("hook result names an action for %v but carries nothing", x.HookOf())
	}
	if got := x.HookOf(); got != hook {
		return fmt.Errorf("hook result for %v carries a %v action", hook, got)
	}

	if ev, ok := x.Action.(*HookResult_EmitEvents); ok {
		if len(ev.EmitEvents.Events) == 0 {
			return fmt.Errorf("hook result emits no events, which emits nothing; " +
				"use suppress instead")
		}
		// A list of empty or nil events emits nothing either. Checking the
		// length alone left two more spellings of suppression.
		for i, e := range ev.EmitEvents.Events {
			if err := e.Validate(); err != nil {
				return fmt.Errorf("hook result event %d: %w", i, err)
			}
		}
	}
	return nil
}
