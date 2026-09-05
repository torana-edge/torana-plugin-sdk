package v1_test

import (
	"bytes"
	"math"
	"strings"
	"testing"

	v1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestHostCallResultEmptyValueIsSuccess(t *testing.T) {
	ok := &v1.HostCallResult{Result: &v1.HostCallResult_Value{Value: []byte{}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	nilBytes := &v1.HostCallResult{Result: &v1.HostCallResult_Value{Value: nil}}
	if err := nilBytes.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestHostCallResultRequiresAnArm(t *testing.T) {
	if err := (&v1.HostCallResult{}).Validate(); err == nil {
		t.Fatal("empty HostCallResult must be rejected")
	}
	if err := (*v1.HostCallResult)(nil).Validate(); err == nil {
		t.Fatal("nil HostCallResult must be rejected")
	}
}

func TestHostCallResultErrorMustBeClassified(t *testing.T) {
	cases := []struct {
		name string
		r    *v1.HostCallResult
		want string
	}{
		{
			name: "typed-nil error wrapper",
			r:    &v1.HostCallResult{Result: (*v1.HostCallResult_Error)(nil)},
			want: "no HostError",
		},
		{
			name: "nil nested error",
			r:    &v1.HostCallResult{Result: &v1.HostCallResult_Error{Error: nil}},
			want: "no HostError",
		},
		{
			name: "unspecified code",
			r: &v1.HostCallResult{Result: &v1.HostCallResult_Error{
				Error: &v1.HostError{Code: v1.ErrorCode_ERROR_CODE_UNSPECIFIED, Message: "x"},
			}},
			want: "UNSPECIFIED",
		},
		{
			name: "unknown code",
			r: &v1.HostCallResult{Result: &v1.HostCallResult_Error{
				Error: &v1.HostError{Code: v1.ErrorCode(99), Message: "x"},
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

	ok := &v1.HostCallResult{Result: &v1.HostCallResult_Error{
		Error: &v1.HostError{Code: v1.ErrorCode_ERROR_CODE_NOT_FOUND, Message: "gone"},
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
	ok := &v1.HostCallResult{Result: &v1.HostCallResult_Value{Value: []byte("x")}}
	raw, err := proto.Marshal(ok)
	if err != nil {
		t.Fatal(err)
	}
	// Field 15, wire type 2 (length-delimited), length 1, byte 0x00 — unknown.
	raw = append(raw, 0x7a, 0x01, 0x00)
	var got v1.HostCallResult
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
	errBody, err := proto.Marshal(&v1.HostError{
		Code:    v1.ErrorCode_ERROR_CODE_NOT_FOUND,
		Message: "gone",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw = protowire.AppendTag(raw, 2, protowire.BytesType)
	raw = protowire.AppendBytes(raw, errBody)

	var got v1.HostCallResult
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
	if v1.MetaAppendCommand != "env.meta_append" {
		t.Fatalf("command: got %q", v1.MetaAppendCommand)
	}
	if v1.MetaAppendPermission != "env.meta_set" {
		t.Fatalf("permission: got %q", v1.MetaAppendPermission)
	}
	// Dispatchers that derive the permission from the command string would
	// look for env.meta_append — which is not a declared capability.
	if v1.MetaAppendCommand == v1.MetaAppendPermission {
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
			got := v1.MetaAppendSuccessValue(tc.fragment, tc.current, tc.present)
			if !bytes.Equal(got, tc.wantVal) {
				t.Fatalf("value=%q, want %q", got, tc.wantVal)
			}
			ok := &v1.HostCallResult{Result: &v1.HostCallResult_Value{Value: got}}
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
		got := v1.MetaAppendSuccessValue(frag, big, true)
		if len(got) != 0 {
			t.Fatalf("non-empty fragment must ack with empty value, got %d bytes", len(got))
		}
	})
}

func TestVerdictAndMetaAppendArgs(t *testing.T) {
	t.Run("block", func(t *testing.T) {
		if err := (&v1.BlockRequestArgs{Status: 422, Code: "pii"}).Validate(); err != nil {
			t.Fatal(err)
		}
		if err := (&v1.BlockRequestArgs{Code: "pii"}).Validate(); err == nil {
			t.Fatal("zero status must fail")
		}
		if err := (&v1.BlockRequestArgs{Status: 200, Code: "pii"}).Validate(); err == nil {
			t.Fatal("2xx status must fail for a block verdict")
		}
		if err := (&v1.BlockRequestArgs{Status: 99, Code: "pii"}).Validate(); err == nil {
			t.Fatal("status below 400 must fail")
		}
		if err := (&v1.BlockRequestArgs{Status: 422}).Validate(); err == nil {
			t.Fatal("empty code must fail")
		}
	})
	t.Run("respond", func(t *testing.T) {
		if err := (&v1.RespondRequestArgs{}).Validate(); err != nil {
			t.Fatal(err)
		}
		if err := (*v1.RespondRequestArgs)(nil).Validate(); err == nil {
			t.Fatal("nil must fail")
		}
	})
	t.Run("route", func(t *testing.T) {
		if err := (&v1.RouteRequestArgs{Model: "cheap"}).Validate(); err != nil {
			t.Fatal(err)
		}
		if err := (&v1.RouteRequestArgs{Provider: "local"}).Validate(); err != nil {
			t.Fatal(err)
		}
		if err := (&v1.RouteRequestArgs{}).Validate(); err == nil {
			t.Fatal("empty route must fail")
		}
	})
	t.Run("set identity", func(t *testing.T) {
		if err := (&v1.SetIdentityArgs{Identity: "user:1"}).Validate(); err != nil {
			t.Fatal(err)
		}
		if err := (&v1.SetIdentityArgs{}).Validate(); err == nil {
			t.Fatal("empty identity must fail")
		}
	})
	t.Run("meta append", func(t *testing.T) {
		if err := (&v1.MetaAppendArgs{BlockIndex: 0, Fragment: nil}).Validate(); err != nil {
			t.Fatal(err)
		}
		if err := (&v1.MetaAppendArgs{BlockIndex: -1, Fragment: []byte("x")}).Validate(); err == nil {
			t.Fatal("negative block_index must fail")
		}
	})
}

func TestHostCallArgRoundTrip(t *testing.T) {
	// Schemas must survive marshal/unmarshal so host and guest agree on wire.
	msgs := []proto.Message{
		&v1.BlockRequestArgs{Status: 403, Code: "denied", Message: "no"},
		&v1.RespondRequestArgs{Content: "hi"},
		&v1.RouteRequestArgs{Provider: "p", Model: "m"},
		&v1.SetIdentityArgs{Identity: "id"},
		&v1.MetaAppendArgs{BlockIndex: 2, Fragment: []byte("frag")},
		&v1.ModelCompleteArgs{Service: "summarizer", Messages: []*v1.ModelMessage{{Role: "user", Content: "text"}}},
		&v1.ModelPricingGetArgs{Resource: "request-model"},
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

func TestModelResourceValidation(t *testing.T) {
	one := uint32(1)
	zero := uint32(0)
	finite := 0.25
	nan := math.NaN()
	valid := &v1.ModelCompleteArgs{
		Service:     "scanner",
		Messages:    []*v1.ModelMessage{{Role: "system", Content: "classify"}, {Role: "user", Content: "text"}},
		MaxTokens:   &one,
		Temperature: &finite,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid model request: %v", err)
	}
	for name, request := range map[string]*v1.ModelCompleteArgs{
		"nil":             nil,
		"invalid slot":    {Service: "../scanner", Messages: []*v1.ModelMessage{{Role: "user"}}},
		"no messages":     {Service: "scanner"},
		"nil message":     {Service: "scanner", Messages: []*v1.ModelMessage{nil}},
		"empty role":      {Service: "scanner", Messages: []*v1.ModelMessage{{Content: "x"}}},
		"zero tokens":     {Service: "scanner", Messages: []*v1.ModelMessage{{Role: "user"}}, MaxTokens: &zero},
		"nan temperature": {Service: "scanner", Messages: []*v1.ModelMessage{{Role: "user"}}, Temperature: &nan},
	} {
		t.Run(name, func(t *testing.T) {
			if err := request.Validate(); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}

	negative := -0.1
	inf := math.Inf(1)
	for name, pricing := range map[string]*v1.ModelPricing{
		"nil":      nil,
		"negative": {InputUsdPerMtok: &negative},
		"infinite": {CacheWriteUsdPerMtok: &inf},
	} {
		t.Run("pricing "+name, func(t *testing.T) {
			if err := pricing.Validate(); err == nil {
				t.Fatal("invalid pricing accepted")
			}
		})
	}
	free := 0.0
	if err := (&v1.ModelPricing{InputUsdPerMtok: &free}).Validate(); err != nil {
		t.Fatalf("explicit free rate rejected: %v", err)
	}
	if err := (&v1.ModelCompleteResult{Usage: &v1.Usage{InputTokens: -1}}).Validate(); err == nil {
		t.Fatal("negative model usage accepted")
	}
	unknownArgs := proto.Clone(valid).(*v1.ModelCompleteArgs)
	unknownArgs.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
	if err := unknownArgs.Validate(); err == nil {
		t.Fatal("unknown model args field accepted")
	}
	unknownMessage := proto.Clone(valid).(*v1.ModelCompleteArgs)
	unknownMessage.Messages[0].ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
	if err := unknownMessage.Validate(); err == nil {
		t.Fatal("unknown model message field accepted")
	}
	unknownUsage := &v1.ModelCompleteResult{Usage: &v1.Usage{}}
	unknownUsage.Usage.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
	if err := unknownUsage.Validate(); err == nil {
		t.Fatal("unknown usage field accepted")
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
		{"StateGetArgs nil", (*v1.StateGetArgs)(nil), true},
		{"StateGetArgs empty key", &v1.StateGetArgs{}, true},
		{"StateGetArgs valid", &v1.StateGetArgs{Key: "k"}, false},

		{"StateSetArgs nil", (*v1.StateSetArgs)(nil), true},
		{"StateSetArgs empty key", &v1.StateSetArgs{Value: "v"}, true},
		{"StateSetArgs valid", &v1.StateSetArgs{Key: "k", Value: "v"}, false},
		// An empty VALUE is legitimate: it stores an empty string. Deletion is
		// represented by the dedicated state-delete command.
		{"StateSetArgs empty value", &v1.StateSetArgs{Key: "k"}, false},

		{"StateDeleteArgs nil", (*v1.StateDeleteArgs)(nil), true},
		{"StateDeleteArgs empty key", &v1.StateDeleteArgs{}, true},
		{"StateDeleteArgs valid", &v1.StateDeleteArgs{Key: "k"}, false},
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
