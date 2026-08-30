package plugin_sdk

import (
	"reflect"
	"testing"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"google.golang.org/protobuf/proto"
)

func framedValue(t *testing.T, value []byte) []byte {
	t.Helper()
	b, err := proto.Marshal(&pbv1.HostCallResult{Result: &pbv1.HostCallResult_Value{Value: value}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPlatformHelpersUseTypedCommands(t *testing.T) {
	var commands []string
	h := &TestHost{HostCall: func(command string, raw []byte) ([]byte, error) {
		commands = append(commands, command)
		switch command {
		case "env.credential_get":
			var args pbv1.CredentialGetArgs
			if err := proto.Unmarshal(raw, &args); err != nil || args.Slot != "github" {
				t.Fatalf("credential args = %q, %v", args.Slot, err)
			}
			return framedValue(t, []byte("secret")), nil
		case "env.file_append", "env.file_write", "env.file_delete":
			return framedValue(t, nil), nil
		case "env.file_read":
			return framedValue(t, []byte("line\n")), nil
		case "env.file_list":
			value, err := proto.Marshal(&pbv1.FileListResult{Paths: []string{"usage.jsonl"}})
			if err != nil {
				t.Fatal(err)
			}
			return framedValue(t, value), nil
		case "env.http_request":
			value, err := proto.Marshal(&pbv1.OutboundHTTPResponse{Status: 200, Body: []byte("ok")})
			if err != nil {
				t.Fatal(err)
			}
			return framedValue(t, value), nil
		default:
			t.Fatalf("unexpected command %q", command)
			return nil, nil
		}
	}}
	WithTestHost(h, func() {
		if value, herr, err := GetCredential("github"); err != nil || herr != nil || string(value) != "secret" {
			t.Fatalf("GetCredential = %q, %v, %v", value, herr, err)
		}
		if herr, err := AppendFile("usage.jsonl", []byte("{}\n")); err != nil || herr != nil {
			t.Fatalf("AppendFile = %v, %v", herr, err)
		}
		if value, herr, err := ReadFile("usage.jsonl"); err != nil || herr != nil || string(value) != "line\n" {
			t.Fatalf("ReadFile = %q, %v, %v", value, herr, err)
		}
		if herr, err := WriteFile("state.json", []byte("{}")); err != nil || herr != nil {
			t.Fatalf("WriteFile = %v, %v", herr, err)
		}
		if paths, herr, err := ListFiles(""); err != nil || herr != nil || !reflect.DeepEqual(paths, []string{"usage.jsonl"}) {
			t.Fatalf("ListFiles = %v, %v, %v", paths, herr, err)
		}
		if herr, err := DeleteFile("state.json"); err != nil || herr != nil {
			t.Fatalf("DeleteFile = %v, %v", herr, err)
		}
		response, herr, err := HTTPRequest(&pbv1.OutboundHTTPRequestArgs{Endpoint: "github", Method: "GET", Path: "/user"})
		if err != nil || herr != nil || response.Status != 200 || string(response.Body) != "ok" {
			t.Fatalf("HTTPRequest = %#v, %v, %v", response, herr, err)
		}
	})
	want := []string{"env.credential_get", "env.file_append", "env.file_read", "env.file_write", "env.file_list", "env.file_delete", "env.http_request"}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %v, want %v", commands, want)
	}
}

func TestPlatformHelpersRejectInvalidArgumentsBeforeHost(t *testing.T) {
	calls := 0
	WithTestHost(&TestHost{HostCall: func(string, []byte) ([]byte, error) {
		calls++
		return nil, nil
	}}, func() {
		if _, _, err := GetCredential("../secret"); err == nil {
			t.Fatal("invalid credential slot accepted")
		}
		if _, err := AppendFile("../escape", nil); err == nil {
			t.Fatal("traversal path accepted")
		}
		if _, _, err := HTTPRequest(&pbv1.OutboundHTTPRequestArgs{Endpoint: "api", Method: "GET", Path: "https://evil.example/"}); err == nil {
			t.Fatal("absolute URL accepted")
		}
	})
	if calls != 0 {
		t.Fatalf("invalid arguments made %d host calls", calls)
	}
}

func TestPlatformPermissionsAreRequestable(t *testing.T) {
	for _, permission := range []string{
		"env.credential_get", "env.file_append", "env.file_read", "env.file_write",
		"env.file_list", "env.file_delete", "env.http_request",
	} {
		if !IsPermission(permission) {
			t.Errorf("%q is not requestable", permission)
		}
	}
}
