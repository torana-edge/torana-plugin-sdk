package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	plugin_sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
	"google.golang.org/protobuf/proto"
)

func TestCompiledGoGuestImplementsRunHook(t *testing.T) {
	path := os.Getenv("TORANA_GO_GUEST")
	if path == "" {
		t.Log("TORANA_GO_GUEST unset; exercised in CI")
		return
	}
	exerciseRunHook(t, path)
}

func TestCompiledRustGuestImplementsRunHook(t *testing.T) {
	path := os.Getenv("TORANA_RUST_GUEST")
	if path == "" {
		t.Log("TORANA_RUST_GUEST unset; exercised in CI")
		return
	}
	exerciseRunHook(t, path)
}

// TestCompiledRustLoggerCallsHost proves the shipped Rust example does more
// than compile and export the right bitmap: its ABI-v2 dispatcher decodes the
// request and reaches the real env.log import with the expected bytes.
func TestCompiledRustLoggerCallsHost(t *testing.T) {
	path := os.Getenv("TORANA_RUST_LOGGER")
	if path == "" {
		t.Log("TORANA_RUST_LOGGER unset; exercised in CI")
		return
	}
	logs := exerciseRunHook(t, path)
	if len(logs) != 1 {
		t.Fatalf("Rust logger emitted %d log calls, want 1", len(logs))
	}
	if logs[0].level != plugin_sdk.LogLevelInfo || logs[0].message != "received request for conformance" {
		t.Fatalf("Rust logger call = level %d message %q", logs[0].level, logs[0].message)
	}
}

// manifestHooks reads the hook set a guest's manifest declares.
//
// This is the ONLY source of truth the bitmap is checked against. Deriving the
// expectation from the guest's own registrations, or from a helper that also
// produced the bitmap, tests that two pieces of code agree with each other and
// says nothing about whether the shipped manifest matches the shipped wasm.
func manifestHooks(t *testing.T, path string) []pbv2.Hook {
	t.Helper()
	raw, err := os.ReadFile(resolveFromModuleRoot(t, path))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m struct {
		Hooks []struct {
			Name string `json:"name"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if len(m.Hooks) == 0 {
		t.Fatalf("%s declares no hooks", path)
	}
	var hooks []pbv2.Hook
	for _, h := range m.Hooks {
		hk, ok := plugin_sdk.ManifestHookName(h.Name)
		if !ok {
			t.Fatalf("unknown hook %q in %s", h.Name, path)
		}
		hooks = append(hooks, hk)
	}
	return hooks
}

// resolveFromModuleRoot makes a relative manifest path mean the same thing
// however the test is invoked.
//
// `go test` runs with the working directory set to the package, so a path like
// examples/go-logger/plugin.json resolves under conformance/host and fails.
// Anchoring to the module root keeps the CI env vars readable and lets the
// same command work locally.
func resolveFromModuleRoot(t *testing.T, path string) string {
	t.Helper()
	if filepath.IsAbs(path) {
		return path
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, path)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s, cannot resolve %q", dir, path)
		}
		dir = parent
	}
}

// TestGuestBitmapMatchesManifest is the real ABI-surface check: it reads
// supported_hooks out of the BUILT guest and compares it with the hook set the
// shipped manifest declares.
//
// The previous regression test derived its expectation from the manifest and
// then validated it against the same manifest, so changing the Go logger's
// registration from before-request to tick while leaving the manifest alone
// passed — the exact drift it claimed to prevent. The conformance host only
// checked the bitmap was non-zero.
func TestGuestBitmapMatchesManifest(t *testing.T) {
	guest := os.Getenv("TORANA_GO_GUEST")
	manifest := os.Getenv("TORANA_GO_GUEST_MANIFEST")
	if guest == "" || manifest == "" {
		t.Log("TORANA_GO_GUEST/TORANA_GO_GUEST_MANIFEST unset; exercised in CI")
	} else {
		assertGuestBitmapMatchesManifest(t, guest, manifest)
	}
}

func TestRustGuestBitmapMatchesManifest(t *testing.T) {
	guest := os.Getenv("TORANA_RUST_GUEST")
	manifest := os.Getenv("TORANA_RUST_GUEST_MANIFEST")
	if guest == "" || manifest == "" {
		t.Log("TORANA_RUST_GUEST/TORANA_RUST_GUEST_MANIFEST unset; exercised in CI")
		return
	}
	assertGuestBitmapMatchesManifest(t, guest, manifest)
}

func assertGuestBitmapMatchesManifest(t *testing.T, guest, manifest string) {
	t.Helper()
	declared := manifestHooks(t, manifest)

	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = runtime.Close(ctx) })
	wasi_snapshot_preview1.MustInstantiate(ctx, runtime)
	if err := instantiateEnvImports(ctx, runtime, nil); err != nil {
		t.Fatal(err)
	}
	wasmBytes, err := os.ReadFile(guest)
	if err != nil {
		t.Fatalf("read guest: %v", err)
	}
	module, err := runtime.InstantiateWithConfig(ctx, wasmBytes, wazero.NewModuleConfig())
	if err != nil {
		t.Fatalf("instantiate guest: %v", err)
	}
	if initialize := module.ExportedFunction("_initialize"); initialize != nil {
		if _, err := initialize.Call(ctx); err != nil {
			t.Fatalf("initialize guest: %v", err)
		}
	}
	supported := module.ExportedFunction("supported_hooks")
	if supported == nil {
		t.Fatal("v2 guest is missing supported_hooks")
	}
	bits, err := supported.Call(ctx)
	if err != nil || len(bits) != 1 {
		t.Fatalf("supported_hooks: %v %v", bits, err)
	}
	got := pbv2.HookBitmap(bits[0])

	want, err := pbv2.ExpectedBitmap(declared)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s exports bitmap %#b, manifest %s declares %#b (hooks %v).\n"+
			"The built guest and its shipped manifest disagree about which hooks it "+
			"implements — one of the two is wrong.", guest, got, manifest, want, declared)
	}
	// ValidateManifestHooks rejects reserved and unassigned bits; run it on the
	// value that came out of the wasm, not on one derived from the manifest.
	if err := pbv2.ValidateManifestHooks(got, declared); err != nil {
		t.Fatalf("exported bitmap is not a valid hook set: %v", err)
	}
}

type loggedMessage struct {
	level   int32
	message string
}

type envRecorder struct {
	logs []loggedMessage
	err  error
}

func exerciseRunHook(t *testing.T, path string) []loggedMessage {
	t.Helper()
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = runtime.Close(ctx) })
	wasi_snapshot_preview1.MustInstantiate(ctx, runtime)
	recorder := &envRecorder{}
	if err := instantiateEnvImports(ctx, runtime, recorder); err != nil {
		t.Fatal(err)
	}
	wasmBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read guest: %v", err)
	}
	module, err := runtime.InstantiateWithConfig(ctx, wasmBytes, wazero.NewModuleConfig())
	if err != nil {
		t.Fatalf("instantiate guest: %v", err)
	}
	if initialize := module.ExportedFunction("_initialize"); initialize != nil {
		if _, err := initialize.Call(ctx); err != nil {
			t.Fatalf("initialize guest: %v", err)
		}
	}
	payload, err := proto.Marshal(&pbv2.HookInput{
		RequestId: 1,
		Payload:   &pbv2.HookInput_ChatRequest{ChatRequest: &pbv2.ChatRequest{Model: "conformance"}},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	alloc := module.ExportedFunction("alloc")
	hook := module.ExportedFunction("run_hook")
	dealloc := module.ExportedFunction("dealloc")
	supported := module.ExportedFunction("supported_hooks")
	if alloc == nil || hook == nil || dealloc == nil {
		t.Fatal("guest is missing alloc, dealloc, or run_hook")
	}
	if supported == nil {
		t.Fatal("v2 guest is missing supported_hooks")
	}
	if module.ExportedFunction("run_before_request") != nil {
		t.Fatal("v1 run_before_request must not remain after Migration A")
	}
	bits, err := supported.Call(ctx)
	if err != nil || len(bits) != 1 {
		t.Fatalf("supported_hooks: %v %v", bits, err)
	}
	if bits[0] == 0 {
		t.Fatal("supported_hooks must not be zero for a live guest")
	}
	pointers, err := alloc.Call(ctx, uint64(len(payload)))
	if err != nil || len(pointers) != 1 {
		t.Fatalf("alloc: %v", err)
	}
	ptr := uint32(pointers[0])
	if !module.Memory().Write(ptr, payload) {
		t.Fatal("write guest input")
	}
	result, err := hook.Call(ctx, uint64(ptr), uint64(len(payload)))
	if err != nil {
		t.Fatalf("call hook: %v", err)
	}
	if len(result) != 1 || result[0] != 0 {
		t.Fatalf("pass-through guest should return 0, got %v", result)
	}
	if _, err := dealloc.Call(ctx, uint64(ptr), uint64(len(payload))); err != nil {
		t.Fatalf("dealloc: %v", err)
	}
	if recorder.err != nil {
		t.Fatal(recorder.err)
	}
	return recorder.logs
}

func instantiateEnvImports(ctx context.Context, runtime wazero.Runtime, recorder *envRecorder) error {
	_, err := runtime.NewHostModuleBuilder("env").
		NewFunctionBuilder().
		WithFunc(func(_ context.Context, module api.Module, level int32, ptr, size uint32) {
			if recorder == nil || recorder.err != nil {
				return
			}
			payload, ok := module.Memory().Read(ptr, size)
			if !ok {
				recorder.err = fmt.Errorf("env.log read outside guest memory: ptr=%d size=%d", ptr, size)
				return
			}
			recorder.logs = append(recorder.logs, loggedMessage{level: level, message: string(payload)})
		}).
		Export("log").
		NewFunctionBuilder().
		WithFunc(func(context.Context, api.Module, int32, uint32, uint32, float64, uint32, uint32) {}).
		Export("emit_metric").
		NewFunctionBuilder().
		WithFunc(func(context.Context, api.Module, uint32, uint32, uint32, uint32) uint64 { return 0 }).
		Export("host_call").
		Instantiate(ctx)
	return err
}
