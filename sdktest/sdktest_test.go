//go:build !wasip1

package sdktest_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	"github.com/torana-edge/torana-plugin-sdk/pb"
	"github.com/torana-edge/torana-plugin-sdk/sdktest"
)

// reset clears registrations between tests. A real plugin registers once in
// init(); these tests register per-case, so they have to undo it.
func reset(t *testing.T) {
	t.Cleanup(func() {
		sdk.OnBeforeRequest(nil)
		sdk.OnAfterResponse(nil)
		sdk.OnStreamChunk(nil)
		sdk.OnHTTPRequest(nil)
		sdk.OnTick(nil)
	})
}

// The load-bearing test: the handler must actually run. Before this package
// existed, sdk_other.go dropped the registration, so a hook could not be
// invoked from a test at all and any assertion about it was vacuous.
func TestHandlerActuallyRuns(t *testing.T) {
	reset(t)
	called := 0
	sdk.OnBeforeRequest(func(ctx context.Context, req *pb.ChatRequest) (*pb.ChatRequest, error) {
		called++
		req.Model = "rewritten"
		return req, nil
	})

	h := sdktest.New(t)
	res := h.BeforeRequest(&pb.ChatRequest{Model: "original"})

	if called != 1 {
		t.Fatalf("handler ran %d times, want 1", called)
	}
	if res.PassedThrough {
		t.Fatal("result reported pass-through, but the handler returned a request")
	}
	if res.Request.Model != "rewritten" {
		t.Fatalf("model = %q, want %q", res.Request.Model, "rewritten")
	}
}

func TestPassThroughIsReported(t *testing.T) {
	reset(t)
	sdk.OnBeforeRequest(func(ctx context.Context, req *pb.ChatRequest) (*pb.ChatRequest, error) {
		return nil, nil
	})

	res := sdktest.New(t).BeforeRequest(&pb.ChatRequest{Model: "m"})
	if !res.PassedThrough {
		t.Fatal("returning nil must report pass-through")
	}
	if res.Request != nil {
		t.Fatal("pass-through must carry no request")
	}
}

func TestVerdictsAreDecoded(t *testing.T) {
	reset(t)
	sdk.OnBeforeRequest(func(ctx context.Context, req *pb.ChatRequest) (*pb.ChatRequest, error) {
		sdk.BlockRequest(req, 422, "pii_detected", "PII in tool result")
		return req, nil
	})

	res := sdktest.New(t).BeforeRequest(&pb.ChatRequest{})
	if res.Block == nil {
		t.Fatal("expected a block verdict")
	}
	if res.Block.Status != 422 || res.Block.Code != "pii_detected" {
		t.Fatalf("block = %+v", res.Block)
	}
	if res.Respond != nil || res.Route != nil {
		t.Fatal("only the block verdict should be set")
	}
}

// The footgun sdk.BlockRequest carries in its own doc comment: setting a
// verdict and then returning nil discards it. The harness reproduces that
// faithfully rather than papering over it, so a plugin author can write a test
// that catches it.
func TestVerdictLostWhenRequestNotReturned(t *testing.T) {
	reset(t)
	sdk.OnBeforeRequest(func(ctx context.Context, req *pb.ChatRequest) (*pb.ChatRequest, error) {
		sdk.BlockRequest(req, 422, "pii_detected", "found")
		return nil, nil // the bug
	})

	res := sdktest.New(t).BeforeRequest(&pb.ChatRequest{})
	if res.Block != nil {
		t.Fatal("a verdict set but not returned must not take effect")
	}
}

func TestConfigReachesThePlugin(t *testing.T) {
	reset(t)
	var got string
	sdk.OnBeforeRequest(func(ctx context.Context, req *pb.ChatRequest) (*pb.ChatRequest, error) {
		got = sdk.PluginConfig()
		return nil, nil
	})

	sdktest.New(t).SetConfig(`{"threshold":42}`).BeforeRequest(&pb.ChatRequest{})

	var cfg struct {
		Threshold int `json:"threshold"`
	}
	if err := json.Unmarshal([]byte(got), &cfg); err != nil {
		t.Fatalf("plugin saw unparseable config %q: %v", got, err)
	}
	if cfg.Threshold != 42 {
		t.Fatalf("threshold = %d, want 42", cfg.Threshold)
	}
}

func TestStubbedHostCallAndCallRecording(t *testing.T) {
	reset(t)
	sdk.OnBeforeRequest(func(ctx context.Context, req *pb.ChatRequest) (*pb.ChatRequest, error) {
		res, _ := sdk.HostCall("torana_offload_completion", `{"user_prompt":"scan"}`)
		req.Model = res
		return req, nil
	})

	h := sdktest.New(t)
	h.StubHostCall("torana_offload_completion", func(args string) (string, error) {
		return `{"completion":"CLEAN"}`, nil
	})
	res := h.BeforeRequest(&pb.ChatRequest{})

	if res.Request.Model != `{"completion":"CLEAN"}` {
		t.Fatalf("plugin got %q", res.Request.Model)
	}
	calls := h.Calls()
	if len(calls) != 1 || calls[0].Command != "torana_offload_completion" {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[0].Args != `{"user_prompt":"scan"}` {
		t.Fatalf("args not recorded: %q", calls[0].Args)
	}
}

func TestCacheAndStateRoundTrip(t *testing.T) {
	reset(t)
	sdk.OnBeforeRequest(func(ctx context.Context, req *pb.ChatRequest) (*pb.ChatRequest, error) {
		if err := sdk.StateSet("seen", "yes"); err != nil {
			return nil, err
		}
		_, _ = sdk.HostCall("env.cache_set", `{"key":"k","value":"v"}`)
		return nil, nil
	})

	h := sdktest.New(t)
	h.BeforeRequest(&pb.ChatRequest{})

	if v, ok := h.State("seen"); !ok || v != "yes" {
		t.Fatalf("state seen = %q, %v", v, ok)
	}
	if v, ok := h.Cache("k"); !ok || v != "v" {
		t.Fatalf("cache k = %q, %v", v, ok)
	}
}

// A host with no durable store answers differently, and the SDK's state
// wrappers have to cope. Reproducing that exactly is why the harness mirrors
// runtime.go's envelopes rather than inventing tidier ones.
func TestUnconfiguredStateMatchesTheHost(t *testing.T) {
	reset(t)
	var setErr error
	sdk.OnBeforeRequest(func(ctx context.Context, req *pb.ChatRequest) (*pb.ChatRequest, error) {
		setErr = sdk.StateSet("k", "v")
		return nil, nil
	})

	h := sdktest.New(t)
	h.StateConfigured = false
	h.BeforeRequest(&pb.ChatRequest{})

	if setErr == nil {
		t.Fatal("StateSet must report an error when the host has no state store")
	}
}

func TestDeniedPermissionUsesTheHostEnvelope(t *testing.T) {
	reset(t)
	var raw string
	sdk.OnBeforeRequest(func(ctx context.Context, req *pb.ChatRequest) (*pb.ChatRequest, error) {
		raw, _ = sdk.HostCall("env.cache_get", "k")
		return nil, nil
	})

	h := sdktest.New(t)
	h.DenyPermission("env.cache_get")
	h.BeforeRequest(&pb.ChatRequest{})

	if raw != `{"status":"error","message":"permission denied"}` {
		t.Fatalf("denial envelope = %q", raw)
	}
}

func TestLogsAndMetricsAreCaptured(t *testing.T) {
	reset(t)
	sdk.OnBeforeRequest(func(ctx context.Context, req *pb.ChatRequest) (*pb.ChatRequest, error) {
		sdk.Log("scanning", sdk.LogLevelInfo)
		sdk.EmitMetric("scans_total", sdk.MetricCounter, 1, map[string]string{"result": "clean"})
		return nil, nil
	})

	h := sdktest.New(t)
	h.BeforeRequest(&pb.ChatRequest{})

	logs := h.Logs()
	if len(logs) != 1 || logs[0].Message != "scanning" {
		t.Fatalf("logs = %+v", logs)
	}
	metrics := h.Metrics()
	if len(metrics) != 1 || metrics[0].Name != "scans_total" || metrics[0].Labels["result"] != "clean" {
		t.Fatalf("metrics = %+v", metrics)
	}
}

// Labels assembled inline in a hook closure were previously unobservable —
// otel/main_test.go carries a comment recording that reverting a real labelling
// fix left its suite green. This is the assertion that would have caught it.
func TestMetricLabelsAreObservable(t *testing.T) {
	reset(t)
	sdk.OnBeforeRequest(func(ctx context.Context, req *pb.ChatRequest) (*pb.ChatRequest, error) {
		sdk.EmitMetric("duration_ms", sdk.MetricHistogram, 12, map[string]string{
			"provider":     "anthropic",
			"status_class": "2xx",
		})
		return nil, nil
	})

	h := sdktest.New(t)
	h.BeforeRequest(&pb.ChatRequest{})

	m := h.Metrics()[0]
	if m.Labels["status_class"] != "2xx" {
		t.Fatalf("status_class label missing from %v", m.Labels)
	}
}

func TestStreamChunksAssembleAcrossEvents(t *testing.T) {
	reset(t)
	sdk.OnStreamChunk(func(ctx context.Context, ev *pb.StreamEvent) (*pb.StreamEventResult, error) {
		// Type-switch on the oneof rather than testing GetTextDelta() != "",
		// because TextDelta is a bare string: an empty delta and an absent one
		// are indistinguishable through the getter.
		if _, ok := ev.Event.(*pb.StreamEvent_TextDelta); ok {
			return sdk.Suppress(), nil
		}
		return sdk.Pass(), nil
	})

	h := sdktest.New(t)
	out := h.StreamChunks(
		&pb.StreamEvent{Event: &pb.StreamEvent_TextDelta{TextDelta: "a"}},
		&pb.StreamEvent{Event: &pb.StreamEvent_FinishReason{FinishReason: "stop"}},
	)
	if len(out) != 1 {
		t.Fatalf("forwarded %d events, want 1 (the text delta should be suppressed)", len(out))
	}
	if out[0].GetFinishReason() != "stop" {
		t.Fatalf("wrong event forwarded: %+v", out[0])
	}
}

func TestMissingHandlerFailsLoudly(t *testing.T) {
	reset(t)
	// No registration at all. BeforeRequest calls t.Fatal, so run it in a
	// subtest whose failure we can observe rather than inherit.
	fake := &recordingTB{TB: t}
	h := sdktest.New(fake)
	func() {
		defer func() { _ = recover() }()
		h.BeforeRequest(&pb.ChatRequest{})
	}()
	if !fake.failed {
		t.Fatal("dispatching with no registered handler must fail the test")
	}
}

// recordingTB captures Fatal instead of aborting, so the test above can assert
// that the harness fails rather than silently doing nothing.
type recordingTB struct {
	testing.TB
	failed bool
}

func (r *recordingTB) Fatal(args ...any)                 { r.failed = true; panic("fatal") }
func (r *recordingTB) Fatalf(f string, args ...any)      { r.failed = true; panic("fatal") }
func (r *recordingTB) Errorf(format string, args ...any) { r.failed = true }
func (r *recordingTB) Error(args ...any)                 { r.failed = true }
func (r *recordingTB) Helper()                           {}

func TestCheckManifestCatchesBothDirections(t *testing.T) {
	reset(t)
	dir := t.TempDir()
	write := func(hooks string) {
		if err := os.WriteFile(filepath.Join(dir, "plugin.json"),
			[]byte(`{"hooks":[`+hooks+`]}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("declared but not registered", func(t *testing.T) {
		write(`{"name":"run_on_tick"}`)
		fake := &recordingTB{TB: t}
		sdktest.CheckManifest(fake, dir)
		if !fake.failed {
			t.Fatal("a declared hook with no handler must be reported")
		}
	})

	t.Run("registered but not declared", func(t *testing.T) {
		sdk.OnBeforeRequest(func(ctx context.Context, req *pb.ChatRequest) (*pb.ChatRequest, error) {
			return nil, nil
		})
		write(``)
		fake := &recordingTB{TB: t}
		sdktest.CheckManifest(fake, dir)
		if !fake.failed {
			t.Fatal("a registered handler with no declaration must be reported")
		}
	})

	t.Run("matching", func(t *testing.T) {
		sdk.OnBeforeRequest(func(ctx context.Context, req *pb.ChatRequest) (*pb.ChatRequest, error) {
			return nil, nil
		})
		write(`{"name":"run_before_request"}`)
		fake := &recordingTB{TB: t}
		sdktest.CheckManifest(fake, dir)
		if fake.failed {
			t.Fatal("a matching manifest must pass")
		}
	})
}
