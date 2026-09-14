package host

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"google.golang.org/protobuf/proto"
)

// TestCompiledRustRespondRequestRoundTripsThroughHost exercises the public
// Rust helper across the real WASI import boundary. The host uses the same
// closed protobuf validation and typed argument validation as production, so
// sending a bare SyntheticResponse instead of RespondRequestArgs fails here.
func TestCompiledRustRespondRequestRoundTripsThroughHost(t *testing.T) {
	path := os.Getenv("TORANA_RUST_GUEST")
	if path == "" {
		if os.Getenv("TORANA_E2E") == "1" {
			t.Fatal("TORANA_RUST_GUEST is required in E2E mode")
		}
		t.Log("TORANA_RUST_GUEST unset; exercised in CI")
		return
	}

	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = runtime.Close(ctx) })
	wasi_snapshot_preview1.MustInstantiate(ctx, runtime)

	var resultPtr atomic.Uint32
	var seenMu sync.Mutex
	var seen *pbv1.RespondRequestArgs
	resultFrame, err := proto.Marshal(&pbv1.HostCallResult{
		Result: &pbv1.HostCallResult_Value{Value: nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.NewHostModuleBuilder("env").
		NewFunctionBuilder().
		WithFunc(func(context.Context, api.Module, int32, uint32, uint32) {}).
		Export("log").
		NewFunctionBuilder().
		WithFunc(func(context.Context, api.Module, int32, uint32, uint32, float64, uint32, uint32) {}).
		Export("emit_metric").
		NewFunctionBuilder().
		WithFunc(func(_ context.Context, module api.Module, commandPtr, commandLen, argsPtr, argsLen uint32) uint64 {
			command, ok := module.Memory().Read(commandPtr, commandLen)
			if !ok || string(command) != "env.respond_request" {
				panic(fmt.Sprintf("unexpected host command %q", command))
			}
			arguments, ok := module.Memory().Read(argsPtr, argsLen)
			if !ok {
				panic("respond_request arguments are outside guest memory")
			}
			if err := pbv1.ValidateWire(arguments, (&pbv1.RespondRequestArgs{}).ProtoReflect().Descriptor()); err != nil {
				panic(fmt.Sprintf("invalid closed RespondRequestArgs: %v", err))
			}
			var decoded pbv1.RespondRequestArgs
			if err := proto.Unmarshal(arguments, &decoded); err != nil {
				panic(fmt.Sprintf("decode RespondRequestArgs: %v", err))
			}
			if err := decoded.Validate(); err != nil {
				panic(fmt.Sprintf("validate RespondRequestArgs: %v", err))
			}
			seenMu.Lock()
			seen = &decoded
			seenMu.Unlock()
			ptr := resultPtr.Load()
			if !module.Memory().Write(ptr, resultFrame) {
				panic("write HostCallResult outside guest memory")
			}
			return uint64(ptr)<<32 | uint64(uint32(len(resultFrame)))
		}).
		Export("host_call").
		Instantiate(ctx)
	if err != nil {
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
	input, err := proto.Marshal(&pbv1.HookInput{
		ContractRevision: 1,
		RequestId:        42,
		Payload: &pbv1.HookInput_ChatRequest{ChatRequest: &pbv1.ChatRequest{
			Model: "__respond_request_conformance__",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	alloc := module.ExportedFunction("alloc")
	inputAllocation, err := alloc.Call(ctx, uint64(len(input)))
	if err != nil {
		t.Fatal(err)
	}
	resultAllocation, err := alloc.Call(ctx, uint64(len(resultFrame)))
	if err != nil {
		t.Fatal(err)
	}
	inputPtr := uint32(inputAllocation[0])
	resultPtr.Store(uint32(resultAllocation[0]))
	if !module.Memory().Write(inputPtr, input) {
		t.Fatal("write hook input outside guest memory")
	}
	result, err := module.ExportedFunction("run_hook").Call(ctx, uint64(inputPtr), uint64(len(input)))
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0] != 0 {
		t.Fatalf("responding guest should pass through after host call, got %v", result)
	}
	seenMu.Lock()
	defer seenMu.Unlock()
	if seen == nil || seen.Response == nil {
		t.Fatal("host did not receive RespondRequestArgs.response")
	}
	if seen.Response.FinishReason != "stop" || len(seen.Response.Message.Blocks) != 1 ||
		seen.Response.Message.Blocks[0].GetText().GetText() != "compiled Rust response" {
		t.Fatalf("unexpected synthetic response: %v", seen.Response)
	}
}
