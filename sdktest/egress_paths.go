package sdktest

// EgressPathCases is the SHARED adversarial matrix for the egress guest path
// contract. It is the reference table both sides are pinned against:
//
//   - the SDK mirror (plugin_sdk.validateEgressPath, exercised through
//     SendRequest in sdktest), and
//   - the host's authoritative check (torana-edge internal/proxy/egress.go,
//     the "guest path contract" block, exercised in its egress tests).
//
// Any divergence between the two predicates fails a row on one side, so the
// copies cannot quietly drift. When adding a row, update BOTH sides' tests.
//
// Contract: the path must be a root-relative request URI — no scheme, no
// authority, no userinfo, no opaque form, no fragment, no control characters,
// and no leading "//" (network-path reference).
var EgressPathCases = []struct {
	// Path is the guest-supplied request path.
	Path string
	// Valid reports whether the predicate must accept it.
	Valid bool
}{
	// Legitimate shapes.
	{"/v1/chat/completions", true},
	{"/v1/chat/completions?stream=true", true},
	{"/v1/chat/completions?x=1&y=2", true},
	{"/model/amazon.titan-text-express-v1:invoke", true},
	{"/v1/messages:generateContent", true},
	{"/a/b/c", true},
	{"/%2F%2Fattacker.example/x", false}, // encoded network-path: the DECODED path starts with "//" and is rejected
	{"/@attacker.example/v1", true},     // '@' inside a path segment is literal

	// Caller bugs and attack shapes.
	{"", false},                      // missing
	{"v1/chat/completions", false},   // relative, not root-relative
	{"@attacker.example/v1", false},  // userinfo redirect shape
	{"//attacker.example/v1", false}, // network-path reference
	{"https://attacker.example/v1", false},
	{"http://attacker.example/v1", false},
	{"//127.0.0.1:8080/v1", false},
	{"/v1/chat/completions#frag", false}, // fragments are not request URIs
	{"/v1/chat/completions\x00x", false}, // control characters
	{"/v1/chat/completions\r\nHost: attacker.example", false},
	{"/v1/chat/completions\nX: y", false},
}
