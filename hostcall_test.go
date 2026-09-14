//go:build !wasip1

package plugin_sdk

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

func TestHostCallRejectsDuplicateResultArms(t *testing.T) {
	value, _ := proto.Marshal(&pbv1.HostCallResult{Result: &pbv1.HostCallResult_Value{Value: []byte("ok")}})
	failure, _ := proto.Marshal(&pbv1.HostCallResult{Result: &pbv1.HostCallResult_Error{Error: &pbv1.HostError{Code: pbv1.ErrorCode_ERROR_CODE_NOT_FOUND}}})
	raw := append([]byte{}, protowire.AppendTag(nil, 1, protowire.BytesType)...)
	raw = protowire.AppendBytes(raw, value)
	raw = protowire.AppendTag(raw, 2, protowire.BytesType)
	raw = protowire.AppendBytes(raw, failure)
	WithTestHost(&TestHost{HostCall: func(string, []byte) ([]byte, error) { return raw, nil }}, func() {
		if _, _, err := HostCall("env.block_request", &pbv1.BlockRequestArgs{Status: 403, Code: "x"}); err == nil {
			t.Fatal("conflicting result arms accepted")
		}
	})
}

func TestBlockRequestReturnsTypedRefusal(t *testing.T) {
	raw, _ := proto.Marshal(&pbv1.HostCallResult{Result: &pbv1.HostCallResult_Error{Error: &pbv1.HostError{Code: pbv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED, Message: "denied"}}})
	WithTestHost(&TestHost{HostCall: func(string, []byte) ([]byte, error) { return raw, nil }}, func() {
		err := BlockRequest(403, "x", "y")
		var refusal *HostCallRefusalError
		if !errors.As(err, &refusal) || refusal.Code != pbv1.ErrorCode_ERROR_CODE_PERMISSION_DENIED {
			t.Fatalf("error=%v, refusal=%v", err, refusal)
		}
	})
}

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

func TestMustHostCallPanicsOnClassifiedHostError(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
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
