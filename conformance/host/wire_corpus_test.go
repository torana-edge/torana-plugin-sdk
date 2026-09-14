package host

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type guestWireVector struct {
	Name        string `json:"name"`
	MessageType string `json:"message_type"`
	WireHex     string `json:"wire_hex"`
	Valid       bool   `json:"valid"`
}

// TestCompiledGuestsValidateSharedInputCorpus proves both shipped decoders
// enforce the same input-side rows as the native Go and Rust validators. The
// wrapper construction is wire-only: malformed nested bytes are never decoded
// and re-marshaled into a more acceptable message.
func TestCompiledGuestsValidateSharedInputCorpus(t *testing.T) {
	paths := []struct{ language, path string }{{"go", os.Getenv("TORANA_GO_GUEST")}, {"rust", os.Getenv("TORANA_RUST_GUEST")}}
	for _, guest := range paths {
		if guest.path == "" {
			if os.Getenv("TORANA_E2E") == "1" {
				t.Fatalf("TORANA_%s_GUEST is required in E2E mode", strings.ToUpper(guest.language))
			}
			continue
		}
		t.Run(guest.language, func(t *testing.T) { runCompiledInputCorpus(t, guest.path) })
	}
}

func runCompiledInputCorpus(t *testing.T, guestPath string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(moduleRoot(t), "pb", "v1", "wire_vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors []guestWireVector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = runtime.Close(ctx) })
	wasi_snapshot_preview1.MustInstantiate(ctx, runtime)
	if err := instantiateEnvImports(ctx, runtime, nil); err != nil {
		t.Fatal(err)
	}
	guestBytes, err := os.ReadFile(guestPath)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := runtime.CompileModule(ctx, guestBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer compiled.Close(ctx)
	run := 0
	for _, vector := range vectors {
		payload, supported, err := wrapGuestInputVector(vector)
		if err != nil {
			t.Fatalf("%s: %v", vector.Name, err)
		}
		if !supported {
			continue
		}
		run++
		t.Run(vector.Name, func(t *testing.T) {
			module, err := runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(vector.Name))
			if err != nil {
				t.Fatal(err)
			}
			defer module.Close(ctx)
			if initialize := module.ExportedFunction("_initialize"); initialize != nil {
				if _, err := initialize.Call(ctx); err != nil {
					t.Fatal(err)
				}
			}
			alloc, hook, dealloc := module.ExportedFunction("alloc"), module.ExportedFunction("run_hook"), module.ExportedFunction("dealloc")
			allocated, err := alloc.Call(ctx, uint64(len(payload)))
			if err != nil || len(allocated) != 1 {
				t.Fatalf("alloc: %v", err)
			}
			ptr := uint32(allocated[0])
			if !module.Memory().Write(ptr, payload) {
				t.Fatal("write guest input")
			}
			result, callErr := hook.Call(ctx, uint64(ptr), uint64(len(payload)))
			if vector.Valid {
				if callErr != nil {
					t.Fatalf("valid input trapped: %v", callErr)
				}
				if _, err := dealloc.Call(ctx, uint64(ptr), uint64(len(payload))); err != nil {
					t.Fatal(err)
				}
				if len(result) != 1 {
					t.Fatalf("run_hook result = %v", result)
				}
				if result[0] != 0 {
					outPtr, outLen := uint32(result[0]>>32), uint32(result[0])
					if _, err := dealloc.Call(ctx, uint64(outPtr), uint64(outLen)); err != nil {
						t.Fatal(err)
					}
				}
			} else if callErr == nil {
				t.Fatalf("invalid input returned normally: %v", result)
			}
		})
	}
	const expectedInputRows = 12
	if run != expectedInputRows {
		t.Fatalf("ran %d supported shared-corpus rows, want %d; classify every new guest-input row", run, expectedInputRows)
	}
}

func wrapGuestInputVector(vector guestWireVector) ([]byte, bool, error) {
	wire, err := hex.DecodeString(vector.WireHex)
	if err != nil {
		return nil, true, err
	}
	wrap := func(field byte, body []byte) []byte {
		out := []byte{0x08, 0x01, 0x10, 0x01, field}
		out = appendVarint(out, uint64(len(body)))
		return append(out, body...)
	}
	field := func(tag byte, body []byte) []byte {
		out := []byte{tag}
		out = appendVarint(out, uint64(len(body)))
		return append(out, body...)
	}
	switch vector.MessageType {
	case "torana.v1.HookInput":
		return wire, true, nil
	case "torana.v1.StreamEvent":
		return wrap(0x32, wire), true, nil
	case "torana.v1.RequestBlock":
		message := append([]byte{0x0a, 0x04}, []byte("user")...)
		message = append(message, field(0x12, wire)...)
		return wrap(0x22, field(0x12, message)), true, nil
	case "torana.v1.ResponseBlock":
		responseMessage := field(0x1a, wire)
		response := field(0x1a, responseMessage)
		after := field(0x0a, response)
		return wrap(0x2a, after), true, nil
	case "torana.v1.OutputFormat":
		return wrap(0x22, field(0x62, wire)), true, nil
	default:
		return nil, false, nil
	}
}

func appendVarint(dst []byte, value uint64) []byte {
	for value >= 0x80 {
		dst = append(dst, byte(value)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal(fmt.Errorf("module root not found"))
		}
		dir = parent
	}
}
