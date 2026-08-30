package host

import (
	"context"
	"os"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"google.golang.org/protobuf/proto"
)

// Pass-through (TickIdle) returns 0; ServeHTTP returns a non-zero framed result.
func TestPassThroughVersusServeHTTP(t *testing.T) {
	path := os.Getenv("TORANA_GO_GUEST")
	if path == "" {
		t.Log("TORANA_GO_GUEST unset; exercised in CI")
		return
	}

	tickPayload, err := proto.Marshal(&pbv1.HookInput{
		RequestId: 1,
		Payload:   &pbv1.HookInput_TickRequest{TickRequest: &pbv1.TickRequest{TickId: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	httpPayload, err := proto.Marshal(&pbv1.HookInput{
		RequestId: 1,
		Payload: &pbv1.HookInput_HttpRequest{HttpRequest: &pbv1.HttpRequest{
			Method: "GET", Path: "/",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := callRunHook(t, path, tickPayload); got != 0 {
		t.Errorf("TickIdle must return 0, got %d", got)
	}
	if got := callRunHook(t, path, httpPayload); got == 0 {
		t.Error("ServeHTTP must return a non-zero framed result")
	}
}

func callRunHook(t *testing.T, path string, payload []byte) uint64 {
	t.Helper()
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = runtime.Close(ctx) })
	wasi_snapshot_preview1.MustInstantiate(ctx, runtime)
	if err := instantiateEnvImports(ctx, runtime, nil); err != nil {
		t.Fatal(err)
	}
	wasmBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	module, err := runtime.InstantiateWithConfig(ctx, wasmBytes, wazero.NewModuleConfig())
	if err != nil {
		t.Fatal(err)
	}
	if initialize := module.ExportedFunction("_initialize"); initialize != nil {
		if _, err := initialize.Call(ctx); err != nil {
			t.Fatal(err)
		}
	}
	alloc := module.ExportedFunction("alloc")
	hook := module.ExportedFunction("run_hook")
	pointers, err := alloc.Call(ctx, uint64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	ptr := uint32(pointers[0])
	if !module.Memory().Write(ptr, payload) {
		t.Fatal("write")
	}
	result, err := hook.Call(ctx, uint64(ptr), uint64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	return result[0]
}
