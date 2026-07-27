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

// `handled` is the guest's way of saying "I acted on this". When it is false
// the host discards the payload, so the SDKs must agree on what goes on the
// wire — and they did not: Go returned 0, Rust encoded and returned a buffer
// the host would throw away.
//
// The corrupt-payload test cannot see this. It only ever reaches the decoder,
// and both guests used to return nil/None from every handler, so the encode
// path was never exercised at all. These two hooks now return a real result,
// one of each polarity, in both guests.
func TestHandledIsHonouredIdenticallyByEveryGuest(t *testing.T) {
	for _, artifact := range []struct{ name, env string }{
		{"go", "TORANA_GO_GUEST"},
		{"rust", "TORANA_RUST_GUEST"},
	} {
		path := os.Getenv(artifact.env)
		if path == "" {
			t.Logf("%s is unset; that compiled guest is exercised in CI", artifact.env)
			continue
		}
		t.Run(artifact.name, func(t *testing.T) {
			tickPayload, err := proto.Marshal(&pb.TickRequest{})
			if err != nil {
				t.Fatal(err)
			}
			httpPayload, err := proto.Marshal(&pb.HttpRequest{Method: "GET", Path: "/"})
			if err != nil {
				t.Fatal(err)
			}

			// handled=false must be reported as pass-through, not as an
			// encoded buffer.
			if got := callHook(t, path, "run_on_tick", tickPayload); got != 0 {
				t.Errorf("run_on_tick returned %d for a handled=false result; the ABI reserves "+
					"a non-zero return for a payload the host should use, and this one it "+
					"discards. Go returns 0 here.", got)
			}

			// handled=true must produce a real buffer — otherwise "always
			// return 0" would pass the assertion above.
			if got := callHook(t, path, "run_on_http_request", httpPayload); got == 0 {
				t.Error("run_on_http_request returned pass-through for a handled=true result; " +
					"the guest's response would be dropped")
			}
		})
	}
}

// callHook instantiates the guest, invokes one hook with a valid payload, and
// returns the packed result.
func callHook(t *testing.T, path, hookName string, payload []byte) uint64 {
	t.Helper()
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = runtime.Close(ctx) })
	wasi_snapshot_preview1.MustInstantiate(ctx, runtime)

	if _, err := runtime.NewHostModuleBuilder("env").
		NewFunctionBuilder().
		WithFunc(func(context.Context, api.Module, int32, uint32, uint32) {}).
		Export("log").
		Instantiate(ctx); err != nil {
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

	hook := module.ExportedFunction(hookName)
	if hook == nil {
		t.Fatalf("guest does not export %s", hookName)
	}
	alloc := module.ExportedFunction("alloc")
	pointers, err := alloc.Call(ctx, uint64(len(payload)))
	if err != nil || len(pointers) != 1 {
		t.Fatalf("alloc: %v", err)
	}
	ptr := uint32(pointers[0])
	if len(payload) > 0 && !module.Memory().Write(ptr, payload) {
		t.Fatal("write guest input")
	}

	result, err := hook.Call(ctx, 1, uint64(ptr), uint64(len(payload)))
	if err != nil {
		t.Fatalf("call %s with a valid payload trapped: %v", hookName, err)
	}
	if len(result) != 1 {
		t.Fatalf("%s returned %d values, want 1", hookName, len(result))
	}
	return result[0]
}
