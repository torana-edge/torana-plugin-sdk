package host

import (
	"context"
	"os"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// A corrupt HookInput payload must trap on run_hook — zero is reserved for
// deliberate pass-through after a successful decode.
func TestCorruptPayloadTrapsOnRunHook(t *testing.T) {
	path := os.Getenv("TORANA_GO_GUEST")
	if path == "" {
		t.Log("TORANA_GO_GUEST unset; exercised in CI")
		return
	}
	assertCorruptPayloadTraps(t, path)
}

func assertCorruptPayloadTraps(t *testing.T, path string) {
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
			t.Fatalf("initialize: %v", err)
		}
	}
	alloc := module.ExportedFunction("alloc")
	hook := module.ExportedFunction("run_hook")
	if alloc == nil || hook == nil {
		t.Fatal("missing alloc or run_hook")
	}
	corrupt := []byte{0xff, 0x00, 0xab}
	pointers, err := alloc.Call(ctx, uint64(len(corrupt)))
	if err != nil {
		t.Fatal(err)
	}
	ptr := uint32(pointers[0])
	if !module.Memory().Write(ptr, corrupt) {
		t.Fatal("write")
	}
	_, err = hook.Call(ctx, uint64(ptr), uint64(len(corrupt)))
	if err == nil {
		t.Fatal("corrupt payload must trap, not return pass-through")
	}
}
