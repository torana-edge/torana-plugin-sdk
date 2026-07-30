package host

import (
	"context"
	"os"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
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

func exerciseRunHook(t *testing.T, path string) {
	t.Helper()
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = runtime.Close(ctx) })
	wasi_snapshot_preview1.MustInstantiate(ctx, runtime)
	if err := instantiateEnvImports(ctx, runtime); err != nil {
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
	if alloc == nil || hook == nil || dealloc == nil {
		t.Fatal("guest is missing alloc, dealloc, or run_hook")
	}
	if module.ExportedFunction("run_before_request") != nil {
		t.Fatal("v1 run_before_request must not remain after Migration A")
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
}

func instantiateEnvImports(ctx context.Context, runtime wazero.Runtime) error {
	_, err := runtime.NewHostModuleBuilder("env").
		NewFunctionBuilder().
		WithFunc(func(context.Context, api.Module, int32, uint32, uint32) {}).
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
