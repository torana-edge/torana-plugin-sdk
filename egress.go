package plugin_sdk

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/torana-edge/torana-plugin-sdk/pb"
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
func SendRequest(req *pb.ChatRequest, opts SendRequestOptions) (EgressResult, error) {
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

	res, err := HostCall("torana_send_request", string(payload))
	if err != nil {
		return EgressResult{}, err
	}
	if res == "" || isPermissionDenied(res) {
		return EgressResult{}, ErrEgressUnavailable
	}

	var envelope struct {
		EgressResult
		Body string `json:"body,omitempty"`
	}
	if err := json.Unmarshal([]byte(res), &envelope); err != nil {
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
	return out, nil
}
