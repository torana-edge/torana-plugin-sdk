package plugin_sdk

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
	"google.golang.org/protobuf/proto"
)

// Plugin-originated provider requests
//
// SendRequest lets a plugin call a provider on its own initiative, rather than
// only transforming requests that pass through. It is what makes background
// work possible: refreshing a cache before it lapses, prefetching, health
// checking.
//
// It is also the only thing a plugin can do that spends the operator's money
// directly, so it is bounded in ways the other host calls are not:
//
//   - You name a CONFIGURED PROVIDER, never a URL. A plugin picks among
//     endpoints the operator already trusts and cannot invent one.
//   - Credentials are added host-side from that provider's configuration and
//     are never visible to the plugin.
//   - Every call is metered against a per-plugin budget the operator sets in
//     plugins.runtime.egress. With no budget configured, sending fails. This is
//     deliberate: a capability that spends money stays unusable until someone
//     has said how much.
//   - Every call appears in the request feed attributed to your plugin.
//
// Requires the env.host_call.torana_send_request permission.
//
// # Paths
//
// Torana never synthesizes a provider path — it forwards whatever the caller
// sent. A plugin replaying or refreshing a conversation must therefore supply
// the path that conversation used. This is why it is a required field rather
// than something the SDK guesses: guessing would silently work for OpenAI-shaped
// providers and silently fail for Bedrock's :invoke or Code Assist's
// :generateContent.

// ErrEgressUnavailable means the host refused the call — no budget configured,
// no permission granted, or egress disabled entirely.
var ErrEgressUnavailable = errors.New("torana: plugin egress is not available")

// EgressUsage is what the provider reported for a plugin-originated request.
type EgressUsage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

// EgressResult is one plugin-originated request's outcome.
type EgressResult struct {
	Status     string       `json:"status"`
	Message    string       `json:"message,omitempty"`
	HTTPStatus int          `json:"http_status,omitempty"`
	Body       []byte       `json:"-"`
	Usage      *EgressUsage `json:"usage,omitempty"`
}

// CacheHit reports whether the provider served this request from its prompt
// cache. For a refresh request this is the signal that matters: a read means
// the entry was alive and the refresh preserved it, while a write means it had
// already lapsed and the refresh paid to rebuild it — which is usually the
// moment to stop refreshing.
func (r EgressResult) CacheHit() bool {
	return r.Usage != nil && r.Usage.CacheRead > 0
}

// CacheRebuilt reports that the request had to write the cache rather than read
// it, meaning the entry had expired.
func (r EgressResult) CacheRebuilt() bool {
	return r.Usage != nil && r.Usage.CacheWrite > 0 && r.Usage.CacheRead == 0
}

// SendRequestOptions configures one plugin-originated request.
type SendRequestOptions struct {
	// Provider is a configured provider key. Required.
	Provider string
	// Path is the upstream path, e.g. "/v1/messages". Required — see the
	// package comment for why the SDK will not guess it.
	Path string
	// TimeoutMS bounds the call. Zero selects the host default (30s); the host
	// caps it at two minutes.
	TimeoutMS int
}

// SendRequest sends req to a configured provider and returns the raw response.
func SendRequest(req *pbv2.ChatRequest, opts SendRequestOptions) (EgressResult, error) {
	if req == nil {
		return EgressResult{}, fmt.Errorf("torana: request is required")
	}
	if opts.Provider == "" {
		return EgressResult{}, fmt.Errorf("torana: provider is required")
	}
	if opts.Path == "" {
		return EgressResult{}, fmt.Errorf("torana: path is required — Torana does not synthesize provider paths")
	}

	raw, err := proto.Marshal(req)
	if err != nil {
		return EgressResult{}, fmt.Errorf("torana: encode request: %w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"provider":   opts.Provider,
		"request_pb": base64.StdEncoding.EncodeToString(raw),
		"path":       opts.Path,
		"timeout_ms": opts.TimeoutMS,
	})
	if err != nil {
		return EgressResult{}, err
	}

	res, herr, err := HostCallExtension("torana_send_request", payload)
	if err != nil {
		return EgressResult{}, err
	}
	if herr != nil {
		// Egress is all-or-nothing for the caller, so every refusal maps to
		// the existing sentinel. The code is wrapped in so the reason is not
		// lost: a missing grant and an unconfigured backend read identically
		// otherwise, and they need different fixes.
		return EgressResult{}, fmt.Errorf("%w (%s)", ErrEgressUnavailable, hostErrorReason(herr))
	}
	if len(res) == 0 {
		return EgressResult{}, ErrEgressUnavailable
	}

	var envelope struct {
		EgressResult
		Body string `json:"body,omitempty"`
	}
	if err := json.Unmarshal(res, &envelope); err != nil {
		return EgressResult{}, fmt.Errorf("torana: decode egress response: %w", err)
	}
	out := envelope.EgressResult
	if envelope.Body != "" {
		if decoded, err := base64.StdEncoding.DecodeString(envelope.Body); err == nil {
			out.Body = decoded
		}
	}
	if out.Status == "error" {
		return out, fmt.Errorf("torana: %s", out.Message)
	}
	// A reached-but-refused provider is a failure, not a success. The host
	// reports transport success separately from what the provider said, and a
	// caller that only checked err would count a 401 as a completed request —
	// burning its budget while achieving nothing and reporting that it worked.
	//
	// The most common cause is a provider with no credential of its own: on the
	// normal request path Torana forwards the caller's, but a plugin-originated
	// request has no caller, so the provider needs api_key_env or api_key_enc.
	if out.HTTPStatus < 200 || out.HTTPStatus > 299 {
		return out, fmt.Errorf("torana: %s returned HTTP %d%s",
			opts.Provider, out.HTTPStatus, credentialHint(out.HTTPStatus))
	}
	return out, nil
}

func credentialHint(status int) string {
	if status == 401 || status == 403 {
		return " — a plugin-originated request cannot borrow the caller's credential, " +
			"so the provider needs its own api_key_env or api_key_enc"
	}
	return ""
}

// EncodeRequest renders a request as base64 protobuf, for storing in durable
// plugin state. Protobuf rather than JSON so a stored prefix round-trips
// byte-exactly — a re-encoding that reorders or drops a field would change the
// prefix and defeat whatever the plugin was preserving it for.
func EncodeRequest(req *pbv2.ChatRequest) (string, error) {
	if req == nil {
		return "", fmt.Errorf("torana: request is required")
	}
	raw, err := proto.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("torana: encode request: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// DecodeRequest is the inverse of EncodeRequest.
func DecodeRequest(encoded string) (*pbv2.ChatRequest, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("torana: decode request: %w", err)
	}
	var req pbv2.ChatRequest
	if err := proto.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("torana: decode request: %w", err)
	}
	return &req, nil
}
