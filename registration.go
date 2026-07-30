package plugin_sdk

import "reflect"

// Hook registration is static configuration, so registering one twice is a
// programming error and fails during initialization.
//
// Until now the second registration silently replaced the first. Nothing
// reported it, so a plugin that grew a second file — or copied a snippet into
// the wrong init() — lost a handler with no error anywhere, and the symptom was
// a hook that simply never ran. That is the same shape as registering in
// main() instead of init(), which is the most expensive footgun in the v1 SDK.
//
// Panicking is right here in a way it usually is not. This runs at
// _initialize, before the host has dispatched anything, so it surfaces as a
// plugin that fails to load rather than as a broken request. The plugin still
// compiles — this is not a build error — and a plugin whose handlers are
// ambiguous should not load at all.
//
// There is deliberately no multi-handler support. Two handlers for one hook
// have no defined order, and inventing one would attach meaning to registration
// order that an author cannot see. If composition is ever wanted it gets an
// explicit ordered API; StreamHandler already covers the case that motivates
// it, by letting one handler carry several interests.

// registered tracks which hooks already have a handler.
var registered = map[string]bool{}

// claimHook records that hook is being registered, and panics if it already
// was. The message names the hook, because "registered more than once" with no
// name sends the reader looking through every file.
func claimHook(hook string, handler any) {
	// A nil handler installs nothing and still claims the hook, so the author
	// cannot correct it — the next registration panics as a duplicate. Reject
	// it where the mistake was made rather than at the first dispatch, which is
	// a request that has already reached a user.
	if handler == nil || reflect.ValueOf(handler).IsNil() {
		panic("torana sdk: " + hook + " registered with a nil handler — " +
			"it would claim the hook, run nothing, and block any later registration")
	}
	if registered[hook] {
		panic("torana sdk: " + hook + " registered more than once — " +
			"a hook may have exactly one handler, and the second would have " +
			"silently replaced the first. Combine the logic into one handler, " +
			"or use StreamHandler if you need several interests in a stream.")
	}
	registered[hook] = true
}

// resetRegistrations clears the record AND the handlers themselves.
//
// Clearing only the bookkeeping map would let a later registration succeed
// while the previous handler stayed installed — so a test asserting "no handler
// registered" would still find one, and the reset would look like it worked.
func resetRegistrations() {
	for k := range registered {
		delete(registered, k)
	}
	beforeRequestHandler = nil
	afterResponseHandler = nil
	streamChunkHandler = nil
	httpRequestHandler = nil
	tickHandler = nil
}

// Hook names, so registration and the manifest cannot drift apart by a typo.
const (
	HookBeforeRequest = "run_before_request"
	HookAfterResponse = "run_after_response"
	HookStreamChunk   = "run_on_stream_chunk"
	HookHTTPRequest   = "run_on_http_request"
	HookTick          = "run_on_tick"
)
