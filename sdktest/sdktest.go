//go:build !wasip1

// Package sdktest runs a plugin's hooks in-process, so a plugin can be tested
// with `go test` without a proxy, a WASM toolchain, or a sibling checkout.
//
// Before this existed a plugin's hooks were unreachable from its own tests:
// the non-WASM SDK build dropped every registration, so `init()` registered
// nothing and there was no handler to call. Authors worked around it by moving
// hook bodies into separately-testable pure functions, which left the hook
// itself — the part the host actually calls — untested. A suite could stay
// green while the hook was broken.
//
// A typical test:
//
//	func TestBlocksOnDetectedPII(t *testing.T) {
//		h := sdktest.New(t)
//		h.SetConfig(`{"on_error":"block"}`)
//		h.StubHostCall("torana_offload_completion", func(args string) (string, error) {
//			return sdktest.HostResultValue([]byte(`{"completion":"EMAIL"}`)), nil
//		})
//
//		res := h.BeforeRequest(&pbv2.ChatRequest{Messages: []*pbv2.Message{
//			{Role: "tool", Content: "contact: someone@example.com"},
//		}})
//
//		if res.Block == nil {
//			t.Fatal("expected the request to be blocked")
//		}
//	}
package sdktest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// LogEntry is one captured sdk.Log call.
type LogEntry struct {
	Message string
	Level   int32
}

// MetricEntry is one captured sdk.EmitMetric call.
type MetricEntry struct {
	Name   string
	Type   int32
	Value  float64
	Labels map[string]string
}

// HostCallEntry records a host call the plugin made, in order. Assert on these
// to prove a plugin asked for what you expect — and, just as usefully, that it
// did not ask for anything it never declared a permission for.
type HostCallEntry struct {
	Command string
	Args    string
	Result  string
}

// Harness is a fake Torana host. Create one with New.
type Harness struct {
	t    testing.TB
	host *sdk.TestHost

	mu       sync.Mutex
	meta     map[string]string
	cache    map[string]string
	state    map[string]string
	config   string
	stubs    map[string]func(args string) (string, error)
	logs     []LogEntry
	metrics  []MetricEntry
	calls    []HostCallEntry
	now      func() int64
	original []byte
	origResp []byte

	// StateConfigured mirrors a host with no durable state store: when false,
	// env.state_* answers exactly as the real host does with StateSetFunc nil.
	StateConfigured bool
}

// Reset clears every registered hook handler.
//
// Most plugin tests never need this, and calling it in one would unregister the
// plugin: a plugin registers in init(), once per process, and those handlers
// must survive the whole run.
//
// It is for tests that register handlers THEMSELVES — typically the SDK's own,
// which install a different handler per case.
func Reset() { sdk.ResetRegistrations() }

// New builds a fake host for this test. Registrations made by the plugin's
// init() are already in place by the time a test runs, so no wiring is needed.
//
// The harness is installed only for the duration of each hook dispatch, not
// for the lifetime of the test — see dispatch.go. That keeps t.Parallel() tests
// from overwriting one another's host, at the cost of serializing the
// dispatches themselves.
func New(t testing.TB) *Harness {
	t.Helper()
	h := &Harness{
		t:               t,
		meta:            map[string]string{},
		cache:           map[string]string{},
		state:           map[string]string{},
		config:          "{}",
		stubs:           map[string]func(string) (string, error){},
		now:             func() int64 { return time.Now().UnixMilli() },
		StateConfigured: true,
	}
	// New deliberately does NOT clear registrations.
	//
	// A real plugin registers in init(), which runs once per process. Clearing
	// them on cleanup would mean the first test passes and every later one
	// fails with "no handler registered" — breaking sdktest for exactly the case
	// it exists to serve, while helping only tests that register dynamically.
	//
	// Tests that register per-case call Reset() themselves.

	h.host = &sdk.TestHost{
		HostCall: h.hostCallBytes,
		Log: func(msg string, level int32) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.logs = append(h.logs, LogEntry{Message: msg, Level: level})
		},
		Metric: func(name string, typ int32, value float64, labels map[string]string) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.metrics = append(h.metrics, MetricEntry{Name: name, Type: typ, Value: value, Labels: labels})
		},
	}
	return h
}

// with installs this harness for one dispatch. Every hook entry point in
// dispatch.go goes through it.
func (h *Harness) with(fn func()) {
	sdk.WithTestHost(h.host, fn)
}

// Run executes fn synchronously with this harness installed as the host.
//
// Hook dispatch already does this, so Run is for the code AROUND a hook: real
// plugins factor logic into helpers that call MetaGet, CacheSet and friends,
// and testing those directly should not require building a hook and a request
// to reach them. Without it, a host call made outside a dispatch reaches no
// host at all and fails with an empty reply, which reads like a broken SDK
// rather than a missing harness.
//
// Do NOT call BeforeRequest, AfterResponse or any other dispatch method from
// inside fn. Those install the host themselves, and installing it twice
// re-acquires the same global dispatch mutex — the test deadlocks rather than
// failing, which is markedly harder to diagnose. Call dispatch methods
// directly; use Run only for code that is not already inside one.
//
// fn runs on the calling goroutine. The host is uninstalled when it returns,
// so a goroutine started inside fn that outlives it will not find a host.
func (h *Harness) Run(fn func()) {
	h.t.Helper()
	h.with(fn)
}

// SetConfig sets what env.plugin_config returns — the raw JSON an operator
// would have saved for this plugin.
func (h *Harness) SetConfig(raw string) *Harness {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.config = raw
	return h
}

// StubHostCall overrides one command. Use it for the calls the harness cannot
// emulate meaningfully — offload completions, egress, pricing — and for
// forcing error paths.
func (h *Harness) StubHostCall(cmd string, fn func(args string) (string, error)) *Harness {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stubs[cmd] = fn
	return h
}

// DenyPermission makes cmd answer with the host's permission-denied envelope,
// so a plugin's handling of a refused capability is testable. Typed v2 commands
// get a HostCallResult error arm; transitional JSON commands keep the legacy
// denial string.
func (h *Harness) DenyPermission(cmd string) *Harness {
	return h.StubHostCall(cmd, func(string) (string, error) {
		if typedHostReply(cmd) {
			return string(hostCallResultError(
				pbv2.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "permission denied")), nil
		}
		return `{"status":"error","message":"permission denied"}`, nil
	})
}

// SetNow fixes the clock env.now reports, so time-dependent logic is
// deterministic.
func (h *Harness) SetNow(ms int64) *Harness {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.now = func() int64 { return ms }
	return h
}

// SetOriginalRequest sets what env.original_request returns.
func (h *Harness) SetOriginalRequest(req *pbv2.ChatRequest) *Harness {
	raw, err := proto.Marshal(req)
	if err != nil {
		h.t.Fatalf("sdktest: marshal original request: %v", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.original = raw
	return h
}

// SetOriginalResponse sets what env.original_response returns — the raw
// upstream body.
func (h *Harness) SetOriginalResponse(body []byte) *Harness {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.origResp = body
	return h
}

// Seed helpers for the stores, so a test can start from a warm cache or an
// existing durable state without driving the plugin through a prior request.

func (h *Harness) SeedCache(key, value string) *Harness {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cache[key] = value
	return h
}

func (h *Harness) SeedState(key, value string) *Harness {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state[key] = value
	return h
}

// Logs, Metrics and Calls return what the plugin did, in order.

func (h *Harness) Logs() []LogEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]LogEntry(nil), h.logs...)
}

func (h *Harness) Metrics() []MetricEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]MetricEntry(nil), h.metrics...)
}

func (h *Harness) Calls() []HostCallEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]HostCallEntry(nil), h.calls...)
}

// Cache and State expose the stores so a test can assert what the plugin
// wrote across a request boundary.

func (h *Harness) Cache(key string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.cache[key]
	return v, ok
}

func (h *Harness) State(key string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.state[key]
	return v, ok
}

func (h *Harness) hostCallBytes(cmd string, args []byte) ([]byte, error) {
	h.mu.Lock()
	stub := h.stubs[cmd]
	h.mu.Unlock()

	argsStr := string(args)
	var res string
	if stub != nil {
		var err error
		res, err = stub(argsStr)
		if err != nil {
			return nil, err
		}
	} else if typedHostReply(cmd) {
		raw, err := h.builtinTyped(cmd, args)
		if err != nil {
			return nil, err
		}
		h.mu.Lock()
		h.calls = append(h.calls, HostCallEntry{Command: cmd, Args: argsStr, Result: string(raw)})
		h.mu.Unlock()
		return raw, nil
	} else {
		res = h.builtin(cmd, argsStr)
	}

	h.mu.Lock()
	h.calls = append(h.calls, HostCallEntry{Command: cmd, Args: argsStr, Result: res})
	h.mu.Unlock()
	return []byte(res), nil
}

func typedHostReply(cmd string) bool {
	switch cmd {
	case "env.block_request", "env.respond_request", "env.route_request",
		"env.set_identity", pbv2.MetaAppendCommand,
		"env.meta_get", "env.meta_set", "env.cache_get", "env.cache_set":
		return true
	default:
		// Extension commands (torana_*, verify_virtual_key) also speak the v2
		// result envelope — only their ARGUMENT body is opaque. Framing them
		// as legacy JSON would make HostCallExtension unusable here, which is
		// how the typed meta/cache helpers were unusable before.
		return isExtensionCommand(cmd)
	}
}

// isExtensionCommand reports whether cmd is a host-feature call rather than a
// core ABI operation. Core commands are the env.* namespace; everything else
// reaching the harness is an extension.
func isExtensionCommand(cmd string) bool {
	return cmd != "" && !strings.HasPrefix(cmd, "env.")
}

func hostCallResultValue(value []byte) []byte {
	raw, _ := proto.Marshal(&pbv2.HostCallResult{
		Result: &pbv2.HostCallResult_Value{Value: value},
	})
	return raw
}

func hostCallResultError(code pbv2.ErrorCode, msg string) []byte {
	raw, _ := proto.Marshal(&pbv2.HostCallResult{
		Result: &pbv2.HostCallResult_Error{Error: &pbv2.HostError{Code: code, Message: msg}},
	})
	return raw
}

func (h *Harness) builtinTyped(cmd string, args []byte) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	switch cmd {
	case "env.block_request":
		var a pbv2.BlockRequestArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid BlockRequestArgs"), nil
		}
		if err := a.Validate(); err != nil {
			return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error()), nil
		}
		return hostCallResultValue(nil), nil

	case "env.respond_request":
		var a pbv2.RespondRequestArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid RespondRequestArgs"), nil
		}
		if err := a.Validate(); err != nil {
			return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error()), nil
		}
		return hostCallResultValue(nil), nil

	case "env.route_request":
		var a pbv2.RouteRequestArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid RouteRequestArgs"), nil
		}
		if err := a.Validate(); err != nil {
			return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error()), nil
		}
		return hostCallResultValue(nil), nil

	case "env.set_identity":
		var a pbv2.SetIdentityArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid SetIdentityArgs"), nil
		}
		if err := a.Validate(); err != nil {
			return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error()), nil
		}
		return hostCallResultValue(nil), nil

	case pbv2.MetaAppendCommand:
		var a pbv2.MetaAppendArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid MetaAppendArgs"), nil
		}
		if err := a.Validate(); err != nil {
			return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error()), nil
		}
		key := "block:" + strconv.FormatInt(int64(a.BlockIndex), 10)
		existing, present := h.meta[key]
		// Mutate storage with amortized growth (string concat here is fine for tests).
		if len(a.Fragment) != 0 {
			if present {
				h.meta[key] = existing + string(a.Fragment)
			} else {
				h.meta[key] = string(a.Fragment)
			}
			present = true
			existing = h.meta[key]
		}
		val := pbv2.MetaAppendSuccessValue(a.Fragment, []byte(existing), present)
		return hostCallResultValue(val), nil
	case "env.meta_get":
		var a pbv2.MetaGetArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid MetaGetArgs"), nil
		}
		if err := a.Validate(); err != nil {
			return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error()), nil
		}
		// Presence, not emptiness. Returning "" for a missing key would make
		// the harness disagree with the contract in the one place an author
		// would trust it.
		v, present := h.meta[a.Key]
		if !present {
			return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_NOT_FOUND, "meta key not found"), nil
		}
		return hostCallResultValue([]byte(v)), nil

	case "env.meta_set":
		var a pbv2.MetaSetArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid MetaSetArgs"), nil
		}
		if err := a.Validate(); err != nil {
			// Rejected arguments must not mutate. A harness that stored the
			// value anyway would hide the failure from the test asserting it.
			return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error()), nil
		}
		// An empty value STORES an empty value; it is not a delete.
		h.meta[a.Key] = a.Value
		return hostCallResultValue(nil), nil

	case "env.cache_get":
		var a pbv2.CacheGetArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid CacheGetArgs"), nil
		}
		if err := a.Validate(); err != nil {
			return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error()), nil
		}
		v, present := h.cache[a.Key]
		if !present {
			return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_NOT_FOUND, "cache key not found"), nil
		}
		return hostCallResultValue([]byte(v)), nil

	case "env.cache_set":
		var a pbv2.CacheSetArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid CacheSetArgs"), nil
		}
		if err := a.Validate(); err != nil {
			return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error()), nil
		}
		h.cache[a.Key] = a.Value
		return hostCallResultValue(nil), nil
	}
	if isExtensionCommand(cmd) {
		// The harness cannot emulate a host feature, so an extension command
		// answers NOT_CONFIGURED unless the test stubs it. That is the honest
		// answer -- a harness with no compaction backend really does not have
		// one -- and it is framed, so a plugin's degrade path is exercised
		// rather than a decode failure.
		return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_NOT_CONFIGURED,
			"extension command "+cmd+" is not configured in sdktest; StubHostCall it"), nil
	}
	return hostCallResultError(pbv2.ErrorCode_ERROR_CODE_NOT_FOUND, "unknown typed command"), nil
}

// builtin answers the commands the harness can emulate faithfully. Replies
// match internal/wasm/runtime.go byte for byte, including its error envelopes
// and its several inconsistent shapes — a harness that answered more tidily
// than the host would let tests pass against responses no plugin will ever
// see in production.
func (h *Harness) builtin(cmd, args string) string {
	h.mu.Lock()
	defer h.mu.Unlock()

	switch cmd {
	case "env.plugin_config":
		return h.config

	case "env.meta_set":
		k, v, ok := decodeKV(args)
		if !ok {
			return `{"status":"error","message":"invalid payload"}`
		}
		h.meta[k] = v
		return `{"status":"ok"}`
	case "env.meta_get":
		return h.meta[args]

	case "env.cache_set":
		k, v, ok := decodeKV(args)
		if !ok {
			return `{"status":"error","message":"invalid payload"}`
		}
		h.cache[k] = v
		return `{"status":"ok"}`
	case "env.cache_get":
		return h.cache[args]

	case "env.state_set":
		k, v, ok := decodeKV(args)
		if !ok {
			return `{"status":"error","message":"invalid payload"}`
		}
		if !h.StateConfigured {
			return `{"status":"error","message":"durable plugin state is not configured"}`
		}
		if v == "" {
			delete(h.state, k)
		} else {
			h.state[k] = v
		}
		return `{"status":"ok"}`
	case "env.state_get":
		if !h.StateConfigured {
			return ""
		}
		return h.state[args]
	case "env.state_keys":
		if !h.StateConfigured {
			return "[]"
		}
		keys := make([]string, 0, len(h.state))
		for k := range h.state {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b, _ := json.Marshal(keys)
		return string(b)

	case "env.now":
		return strconv.FormatInt(h.now(), 10)

	case "env.original_request":
		return string(h.original)
	case "env.original_response":
		return string(h.origResp)

	// Unconfigured-host answers, matching runtime.go exactly. Stub these when
	// a test needs them to succeed.
	case "torana_send_request":
		return `{"status":"error","message":"plugin egress is not configured"}`
	case "torana_cache_pricing":
		return `{"status":"unavailable","reason":"pricing_unconfigured"}`
	case "torana_evaluate_compaction":
		return `{"apply":false,"reason":"no economics configured"}`
	case "torana_record_savings", "torana_plugin_counter":
		return `{"status":"ok"}`
	}
	return fmt.Sprintf(`{"status":"error","message":"unknown command %q"}`, cmd)
}

func decodeKV(args string) (string, string, bool) {
	var kv struct {
		Key   string `json:"key"`
		Value any    `json:"value"`
	}
	if err := json.Unmarshal([]byte(args), &kv); err != nil {
		return "", "", false
	}
	switch v := kv.Value.(type) {
	case nil:
		return kv.Key, "", true
	case string:
		return kv.Key, v, true
	default:
		b, _ := json.Marshal(v)
		return kv.Key, string(b), true
	}
}

// CheckManifest cross-checks plugin.json against what the plugin actually
// registered. Both directions fail silently in production: a declared hook
// with no handler loads healthy and never acts, and a registered handler for
// an undeclared hook is never dispatched.
func CheckManifest(t testing.TB, dir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		t.Fatalf("sdktest: read manifest: %v", err)
	}
	var m struct {
		Hooks []struct {
			Name string `json:"name"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("sdktest: parse manifest: %v", err)
	}

	declared := map[string]bool{}
	for _, hk := range m.Hooks {
		declared[hk.Name] = true
	}
	registered := map[string]bool{}
	for _, name := range sdk.RegisteredHooks() {
		registered[name] = true
	}

	for name := range declared {
		if !registered[name] {
			t.Errorf("plugin.json declares hook %q but no handler is registered — "+
				"the host would load this plugin healthy and it would never act", name)
		}
	}
	for name := range registered {
		if !declared[name] {
			t.Errorf("a handler is registered for %q but plugin.json does not declare it — "+
				"the host skips undeclared hooks, so this handler would never be called", name)
		}
	}
}

// HostResultValue frames a successful host reply for StubHostCall.
//
// Typed and extension commands speak HostCallResult, so a stub returning a
// bare payload produces a decode failure rather than the value it meant. There
// was no public way to build one, which made StubHostCall unusable for exactly
// the commands most worth stubbing — an extension backend a plugin degrades
// without.
func HostResultValue(value []byte) string {
	return string(hostCallResultValue(value))
}

// HostResultError frames a classified refusal for StubHostCall, so a plugin's
// degrade path can be tested with the code it will really see.
//
// Panics on an unspecified or unknown code. The whole purpose of this
// constructor is to produce a CLASSIFIED refusal; returning a frame that
// HostCallResult.Validate rejects would surface as a protocol error inside the
// plugin under test, and the author would debug their plugin instead of their
// fixture. A test that deliberately wants a malformed frame can return a raw
// string from StubHostCall.
func HostResultError(code pbv2.ErrorCode, msg string) string {
	frame := &pbv2.HostError{Code: code, Message: msg}
	if err := frame.Validate(); err != nil {
		panic(fmt.Sprintf("sdktest: HostResultError(%v): %v — this constructor "+
			"builds classified refusals; return a raw string from StubHostCall "+
			"if you want a malformed frame", code, err))
	}
	return string(hostCallResultError(code, msg))
}
