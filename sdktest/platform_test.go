package sdktest

import (
	"bytes"
	"testing"

	sdk "github.com/torana-edge/torana-plugin-sdk"
	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

func TestPlatformCapabilityState(t *testing.T) {
	h := New(t).SetCredential("api", []byte("secret")).SeedFile("usage.jsonl", []byte("one\n"))
	h.Run(func() {
		credential, refusal, err := sdk.GetCredential("api")
		if err != nil || refusal != nil || string(credential) != "secret" {
			t.Fatalf("GetCredential = %q, %v, %v", credential, refusal, err)
		}
		if refusal, err := sdk.AppendFile("usage.jsonl", []byte("two\n")); err != nil || refusal != nil {
			t.Fatalf("AppendFile = %v, %v", refusal, err)
		}
		paths, refusal, err := sdk.ListFiles("usage")
		if err != nil || refusal != nil || len(paths) != 1 || paths[0] != "usage.jsonl" {
			t.Fatalf("ListFiles = %v, %v, %v", paths, refusal, err)
		}
		if _, refusal, err := sdk.HTTPRequest(&pbv1.OutboundHTTPRequestArgs{Endpoint: "service", Method: "GET", Path: "/"}); err != nil || refusal == nil || refusal.Code != pbv1.ErrorCode_ERROR_CODE_NOT_CONFIGURED {
			t.Fatalf("HTTPRequest = %v, %v", refusal, err)
		}
	})
	data, ok := h.File("usage.jsonl")
	if !ok || !bytes.Equal(data, []byte("one\ntwo\n")) {
		t.Fatalf("file = %q, %v", data, ok)
	}
	data[0] = 'X'
	again, _ := h.File("usage.jsonl")
	if again[0] != 'o' {
		t.Fatal("File returned harness-owned storage")
	}
}
