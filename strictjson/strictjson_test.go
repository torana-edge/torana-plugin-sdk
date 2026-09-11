package strictjson

import (
	"encoding/json"
	"strings"
	"testing"
)

// This package exists because encoding/json's conveniences are unsafe at a
// trust boundary. Each row below is one of those conveniences, written down as
// what this package does INSTEAD — so the table states the contract rather
// than agreeing with the implementation.
var textCases = []struct {
	name   string
	raw    string
	accept bool
}{
	{name: "an ordinary object", raw: `{"a":1}`, accept: true},
	{name: "a well-formed escape", raw: `{"a":"A"}`, accept: true},
	{name: "a matched surrogate pair", raw: `{"a":"😀"}`, accept: true},
	{name: "an escaped backslash before what looks like an escape", raw: `{"a":"\\ud800"}`, accept: true},
	{name: "a string containing a quote escape", raw: `{"a":"say \"hi\""}`, accept: true},
	{name: "nested objects and arrays", raw: `{"a":[{"b":1},{"c":2}]}`, accept: true},

	{name: "invalid UTF-8", raw: "{\"a\":\"\xff\xfe\"}", accept: false},
	{name: "a lone high surrogate", raw: `{"a":"\ud800"}`, accept: false},
	{name: "a lone low surrogate", raw: `{"a":"\udc00"}`, accept: false},
	{name: "a high surrogate followed by a plain escape", raw: `{"a":"\ud800A"}`, accept: false},
	{name: "a malformed hex escape", raw: `{"a":"\u00zz"}`, accept: false},
	{name: "a truncated escape at the end", raw: `{"a":"\u00`, accept: false},
}

func TestValidateTextContract(t *testing.T) {
	for _, tc := range textCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateText([]byte(tc.raw))
			if tc.accept && err != nil {
				t.Fatalf("ValidateText(%q) = %v, want accepted", tc.raw, err)
			}
			if !tc.accept && err == nil {
				t.Fatalf("ValidateText(%q) was accepted, want rejected", tc.raw)
			}
		})
	}
}

// encoding/json replaces invalid UTF-8 rather than refusing it, so two
// different byte sequences decode to the same string. That is the exact
// failure this package exists to prevent, so it is pinned directly.
func TestInvalidUTF8DoesNotCollapse(t *testing.T) {
	a := []byte("{\"k\":\"\xff\"}")
	b := []byte("{\"k\":\"\xfe\"}")

	var viaStdlibA, viaStdlibB map[string]string
	if err := json.Unmarshal(a, &viaStdlibA); err != nil {
		t.Fatalf("stdlib rejected %q, so this comparison proves nothing: %v", a, err)
	}
	if err := json.Unmarshal(b, &viaStdlibB); err != nil {
		t.Fatalf("stdlib rejected %q, so this comparison proves nothing: %v", b, err)
	}
	if viaStdlibA["k"] != viaStdlibB["k"] {
		t.Skip("encoding/json no longer collapses these to U+FFFD; the premise has changed")
	}

	if err := ValidateText(a); err == nil {
		t.Error("invalid UTF-8 was accepted; two distinct inputs can now decode to the same text")
	}
	if err := ValidateText(b); err == nil {
		t.Error("invalid UTF-8 was accepted; two distinct inputs can now decode to the same text")
	}
}

func TestDecodeObjectContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		raw    string
		accept bool
	}{
		{name: "an object", raw: `{"a":1}`, accept: true},
		{name: "an empty object", raw: `{}`, accept: true},
		{name: "null yields a nil map, not an error", raw: `null`, accept: true},
		{name: "a duplicate member at the top level", raw: `{"a":1,"a":2}`, accept: false},
		{name: "a duplicate member nested in an array", raw: `{"a":[{"b":1,"b":2}]}`, accept: false},
		{name: "a duplicate member nested in an object", raw: `{"a":{"b":1,"b":2}}`, accept: false},
		{name: "trailing data after the value", raw: `{"a":1} {"b":2}`, accept: false},
		{name: "a trailing scalar", raw: `{"a":1} 7`, accept: false},
		{name: "an array is not an object", raw: `[1,2]`, accept: false},
		{name: "a scalar is not an object", raw: `7`, accept: false},
		{name: "invalid UTF-8 inside a member", raw: "{\"a\":\"\xff\"}", accept: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeObject([]byte(tc.raw))
			if tc.accept && err != nil {
				t.Fatalf("DecodeObject(%q) = %v, want accepted", tc.raw, err)
			}
			if !tc.accept && err == nil {
				t.Fatalf("DecodeObject(%q) was accepted, want rejected", tc.raw)
			}
		})
	}
}

// A number must survive a round trip as the exact lexeme it was written as.
// Decoding into float64 silently rewrites large integers and trailing zeros,
// and a plugin that re-emits the request would hand the provider a different
// number than the caller sent.
func TestNumbersKeepTheirLexeme(t *testing.T) {
	for _, lexeme := range []string{
		"9007199254740993", // beyond float64's exact integer range
		"1.0",
		"1e400",
		"0.1000",
		"-0",
	} {
		obj, err := DecodeObject([]byte(`{"n":` + lexeme + `}`))
		if err != nil {
			t.Errorf("DecodeObject with n=%s: %v", lexeme, err)
			continue
		}
		n, ok := obj["n"].(json.Number)
		if !ok {
			t.Errorf("n=%s decoded as %T, want json.Number", lexeme, obj["n"])
			continue
		}
		if n.String() != lexeme {
			t.Errorf("n=%s round-tripped as %s", lexeme, n.String())
		}
	}
}

func TestDecodeObjectStrictContract(t *testing.T) {
	known := []string{"version", "tools"}
	for _, tc := range []struct {
		name   string
		raw    string
		accept bool
	}{
		{name: "only known members", raw: `{"version":1,"tools":[]}`, accept: true},
		{name: "a subset of the known members", raw: `{"version":1}`, accept: true},
		{name: "no members at all", raw: `{}`, accept: true},
		{name: "an unknown member", raw: `{"version":1,"extra":2}`, accept: false},
		{name: "a top-level null is not an object", raw: `null`, accept: false},
		{name: "an empty object still is one", raw: `{}`, accept: true},
		{name: "a null member", raw: `{"version":null}`, accept: false},
		{name: "a duplicate known member", raw: `{"version":1,"version":2}`, accept: false},
		{name: "invalid UTF-8", raw: "{\"version\":\"\xff\"}", accept: false},
		{name: "an array", raw: `[]`, accept: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeObjectStrict([]byte(tc.raw), known...)
			if tc.accept && err != nil {
				t.Fatalf("DecodeObjectStrict(%q) = %v, want accepted", tc.raw, err)
			}
			if !tc.accept && err == nil {
				t.Fatalf("DecodeObjectStrict(%q) was accepted, want rejected", tc.raw)
			}
		})
	}
}

// Presence is the point of returning RawMessage: a member that was written as
// an empty string or false must be distinguishable from one that was absent.
func TestDecodeObjectStrictPreservesPresence(t *testing.T) {
	raw, err := DecodeObjectStrict([]byte(`{"a":"","b":false}`), "a", "b", "c")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["a"]; !ok {
		t.Error(`member "a" was written as an empty string and is reported absent`)
	}
	if _, ok := raw["b"]; !ok {
		t.Error(`member "b" was written as false and is reported absent`)
	}
	if _, ok := raw["c"]; ok {
		t.Error(`member "c" was never written but is reported present`)
	}
}

// The rejection has to name the member, or an operator staring at a large
// registry has no way to find it.
func TestErrorsNameTheOffendingMember(t *testing.T) {
	_, err := DecodeObjectStrict([]byte(`{"version":1,"whoops":2}`), "version")
	if err == nil {
		t.Fatal("an unknown member was accepted")
	}
	if !strings.Contains(err.Error(), "whoops") {
		t.Errorf("error %q does not name the offending member", err)
	}
	_, err = DecodeObject([]byte(`{"a":1,"a":2}`))
	if err == nil {
		t.Fatal("a duplicate member was accepted")
	}
	if !strings.Contains(err.Error(), `"a"`) {
		t.Errorf("error %q does not name the duplicated member", err)
	}
}
