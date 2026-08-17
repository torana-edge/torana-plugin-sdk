package v2_test

import (
	"bytes"
	"strings"
	"testing"

	v2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
	"google.golang.org/protobuf/encoding/protowire"
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
	// newer ABI arm would. This is not a double-known-arm case: two known arms
	// (fields 1 and 2) last-wins on unmarshal and leave no unknown bytes —
	// HostCallResult accepts that because the envelope is host-produced.
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

func TestHostCallResultKnownDoubleArmLastWins(t *testing.T) {
	// Document the honest rule: two known arms on the wire are not detectable
	// after unmarshal. Field 1 then field 2 → error arm wins.
	raw := protowire.AppendTag(nil, 1, protowire.BytesType)
	raw = protowire.AppendBytes(raw, []byte("value"))
	errBody, err := proto.Marshal(&v2.HostError{
		Code:    v2.ErrorCode_ERROR_CODE_NOT_FOUND,
		Message: "gone",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw = protowire.AppendTag(raw, 2, protowire.BytesType)
	raw = protowire.AppendBytes(raw, errBody)

	var got v2.HostCallResult
	if err := proto.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.ProtoReflect().GetUnknown()) != 0 {
		t.Fatal("precondition: known double-arm must leave no unknown fields")
	}
	if got.GetError() == nil {
		t.Fatal("precondition: last arm (error) should win")
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("host-produced last-wins frame must validate: %v", err)
	}
}

func TestMetaAppendContract(t *testing.T) {
	if v2.MetaAppendCommand != "env.meta_append" {
		t.Fatalf("command: got %q", v2.MetaAppendCommand)
	}
	if v2.MetaAppendPermission != "env.meta_set" {
		t.Fatalf("permission: got %q", v2.MetaAppendPermission)
	}
	// Dispatchers that derive the permission from the command string would
	// look for env.meta_append — which is not a declared capability.
	if v2.MetaAppendCommand == v2.MetaAppendPermission {
		t.Fatal("command and permission must differ so the dispatcher special-case is load-bearing")
	}

	cases := []struct {
		name     string
		current  []byte
		present  bool
		fragment []byte
		wantVal  []byte
	}{
		{"absent empty read", nil, false, nil, []byte{}},
		{"absent empty-slice read", nil, false, []byte{}, []byte{}},
		{"present empty read", []byte("ab"), true, nil, []byte("ab")},
		{"absent create ack", nil, false, []byte("xy"), []byte{}},
		{"present append ack", []byte("ab"), true, []byte("cd"), []byte{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := v2.MetaAppendSuccessValue(tc.fragment, tc.current, tc.present)
			if !bytes.Equal(got, tc.wantVal) {
				t.Fatalf("value=%q, want %q", got, tc.wantVal)
			}
			ok := &v2.HostCallResult{Result: &v2.HostCallResult_Value{Value: got}}
			if err := ok.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}

	// Ordinary appends must not imply returning the cumulative buffer — that
	// would force O(total×fragments) copies across the WASM boundary.
	t.Run("append ack is constant-size", func(t *testing.T) {
		big := bytes.Repeat([]byte("x"), 1<<20)
		frag := []byte("more")
		got := v2.MetaAppendSuccessValue(frag, big, true)
		if len(got) != 0 {
			t.Fatalf("non-empty fragment must ack with empty value, got %d bytes", len(got))
		}
	})
}

func TestVerdictAndMetaAppendArgs(t *testing.T) {
	t.Run("block", func(t *testing.T) {
		if err := (&v2.BlockRequestArgs{Status: 422, Code: "pii"}).Validate(); err != nil {
			t.Fatal(err)
		}
		if err := (&v2.BlockRequestArgs{Code: "pii"}).Validate(); err == nil {
			t.Fatal("zero status must fail")
		}
		if err := (&v2.BlockRequestArgs{Status: 200, Code: "pii"}).Validate(); err == nil {
			t.Fatal("2xx status must fail for a block verdict")
		}
		if err := (&v2.BlockRequestArgs{Status: 99, Code: "pii"}).Validate(); err == nil {
			t.Fatal("status below 400 must fail")
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

// The durable-state argument bodies are part of the adversarial guest boundary:
// the host validates bytes a guest controls, so the guest chooses when each
// path runs. A nil message must be refused rather than dereferenced.
func TestStateArgsValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    interface{ Validate() error }
		wantErr bool
	}{
		{"StateGetArgs nil", (*v2.StateGetArgs)(nil), true},
		{"StateGetArgs empty key", &v2.StateGetArgs{}, true},
		{"StateGetArgs valid", &v2.StateGetArgs{Key: "k"}, false},

		{"StateSetArgs nil", (*v2.StateSetArgs)(nil), true},
		{"StateSetArgs empty key", &v2.StateSetArgs{Value: "v"}, true},
		{"StateSetArgs valid", &v2.StateSetArgs{Key: "k", Value: "v"}, false},
		// An empty VALUE is legitimate: it stores an empty string. Deletion is
		// represented by the dedicated state-delete command.
		{"StateSetArgs empty value", &v2.StateSetArgs{Key: "k"}, false},

		{"StateDeleteArgs nil", (*v2.StateDeleteArgs)(nil), true},
		{"StateDeleteArgs empty key", &v2.StateDeleteArgs{}, true},
		{"StateDeleteArgs valid", &v2.StateDeleteArgs{Key: "k"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.args.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("accepted an invalid argument body")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("rejected a valid argument body: %v", err)
			}
		})
	}
}
