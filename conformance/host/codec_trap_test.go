package host

import (
	"context"
	"os"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// ABI.md reserves a zero return for a DELIBERATE pass-through: the plugin saw
// the payload and chose not to act. A codec failure is not that — the plugin
// never saw anything — and must trap so the host discards the instance and
// applies the bundle's failure_mode.
//
// Two of the five Go hooks returned 0 instead. A corrupt payload was therefore
// reported as a healthy no-op: the instance was kept, failure_mode was never
// consulted, and the operator saw nothing. Rust was uniformly strict, so the
// two SDKs disagreed for run_after_response and run_on_stream_chunk — meaning
// the same plugin logic behaved differently depending on which language it was
// written in.
//
// This is the test that makes that parity provable rather than asserted, which
// is why it lives in the dual-guest harness.

// hooksTakingProtobuf are the hooks whose input is a protobuf message, so a
// non-protobuf payload must trap. run_on_tick is excluded: it takes no
// meaningful input.
var hooksTakingProtobuf = []string{
	"run_before_request",
	"run_after_response",
	"run_on_stream_chunk",
	"run_on_http_request",
}

func TestCorruptPayloadTrapsInEveryGuest(t *testing.T) {
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
			for _, hookName := range hooksTakingProtobuf {
				t.Run(hookName, func(t *testing.T) {
					assertCorruptPayloadTraps(t, path, hookName)
				})
			}
		})
	}
}

func assertCorruptPayloadTraps(t *testing.T, path, hookName string) {
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
		t.Skipf("guest does not export %s", hookName)
	}
	alloc := module.ExportedFunction("alloc")
	if alloc == nil {
		t.Fatal("guest is missing alloc")
	}

	// Not valid protobuf for any of these messages: field number 0 is illegal
	// in the wire format, so every decoder must reject it.
	payload := []byte{0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}

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
		return // trapped, which is the contract
	}

	// A registered handler that decoded nothing must not report success. Zero
	// means "I saw the payload and chose to pass it through" — indistinguishable
	// to the host from a plugin working correctly.
	if len(result) == 1 && result[0] == 0 {
		t.Errorf("%s returned 0 (pass-through) on a payload it could not decode.\n"+
			"ABI.md reserves zero for a deliberate no-op, so the host keeps the instance "+
			"and never applies failure_mode — a corrupt payload is silently indistinguishable "+
			"from a healthy plugin. It must trap.", hookName)
		return
	}
	t.Errorf("%s returned %v on an undecodable payload; expected a trap", hookName, result)
}
