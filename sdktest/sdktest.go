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
//		h.StubModelComplete(func(args *pbv1.ModelCompleteArgs) (*pbv1.ModelCompleteResult, *pbv1.HostError, error) {
//			return &pbv1.ModelCompleteResult{Content: "summary"}, nil, nil
//		})
//
//		res := h.BeforeRequest(&pbv1.ChatRequest{Messages: []*pbv1.Message{
//			{Role: "user", Blocks: []*pbv1.RequestBlock{{
//				Kind: &pbv1.RequestBlock_Text{Text: &pbv1.RequestTextBlock{Text: "hi"}},
//			}}},
//			{Role: "tool", Blocks: []*pbv1.RequestBlock{{
//				Kind: &pbv1.RequestBlock_ToolResult{ToolResult: &pbv1.RequestToolResultBlock{
//					ToolCallId: "t1",
//					Content: []*pbv1.ToolResultContentBlock{{
//						Kind: &pbv1.ToolResultContentBlock_Text{Text: &pbv1.ToolResultTextBlock{Text: "contact: someone@example.com"}},
//					}},
//				}},
//			}}},
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
	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
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
	Command   string
	Args      string
	Result    string
	Effective bool
	index     int
}

// Harness is a fake Torana host. Create one with New.
type Harness struct {
	t    testing.TB
	host *sdk.TestHost

	// scopeMu serializes the complete request-metadata install, dispatch,
	// capture, and restore transaction. It must remain distinct from mu:
	// host calls made during the dispatch acquire mu themselves.
	scopeMu      sync.Mutex
	mu           sync.Mutex
	meta         map[string]string
	cache        map[string]string
	cacheExpiry  map[string]int64
	sharedCache  map[string]string
	sharedExpiry map[string]int64
	state        map[string]string
	stateVersion map[string]string
	stateSeq     uint64
	files        map[string][]byte
	credentials  map[string][]byte
	config       string
	stubs        map[string]func(args string) (string, error)
	logs         []LogEntry
	metrics      []MetricEntry
	calls        []HostCallEntry
	accepted     []HostCallEntry
	permissions  map[string]bool
	hooks        map[string]bool
	active       *Request
	now          func() int64
	// Presence is tracked separately from the byte slices. An all-default
	// ChatRequest marshals to zero bytes and an upstream body can legitimately
	// be empty, so length is not presence — a harness that conflated them
	// could not test the contract it exists to pin.
	original    []byte
	originSet   bool
	origResp    []byte
	origRespSet bool

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
		cacheExpiry:     map[string]int64{},
		sharedCache:     map[string]string{},
		sharedExpiry:    map[string]int64{},
		state:           map[string]string{},
		stateVersion:    map[string]string{},
		files:           map[string][]byte{},
		credentials:     map[string][]byte{},
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
			if h.permissions != nil && !h.permissions["env.log"] {
				return
			}
			h.logs = append(h.logs, LogEntry{Message: msg, Level: level})
		},
		Metric: func(name string, typ int32, value float64, labels map[string]string) {
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.permissions != nil && !h.permissions["env.emit_metric"] {
				return
			}
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
// emulate meaningfully — custom extensions and egress — and for
// forcing error paths.
func (h *Harness) StubHostCall(cmd string, fn func(args string) (string, error)) *Harness {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stubs[cmd] = fn
	return h
}

// WithPermissions enables manifest-like permission enforcement for this
// harness. An unset permission set keeps the historical unrestricted fixture
// mode; once configured, refusals happen before any stub callback runs.
func (h *Harness) WithPermissions(permissions []string) *Harness {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.permissions = make(map[string]bool, len(permissions))
	for _, p := range permissions {
		if !sdk.IsPermission(p) {
			h.t.Fatalf("sdktest: unknown permission %q", p)
		}
		h.permissions[p] = true
	}
	return h
}

// WithHooks constrains dispatch to the hooks declared by a manifest.
func (h *Harness) WithHooks(hooks []string) *Harness {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hooks = make(map[string]bool, len(hooks))
	for _, hook := range hooks {
		if !sdk.IsHook(hook) {
			h.t.Fatalf("sdktest: unknown hook %q", hook)
		}
		h.hooks[hook] = true
	}
	return h
}

func (h *Harness) hookAllowed(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hooks == nil || h.hooks[name]
}

// StubModelComplete installs a typed model-service double. The callback sees
// the exact provider-neutral request the plugin issued; sdktest owns protobuf
// framing so plugin tests cannot accidentally return a legacy JSON envelope.
func (h *Harness) StubModelComplete(fn func(*pbv1.ModelCompleteArgs) (*pbv1.ModelCompleteResult, *pbv1.HostError, error)) *Harness {
	return h.StubHostCall("env.model_complete", func(args string) (string, error) {
		var request pbv1.ModelCompleteArgs
		if err := proto.Unmarshal([]byte(args), &request); err != nil {
			return HostResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid ModelCompleteArgs"), nil
		}
		result, refusal, err := fn(&request)
		if err != nil {
			return "", err
		}
		if refusal != nil {
			return HostResultError(refusal.Code, refusal.Message), nil
		}
		if result == nil {
			return HostResultValue(nil), nil
		}
		raw, err := proto.Marshal(result)
		if err != nil {
			return "", err
		}
		return HostResultValue(raw), nil
	})
}

// StubModelPricing installs a typed named-pricing double.
func (h *Harness) StubModelPricing(fn func(*pbv1.ModelPricingGetArgs) (*pbv1.ModelPricing, *pbv1.HostError, error)) *Harness {
	return h.StubHostCall("env.model_pricing", func(args string) (string, error) {
		var request pbv1.ModelPricingGetArgs
		if err := proto.Unmarshal([]byte(args), &request); err != nil {
			return HostResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid ModelPricingGetArgs"), nil
		}
		result, refusal, err := fn(&request)
		if err != nil {
			return "", err
		}
		if refusal != nil {
			return HostResultError(refusal.Code, refusal.Message), nil
		}
		if result == nil {
			return HostResultValue(nil), nil
		}
		raw, err := proto.Marshal(result)
		if err != nil {
			return "", err
		}
		return HostResultValue(raw), nil
	})
}

// StubPromptCachePolicy installs a typed named prompt-cache-policy double.
func (h *Harness) StubPromptCachePolicy(fn func(*pbv1.PromptCachePolicyGetArgs) (*pbv1.PromptCachePolicy, *pbv1.HostError, error)) *Harness {
	return h.StubHostCall("env.cache_policy", func(args string) (string, error) {
		var request pbv1.PromptCachePolicyGetArgs
		if err := proto.Unmarshal([]byte(args), &request); err != nil {
			return HostResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid PromptCachePolicyGetArgs"), nil
		}
		result, refusal, err := fn(&request)
		if err != nil {
			return "", err
		}
		if refusal != nil {
			return HostResultError(refusal.Code, refusal.Message), nil
		}
		if result == nil {
			return HostResultValue(nil), nil
		}
		raw, err := proto.Marshal(result)
		if err != nil {
			return "", err
		}
		return HostResultValue(raw), nil
	})
}

// StubResourceInfo installs a typed resource metadata double.
func (h *Harness) StubResourceInfo(fn func(*pbv1.ResourceInfoArgs) (*pbv1.ResourceInfo, *pbv1.HostError, error)) *Harness {
	return h.StubHostCall("env.resource_info", func(raw string) (string, error) {
		var a pbv1.ResourceInfoArgs
		if err := proto.Unmarshal([]byte(raw), &a); err != nil {
			return string(hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid ResourceInfoArgs")), nil
		}
		v, he, err := fn(&a)
		if err != nil {
			return "", err
		}
		if he != nil {
			return string(hostCallResultError(he.Code, he.Message)), nil
		}
		b, err := proto.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(hostCallResultValue(b)), nil
	})
}

// DenyPermission makes cmd answer with the host's permission-denied envelope,
// so a plugin's handling of a refused capability is testable.
func (h *Harness) DenyPermission(cmd string) *Harness {
	return h.StubHostCall(cmd, func(string) (string, error) {
		return string(hostCallResultError(
			pbv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "permission denied")), nil
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
func (h *Harness) SetOriginalRequest(req *pbv1.ChatRequest) *Harness {
	raw, err := proto.Marshal(req)
	if err != nil {
		h.t.Fatalf("sdktest: marshal original request: %v", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.original = raw
	h.originSet = true
	return h
}

// SetOriginalResponse sets what env.original_response returns — the raw
// upstream body.
func (h *Harness) SetOriginalResponse(body []byte) *Harness {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.origResp = body
	h.origRespSet = true
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

// SeedSharedCache warms the CROSS-PLUGIN cache, which sdk.SharedCacheGet reads
// and SeedCache does not touch.
//
// The two stores are deliberately separate — a plugin's private cache is its
// own, the shared one is how plugins hand each other derived facts — so a test
// has to say which it means. Seeding the private store for a plugin that reads
// the shared one produces a miss, and a cache miss usually looks like the
// plugin simply choosing not to use the value.
func (h *Harness) SeedSharedCache(key, value string) *Harness {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sharedCache[key] = value
	return h
}

func (h *Harness) SeedState(key, value string) *Harness {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state[key] = value
	return h
}

// SetCredential binds a test value to a plugin-declared credential slot.
func (h *Harness) SetCredential(slot string, value []byte) *Harness {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.credentials[slot] = append([]byte(nil), value...)
	return h
}

// SeedFile initializes one plugin-private logical file.
func (h *Harness) SeedFile(path string, value []byte) *Harness {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.files[path] = append([]byte(nil), value...)
	return h
}

// File returns a defensive copy of one plugin-private logical file.
func (h *Harness) File(path string) ([]byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	value, ok := h.files[path]
	return append([]byte(nil), value...), ok
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

// SharedCache reads the cross-plugin store, so a test can assert what the
// plugin published for other plugins rather than only what it kept.
func (h *Harness) SharedCache(key string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.sharedCache[key]
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
	if h.active != nil && h.active.invocationHook != "" {
		spec, ok := sdk.Command(cmd)
		if !ok || !containsString(spec.Hooks, h.active.invocationHook) {
			denied := []byte(HostResultError(pbv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "command unavailable in this hook"))
			entry := HostCallEntry{Command: cmd, Args: string(args), Result: string(denied)}
			entry.index = len(h.calls)
			h.calls = append(h.calls, entry)
			h.active.calls = append(h.active.calls, entry)
			h.mu.Unlock()
			return denied, nil
		}
	}
	if h.permissions != nil {
		permission, ok := sdk.CommandPermission(cmd)
		if !ok || !h.permissions[permission] {
			denied := []byte(HostResultError(pbv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, "permission denied"))
			entry := HostCallEntry{Command: cmd, Args: string(args), Result: string(denied)}
			entry.index = len(h.calls)
			h.calls = append(h.calls, entry)
			if h.active != nil {
				h.active.calls = append(h.active.calls, entry)
			}
			h.mu.Unlock()
			return denied, nil
		}
	}
	h.mu.Unlock()

	argsStr := string(args)
	if stub != nil {
		res, err := stub(argsStr)
		if err != nil {
			h.mu.Lock()
			entry := HostCallEntry{Command: cmd, Args: argsStr, Result: "<transport error>"}
			entry.index = len(h.calls)
			h.calls = append(h.calls, entry)
			if h.active != nil {
				h.active.calls = append(h.active.calls, entry)
			}
			h.mu.Unlock()
			return nil, err
		}
		h.mu.Lock()
		entry := HostCallEntry{Command: cmd, Args: argsStr, Result: res, Effective: hostResultAccepted([]byte(res))}
		entry.index = len(h.calls)
		h.calls = append(h.calls, entry)
		if h.active != nil {
			h.active.calls = append(h.active.calls, entry)
		}
		if hostResultAccepted([]byte(res)) {
			h.accepted = append(h.accepted, entry)
			if h.active != nil {
				h.active.accepted = append(h.active.accepted, entry)
			}
		}
		h.mu.Unlock()
		return []byte(res), nil
	}

	raw, err := h.builtinTyped(cmd, args)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	entry := HostCallEntry{Command: cmd, Args: argsStr, Result: string(raw), Effective: hostResultAccepted(raw)}
	entry.index = len(h.calls)
	h.calls = append(h.calls, entry)
	if h.active != nil {
		h.active.calls = append(h.active.calls, entry)
	}
	if hostResultAccepted(raw) {
		h.accepted = append(h.accepted, entry)
		if h.active != nil {
			h.active.accepted = append(h.active.accepted, entry)
		}
	}
	h.mu.Unlock()
	return raw, nil
}

func hostResultAccepted(raw []byte) bool {
	r, err := pbv1.DecodeHostCallResult(raw)
	if err != nil {
		return false
	}
	_, ok := r.Result.(*pbv1.HostCallResult_Value)
	return ok
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
func (h *Harness) AcceptedCalls() []HostCallEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]HostCallEntry(nil), h.accepted...)
}
func (h *Harness) EffectiveBlockCalls() []HostCallEntry {
	var out []HostCallEntry
	for _, c := range h.AcceptedCalls() {
		if c.Command == "env.block_request" {
			out = append(out, c)
		}
	}
	return out
}

// isExtensionCommand reports whether cmd is a host-feature call rather than a
// core ABI operation. Core commands are the env.* namespace; everything else
// reaching the harness is an extension.
func isExtensionCommand(cmd string) bool {
	return cmd != "" && !strings.HasPrefix(cmd, "env.")
}

func hostCallResultValue(value []byte) []byte {
	raw, _ := proto.Marshal(&pbv1.HostCallResult{
		Result: &pbv1.HostCallResult_Value{Value: value},
	})
	return raw
}

func hostCallResultError(code pbv1.ErrorCode, msg string) []byte {
	raw, _ := proto.Marshal(&pbv1.HostCallResult{
		Result: &pbv1.HostCallResult_Error{Error: &pbv1.HostError{Code: code, Message: msg}},
	})
	return raw
}

func (h *Harness) builtinTyped(cmd string, args []byte) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	switch cmd {
	case "env.credential_get":
		var a pbv1.CredentialGetArgs
		if err := proto.Unmarshal(args, &a); err != nil || a.Validate() != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid CredentialGetArgs"), nil
		}
		value, ok := h.credentials[a.Slot]
		if !ok {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_NOT_CONFIGURED, "credential slot is not configured"), nil
		}
		return hostCallResultValue(append([]byte(nil), value...)), nil

	case "env.file_append":
		var a pbv1.FileAppendArgs
		if err := proto.Unmarshal(args, &a); err != nil || a.Validate() != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid FileAppendArgs"), nil
		}
		h.files[a.Path] = append(h.files[a.Path], a.Data...)
		return hostCallResultValue(nil), nil

	case "env.file_read":
		var a pbv1.FileReadArgs
		if err := proto.Unmarshal(args, &a); err != nil || a.Validate() != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid FileReadArgs"), nil
		}
		value, ok := h.files[a.Path]
		if !ok {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_NOT_FOUND, "file not found"), nil
		}
		return hostCallResultValue(append([]byte(nil), value...)), nil

	case "env.file_write":
		var a pbv1.FileWriteArgs
		if err := proto.Unmarshal(args, &a); err != nil || a.Validate() != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid FileWriteArgs"), nil
		}
		h.files[a.Path] = append([]byte(nil), a.Data...)
		return hostCallResultValue(nil), nil

	case "env.file_list":
		var a pbv1.FileListArgs
		if err := proto.Unmarshal(args, &a); err != nil || a.Validate() != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid FileListArgs"), nil
		}
		paths := make([]string, 0)
		for path := range h.files {
			if strings.HasPrefix(path, a.Prefix) {
				paths = append(paths, path)
			}
		}
		sort.Strings(paths)
		value, _ := proto.Marshal(&pbv1.FileListResult{Paths: paths})
		return hostCallResultValue(value), nil

	case "env.file_delete":
		var a pbv1.FileDeleteArgs
		if err := proto.Unmarshal(args, &a); err != nil || a.Validate() != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid FileDeleteArgs"), nil
		}
		delete(h.files, a.Path)
		return hostCallResultValue(nil), nil

	case "env.http_request":
		var a pbv1.OutboundHTTPRequestArgs
		if err := proto.Unmarshal(args, &a); err != nil || a.Validate() != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid OutboundHTTPRequestArgs"), nil
		}
		return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_NOT_CONFIGURED, "stub env.http_request in this test"), nil

	case "env.block_request":
		var a pbv1.BlockRequestArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid BlockRequestArgs"), nil
		}
		if err := a.Validate(); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error()), nil
		}
		return hostCallResultValue(nil), nil

	case "env.respond_request":
		var a pbv1.RespondRequestArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid RespondRequestArgs"), nil
		}
		if err := a.Validate(); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error()), nil
		}
		return hostCallResultValue(nil), nil

	case "env.route_request":
		var a pbv1.RouteRequestArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid RouteRequestArgs"), nil
		}
		if err := a.Validate(); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error()), nil
		}
		return hostCallResultValue(nil), nil

	case "env.set_identity":
		var a pbv1.SetIdentityArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid SetIdentityArgs"), nil
		}
		if err := a.Validate(); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error()), nil
		}
		return hostCallResultValue(nil), nil

	case pbv1.MetaAppendCommand:
		var a pbv1.MetaAppendArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid MetaAppendArgs"), nil
		}
		if err := a.Validate(); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error()), nil
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
		val := pbv1.MetaAppendSuccessValue(a.Fragment, []byte(existing), present)
		return hostCallResultValue(val), nil
	case "env.meta_get":
		var a pbv1.MetaGetArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid MetaGetArgs"), nil
		}
		if err := a.Validate(); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error()), nil
		}
		// Presence, not emptiness. Returning "" for a missing key would make
		// the harness disagree with the contract in the one place an author
		// would trust it.
		v, present := h.meta[a.Key]
		if !present {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_NOT_FOUND, "meta key not found"), nil
		}
		return hostCallResultValue([]byte(v)), nil

	case "env.meta_set":
		var a pbv1.MetaSetArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid MetaSetArgs"), nil
		}
		if err := a.Validate(); err != nil {
			// Rejected arguments must not mutate. A harness that stored the
			// value anyway would hide the failure from the test asserting it.
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error()), nil
		}
		// An empty value STORES an empty value; it is not a delete.
		h.meta[a.Key] = a.Value
		return hostCallResultValue(nil), nil

	case "env.cache_get":
		var a pbv1.CacheGetArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid CacheGetArgs"), nil
		}
		if err := a.Validate(); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error()), nil
		}
		v, present := h.cache[a.Key]
		if present && h.cacheExpiry[a.Key] > 0 && h.now() >= h.cacheExpiry[a.Key] {
			delete(h.cache, a.Key)
			delete(h.cacheExpiry, a.Key)
			present = false
		}
		if !present {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_NOT_FOUND, "cache key not found"), nil
		}
		return hostCallResultValue([]byte(v)), nil

	case "env.cache_set":
		var a pbv1.CacheSetArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid CacheSetArgs"), nil
		}
		if err := a.Validate(); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error()), nil
		}
		h.cache[a.Key] = a.Value
		if a.TtlMs != nil {
			h.cacheExpiry[a.Key] = h.now() + int64(*a.TtlMs)
		}
		return hostCallResultValue(nil), nil
	case "env.cache_delete":
		var a pbv1.CacheDeleteArgs
		if err := proto.Unmarshal(args, &a); err != nil || a.Validate() != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid CacheDeleteArgs"), nil
		}
		delete(h.cache, a.Key)
		delete(h.cacheExpiry, a.Key)
		return hostCallResultValue(nil), nil
	case "env.shared_cache_get":
		var a pbv1.CacheGetArgs
		if err := proto.Unmarshal(args, &a); err != nil || a.Validate() != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid CacheGetArgs"), nil
		}
		v, ok := h.sharedCache[a.Key]
		if ok && h.sharedExpiry[a.Key] > 0 && h.now() >= h.sharedExpiry[a.Key] {
			delete(h.sharedCache, a.Key)
			delete(h.sharedExpiry, a.Key)
			ok = false
		}
		if !ok {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_NOT_FOUND, "cache key not found"), nil
		}
		return hostCallResultValue([]byte(v)), nil
	case "env.shared_cache_set":
		var a pbv1.CacheSetArgs
		if err := proto.Unmarshal(args, &a); err != nil || a.Validate() != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid CacheSetArgs"), nil
		}
		h.sharedCache[a.Key] = a.Value
		if a.TtlMs != nil {
			h.sharedExpiry[a.Key] = h.now() + int64(*a.TtlMs)
		}
		return hostCallResultValue(nil), nil
	case "env.shared_cache_delete":
		var a pbv1.CacheDeleteArgs
		if err := proto.Unmarshal(args, &a); err != nil || a.Validate() != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid CacheDeleteArgs"), nil
		}
		delete(h.sharedCache, a.Key)
		delete(h.sharedExpiry, a.Key)
		return hostCallResultValue(nil), nil

	case "env.state_get":
		var a pbv1.StateGetArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid StateGetArgs"), nil
		}
		if err := a.Validate(); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error()), nil
		}
		if !h.StateConfigured {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_NOT_CONFIGURED,
				"durable plugin state is not configured"), nil
		}
		v, present := h.state[a.Key]
		if !present {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_NOT_FOUND, "state key not found"), nil
		}
		return hostCallResultValue([]byte(v)), nil

	case "env.state_set":
		var a pbv1.StateSetArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid StateSetArgs"), nil
		}
		if err := a.Validate(); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error()), nil
		}
		if !h.StateConfigured {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_NOT_CONFIGURED,
				"durable plugin state is not configured"), nil
		}
		// An empty value STORES an empty value. The old helper deleted the key
		// here, which is exactly why state has a separate delete command.
		h.state[a.Key] = a.Value
		h.stateSeq++
		h.stateVersion[a.Key] = strconv.FormatUint(h.stateSeq, 10)
		return hostCallResultValue(nil), nil
	case "env.state_get_versioned":
		var a pbv1.StateGetArgs
		if err := proto.Unmarshal(args, &a); err != nil || a.Validate() != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid StateGetArgs"), nil
		}
		if !h.StateConfigured {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_NOT_CONFIGURED, "state unavailable"), nil
		}
		v, ok := h.state[a.Key]
		if !ok {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_NOT_FOUND, "state key not found"), nil
		}
		b, _ := proto.Marshal(&pbv1.StateValue{Value: v, Version: h.stateVersion[a.Key]})
		return hostCallResultValue(b), nil
	case "env.state_compare_and_set":
		var a pbv1.StateCompareAndSetArgs
		if err := proto.Unmarshal(args, &a); err != nil || a.Validate() != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid StateCompareAndSetArgs"), nil
		}
		if !h.StateConfigured {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_NOT_CONFIGURED, "state unavailable"), nil
		}
		current, exists := h.stateVersion[a.Key]
		match := (a.ExpectedVersion == nil && !exists) || (a.ExpectedVersion != nil && exists && *a.ExpectedVersion == current)
		if !match {
			b, _ := proto.Marshal(&pbv1.StateMutationResult{Applied: false})
			return hostCallResultValue(b), nil
		}
		h.state[a.Key] = a.Value
		h.stateSeq++
		ver := strconv.FormatUint(h.stateSeq, 10)
		h.stateVersion[a.Key] = ver
		b, _ := proto.Marshal(&pbv1.StateMutationResult{Applied: true, Version: &ver})
		return hostCallResultValue(b), nil
	case "env.state_compare_and_delete":
		var a pbv1.StateCompareAndDeleteArgs
		if err := proto.Unmarshal(args, &a); err != nil || a.Validate() != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid StateCompareAndDeleteArgs"), nil
		}
		if !h.StateConfigured {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_NOT_CONFIGURED, "state unavailable"), nil
		}
		if h.stateVersion[a.Key] != a.ExpectedVersion {
			b, _ := proto.Marshal(&pbv1.StateMutationResult{Applied: false})
			return hostCallResultValue(b), nil
		}
		delete(h.state, a.Key)
		delete(h.stateVersion, a.Key)
		b, _ := proto.Marshal(&pbv1.StateMutationResult{Applied: true})
		return hostCallResultValue(b), nil
	case "env.state_scan":
		var a pbv1.StateScanArgs
		if err := proto.Unmarshal(args, &a); err != nil || a.Validate() != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid StateScanArgs"), nil
		}
		entries := make([]*pbv1.StateEntry, 0)
		for k, v := range h.state {
			if strings.HasPrefix(k, a.Prefix) && (a.Cursor == "" || k > a.Cursor) {
				ver := h.stateVersion[k]
				entries = append(entries, &pbv1.StateEntry{Key: k, Value: &pbv1.StateValue{Value: v, Version: ver}})
			}
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
		next := ""
		if a.Limit > 0 && uint32(len(entries)) > a.Limit {
			next = entries[a.Limit-1].Key
			entries = entries[:a.Limit]
		}
		b, _ := proto.Marshal(&pbv1.StateScanResult{Entries: entries, NextCursor: next})
		return hostCallResultValue(b), nil

	case "env.state_delete":
		var a pbv1.StateDeleteArgs
		if err := proto.Unmarshal(args, &a); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, "invalid StateDeleteArgs"), nil
		}
		if err := a.Validate(); err != nil {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, err.Error()), nil
		}
		if !h.StateConfigured {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_NOT_CONFIGURED,
				"durable plugin state is not configured"), nil
		}
		// Deleting an absent key succeeds: the caller wants it gone.
		delete(h.state, a.Key)
		return hostCallResultValue(nil), nil

	case "env.state_keys":
		if !h.StateConfigured {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_NOT_CONFIGURED,
				"durable plugin state is not configured"), nil
		}
		keys := make([]string, 0, len(h.state))
		for k := range h.state {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b, _ := json.Marshal(keys)
		return hostCallResultValue(b), nil

	case "env.now":
		return hostCallResultValue([]byte(strconv.FormatInt(h.now(), 10))), nil

	case "env.plugin_config":
		return hostCallResultValue([]byte(h.config)), nil

	case "env.original_request":
		if !h.originSet {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_NOT_FOUND,
				"no original request captured"), nil
		}
		return hostCallResultValue(h.original), nil

	case "env.original_response":
		if !h.origRespSet {
			return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_NOT_FOUND,
				"no original response captured"), nil
		}
		return hostCallResultValue(h.origResp), nil
	}
	if isExtensionCommand(cmd) {
		// The harness cannot emulate a host feature, so an extension command
		// answers NOT_CONFIGURED unless the test stubs it. That is the honest
		// answer -- a harness with no compaction backend really does not have
		// one -- and it is framed, so a plugin's degrade path is exercised
		// rather than a decode failure.
		return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_NOT_CONFIGURED,
			"extension command "+cmd+" is not configured in sdktest; StubHostCall it"), nil
	}
	return hostCallResultError(pbv1.ErrorCode_ERROR_CODE_NOT_FOUND, "unknown typed command"), nil
}

// CheckManifest cross-checks plugin.json against what the plugin actually
// registered, catching locally a mismatch the host would reject at load.
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
		Permissions []struct {
			Name string `json:"name"`
		} `json:"permissions"`
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
	for _, permission := range m.Permissions {
		if !sdk.IsPermission(permission.Name) {
			t.Errorf("plugin.json declares unknown permission %q", permission.Name)
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
func HostResultError(code pbv1.ErrorCode, msg string) string {
	frame := &pbv1.HostError{Code: code, Message: msg}
	if err := frame.Validate(); err != nil {
		panic(fmt.Sprintf("sdktest: HostResultError(%v): %v — this constructor "+
			"builds classified refusals; return a raw string from StubHostCall "+
			"if you want a malformed frame", code, err))
	}
	return string(hostCallResultError(code, msg))
}
