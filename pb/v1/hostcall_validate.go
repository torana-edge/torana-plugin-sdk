package v1

import (
	"fmt"
	"math"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Validation for host-call envelopes and their normative argument schemas.
//
// These are what the host must reject rather than interpret: a missing oneof
// arm, an unspecified error code, a typed-nil nested error, or argument
// frames that cannot be acted on. Guests use the same helpers so handwritten
// plugins fail closed the same way the host would.

// knownErrorCodes are the ErrorCode values this build can act on. An unknown
// numeric code (from a newer host/guest) is refused rather than treated as a
// successful classification the plugin cannot branch on.
var knownErrorCodes = map[ErrorCode]bool{
	ErrorCode_ERROR_CODE_PERMISSION_DENIED: true,
	ErrorCode_ERROR_CODE_NOT_FOUND:         true,
	ErrorCode_ERROR_CODE_NOT_CONFIGURED:    true,
	ErrorCode_ERROR_CODE_UNAVAILABLE:       true,
	ErrorCode_ERROR_CODE_INVALID_ARGUMENT:  true,
	ErrorCode_ERROR_CODE_INTERNAL:          true,
}

// Validate reports whether a HostError carries a classified failure.
//
// UNSPECIFIED is the zero value and means "no code was set", not a real
// classification — plugins must not branch on it. Unknown numeric codes from
// a newer ABI are likewise refused.
func (x *HostError) Validate() error {
	if x == nil {
		return fmt.Errorf("host error is nil")
	}
	if x.Code == ErrorCode_ERROR_CODE_UNSPECIFIED {
		return fmt.Errorf("host error has no code; UNSPECIFIED is not a classification")
	}
	if !knownErrorCodes[x.Code] {
		return fmt.Errorf("host error code %v is not recognised by this build", x.Code)
	}
	return nil
}

// Validate reports whether a HostCallResult is a usable host-call reply.
//
// The result oneof must be set. Empty bytes on the value arm are success with
// no payload. The error arm must carry a validated HostError. Unknown top-level
// bytes are an unrecognised (future) result arm and are refused.
//
// This envelope is host-produced. Multiple known arms on the wire follow
// protobuf last-known-arm-wins after unmarshal; Validate cannot and does not
// detect that case. Guest-controlled HookResult uses DecodeHookResult instead.
func (x *HostCallResult) Validate() error {
	if x == nil {
		return fmt.Errorf("host call result is nil")
	}
	if len(x.ProtoReflect().GetUnknown()) != 0 {
		return fmt.Errorf("host call result carries a result arm this build does not recognise")
	}
	switch r := x.Result.(type) {
	case nil:
		return fmt.Errorf("host call result carries no result; empty is not a valid reply")
	case *HostCallResult_Value:
		// A typed-nil wrapper is a frame that claims value but hands over
		// nothing usable — refuse it the same way HookInput refuses typed-nil
		// payloads. Empty (non-nil) bytes are success with no payload.
		if r == nil {
			return fmt.Errorf("host call result names value but the wrapper is nil")
		}
		// r.Value may be nil or empty; both mean success with no bytes.
		return nil
	case *HostCallResult_Error:
		if r == nil || r.Error == nil {
			return fmt.Errorf("host call result names error but carries no HostError")
		}
		if err := r.Error.Validate(); err != nil {
			return fmt.Errorf("host call result: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("host call result carries an unhandled result arm")
	}
}

// Validate reports whether BlockRequestArgs can be acted on as a block verdict.
func (x *BlockRequestArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("block request args are nil")
	}
	if !blockRequestStatusOK(x.Status) {
		return fmt.Errorf("block request args status %d is outside 400–599", x.Status)
	}
	if x.Code == "" {
		return fmt.Errorf("block request args need a code")
	}
	return nil
}

// Validate reports whether RespondRequestArgs can be acted on.
// Empty content is allowed.
func (x *RespondRequestArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("respond request args are nil")
	}
	return nil
}

// Validate reports whether RouteRequestArgs names a route change.
func (x *RouteRequestArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("route request args are nil")
	}
	if x.Provider == "" && x.Model == "" {
		return fmt.Errorf("route request args need a provider and/or model")
	}
	return nil
}

// Validate reports whether SetIdentityArgs names an identity.
func (x *SetIdentityArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("set identity args are nil")
	}
	if x.Identity == "" {
		return fmt.Errorf("set identity args need a non-empty identity")
	}
	return nil
}

// Validate reports whether MetaAppendArgs can be accepted.
// Empty fragment is valid — it is the no-op/read path (see MetaAppendSuccessValue).
func (x *MetaAppendArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("meta append args are nil")
	}
	if x.BlockIndex < 0 {
		return fmt.Errorf("meta append args block_index %d is negative", x.BlockIndex)
	}
	return nil
}

// Validation for the metadata and cache argument bodies.
//
// A key is required in all four. An empty key is never a meaningful address:
// on the meta side the host namespaces it per plugin, so an empty key names
// the namespace itself rather than a value in it, and on the cache side it
// names a slot every plugin would collide on. Refusing it here means the guest
// learns at the call rather than reading back a value that silently was not
// stored.
//
// Values are deliberately not required. Empty is a legitimate value; deletion
// uses its explicit command.

func (x *MetaGetArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("meta get args are nil")
	}
	if x.Key == "" {
		return fmt.Errorf("meta get args have no key")
	}
	return nil
}

func (x *MetaSetArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("meta set args are nil")
	}
	if x.Key == "" {
		return fmt.Errorf("meta set args have no key")
	}
	return nil
}

func (x *CacheGetArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("cache get args are nil")
	}
	if x.Key == "" {
		return fmt.Errorf("cache get args have no key")
	}
	return nil
}

func (x *CacheSetArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("cache set args are nil")
	}
	if x.Key == "" {
		return fmt.Errorf("cache set args have no key")
	}
	return nil
}

// Durable-state argument bodies. A key is required for the same reason as meta
// and cache: an empty key names the plugin's namespace rather than a value in
// it. Values are not required — empty is a value, and env.state_delete is how a
// key is released.

func (x *StateGetArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("state get args are nil")
	}
	if x.Key == "" {
		return fmt.Errorf("state get args have no key")
	}
	return nil
}

func (x *StateSetArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("state set args are nil")
	}
	if x.Key == "" {
		return fmt.Errorf("state set args have no key")
	}
	return nil
}

func (x *StateDeleteArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("state delete args are nil")
	}
	if x.Key == "" {
		return fmt.Errorf("state delete args have no key")
	}
	return nil
}

func validateSlot(kind, slot string) error {
	if slot == "" || len(slot) > 64 {
		return fmt.Errorf("%s must be 1–64 ASCII characters", kind)
	}
	for i := range len(slot) {
		c := slot[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || (i > 0 && (c == '.' || c == '_' || c == '-')) {
			continue
		}
		return fmt.Errorf("%s contains an invalid character", kind)
	}
	return nil
}

func validateLogicalPath(kind, value string, emptyOK bool) error {
	if value == "" && emptyOK {
		return nil
	}
	if value == "" || len(value) > 240 || !utf8.ValidString(value) {
		return fmt.Errorf("%s must be a non-empty UTF-8 path of at most 240 bytes", kind)
	}
	if strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return fmt.Errorf("%s must be a relative slash-separated path", kind)
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("%s contains an invalid path segment", kind)
		}
	}
	return nil
}

func (x *CredentialGetArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("credential get args are nil")
	}
	return validateSlot("credential slot", x.Slot)
}

func (x *FileAppendArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("file append args are nil")
	}
	return validateLogicalPath("file path", x.Path, false)
}

func (x *FileReadArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("file read args are nil")
	}
	return validateLogicalPath("file path", x.Path, false)
}

func (x *FileWriteArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("file write args are nil")
	}
	return validateLogicalPath("file path", x.Path, false)
}

func (x *FileListArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("file list args are nil")
	}
	return validateLogicalPath("file prefix", x.Prefix, true)
}

func (x *FileDeleteArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("file delete args are nil")
	}
	return validateLogicalPath("file path", x.Path, false)
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := range len(name) {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)) {
			continue
		}
		return false
	}
	return true
}

func validateHTTPHeaders(headers []*HTTPHeader) error {
	seen := make(map[string]struct{}, len(headers))
	for i, header := range headers {
		if header == nil || !validHeaderName(header.Name) {
			return fmt.Errorf("http header %d has an invalid name", i)
		}
		name := strings.ToLower(header.Name)
		if _, ok := seen[name]; ok {
			return fmt.Errorf("http header %q is duplicated", header.Name)
		}
		seen[name] = struct{}{}
		for _, value := range header.Values {
			if strings.ContainsAny(value, "\r\n") {
				return fmt.Errorf("http header %q contains a line break", header.Name)
			}
		}
	}
	return nil
}

func (x *OutboundHTTPRequestArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("outbound http request args are nil")
	}
	if err := validateSlot("http endpoint", x.Endpoint); err != nil {
		return err
	}
	if x.Method == "" {
		return fmt.Errorf("http method is required")
	}
	for i := range len(x.Method) {
		if c := x.Method[i]; c < 'A' || c > 'Z' {
			return fmt.Errorf("http method must be uppercase ASCII")
		}
	}
	if !strings.HasPrefix(x.Path, "/") || strings.HasPrefix(x.Path, "//") || strings.Contains(x.Path, "#") {
		return fmt.Errorf("http path must be an absolute path without an authority or fragment")
	}
	u, err := url.ParseRequestURI(x.Path)
	if err != nil || u.IsAbs() || u.Host != "" {
		return fmt.Errorf("http path is invalid")
	}
	return validateHTTPHeaders(x.Headers)
}

func (x *ModelCompleteArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("model complete args are nil")
	}
	if len(x.ProtoReflect().GetUnknown()) != 0 {
		return fmt.Errorf("model complete args contain unknown fields")
	}
	if err := validateSlot("model service", x.Service); err != nil {
		return err
	}
	if len(x.Messages) == 0 {
		return fmt.Errorf("model completion requires at least one message")
	}
	for i, message := range x.Messages {
		if message == nil {
			return fmt.Errorf("model message %d is nil", i)
		}
		if len(message.ProtoReflect().GetUnknown()) != 0 {
			return fmt.Errorf("model message %d contains unknown fields", i)
		}
		if message.Role == "" || !utf8.ValidString(message.Role) {
			return fmt.Errorf("model message %d role must be non-empty UTF-8", i)
		}
		if !utf8.ValidString(message.Content) {
			return fmt.Errorf("model message %d content must be UTF-8", i)
		}
	}
	if x.MaxTokens != nil && *x.MaxTokens == 0 {
		return fmt.Errorf("model completion max_tokens must be positive when present")
	}
	if x.Temperature != nil && (math.IsNaN(*x.Temperature) || math.IsInf(*x.Temperature, 0)) {
		return fmt.Errorf("model completion temperature must be finite when present")
	}
	return nil
}

func (x *ModelCompleteResult) Validate() error {
	if x == nil {
		return fmt.Errorf("model completion result is nil")
	}
	if len(x.ProtoReflect().GetUnknown()) != 0 {
		return fmt.Errorf("model completion result contains unknown fields")
	}
	if !utf8.ValidString(x.Content) || !utf8.ValidString(x.ReportedModel) || !utf8.ValidString(x.FinishReason) {
		return fmt.Errorf("model completion result strings must be UTF-8")
	}
	if x.Usage != nil && (x.Usage.InputTokens < 0 || x.Usage.OutputTokens < 0 ||
		x.Usage.CacheReadTokens < 0 || x.Usage.CacheWriteTokens < 0) {
		return fmt.Errorf("model completion usage counts must be non-negative")
	}
	if x.Usage != nil && len(x.Usage.ProtoReflect().GetUnknown()) != 0 {
		return fmt.Errorf("model completion usage contains unknown fields")
	}
	return nil
}

func (x *ModelPricingGetArgs) Validate() error {
	if x == nil {
		return fmt.Errorf("model pricing args are nil")
	}
	if len(x.ProtoReflect().GetUnknown()) != 0 {
		return fmt.Errorf("model pricing args contain unknown fields")
	}
	return validateSlot("model pricing resource", x.Resource)
}

func (x *ModelPricing) Validate() error {
	if x == nil {
		return fmt.Errorf("model pricing is nil")
	}
	if len(x.ProtoReflect().GetUnknown()) != 0 {
		return fmt.Errorf("model pricing contains unknown fields")
	}
	rates := []struct {
		name  string
		value *float64
	}{
		{"input_usd_per_mtok", x.InputUsdPerMtok},
		{"output_usd_per_mtok", x.OutputUsdPerMtok},
		{"cache_read_usd_per_mtok", x.CacheReadUsdPerMtok},
		{"cache_write_usd_per_mtok", x.CacheWriteUsdPerMtok},
	}
	for _, rate := range rates {
		if rate.value != nil && (math.IsNaN(*rate.value) || math.IsInf(*rate.value, 0) || *rate.value < 0) {
			return fmt.Errorf("model pricing %s must be finite and non-negative", rate.name)
		}
	}
	return nil
}

func (x *OutboundHTTPResponse) Validate() error {
	if x == nil {
		return fmt.Errorf("outbound http response is nil")
	}
	if x.Status < 100 || x.Status > 599 {
		return fmt.Errorf("outbound http response status %d is invalid", x.Status)
	}
	return validateHTTPHeaders(x.Headers)
}
