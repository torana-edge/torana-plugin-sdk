//go:build !wasip1

package plugin_sdk

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// frameError builds a framed classified refusal, exactly as the host sends it.
func frameError(code pbv2.ErrorCode, msg string) []byte {
	raw, _ := proto.Marshal(&pbv2.HostCallResult{
		Result: &pbv2.HostCallResult_Error{Error: &pbv2.HostError{Code: code, Message: msg}},
	})
	return raw
}

func frameValue(b []byte) []byte {
	raw, _ := proto.Marshal(&pbv2.HostCallResult{
		Result: &pbv2.HostCallResult_Value{Value: b},
	})
	return raw
}

// allCodes is every code the host may legally frame: the six classifications.
// UNSPECIFIED and unknown codes are rejected by the SDK's own HostCallResult
// validator as protocol defects BEFORE any helper sees them (see
// TestUnclassifiableFramesAreProtocolErrors), so they can never be refusals.
func allCodes() []pbv2.ErrorCode {
	codes := []pbv2.ErrorCode{
		pbv2.ErrorCode_ERROR_CODE_NOT_FOUND,
		pbv2.ErrorCode_ERROR_CODE_NOT_CONFIGURED,
		pbv2.ErrorCode_ERROR_CODE_PERMISSION_DENIED,
		pbv2.ErrorCode_ERROR_CODE_UNAVAILABLE,
		pbv2.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
		pbv2.ErrorCode_ERROR_CODE_INTERNAL,
	}
	return codes
}

// assertRefusalPreserved checks the classification contract: errors.As
// recovers the typed refusal with the exact code and message. wantSentinel
// additionally requires (for the STATE helpers only) that NOT_CONFIGURED
// satisfies errors.Is(ErrStateUnavailable) — and that NO other code matches
// the sentinel. Now has no sentinel contract and passes wantSentinel=false.
func assertRefusalPreserved(t *testing.T, err error, code pbv2.ErrorCode, wantSentinel bool) {
	t.Helper()
	if err == nil {
		t.Fatalf("code %v: expected an error", code)
	}
	var refusal *HostCallRefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("code %v: errors.As did not recover *HostCallRefusalError from %v", code, err)
	}
	if refusal.Code != code {
		t.Fatalf("code %v: refusal code %v", code, refusal.Code)
	}
	if refusal.Message != "stub" {
		t.Fatalf("code %v: refusal message %q, want %q", code, refusal.Message, "stub")
	}
	if !wantSentinel {
		return
	}
	if code == pbv2.ErrorCode_ERROR_CODE_NOT_CONFIGURED {
		if !errors.Is(err, ErrStateUnavailable) {
			t.Fatalf("NOT_CONFIGURED must keep errors.Is(ErrStateUnavailable): %v", err)
		}
	} else if errors.Is(err, ErrStateUnavailable) {
		t.Fatalf("code %v: ErrStateUnavailable matched outside NOT_CONFIGURED", code)
	}
}

// TestStateGetJSONPreservesClassifiedRefusals — every framed refusal on the
// state read path satisfies BOTH contracts: errors.As recovers the typed
// refusal with the exact code, and NOT_CONFIGURED keeps
// errors.Is(ErrStateUnavailable). A caller can therefore branch
// advisory-vs-contract without string matching.
func TestStateGetJSONPreservesClassifiedRefusals(t *testing.T) {
	for _, code := range allCodes() {
		if code == pbv2.ErrorCode_ERROR_CODE_NOT_FOUND {
			continue // documented absence: found=false, nil error (tested separately)
		}
		t.Run(code.String(), func(t *testing.T) {
			WithTestHost(&TestHost{
				HostCall: func(cmd string, args []byte) ([]byte, error) {
					if cmd != "env.state_get" {
						t.Fatalf("unexpected command %q", cmd)
					}
					return frameError(code, "stub"), nil
				},
			}, func() {
				var v struct{ X int }
				_, err := StateGetJSON("k", &v)
				assertRefusalPreserved(t, err, code, true)
				if !strings.Contains(err.Error(), `"k"`) {
					t.Fatalf("error does not name the key: %v", err)
				}
			})
		})
	}
}

// TestStateSetJSONPreservesClassifiedRefusals — same contract on the write
// path.
func TestStateSetJSONPreservesClassifiedRefusals(t *testing.T) {
	for _, code := range allCodes() {
		t.Run(code.String(), func(t *testing.T) {
			WithTestHost(&TestHost{
				HostCall: func(cmd string, args []byte) ([]byte, error) {
					if cmd != "env.state_set" {
						t.Fatalf("unexpected command %q", cmd)
					}
					return frameError(code, "stub"), nil
				},
			}, func() {
				err := StateSetJSON("k", map[string]int{"x": 1})
				assertRefusalPreserved(t, err, code, true)
			})
		})
	}
}

// TestNowPreservesClassifiedRefusals — the clock helper carries the same
// typed refusal through for every code.
func TestNowPreservesClassifiedRefusals(t *testing.T) {
	for _, code := range allCodes() {
		t.Run(code.String(), func(t *testing.T) {
			WithTestHost(&TestHost{
				HostCall: func(cmd string, args []byte) ([]byte, error) {
					if cmd != "env.now" {
						t.Fatalf("unexpected command %q", cmd)
					}
					return frameError(code, "stub"), nil
				},
			}, func() {
				_, err := Now()
				assertRefusalPreserved(t, err, code, false)
			})
		})
	}
}

// TestStateHelpersMalformedFrameIsNotARefusal — a malformed HostCallResult
// frame is a protocol defect: a plain error, never a refusal, never the
// sentinel.
func TestStateHelpersMalformedFrameIsNotARefusal(t *testing.T) {
	for _, cmd := range []string{"env.state_get", "env.state_set", "env.now"} {
		t.Run(cmd, func(t *testing.T) {
			WithTestHost(&TestHost{
				HostCall: func(string, []byte) ([]byte, error) { return []byte("not a frame"), nil },
			}, func() {
				var err error
				switch cmd {
				case "env.state_get":
					var v struct{ X int }
					_, err = StateGetJSON("k", &v)
				case "env.state_set":
					err = StateSetJSON("k", map[string]int{"x": 1})
				case "env.now":
					_, err = Now()
				}
				if err == nil {
					t.Fatal("expected a protocol error")
				}
				var refusal *HostCallRefusalError
				if errors.As(err, &refusal) {
					t.Fatalf("malformed frame surfaced as a refusal: %v", err)
				}
				if errors.Is(err, ErrStateUnavailable) {
					t.Fatal("malformed frame matched ErrStateUnavailable")
				}
			})
		})
	}
}

// TestNowEmptyAndMalformedSuccessesArePlainErrors — an empty success or a
// non-numeric reading is a protocol defect, not a refusal.
func TestNowEmptyAndMalformedSuccessesArePlainErrors(t *testing.T) {
	for name, reply := range map[string][]byte{
		"empty":   frameValue(nil),
		"garbage": frameValue([]byte("not a number")),
	} {
		t.Run(name, func(t *testing.T) {
			WithTestHost(&TestHost{
				HostCall: func(string, []byte) ([]byte, error) { return reply, nil },
			}, func() {
				_, err := Now()
				if err == nil {
					t.Fatal("expected an error")
				}
				var refusal *HostCallRefusalError
				if errors.As(err, &refusal) {
					t.Fatalf("protocol defect surfaced as a refusal: %v", err)
				}
			})
		})
	}
}

// TestStateGetJSONPresenceSemanticsUnchanged — absence is found=false with no
// error; a present-empty value is a decode ERROR (distinguishable from
// absence by err), never absence, never a refusal.
func TestStateGetJSONPresenceSemanticsUnchanged(t *testing.T) {
	WithTestHost(&TestHost{
		HostCall: func(cmd string, args []byte) ([]byte, error) {
			if cmd != "env.state_get" {
				t.Fatalf("unexpected command %q", cmd)
			}
			return frameError(pbv2.ErrorCode_ERROR_CODE_NOT_FOUND, "missing"), nil
		},
	}, func() {
		var v struct{ X int }
		found, err := StateGetJSON("absent", &v)
		if found || err != nil {
			t.Fatalf("absence: found=%v err=%v, want (false, nil)", found, err)
		}
	})

	WithTestHost(&TestHost{
		HostCall: func(cmd string, args []byte) ([]byte, error) {
			return frameValue(nil), nil // present, empty bytes
		},
	}, func() {
		var v struct{ X int }
		_, err := StateGetJSON("empty", &v)
		if err == nil {
			t.Fatal("present-empty must be a decode error, not absence")
		}
		var refusal *HostCallRefusalError
		if errors.As(err, &refusal) {
			t.Fatalf("present-empty surfaced as a refusal: %v", err)
		}
	})
}

// TestStateSetJSONLocalErrorsAreNotRefusals — a local marshal failure is a
// plain error.
func TestStateSetJSONLocalErrorsAreNotRefusals(t *testing.T) {
	WithTestHost(&TestHost{
		HostCall: func(string, []byte) ([]byte, error) {
			return frameValue(nil), nil
		},
	}, func() {
		err := StateSetJSON("k", make(chan int))
		if err == nil {
			t.Fatal("expected a marshal error")
		}
		var refusal *HostCallRefusalError
		if errors.As(err, &refusal) {
			t.Fatalf("local marshal failure surfaced as a refusal: %v", err)
		}
	})
}

// TestUnclassifiableFramesAreProtocolErrors — UNSPECIFIED and unknown codes
// are rejected by the SDK's frame validator before any helper sees them: a
// plain protocol error, never a refusal, never the sentinel. A newer host
// that frames a code this build does not recognise must not silently become
// an advisory or contract decision.
func TestUnclassifiableFramesAreProtocolErrors(t *testing.T) {
	for _, code := range []pbv2.ErrorCode{
		pbv2.ErrorCode_ERROR_CODE_UNSPECIFIED,
		pbv2.ErrorCode(99),
	} {
		t.Run(code.String(), func(t *testing.T) {
			for _, cmd := range []string{"env.state_get", "env.state_set", "env.now"} {
				t.Run(cmd, func(t *testing.T) {
					WithTestHost(&TestHost{
						HostCall: func(string, []byte) ([]byte, error) {
							return frameError(code, "stub"), nil
						},
					}, func() {
						var err error
						switch cmd {
						case "env.state_get":
							var v struct{ X int }
							_, err = StateGetJSON("k", &v)
						case "env.state_set":
							err = StateSetJSON("k", map[string]int{"x": 1})
						case "env.now":
							_, err = Now()
						}
						if err == nil {
							t.Fatal("expected a protocol error")
						}
						var refusal *HostCallRefusalError
						if errors.As(err, &refusal) {
							t.Fatalf("unclassifiable frame surfaced as a refusal: %v", err)
						}
						if errors.Is(err, ErrStateUnavailable) {
							t.Fatal("unclassifiable frame matched ErrStateUnavailable")
						}
					})
				})
			}
		})
	}
}
