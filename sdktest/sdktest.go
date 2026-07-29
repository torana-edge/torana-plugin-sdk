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
//			return `{"completion":"EMAIL"}`, nil
//		})
//
//		res := h.BeforeRequest(&pb.ChatRequest{Messages: []*pb.Message{
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
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	"github.com/torana-edge/torana-plugin-sdk/pb"
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
	t testing.TB

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

// New installs a fake host for the duration of the test and removes it
// afterwards. Registrations made by the plugin's init() are already in place
// by the time a test runs, so no explicit wiring is needed.
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
	sdk.InstallTestHost(&sdk.TestHost{
		HostCall: h.hostCall,
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
	})
	t.Cleanup(func() { sdk.InstallTestHost(nil) })
	return h
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
// byte for byte, so a plugin's handling of a refused capability is testable.
func (h *Harness) DenyPermission(cmd string) *Harness {
	return h.StubHostCall(cmd, func(string) (string, error) {
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
func (h *Harness) SetOriginalRequest(req *pb.ChatRequest) *Harness {
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

func (h *Harness) hostCall(cmd, args string) (string, error) {
	h.mu.Lock()
	stub := h.stubs[cmd]
	h.mu.Unlock()

	var res string
	if stub != nil {
		var err error
		res, err = stub(args)
		if err != nil {
			return "", err
		}
	} else {
		res = h.builtin(cmd, args)
	}

	h.mu.Lock()
	h.calls = append(h.calls, HostCallEntry{Command: cmd, Args: args, Result: res})
	h.mu.Unlock()
	return res, nil
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
