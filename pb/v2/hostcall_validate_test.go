package v2_test

import (
	"strings"
	"testing"

	v2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
	"google.golang.org/protobuf/proto"
)

func TestHostCallResultEmptyValueIsSuccess(t *testing.T) {
	ok := &v2.HostCallResult{Result: &v2.HostCallResult_Value{Value: []byte{}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	nilBytes := &v2.HostCallResult{Result: &v2.HostCallResult_Value{Value: nil}}
	if err := nilBytes.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestHostCallResultRequiresAnArm(t *testing.T) {
	if err := (&v2.HostCallResult{}).Validate(); err == nil {
		t.Fatal("empty HostCallResult must be rejected")
	}
	if err := (*v2.HostCallResult)(nil).Validate(); err == nil {
		t.Fatal("nil HostCallResult must be rejected")
	}
}

func TestHostCallResultErrorMustBeClassified(t *testing.T) {
	cases := []struct {
		name string
		r    *v2.HostCallResult
		want string
	}{
		{
			name: "typed-nil error wrapper",
			r:    &v2.HostCallResult{Result: (*v2.HostCallResult_Error)(nil)},
			want: "no HostError",
		},
		{
			name: "nil nested error",
			r:    &v2.HostCallResult{Result: &v2.HostCallResult_Error{Error: nil}},
			want: "no HostError",
		},
		{
			name: "unspecified code",
			r: &v2.HostCallResult{Result: &v2.HostCallResult_Error{
				Error: &v2.HostError{Code: v2.ErrorCode_ERROR_CODE_UNSPECIFIED, Message: "x"},
			}},
			want: "UNSPECIFIED",
		},
		{
			name: "unknown code",
			r: &v2.HostCallResult{Result: &v2.HostCallResult_Error{
				Error: &v2.HostError{Code: v2.ErrorCode(99), Message: "x"},
			}},
			want: "not recognised",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.r.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want substring %q", err, tc.want)
			}
		})
	}

	ok := &v2.HostCallResult{Result: &v2.HostCallResult_Error{
		Error: &v2.HostError{Code: v2.ErrorCode_ERROR_CODE_NOT_FOUND, Message: "gone"},
	}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestHostCallResultRejectsUnknownTopLevelFields(t *testing.T) {
	// Encode a result, then stuff unknown bytes at the top level the way a
	// newer ABI arm or a double-arm handwritten frame would.
	ok := &v2.HostCallResult{Result: &v2.HostCallResult_Value{Value: []byte("x")}}
	raw, err := proto.Marshal(ok)
	if err != nil {
		t.Fatal(err)
	}
	// Field 15, wire type 2 (length-delimited), length 1, byte 0x00 — unknown.
	raw = append(raw, 0x7a, 0x01, 0x00)
	var got v2.HostCallResult
	if err := proto.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if err := got.Validate(); err == nil {
		t.Fatal("unknown top-level field must be rejected")
	}
}

func TestVerdictAndMetaAppendArgs(t *testing.T) {
	t.Run("block", func(t *testing.T) {
		if err := (&v2.BlockRequestArgs{Status: 422, Code: "pii"}).Validate(); err != nil {
			t.Fatal(err)
		}
		if err := (&v2.BlockRequestArgs{Code: "pii"}).Validate(); err == nil {
			t.Fatal("zero status must fail")
		}
		if err := (&v2.BlockRequestArgs{Status: 422}).Validate(); err == nil {
			t.Fatal("empty code must fail")
		}
	})
	t.Run("respond", func(t *testing.T) {
		if err := (&v2.RespondRequestArgs{}).Validate(); err != nil {
			t.Fatal(err)
		}
		if err := (*v2.RespondRequestArgs)(nil).Validate(); err == nil {
			t.Fatal("nil must fail")
		}
	})
	t.Run("route", func(t *testing.T) {
		if err := (&v2.RouteRequestArgs{Model: "cheap"}).Validate(); err != nil {
			t.Fatal(err)
		}
		if err := (&v2.RouteRequestArgs{Provider: "local"}).Validate(); err != nil {
			t.Fatal(err)
		}
		if err := (&v2.RouteRequestArgs{}).Validate(); err == nil {
			t.Fatal("empty route must fail")
		}
	})
	t.Run("set identity", func(t *testing.T) {
		if err := (&v2.SetIdentityArgs{Identity: "user:1"}).Validate(); err != nil {
			t.Fatal(err)
		}
		if err := (&v2.SetIdentityArgs{}).Validate(); err == nil {
			t.Fatal("empty identity must fail")
		}
	})
	t.Run("meta append", func(t *testing.T) {
		if err := (&v2.MetaAppendArgs{BlockIndex: 0, Fragment: nil}).Validate(); err != nil {
			t.Fatal(err)
		}
		if err := (&v2.MetaAppendArgs{BlockIndex: -1, Fragment: []byte("x")}).Validate(); err == nil {
			t.Fatal("negative block_index must fail")
		}
	})
}

func TestHostCallArgRoundTrip(t *testing.T) {
	// Schemas must survive marshal/unmarshal so host and guest agree on wire.
	msgs := []proto.Message{
		&v2.BlockRequestArgs{Status: 403, Code: "denied", Message: "no"},
		&v2.RespondRequestArgs{Content: "hi"},
		&v2.RouteRequestArgs{Provider: "p", Model: "m"},
		&v2.SetIdentityArgs{Identity: "id"},
		&v2.MetaAppendArgs{BlockIndex: 2, Fragment: []byte("frag")},
	}
	for _, m := range msgs {
		raw, err := proto.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		dst := proto.Clone(m)
		proto.Reset(dst)
		if err := proto.Unmarshal(raw, dst); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(m, dst) {
			t.Fatalf("round-trip mismatch for %T", m)
		}
	}
}
