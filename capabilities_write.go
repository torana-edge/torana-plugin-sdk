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
// Reads are deliberately NOT gated. An enabled plugin sees the whole request.
// The sandbox already stops it telling anyone: no filesystem, no sockets, and
// no egress without env.host_call.torana_send_request. Gating reads would buy
// disclosure rather than containment, at the cost of doubling this vocabulary.
var WritePermissions = []string{
	"ir.messages.write.assistant",
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
// given role.
//
// An unrecognised role maps to no section, and the caller must treat that as
// ungrantable rather than as unrestricted — a role Torana does not model is
// exactly the case where guessing is wrong.
func MessageWriteSection(role string) (WriteSection, bool) {
	switch role {
	case "user":
		return SectionMessagesUser, true
	case "assistant":
		return SectionMessagesAssistant, true
	case "system":
		return SectionMessagesSystem, true
	case "tool":
		return SectionMessagesTool, true
	}
	return "", false
}

// IsWritePermission reports whether name is a write grant.
func IsWritePermission(name string) bool { return contains(WritePermissions, name) }
