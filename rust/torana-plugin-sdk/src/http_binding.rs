use crate::{pbv1, strict_json};

/// Host-owned MCP evidence. A session identity is not a plugin's thread/state key.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct HttpConversationBinding {
    pub bound: bool,
    pub conversation_id: String,
    pub call_id: String,
}

/// Read only from the request supplied to Plugin::on_http. This validates the
/// envelope, not provenance; model arguments and caller-created headers are not
/// trusted evidence. None means ordinary HTTP; Some with bound=false means MCP
/// explicitly reported an unbound call. Neither authorizes scoped state access.
pub fn http_conversation(
    req: &pbv1::HttpRequest,
) -> Result<Option<HttpConversationBinding>, String> {
    if req.headers_json.is_empty() {
        return Ok(None);
    }
    let raw = strict_json(&req.headers_json).map_err(|_| "invalid HTTP header envelope")?;
    let object = raw.as_object().ok_or("invalid HTTP header envelope")?;
    let mut values = std::collections::BTreeMap::new();
    for (name, value) in object {
        let key = name.to_ascii_lowercase();
        if !matches!(
            key.as_str(),
            "x-torana-mcp-binding" | "x-torana-conversation-id" | "x-torana-tool-use-id"
        ) {
            continue;
        }
        let list = value
            .as_array()
            .ok_or("HTTP binding header needs one value")?;
        if list.len() != 1 {
            return Err("HTTP binding header needs one value".into());
        }
        let value = list[0]
            .as_str()
            .ok_or("HTTP binding header needs one value")?;
        if values.insert(key, value).is_some() {
            return Err("duplicate HTTP binding header".into());
        }
    }
    let status = values.get("x-torana-mcp-binding").copied();
    let conversation = values.get("x-torana-conversation-id").copied();
    let call = values.get("x-torana-tool-use-id").copied();
    if status.is_none() || status == Some("unbound") {
        if conversation.is_some() || call.is_some() {
            return Err("unbound HTTP call cannot carry identities".into());
        }
        return Ok(status.map(|_| HttpConversationBinding {
            bound: false,
            conversation_id: String::new(),
            call_id: String::new(),
        }));
    }
    let valid = |value: Option<&str>| {
        value.is_some_and(|v| !v.is_empty() && v.len() <= 256 && !v.chars().any(char::is_control))
    };
    if status != Some("bound") || !valid(conversation) || !valid(call) {
        return Err("invalid HTTP conversation binding".into());
    }
    Ok(Some(HttpConversationBinding {
        bound: true,
        conversation_id: conversation.unwrap().into(),
        call_id: call.unwrap().into(),
    }))
}

#[cfg(test)]
mod tests {
    use super::*;
    fn read(raw: &str) -> Result<Option<HttpConversationBinding>, String> {
        http_conversation(&pbv1::HttpRequest {
            headers_json: raw.as_bytes().into(),
            ..Default::default()
        })
    }
    #[test]
    fn absent_unbound_and_bound_are_distinct() {
        assert_eq!(read("").unwrap(), None);
        assert_eq!(read("{}").unwrap(), None);
        assert!(
            !read(r#"{"X-Torana-MCP-Binding":["unbound"]}"#)
                .unwrap()
                .unwrap()
                .bound
        );
        let binding = read(r#"{"x-torana-mcp-binding":["bound"],"X-Torana-Conversation-Id":["session"],"X-Torana-Tool-Use-Id":["call"]}"#).unwrap().unwrap();
        assert_eq!(
            binding,
            HttpConversationBinding {
                bound: true,
                conversation_id: "session".into(),
                call_id: "call".into()
            }
        );
    }
    #[test]
    fn refuses_ambiguous_and_invalid_evidence() {
        for raw in [
            "null",
            "[]",
            "{} {}",
            r#"{"X-Torana-MCP-Binding":["bound"],"X-Torana-MCP-Binding":["unbound"]}"#,
            r#"{"X-Torana-MCP-Binding":["unbound"],"x-torana-mcp-binding":["unbound"]}"#,
            r#"{"X-Torana-MCP-Binding":[]}"#,
            r#"{"X-Torana-MCP-Binding":["bound","unbound"]}"#,
            r#"{"X-Torana-MCP-Binding":["unknown"]}"#,
            r#"{"X-Torana-MCP-Binding":["bound"]}"#,
            r#"{"X-Torana-Conversation-Id":["private-session"]}"#,
            r#"{"X-Torana-MCP-Binding":["unbound"],"X-Torana-Tool-Use-Id":["private-call"]}"#,
            r#"{"X-Torana-MCP-Binding":["bound"],"X-Torana-Conversation-Id":["session\n"],"X-Torana-Tool-Use-Id":["call"]}"#,
        ] {
            assert!(read(raw).is_err());
        }
        assert!(read(&format!(r#"{{"X-Torana-MCP-Binding":["bound"],"X-Torana-Conversation-Id":["{}"],"X-Torana-Tool-Use-Id":["call"]}}"#, "a".repeat(257))).is_err());
    }
}
