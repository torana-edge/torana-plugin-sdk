package plugin_sdk

// Write grants: what a plugin may CHANGE in the request it is handed.
//
// Every other capability in this vocabulary answers "may the plugin do this
// thing?" — call out, read state, block a request. These answer a question v1
// never asked at all: a plugin that ran could rewrite any part of the request,
// because content mutation is what a request hook is for and nothing
// distinguished a compactor rewriting tool results from an observer quietly
// changing the model.
//
// That last case is not hypothetical. `model` decides what the operator pays,
// and in v1 a plugin could change it with no capability whatsoever, while
// achieving the same thing through env.route_request required a grant. The two
// paths produced identical wire requests and identical bills.
//
// Grants are scoped by message role because that is where the value is. `pii`
// needs to redact a tool result; it has no business rewriting the user's
// prompt. `compactor` rewrites tool results; it has no business injecting tool
// definitions. Declaring the narrow thing is only possible if the vocabulary
// offers it.
//
// Reads are NOT gated in this cut, and the reason is scope rather than safety.
//
// It would be wrong to claim a plugin cannot disclose what it reads. Several
// granted capabilities carry data out: env.emit_metric puts arbitrary strings in
// metric labels, env.log writes to the proxy's output,
// env.host_call.torana_offload_completion sends content to a model by design,
// and env.state_set and env.cache_set persist it. A plugin that reads and a
// plugin that tells are not cleanly separated today.
//
// Read grants are deferred because they double this vocabulary, require
// redaction semantics for what an ungranted plugin sees instead, and — per the
// pipeline benchmarks — the projection they need costs real work per plugin.
// Write enforcement closes the escalation that exists; read enforcement is a
// disclosure control worth doing on its own terms, later, with that cost
// measured rather than assumed.
var WritePermissions = []string{
	"ir.messages.write.assistant",
	"ir.messages.write.developer",
	"ir.messages.write.other",
	"ir.messages.write.system",
	"ir.messages.write.tool",
	"ir.messages.write.user",
	"ir.model.write",
	"ir.params.write",
	"ir.tools.write",
}

// WriteSection names a grantable area of the request.
type WriteSection string

const (
	// SectionMessagesUser and friends cover one message role each. A change at
	// a position where the role differs on either side needs both grants: a
	// reorder or replacement involves the role that left the slot and the one
	// that took it.
	SectionMessagesUser      WriteSection = "ir.messages.write.user"
	SectionMessagesAssistant WriteSection = "ir.messages.write.assistant"
	SectionMessagesSystem    WriteSection = "ir.messages.write.system"
	SectionMessagesTool      WriteSection = "ir.messages.write.tool"

	// SectionMessagesDeveloper covers OpenAI's "developer" role, which is its
	// rename of "system". Format adapters pass roles through verbatim — a
	// transparent proxy must not rewrite "developer" to "system" on the way in,
	// or it changes the request on the way back out — so the role reaches
	// plugins as itself and needs a grant of its own.
	SectionMessagesDeveloper WriteSection = "ir.messages.write.developer"

	// SectionMessagesOther covers any role Torana does not model.
	//
	// Adapters cast provider role strings straight into the IR, so a role added
	// by any provider tomorrow arrives without code changes. Without this, such
	// a message would be permanently unmutatable: no grant could cover it, and a
	// plugin that legitimately edited it would have its whole output rejected.
	//
	// It is deliberately coarse. A plugin holding it may write any unmodelled
	// role, which is worse than naming them — so a role that becomes common
	// should graduate to its own section rather than living here forever.
	SectionMessagesOther WriteSection = "ir.messages.write.other"

	// SectionTools covers the tool definitions offered to the model. Adding one
	// is offering the model a capability the caller did not.
	SectionTools WriteSection = "ir.tools.write"

	// SectionModel covers the model name, which decides the bill.
	SectionModel WriteSection = "ir.model.write"

	// SectionParams covers temperature, top_p, max_tokens, stop sequences, the
	// stream flag, and the provider-specific extension blobs.
	SectionParams WriteSection = "ir.params.write"
)

// MessageWriteSection returns the grant governing writes to a message of the
// given role. Every role maps to one, so there is nothing to report as absent —
// unmodelled roles fall to SectionMessagesOther, which still requires a grant.
//
// This deliberately does NOT return (WriteSection, bool). It did briefly, with
// false meaning "this is the catch-all", which reads as Go's (value, ok) idiom
// and means the opposite of it: a host writing the idiomatic
//
//	section, ok := MessageWriteSection(role)
//	if !ok { reject }
//
// would have rejected every unmodelled role — the exact failure the catch-all
// exists to prevent. Use IsModelledRole when the distinction is actually wanted.
func MessageWriteSection(role string) WriteSection {
	switch role {
	case "user":
		return SectionMessagesUser
	case "assistant":
		return SectionMessagesAssistant
	case "system":
		return SectionMessagesSystem
	case "tool":
		return SectionMessagesTool
	case "developer":
		return SectionMessagesDeveloper
	}
	return SectionMessagesOther
}

// IsModelledRole reports whether Torana names this role, as opposed to handling
// it through the catch-all grant.
//
// Useful for diagnostics — telling an operator that a plugin asked for
// ir.messages.write.other because the provider sent a role Torana does not
// model is more informative than naming the grant alone. It must not be used to
// decide whether a write is permitted: that is MessageWriteSection's job, and
// every role has an answer.
func IsModelledRole(role string) bool {
	return MessageWriteSection(role) != SectionMessagesOther
}

// IsWritePermission reports whether name is a write grant.
func IsWritePermission(name string) bool { return contains(WritePermissions, name) }
