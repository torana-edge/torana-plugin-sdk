package host

import (
	"context"
	"os"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/torana-edge/torana-plugin-sdk/pb"
	"google.golang.org/protobuf/proto"
)

func TestCompiledGuestsImplementBeforeRequestABI(t *testing.T) {
	artifacts := []struct {
		name string
		env  string
	}{
		{name: "go", env: "TORANA_GO_GUEST"},
		{name: "rust", env: "TORANA_RUST_GUEST"},
	}
	for _, artifact := range artifacts {
		path := os.Getenv(artifact.env)
		if path == "" {
			t.Logf("%s is unset; that compiled guest is exercised in CI", artifact.env)
			continue
		}
		t.Run(artifact.name, func(t *testing.T) {
			exerciseBeforeRequest(t, path)
		})
	}
}

func exerciseBeforeRequest(t *testing.T, path string) {
	t.Helper()
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = runtime.Close(ctx) })
	wasi_snapshot_preview1.MustInstantiate(ctx, runtime)
	_, err := runtime.NewHostModuleBuilder("env").
		NewFunctionBuilder().
		WithFunc(func(context.Context, api.Module, int32, uint32, uint32) {}).
		Export("log").
		Instantiate(ctx)
	if err != nil {
		t.Fatalf("instantiate host imports: %v", err)
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
	payload, err := proto.Marshal(&pb.ChatRequest{Model: "conformance"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	alloc := module.ExportedFunction("alloc")
	hook := module.ExportedFunction("run_before_request")
	dealloc := module.ExportedFunction("dealloc")
	if alloc == nil || hook == nil || dealloc == nil {
		t.Fatal("guest is missing alloc, dealloc, or run_before_request")
	}
	pointers, err := alloc.Call(ctx, uint64(len(payload)))
	if err != nil || len(pointers) != 1 {
		t.Fatalf("alloc: %v", err)
	}
	ptr := uint32(pointers[0])
	if !module.Memory().Write(ptr, payload) {
		t.Fatal("write guest input")
	}
	result, err := hook.Call(ctx, 1, uint64(ptr), uint64(len(payload)))
	if err != nil {
		t.Fatalf("call hook: %v", err)
	}
	if len(result) != 1 || result[0] != 0 {
		t.Fatalf("logger should pass through, got %v", result)
	}
	if _, err := dealloc.Call(ctx, uint64(ptr), uint64(len(payload))); err != nil {
		t.Fatalf("dealloc: %v", err)
	}
}
