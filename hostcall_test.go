//go:build !wasip1

package plugin_sdk

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

func TestHostCallRejectsEmptyCommand(t *testing.T) {
	_, _, err := HostCall("", nil)
	if err == nil || !strings.Contains(err.Error(), "command") {
		t.Fatalf("got %v", err)
	}
}

func TestHostCallRejectsInvalidArgs(t *testing.T) {
	_, _, err := HostCall("env.block_request", &pbv1.BlockRequestArgs{Status: 200, Code: "x"})
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("got %v", err)
	}
}

func TestHostCallRejectsEmptyReply(t *testing.T) {
	WithTestHost(&TestHost{
		HostCall: func(string, []byte) ([]byte, error) { return nil, nil },
	}, func() {
		_, _, err := HostCall("env.block_request", &pbv1.BlockRequestArgs{
			Status: 403, Code: "x", Message: "y",
		})
		if err == nil || !strings.Contains(err.Error(), "empty reply") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestHostCallTypedError(t *testing.T) {
	raw, _ := proto.Marshal(&pbv1.HostCallResult{
		Result: &pbv1.HostCallResult_Error{Error: &pbv1.HostError{
			Code: pbv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, Message: "no",
		}},
	})
	WithTestHost(&TestHost{
		HostCall: func(string, []byte) ([]byte, error) { return raw, nil },
	}, func() {
		val, herr, err := HostCall("env.block_request", &pbv1.BlockRequestArgs{
			Status: 403, Code: "x", Message: "y",
		})
		if err != nil || val != nil || herr == nil || herr.Code != pbv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED {
			t.Fatalf("val=%v herr=%v err=%v", val, herr, err)
		}
	})
}

func TestMustHostCallPanicsOnProtocolError(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	WithTestHost(&TestHost{
		HostCall: func(string, []byte) ([]byte, error) { return []byte("garbage"), nil },
	}, func() {
		mustHostCall("env.route_request", &pbv1.RouteRequestArgs{Model: "m"})
	})
}

func TestMustHostCallIgnoresClassifiedHostError(t *testing.T) {
	raw, _ := proto.Marshal(&pbv1.HostCallResult{
		Result: &pbv1.HostCallResult_Error{Error: &pbv1.HostError{
			Code: pbv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, Message: "no",
		}},
	})
	WithTestHost(&TestHost{
		HostCall: func(string, []byte) ([]byte, error) { return raw, nil },
	}, func() {
		mustHostCall("env.set_identity", &pbv1.SetIdentityArgs{Identity: "u"})
	})
}
