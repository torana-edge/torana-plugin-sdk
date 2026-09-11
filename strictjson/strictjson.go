// Package strictjson decodes JSON without the normalizations encoding/json
// performs silently.
//
// A plugin receives JSON that a model, a harness, or an operator wrote, and
// then usually re-emits it. encoding/json is built to be forgiving: it
// replaces invalid UTF-8 with U+FFFD, keeps the LAST of a set of duplicate
// members, accepts trailing data after a value, and turns every number into a
// float64. Each of those is a silent rewrite of the caller's bytes, and a
// plugin that performs one is a plugin that changed a request nobody asked it
// to change.
//
// This package refuses instead:
//
//   - invalid UTF-8 is an error, not a substitution, so two distinct inputs
//     can never collapse to the same text;
//   - a malformed or lone-surrogate \uXXXX escape is an error;
//   - a duplicate member is an error AT EVERY NESTING LEVEL, rather than a
//     silent last-wins;
//   - numbers decode through json.Number, so a lexeme survives a round trip
//     exactly as it was written;
//   - anything after the single top-level value is an error.
//
// It was written twice, byte-identically, inside two official plugins before
// it lived here. Strict decoding is not a per-plugin concern: every plugin
// that reads JSON off the wire needs the same refusals, and a plugin author
// should not have to rediscover which of encoding/json's conveniences are
// unsafe at a trust boundary.
package strictjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

// ValidateText is the ONE pre-decode JSON-text validator used by every
// strict decoder in this package. It enforces the textual invariants that
// encoding/json silently normalizes away:
//
//   - the raw bytes are valid UTF-8 (distinct invalid inputs must never
//     collapse to the same U+FFFD);
//   - every \uXXXX escape in every string is well-formed hex;
//   - a high surrogate escape is accepted ONLY when immediately followed by
//     a low surrogate escape; lone high and lone low surrogates are rejected;
//   - an escaped backslash is consumed as a unit, so "\\ud800" is literal
//     text, never a Unicode escape.
//
// Numbers are NOT touched here: losslessness is the decoder's job
// (UseNumber).
func ValidateText(data []byte) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("invalid UTF-8")
	}
	for i := 0; i < len(data); i++ {
		if data[i] != '"' {
			continue
		}
		i++
		for {
			if i >= len(data) {
				return fmt.Errorf("unterminated string")
			}
			c := data[i]
			if c == '"' {
				break
			}
			if c != '\\' {
				i++
				continue
			}
			i++
			if i >= len(data) {
				return fmt.Errorf("unterminated escape")
			}
			esc := data[i]
			switch esc {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				// ordinary escape
			case 'u':
				code, next, err := readHex4(data, i)
				if err != nil {
					return err
				}
				i = next
				switch {
				case code >= 0xD800 && code <= 0xDBFF:
					if i+2 < len(data) && data[i+1] == '\\' && data[i+2] == 'u' {
						low, next2, err := readHex4(data, i+2)
						if err != nil {
							return err
						}
						if low < 0xDC00 || low > 0xDFFF {
							return fmt.Errorf("high surrogate not paired with a low surrogate")
						}
						i = next2
					} else {
						return fmt.Errorf("lone high surrogate")
					}
				case code >= 0xDC00 && code <= 0xDFFF:
					return fmt.Errorf("lone low surrogate")
				}
			default:
				return fmt.Errorf("invalid escape \\%c", esc)
			}
			i++
		}
	}
	return nil
}

// readHex4 parses the four hex digits of a \u escape whose 'u' sits at
// uIdx. Returns the code point and the index of its last hex digit.
func readHex4(data []byte, uIdx int) (int, int, error) {
	if uIdx+4 >= len(data) {
		return 0, 0, fmt.Errorf("short \\u escape")
	}
	code := 0
	for k := 1; k <= 4; k++ {
		d := hexVal(data[uIdx+k])
		if d < 0 {
			return 0, 0, fmt.Errorf("invalid \\u escape")
		}
		code = code<<4 | d
	}
	return code, uIdx + 4, nil
}

func hexVal(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	}
	return -1
}

// DecodeObject decodes exactly one JSON value as a LOSSLESS object:
// ValidateText (UTF-8 + surrogate invariants), duplicate-key rejection,
// UseNumber decoding (numbers keep their exact lexeme through any later
// re-marshalling), and an exact one-value/EOF check. "null" yields a nil map
// with no error; callers decide whether null is tolerable (a schema body) or
// terminal (tool arguments).
func DecodeObject(data []byte) (map[string]any, error) {
	if err := ValidateText(data); err != nil {
		return nil, err
	}
	if err := rejectDuplicates(data); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, fmt.Errorf("not a JSON object: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("trailing JSON after the value")
		}
		return nil, err
	}
	return obj, nil
}

// DecodeObjectStrict decodes data as a JSON OBJECT, rejecting:
//   - a top-level null, which is not an object (unlike DecodeObject, which
//     returns it as a nil map and leaves the policy to the caller);
//   - duplicate keys at ANY nesting level (a repeated tool name in a registry,
//     a repeated status member in a verify response, ...);
//   - unknown members (anything outside known);
//   - null members;
//   - trailing data after the value.
//
// Member PRESENCE is preserved: the returned map contains exactly the members
// that were written, so a missing member is distinguishable from an empty
// string or false value.
func DecodeObjectStrict(data []byte, known ...string) (map[string]json.RawMessage, error) {
	if err := ValidateText(data); err != nil {
		return nil, err
	}
	if err := rejectDuplicates(data); err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("not a JSON object")
	}
	// encoding/json decodes a top-level null into a nil map with no error, so
	// without this check "null" would be accepted as a valid closed envelope
	// and the member loop below would simply have nothing to walk. An empty
	// object decodes to a non-nil empty map and stays valid: {} is an object
	// with no members, null is not an object.
	//
	// DecodeObject deliberately differs — it hands top-level null back as a
	// nil map and says so, leaving the policy to the caller. This entry point
	// makes a stronger promise and has to keep it.
	if raw == nil {
		return nil, fmt.Errorf("not a JSON object: null")
	}
	permitted := make(map[string]bool, len(known))
	for _, member := range known {
		permitted[member] = true
	}
	for member, value := range raw {
		if !permitted[member] {
			return nil, fmt.Errorf("unknown member %q", member)
		}
		if string(value) == "null" {
			return nil, fmt.Errorf("member %q must not be null", member)
		}
	}
	return raw, nil
}

// rejectDuplicates walks the raw JSON and rejects any object that repeats a
// key, at every nesting level.
func rejectDuplicates(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return walkNoDups(dec)
}

func walkNoDups(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	return walkValueNoDups(dec, tok)
}

func walkValueNoDups(dec *json.Decoder, tok json.Token) error {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return err
				}
				key, ok := keyTok.(string)
				if !ok {
					return fmt.Errorf("object key is not a string")
				}
				if seen[key] {
					return fmt.Errorf("duplicate JSON member %q", key)
				}
				seen[key] = true
				if err := walkNoDups(dec); err != nil {
					return err
				}
			}
			end, err := dec.Token()
			if err != nil {
				return err
			}
			if end != json.Delim('}') {
				return fmt.Errorf("object not closed")
			}
		case '[':
			for dec.More() {
				if err := walkNoDups(dec); err != nil {
					return err
				}
			}
			end, err := dec.Token()
			if err != nil {
				return err
			}
			if end != json.Delim(']') {
				return fmt.Errorf("array not closed")
			}
		default:
			return fmt.Errorf("unexpected delimiter %v", t)
		}
	case nil:
		// A null VALUE is legal at any depth (a KV item's value may be null).
		// Top-level null reaches callers as a nil map and is rejected by
		// their required-member checks.
	default:
		// scalar: nothing to check
	}
	return nil
}
