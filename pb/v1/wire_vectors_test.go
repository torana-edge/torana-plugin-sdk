package v1

import (
	"encoding/hex"
	"encoding/json"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"os"
	"testing"
)

func TestSharedWireConformanceVectors(t *testing.T) {
	raw, err := os.ReadFile("wire_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name        string `json:"name"`
		MessageType string `json:"message_type"`
		WireHex     string `json:"wire_hex"`
		Valid       bool   `json:"valid"`
	}
	if err = json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, vector := range vectors {
		t.Run(vector.Name, func(t *testing.T) {
			mt, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(vector.MessageType))
			if err != nil {
				t.Fatal(err)
			}
			message := mt.New().Interface()
			wire, err := hex.DecodeString(vector.WireHex)
			if err != nil {
				t.Fatal(err)
			}
			err = ValidateWire(wire, message.ProtoReflect().Descriptor())
			if err == nil {
				err = proto.Unmarshal(wire, message)
			}
			if err == nil {
				if result, ok := message.(*HookResult); ok {
					hook := Hook_HOOK_BEFORE_REQUEST
					switch result.Action.(type) {
					case *HookResult_Suppress, *HookResult_EmitEvents:
						hook = Hook_HOOK_ON_STREAM_CHUNK
					case *HookResult_ReplaceResponse:
						hook = Hook_HOOK_AFTER_RESPONSE
					case *HookResult_ServeHttp:
						hook = Hook_HOOK_ON_HTTP_REQUEST
					case *HookResult_TickOutcome:
						hook = Hook_HOOK_ON_TICK
					}
					err = result.ValidateFor(hook)
				} else if validator, ok := message.(interface{ Validate() error }); ok {
					err = validator.Validate()
				} else {
					t.Fatalf("corpus type %s has no semantic validator", vector.MessageType)
				}
			}
			if (err == nil) != vector.Valid {
				t.Fatalf("valid=%v, want %v: %v", err == nil, vector.Valid, err)
			}
		})
	}
}
