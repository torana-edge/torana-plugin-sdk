//! Rust bindings and WASI Preview 1 trampolines for Torana Plugin ABI v1.
//!
//! The SDK deliberately exposes only the host calls granted by Torana. A
//! plugin cannot gain a capability by importing a function that the operator
//! did not grant.

use core::alloc::Layout;
use core::{ptr, slice};
use prost::Message;
mod capability_contract;

#[cfg(not(target_arch = "wasm32"))]
type NativeTransport = dyn Fn(&str, &[u8]) -> Result<Vec<u8>, HostCallError>;
#[cfg(not(target_arch = "wasm32"))]
thread_local! { static NATIVE_HOST: std::cell::RefCell<Option<Box<NativeTransport>>> = const { std::cell::RefCell::new(None) }; }
thread_local! { static EXECUTION_CTX: std::cell::RefCell<Option<(u64, Option<pbv1::ExecutionInfo>)>> = const { std::cell::RefCell::new(None) }; }

#[doc(hidden)]
pub use prost;

pub mod pbv1 {
    include!(concat!(env!("OUT_DIR"), "/torana.v1.rs"));
}

/// Runtime ABI contract identifier. The high 32 bits are the ABI epoch and
/// the low 32 bits are the contract revision within that epoch.
pub const ABI_VERSION: u64 = (1u64 << 32) | 1;

pub const fn abi_version() -> u64 {
    ABI_VERSION
}

/// Error returned by an ABI-v1 host call.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum HostCallError {
    /// The host refused the operation with a stable, machine-readable code.
    Refused(pbv1::HostError),
    /// The host returned a frame that violates the ABI-v1 result contract.
    Protocol(String),
}

/// Installs a native transport for the duration of a test. The guard restores
/// the previous transport on drop and storage is thread-local.
pub struct NativeHostGuard;
#[cfg(not(target_arch = "wasm32"))]
pub fn install_native_host<F>(call: F) -> NativeHostGuard
where
    F: Fn(&str, &[u8]) -> Result<Vec<u8>, HostCallError> + 'static,
{
    NATIVE_HOST.with(|slot| *slot.borrow_mut() = Some(Box::new(call)));
    NativeHostGuard
}
#[cfg(not(target_arch = "wasm32"))]
impl Drop for NativeHostGuard {
    fn drop(&mut self) {
        NATIVE_HOST.with(|slot| *slot.borrow_mut() = None);
    }
}

pub fn pass_request() -> Option<pbv1::HookResult> {
    None
}
pub fn pass_response() -> Option<pbv1::HookResult> {
    None
}
pub fn pass_event() -> Option<pbv1::HookResult> {
    None
}
pub fn pass_http() -> Option<pbv1::HookResult> {
    None
}
pub fn pass_tick() -> Option<pbv1::HookResult> {
    None
}
pub struct RequestResult(Option<pbv1::HookResult>);
pub struct ResponseResult(Option<pbv1::HookResult>);
pub struct StreamResult(Option<pbv1::HookResult>);
pub struct HttpResult(Option<pbv1::HookResult>);
pub struct TickResult(Option<pbv1::HookResult>);
impl RequestResult {
    pub fn pass() -> Self {
        Self(None)
    }
    pub fn replace(r: pbv1::ChatRequest) -> Result<Self, String> {
        Ok(Self(Some(replace_request(r)?)))
    }
    pub fn into_hook_result(self) -> Option<pbv1::HookResult> {
        self.0
    }
}
impl ResponseResult {
    pub fn pass() -> Self {
        Self(None)
    }
    pub fn replace(r: pbv1::ChatResponse) -> Result<Self, String> {
        Ok(Self(Some(replace_response(r)?)))
    }
    pub fn into_hook_result(self) -> Option<pbv1::HookResult> {
        self.0
    }
}
impl StreamResult {
    pub fn pass() -> Self {
        Self(None)
    }
    pub fn suppress() -> Self {
        Self(Some(pbv1::HookResult {
            action: Some(pbv1::hook_result::Action::Suppress(pbv1::Suppress {})),
        }))
    }
    pub fn emit(e: Vec<pbv1::StreamEvent>) -> Result<Self, String> {
        Ok(Self(Some(emit_events(e)?)))
    }
    pub fn into_hook_result(self) -> Option<pbv1::HookResult> {
        self.0
    }
}
impl HttpResult {
    pub fn pass() -> Self {
        Self(None)
    }
    pub fn serve(r: pbv1::HttpResponse) -> Result<Self, String> {
        Ok(Self(Some(serve_http(r)?)))
    }
    pub fn into_hook_result(self) -> Option<pbv1::HookResult> {
        self.0
    }
}
impl TickResult {
    pub fn pass() -> Self {
        Self(None)
    }
    pub fn outcome(actions: i32, note: impl Into<String>) -> Result<Self, String> {
        Ok(Self(Some(tick_outcome(actions, note)?)))
    }
    pub fn into_hook_result(self) -> Option<pbv1::HookResult> {
        self.0
    }
}

/// Typed plugin callbacks cannot return another hook family's result:
///
/// ```compile_fail
/// use torana_plugin_sdk::{Plugin, RequestResult, ResponseResult, pbv1};
/// struct Wrong;
/// impl Plugin for Wrong {
///     const SUPPORTED_HOOKS: u32 = 1 << 2;
///     fn after_response(_: pbv1::ChatResponse) -> Result<ResponseResult, String> {
///         Ok(RequestResult::pass())
///     }
/// }
/// ```
pub trait Plugin {
    const SUPPORTED_HOOKS: u32;
    fn before_request(_: pbv1::ChatRequest) -> Result<RequestResult, String> {
        Ok(RequestResult::pass())
    }
    fn after_response(_: pbv1::ChatResponse) -> Result<ResponseResult, String> {
        Ok(ResponseResult::pass())
    }
    fn on_stream(_: pbv1::StreamEvent) -> Result<StreamResult, String> {
        Ok(StreamResult::pass())
    }
    fn on_http(_: pbv1::HttpRequest) -> Result<HttpResult, String> {
        Ok(HttpResult::pass())
    }
    fn on_tick(_: pbv1::TickRequest) -> Result<TickResult, String> {
        Ok(TickResult::pass())
    }
}

pub fn __plugin_dispatch<P: Plugin>(
    input: pbv1::HookInput,
) -> Result<Option<pbv1::HookResult>, String> {
    match input.payload {
        Some(pbv1::hook_input::Payload::ChatRequest(r)) => {
            P::before_request(r).map(|x| x.into_hook_result())
        }
        Some(pbv1::hook_input::Payload::AfterResponse(r)) => {
            P::after_response(r.response.ok_or("missing response")?).map(|x| x.into_hook_result())
        }
        Some(pbv1::hook_input::Payload::StreamEvent(e)) => {
            P::on_stream(e).map(|x| x.into_hook_result())
        }
        Some(pbv1::hook_input::Payload::HttpRequest(r)) => {
            P::on_http(r).map(|x| x.into_hook_result())
        }
        Some(pbv1::hook_input::Payload::TickRequest(r)) => {
            P::on_tick(r).map(|x| x.into_hook_result())
        }
        None => Err("missing hook payload".into()),
    }
}

pub fn replace_request(request: pbv1::ChatRequest) -> Result<pbv1::HookResult, String> {
    validate_chat_request(&request)?;
    Ok(pbv1::HookResult {
        action: Some(pbv1::hook_result::Action::ReplaceRequest(request)),
    })
}

pub fn set_text_at(message: &mut pbv1::Message, block: usize, text: &str) -> Result<(), String> {
    let Some(slot) = message.blocks.get_mut(block) else {
        return Err("text block index out of range".into());
    };
    let Some(pbv1::request_block::Kind::Text(t)) = slot.kind.as_mut() else {
        return Err("block is not text".into());
    };
    if t.text != text {
        t.text = text.into();
        t.signature.clear();
        if message.blocks.last().is_some_and(|b| {
            matches!(
                b.kind,
                Some(pbv1::request_block::Kind::TrailingSignature(_))
            )
        }) {
            message.blocks.pop();
        }
    }
    Ok(())
}
pub fn replace_tool_result_text(
    message: &mut pbv1::Message,
    block: usize,
    text: &str,
) -> Result<bool, String> {
    let Some(slot) = message.blocks.get_mut(block) else {
        return Err("tool result index out of range".into());
    };
    let Some(pbv1::request_block::Kind::ToolResult(tr)) = slot.kind.as_mut() else {
        return Err("block is not tool result".into());
    };
    let mut found = None;
    for (i, c) in tr.content.iter().enumerate() {
        match c.kind.as_ref() {
            Some(pbv1::tool_result_content_block::Kind::Text(_)) if found.is_none() => {
                found = Some(i)
            }
            Some(pbv1::tool_result_content_block::Kind::Text(_)) => {
                return Err("multiple text arms".into())
            }
            Some(pbv1::tool_result_content_block::Kind::Unknown(_)) => {
                return Err("unknown content arm".into())
            }
            Some(pbv1::tool_result_content_block::Kind::CacheBreakpoint(_)) => {}
            None => return Err("empty content arm".into()),
        }
    }
    let i = found.ok_or("tool result has no text")?;
    let Some(pbv1::tool_result_content_block::Kind::Text(t)) = tr.content[i].kind.as_mut() else {
        unreachable!()
    };
    if t.text == text {
        return Ok(false);
    }
    t.text = text.into();
    tr.signature.clear();
    Ok(true)
}
pub fn set_cache_breakpoint(
    message: &mut pbv1::Message,
    block: usize,
    marker: &[u8],
) -> Result<(), String> {
    let Some(slot) = message.blocks.get_mut(block) else {
        return Err("cache index out of range".into());
    };
    let Some(pbv1::request_block::Kind::CacheBreakpoint(c)) = slot.kind.as_mut() else {
        return Err("block is not cache breakpoint".into());
    };
    json_object(marker, "marker_json")?;
    c.marker_json = marker.to_vec();
    Ok(())
}
pub fn move_cache_breakpoint(
    message: &mut pbv1::Message,
    from: usize,
    to: usize,
) -> Result<(), String> {
    if from >= message.blocks.len() || to > message.blocks.len() {
        return Err("cache breakpoint index out of range".into());
    }
    let b = message.blocks.remove(from);
    if !matches!(b.kind, Some(pbv1::request_block::Kind::CacheBreakpoint(_))) {
        return Err("block is not cache breakpoint".into());
    }
    let at = if to > from { to - 1 } else { to };
    message.blocks.insert(at, b);
    Ok(())
}
pub fn replace_response(response: pbv1::ChatResponse) -> Result<pbv1::HookResult, String> {
    validate_response(&response)?;
    Ok(pbv1::HookResult {
        action: Some(pbv1::hook_result::Action::ReplaceResponse(response)),
    })
}
pub fn emit_events(events: Vec<pbv1::StreamEvent>) -> Result<pbv1::HookResult, String> {
    if events.is_empty() {
        return Err("torana sdk: emitted events cannot be empty".into());
    }
    Ok(pbv1::HookResult {
        action: Some(pbv1::hook_result::Action::EmitEvents(pbv1::StreamEvents {
            events,
        })),
    })
}
pub fn serve_http(response: pbv1::HttpResponse) -> Result<pbv1::HookResult, String> {
    validate_action(&pbv1::hook_result::Action::ServeHttp(response.clone()))?;
    Ok(pbv1::HookResult {
        action: Some(pbv1::hook_result::Action::ServeHttp(response)),
    })
}
pub fn tick_outcome(actions: i32, note: impl Into<String>) -> Result<pbv1::HookResult, String> {
    if actions < 0 {
        return Err("torana sdk: TickOutcome.actions cannot be negative".into());
    }
    Ok(pbv1::HookResult {
        action: Some(pbv1::hook_result::Action::TickOutcome(pbv1::TickOutcome {
            actions,
            note: note.into(),
        })),
    })
}

impl core::fmt::Display for HostCallError {
    fn fmt(&self, f: &mut core::fmt::Formatter<'_>) -> core::fmt::Result {
        match self {
            Self::Refused(err) => write!(f, "host call refused ({})", err.code),
            Self::Protocol(message) => f.write_str(message),
        }
    }
}

impl std::error::Error for HostCallError {}

/// ABI-v1 hook bitmap bits. Bit N corresponds to `pbv1::Hook` value N.
pub const HOOK_BEFORE_REQUEST: u32 = 1 << (pbv1::Hook::BeforeRequest as u32);
pub const HOOK_AFTER_RESPONSE: u32 = 1 << (pbv1::Hook::AfterResponse as u32);
pub const HOOK_ON_STREAM_CHUNK: u32 = 1 << (pbv1::Hook::OnStreamChunk as u32);
pub const HOOK_ON_HTTP_REQUEST: u32 = 1 << (pbv1::Hook::OnHttpRequest as u32);
pub const HOOK_ON_TICK: u32 = 1 << (pbv1::Hook::OnTick as u32);
const ALL_V1_HOOKS: u32 = HOOK_BEFORE_REQUEST
    | HOOK_AFTER_RESPONSE
    | HOOK_ON_STREAM_CHUNK
    | HOOK_ON_HTTP_REQUEST
    | HOOK_ON_TICK;

/// Returns the hook selected by a valid ABI-v1 input envelope.
pub fn hook_of(input: &pbv1::HookInput) -> Result<pbv1::Hook, String> {
    use pbv1::hook_input::Payload;
    match input.payload.as_ref() {
        Some(Payload::ChatRequest(_)) => Ok(pbv1::Hook::BeforeRequest),
        Some(Payload::AfterResponse(_)) => Ok(pbv1::Hook::AfterResponse),
        Some(Payload::StreamEvent(_)) => Ok(pbv1::Hook::OnStreamChunk),
        Some(Payload::HttpRequest(_)) => Ok(pbv1::Hook::OnHttpRequest),
        Some(Payload::TickRequest(_)) => Ok(pbv1::Hook::OnTick),
        None => Err("torana sdk: HookInput requires a payload".to_owned()),
    }
}
pub fn execution() -> Option<pbv1::ExecutionInfo> {
    EXECUTION_CTX.with(|x| x.borrow().as_ref().and_then(|(_, e)| e.clone()))
}
pub fn request_id() -> Option<u64> {
    EXECUTION_CTX.with(|x| x.borrow().as_ref().map(|(id, _)| *id))
}

struct ExecutionGuard(Option<(u64, Option<pbv1::ExecutionInfo>)>);
impl ExecutionGuard {
    fn enter(input: &pbv1::HookInput) -> Self {
        let old = EXECUTION_CTX
            .with(|slot| slot.replace(Some((input.request_id, input.execution.clone()))));
        Self(old)
    }
}
impl Drop for ExecutionGuard {
    fn drop(&mut self) {
        EXECUTION_CTX.with(|slot| {
            slot.replace(self.0.take());
        });
    }
}

fn json_object(raw: &[u8], field: &str) -> Result<(), String> {
    if raw.is_empty() {
        return Ok(());
    }
    let value: serde_json::Value =
        strict_json(raw).map_err(|e| format!("torana sdk: {field} is invalid JSON: {e}"))?;
    if !value.is_object() {
        return Err(format!("torana sdk: {field} must be a JSON object"));
    }
    Ok(())
}
fn json_array(raw: &[u8], field: &str) -> Result<(), String> {
    if raw.is_empty() {
        return Ok(());
    }
    let value: serde_json::Value =
        strict_json(raw).map_err(|e| format!("torana sdk: {field} is invalid JSON: {e}"))?;
    if !value.is_array() {
        return Err(format!("torana sdk: {field} must be a JSON array"));
    }
    Ok(())
}

fn strict_json(raw: &[u8]) -> Result<serde_json::Value, serde_json::Error> {
    use serde::de::{DeserializeSeed, Deserializer, MapAccess, SeqAccess, Visitor};
    use std::{collections::HashSet, fmt};
    struct V;
    impl<'de> DeserializeSeed<'de> for V {
        type Value = serde_json::Value;
        fn deserialize<D: Deserializer<'de>>(self, d: D) -> Result<Self::Value, D::Error> {
            d.deserialize_any(V)
        }
    }
    impl<'de> Visitor<'de> for V {
        type Value = serde_json::Value;
        fn expecting(&self, f: &mut fmt::Formatter) -> fmt::Result {
            f.write_str("JSON value")
        }
        fn visit_map<A: MapAccess<'de>>(self, mut a: A) -> Result<Self::Value, A::Error> {
            let mut m = serde_json::Map::new();
            let mut keys = HashSet::new();
            while let Some(k) = a.next_key::<String>()? {
                if !keys.insert(k.clone()) {
                    return Err(serde::de::Error::custom("duplicate JSON key"));
                }
                m.insert(k, a.next_value_seed(V)?);
            }
            Ok(serde_json::Value::Object(m))
        }
        fn visit_seq<A: SeqAccess<'de>>(self, mut a: A) -> Result<Self::Value, A::Error> {
            let mut v = Vec::new();
            while let Some(x) = a.next_element_seed(V)? {
                v.push(x);
            }
            Ok(serde_json::Value::Array(v))
        }
        fn visit_bool<E: serde::de::Error>(self, v: bool) -> Result<Self::Value, E> {
            Ok(serde_json::Value::Bool(v))
        }
        fn visit_i64<E: serde::de::Error>(self, v: i64) -> Result<Self::Value, E> {
            Ok(serde_json::Value::Number(v.into()))
        }
        fn visit_u64<E: serde::de::Error>(self, v: u64) -> Result<Self::Value, E> {
            Ok(serde_json::Value::Number(v.into()))
        }
        fn visit_f64<E: serde::de::Error>(self, v: f64) -> Result<Self::Value, E> {
            serde_json::Number::from_f64(v)
                .map(serde_json::Value::Number)
                .ok_or_else(|| E::custom("non-finite number"))
        }
        fn visit_str<E: serde::de::Error>(self, v: &str) -> Result<Self::Value, E> {
            Ok(serde_json::Value::String(v.into()))
        }
        fn visit_string<E: serde::de::Error>(self, v: String) -> Result<Self::Value, E> {
            Ok(serde_json::Value::String(v))
        }
        fn visit_none<E: serde::de::Error>(self) -> Result<Self::Value, E> {
            Ok(serde_json::Value::Null)
        }
        fn visit_unit<E: serde::de::Error>(self) -> Result<Self::Value, E> {
            Ok(serde_json::Value::Null)
        }
    }
    let mut d = serde_json::Deserializer::from_slice(raw);
    let v = d.deserialize_any(V)?;
    d.end()?;
    Ok(v)
}

/* fn strict_json(raw: &[u8]) -> Result<serde_json::Value, serde_json::Error> {
    use serde::de::{Deserialize, DeserializeSeed, MapAccess, SeqAccess, Visitor};
    use std::{collections::HashSet, fmt};
    struct V;
    impl<'de> Visitor<'de> for V {
        type Value = serde_json::Value;
        fn expecting(&self, f: &mut fmt::Formatter) -> fmt::Result {
            f.write_str("JSON value")
        }
        fn visit_map<A: MapAccess<'de>>(self, mut a: A) -> Result<Self::Value, A::Error> {
            let mut m = serde_json::Map::new();
            let mut keys = HashSet::new();
            while let Some(k) = a.next_key::<String>()? {
                if !keys.insert(k.clone()) {
                    return Err(serde::de::Error::custom("duplicate JSON key"));
                }
                m.insert(k, a.next_value_seed(V)?);
            }
            Ok(serde_json::Value::Object(m))
        }
        fn visit_seq<A: SeqAccess<'de>>(self, mut a: A) -> Result<Self::Value, A::Error> {
            let mut v = Vec::new();
            while let Some(x) = a.next_element_seed(V)? {
                v.push(x);
            }
            Ok(serde_json::Value::Array(v))
        }
        fn visit_bool<E: serde::de::Error>(self, v: bool) -> Result<Self::Value, E> {
            Ok(serde_json::Value::Bool(v))
        }
        fn visit_i64<E: serde::de::Error>(self, v: i64) -> Result<Self::Value, E> {
            Ok(serde_json::Value::Number(v.into()))
        }
        fn visit_u64<E: serde::de::Error>(self, v: u64) -> Result<Self::Value, E> {
            Ok(serde_json::Value::Number(v.into()))
        }
        fn visit_f64<E: serde::de::Error>(self, v: f64) -> Result<Self::Value, E> {
            serde_json::Number::from_f64(v)
                .map(serde_json::Value::Number)
                .ok_or_else(|| E::custom("non-finite number"))
        }
        fn visit_str<E: serde::de::Error>(self, v: &str) -> Result<Self::Value, E> {
            Ok(serde_json::Value::String(v.into()))
        }
        fn visit_string<E: serde::de::Error>(self, v: String) -> Result<Self::Value, E> {
            Ok(serde_json::Value::String(v))
        }
        fn visit_none<E: serde::de::Error>(self) -> Result<Self::Value, E> {
            Ok(serde_json::Value::Null)
        }
        fn visit_unit<E: serde::de::Error>(self) -> Result<Self::Value, E> {
            Ok(serde_json::Value::Null)
        }
    }
    let mut d = serde_json::Deserializer::from_slice(raw);
    let v = V.deserialize(&mut d)?;
    d.end()?;
    Ok(v)
} */

fn validate_chat_request(request: &pbv1::ChatRequest) -> Result<(), String> {
    if request.max_tokens.is_some_and(|v| v <= 0)
        || request.temperature.is_some_and(|v| !v.is_finite())
        || request.top_p.is_some_and(|v| !v.is_finite())
    {
        return Err("torana sdk: ChatRequest numeric fields are invalid".into());
    }
    for (i, message) in request.messages.iter().enumerate() {
        if message.role.is_empty() {
            return Err(format!("torana sdk: message {i} role is required"));
        }
        if message.blocks.is_empty() {
            return Err(format!("torana sdk: message {i} requires blocks"));
        }
        for block in &message.blocks {
            let Some(kind) = block.kind.as_ref() else {
                return Err(format!("torana sdk: message {i} contains an empty block"));
            };
            match kind {
                pbv1::request_block::Kind::ToolUse(t) => {
                    if t.id.is_empty() || t.name.is_empty() {
                        return Err("torana sdk: tool use id/name required".into());
                    }
                    if !matches!(t.invocation_kind, x if x == pbv1::ToolInvocationKind::Function as i32 || x == pbv1::ToolInvocationKind::Freeform as i32)
                    {
                        return Err("torana sdk: invalid tool invocation kind".into());
                    }
                    match t.invocation_kind {
                        x if x == pbv1::ToolInvocationKind::Function as i32 => {
                            if t.arguments_json.is_empty() || t.input_text.is_some() {
                                return Err("torana sdk: invalid function tool input".into());
                            }
                            json_object(&t.arguments_json, "arguments_json")?;
                        }
                        _ => {
                            if t.input_text.is_none() || !t.arguments_json.is_empty() {
                                return Err("torana sdk: invalid freeform tool input".into());
                            }
                        }
                    }
                    json_object(&t.part_metadata_json, "part_metadata_json")?;
                }
                pbv1::request_block::Kind::ToolResult(t) => {
                    if t.tool_call_id.is_empty()
                        || t.content.is_empty()
                        || t.content.iter().any(|x| x.kind.is_none())
                    {
                        return Err("torana sdk: invalid tool result".into());
                    }
                }
                pbv1::request_block::Kind::CacheBreakpoint(t) => {
                    if t.marker_json.is_empty() {
                        return Err("torana sdk: cache marker required".into());
                    }
                    json_object(&t.marker_json, "marker_json")?;
                }
                pbv1::request_block::Kind::Unknown(t) => {
                    if t.kind.is_empty() || t.payload_json.is_empty() {
                        return Err("torana sdk: unknown block requires kind and payload".into());
                    }
                    json_object(&t.payload_json, "payload_json")?;
                }
                pbv1::request_block::Kind::TrailingSignature(t) if t.signature.is_empty() => {
                    return Err("torana sdk: trailing signature required".into())
                }
                _ => {}
            }
        }
    }
    json_object(
        &request.provider_extensions_json,
        "provider_extensions_json",
    )?;
    json_array(&request.safety_settings_json, "safety_settings_json")?;
    Ok(())
}

fn validate_response(response: &pbv1::ChatResponse) -> Result<(), String> {
    if let Some(message) = response.message.as_ref() {
        if message.blocks.iter().any(|b| b.kind.is_none()) {
            return Err("torana sdk: response contains an empty block".into());
        }
    }
    json_object(
        &response.provider_extensions_json,
        "provider_extensions_json",
    )
}

fn validate_input(input: &pbv1::HookInput, raw: &[u8]) -> Result<(), String> {
    __validate_wire_message(raw, ".torana.v1.HookInput")?;
    if input.contract_revision != 1 {
        return Err(format!(
            "torana sdk: unsupported contract revision {}",
            input.contract_revision
        ));
    }
    match input.payload.as_ref() {
        Some(pbv1::hook_input::Payload::ChatRequest(r)) => validate_chat_request(r),
        Some(pbv1::hook_input::Payload::AfterResponse(r)) => {
            let response = r
                .response
                .as_ref()
                .ok_or("torana sdk: AfterResponse.response is required")?;
            validate_response(response)
        }
        Some(pbv1::hook_input::Payload::HttpRequest(r)) => {
            json_object(&r.headers_json, "HttpRequest.headers_json")
        }
        Some(pbv1::hook_input::Payload::StreamEvent(event)) => validate_stream_event(event),
        Some(pbv1::hook_input::Payload::TickRequest(_)) => Ok(()),
        None => Err("torana sdk: HookInput requires a payload".into()),
    }
}

fn validate_stream_event(event: &pbv1::StreamEvent) -> Result<(), String> {
    use pbv1::stream_event::Event;
    match event.event.as_ref() {
        None => Err("torana sdk: stream event carries no event".into()),
        Some(Event::ToolCallDelta(delta)) => {
            if delta.index < 0 {
                Err("torana sdk: tool call delta index must be non-negative".into())
            } else if delta.input_text_delta.is_some() && !delta.arguments_delta.is_empty() {
                Err("torana sdk: tool call delta cannot carry both argument forms".into())
            } else {
                Ok(())
            }
        }
        Some(Event::ContentBlockStart(start)) => {
            if start.index < 0 || start.block.is_none() {
                return Err(
                    "torana sdk: content block start requires non-negative index and block".into(),
                );
            }
            if let Some(pbv1::content_block_start::Block::ToolCall(call)) = start.block.as_ref() {
                if call.id.is_empty() || call.name.is_empty() {
                    return Err("torana sdk: tool call start requires id and name".into());
                }
                if !matches!(call.invocation_kind, 1 | 2) {
                    return Err("torana sdk: invalid tool invocation kind".into());
                }
            }
            if let Some(pbv1::content_block_start::Block::Provider(provider)) = start.block.as_ref()
            {
                if provider.kind.is_empty() {
                    return Err("torana sdk: provider block kind is required".into());
                }
            }
            Ok(())
        }
        Some(Event::ContentBlockStop(stop)) if stop.index < 0 => {
            Err("torana sdk: content block stop index must be non-negative".into())
        }
        Some(_) => Ok(()),
    }
}

#[doc(hidden)]
pub fn __validate_wire_message(mut bytes: &[u8], name: &str) -> Result<(), String> {
    use prost::Message;
    use prost_types::{
        field_descriptor_proto::{Label, Type},
        FileDescriptorSet,
    };
    static SET: std::sync::OnceLock<Result<FileDescriptorSet, String>> = std::sync::OnceLock::new();
    let set = SET
        .get_or_init(|| {
            FileDescriptorSet::decode(
                include_bytes!(concat!(env!("OUT_DIR"), "/torana.descriptor.bin")).as_slice(),
            )
            .map_err(|e| format!("torana sdk: descriptor decode: {e}"))
        })
        .as_ref()
        .map_err(Clone::clone)?;
    let descriptor = set
        .file
        .iter()
        .find_map(|f| {
            let prefix = f
                .package
                .as_deref()
                .map(|p| format!(".{p}."))
                .unwrap_or_else(|| ".".into());
            f.message_type
                .iter()
                .find(|m| format!("{}{}", prefix, m.name.as_deref().unwrap_or("")) == name)
        })
        .ok_or_else(|| format!("torana sdk: unknown message descriptor {name}"))?;
    let mut seen_oneof = std::collections::HashSet::new();
    let mut seen_singular = std::collections::HashSet::new();
    while !bytes.is_empty() {
        let (key, n) = read_varint_raw(bytes)?;
        bytes = &bytes[n..];
        let number = i32::try_from(key >> 3).map_err(|_| "torana sdk: field number overflow")?;
        let wire = key & 7;
        let field = descriptor
            .field
            .iter()
            .find(|f| f.number == Some(number))
            .ok_or_else(|| format!("torana sdk: unknown field {number} in {name}"))?;
        if field.label.and_then(|label| Label::try_from(label).ok()) != Some(Label::Repeated)
            && !seen_singular.insert(number)
        {
            return Err(format!("torana sdk: duplicate field {number} in {name}"));
        }
        let expected = match field.r#type.and_then(|value| Type::try_from(value).ok()) {
            Some(Type::Double) | Some(Type::Fixed64) | Some(Type::Sfixed64) => 1,
            Some(Type::Float) | Some(Type::Fixed32) | Some(Type::Sfixed32) => 5,
            Some(Type::Int32) | Some(Type::Sint32) | Some(Type::Int64) | Some(Type::Sint64)
            | Some(Type::Uint32) | Some(Type::Uint64) | Some(Type::Bool) | Some(Type::Enum) => 0,
            _ => 2,
        };
        if wire != expected {
            return Err(format!(
                "torana sdk: field {number} in {name} has wrong wire type"
            ));
        }
        if let Some(index) = field.oneof_index {
            if !seen_oneof.insert(index) {
                return Err(format!("torana sdk: duplicate oneof arm in {name}"));
            }
        }
        if wire == 2 {
            let (len, p) = read_varint_raw(bytes)?;
            let len = usize::try_from(len).map_err(|_| "torana sdk: length overflow")?;
            if p.checked_add(len).ok_or("torana sdk: length overflow")? > bytes.len() {
                return Err("torana sdk: truncated nested field".into());
            }
            if let Some(ty) = field.type_name.as_deref() {
                __validate_wire_message(&bytes[p..p + len], ty)?;
            }
        }
        let consumed = skip_wire(bytes, wire)?;
        bytes = &bytes[consumed..];
    }
    Ok(())
}

fn validate_action(action: &pbv1::hook_result::Action) -> Result<(), String> {
    match action {
        pbv1::hook_result::Action::ReplaceRequest(r) => validate_chat_request(r),
        pbv1::hook_result::Action::ReplaceResponse(r) => validate_response(r),
        pbv1::hook_result::Action::ServeHttp(r) => {
            if !(200..=599).contains(&r.status) {
                return Err("torana sdk: HttpResponse.status must be 200..=599".into());
            }
            if matches!(r.status, 204 | 205 | 304) && !r.body.is_empty() {
                return Err("torana sdk: status forbids a response body".into());
            }
            json_object(&r.headers_json, "HttpResponse.headers_json")
        }
        pbv1::hook_result::Action::TickOutcome(r) if r.actions < 0 => {
            Err("torana sdk: TickOutcome.actions cannot be negative".into())
        }
        pbv1::hook_result::Action::EmitEvents(r) if r.events.is_empty() => {
            Err("torana sdk: emitted events cannot be empty".into())
        }
        pbv1::hook_result::Action::EmitEvents(r) => {
            if r.events.iter().any(|event| event.event.is_none()) {
                return Err("torana sdk: emitted event requires an event arm".into());
            }
            Ok(())
        }
        _ => Ok(()),
    }
}

fn read_varint_raw(bytes: &[u8]) -> Result<(u64, usize), String> {
    for i in 0..10 {
        let b = *bytes.get(i).ok_or("torana sdk: truncated protobuf")?;
        if i == 9 && b > 1 {
            break;
        }
        if b & 0x80 == 0 {
            let mut v = 0;
            for (j, x) in bytes[..=i].iter().enumerate() {
                v |= u64::from(x & 0x7f) << (j * 7);
            }
            return Ok((v, i + 1));
        }
    }
    Err("torana sdk: invalid protobuf varint".into())
}
fn skip_wire(bytes: &[u8], wire: u64) -> Result<usize, String> {
    match wire {
        0 => Ok(read_varint_raw(bytes)?.1),
        1 => {
            if bytes.len() >= 8 {
                Ok(8)
            } else {
                Err("torana sdk: truncated fixed64".into())
            }
        }
        2 => {
            let (n, m) = read_varint_raw(bytes)?;
            let n = usize::try_from(n).map_err(|_| "torana sdk: length overflow")?;
            if bytes.len() - m < n {
                Err("torana sdk: truncated bytes".into())
            } else {
                Ok(m + n)
            }
        }
        5 => {
            if bytes.len() >= 4 {
                Ok(4)
            } else {
                Err("torana sdk: truncated fixed32".into())
            }
        }
        _ => Err("torana sdk: unsupported protobuf wire type".into()),
    }
}

fn validate_hook_result(hook: pbv1::Hook, result: &pbv1::HookResult) -> Result<(), String> {
    use pbv1::hook_result::Action;
    let valid = matches!(
        (hook, result.action.as_ref()),
        (pbv1::Hook::BeforeRequest, Some(Action::ReplaceRequest(_)))
            | (pbv1::Hook::AfterResponse, Some(Action::ReplaceResponse(_)))
            | (pbv1::Hook::OnStreamChunk, Some(Action::EmitEvents(_)))
            | (pbv1::Hook::OnStreamChunk, Some(Action::Suppress(_)))
            | (pbv1::Hook::OnHttpRequest, Some(Action::ServeHttp(_)))
            | (pbv1::Hook::OnTick, Some(Action::TickOutcome(_)))
    );
    if valid {
        Ok(())
    } else {
        Err(format!(
            "torana sdk: HookResult action does not match {}",
            hook.as_str_name()
        ))
    }
}

/// Decodes, dispatches, validates, and encodes one ABI-v1 hook invocation.
///
/// Returning `Ok(None)` from `handler` is the sole pass-through representation.
/// This ordinary Rust function owns the contract logic so it is testable off
/// WASM; [`export_plugin_v1!`] only supplies the two required exports.
#[doc(hidden)]
pub fn __dispatch_v1<E: core::fmt::Display>(
    input_bytes: &[u8],
    hooks: u32,
    handler: fn(pbv1::HookInput) -> Result<Option<pbv1::HookResult>, E>,
) -> Result<Vec<u8>, String> {
    use prost::Message;

    if hooks == 0 || hooks & !ALL_V1_HOOKS != 0 {
        return Err(
            "torana sdk: supported hook bitmap is empty or contains unknown bits".to_owned(),
        );
    }
    let input = pbv1::HookInput::decode(input_bytes)
        .map_err(|err| format!("torana sdk: decode run_hook: {err}"))?;
    validate_input(&input, input_bytes)?;
    let hook = hook_of(&input)?;
    if hooks & (1 << hook as u32) == 0 {
        return Err(format!(
            "torana sdk: dispatched unregistered hook {}",
            hook.as_str_name()
        ));
    }
    let _execution_guard = ExecutionGuard::enter(&input);
    let Some(result) = handler(input).map_err(|err| format!("torana plugin: {err}"))? else {
        return Ok(Vec::new());
    };
    validate_hook_result(hook, &result)?;
    if let Some(action) = result.action.as_ref() {
        validate_action(action)?;
    }
    let mut output = Vec::new();
    result
        .encode(&mut output)
        .map_err(|err| format!("torana sdk: encode run_hook: {err}"))?;
    if output.is_empty() {
        return Err("torana sdk: non-pass HookResult encoded to empty bytes".to_owned());
    }
    Ok(output)
}

/// Exports the two functions required by ABI v1: `supported_hooks` and
/// `run_hook`. The handler receives the typed HookInput and returns `Ok(None)`
/// for pass-through or one hook-appropriate HookResult.
#[macro_export]
macro_rules! export_plugin_v1 {
    ($plugin:ty) => {
        #[no_mangle]
        pub extern "C" fn abi_version() -> u64 {
            $crate::ABI_VERSION
        }
        #[no_mangle]
        pub extern "C" fn supported_hooks() -> u32 {
            <$plugin as $crate::Plugin>::SUPPORTED_HOOKS
        }
        #[no_mangle]
        pub extern "C" fn run_hook(ptr: u32, len: u32) -> u64 {
            let input = unsafe { $crate::__input(ptr, len) };
            let output = $crate::__dispatch_v1(
                input,
                <$plugin as $crate::Plugin>::SUPPORTED_HOOKS,
                $crate::__plugin_dispatch::<$plugin>,
            )
            .unwrap_or_else(|e| panic!("{e}"));
            $crate::__result(&output)
        }
    };
    ($hooks:expr, $handler:path) => {
        #[no_mangle]
        pub extern "C" fn abi_version() -> u64 {
            $crate::ABI_VERSION
        }

        #[no_mangle]
        pub extern "C" fn supported_hooks() -> u32 {
            $hooks
        }

        #[no_mangle]
        pub extern "C" fn run_hook(ptr: u32, len: u32) -> u64 {
            // SAFETY: ptr/len are the guest-memory allocation populated by
            // the Torana host for this invocation and are consumed before
            // the hook returns.
            let input = unsafe { $crate::__input(ptr, len) };
            let output = $crate::__dispatch_v1(input, $hooks, $handler)
                .unwrap_or_else(|error| panic!("{error}"));
            $crate::__result(&output)
        }
    };
}

#[repr(i32)]
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum LogLevel {
    Debug = 0,
    Info = 1,
}
pub const LOG_DEBUG: LogLevel = LogLevel::Debug;
pub const LOG_INFO: LogLevel = LogLevel::Info;

#[link(wasm_import_module = "env")]
#[cfg(target_arch = "wasm32")]
extern "C" {
    #[link_name = "log"]
    fn host_log(level: i32, ptr: u32, len: u32);
    #[link_name = "emit_metric"]
    fn host_emit_metric(
        kind: i32,
        ptr: u32,
        len: u32,
        value: f64,
        labels_ptr: u32,
        labels_len: u32,
    );
    #[link_name = "host_call"]
    fn raw_host_call(cmd_ptr: u32, cmd_len: u32, args_ptr: u32, args_len: u32) -> u64;
}

/// Logs a bounded diagnostic string when the host granted `env.log`.
/// Delivery is best effort: the void ABI import cannot report refusal.
pub fn log(message: &str, level: LogLevel) {
    if message.is_empty() {
        return;
    }
    #[cfg(target_arch = "wasm32")]
    unsafe {
        host_log(level as i32, message.as_ptr() as u32, message.len() as u32)
    }
    #[cfg(not(target_arch = "wasm32"))]
    let _ = level as i32;
}

// Allocation goes through `std::alloc` with an explicit `Layout`, which is the
// pattern docs/WASM_PLUGIN_GUIDE.md already documents and this crate did not
// follow.
//
// The previous version paired `Vec::with_capacity(n)` + `mem::forget` with
// `Vec::from_raw_parts(ptr, 0, n)`. That requires n to be the vector's ACTUAL
// capacity, and `with_capacity` is only obliged to allocate *at least* n. It
// happens to allocate exactly n for `u8` today, so nothing has gone wrong yet —
// but the deallocation is only correct by coincidence, and freeing with a
// layout that does not match the allocation is undefined behaviour. Naming the
// layout in both places removes the coincidence.

/// Allocates `size` bytes and returns a host-visible pointer.
///
/// The buffer is uninitialised. Hosts call [`dealloc`] with the same size after
/// consuming a non-zero hook result.
///
/// Allocation failure TRAPS rather than returning a sentinel. ABI v1 defines
/// no failure value for `alloc`, and the host treats 0 as a valid pointer — it
/// would write the payload at linear-memory offset 0, over the guest's own
/// memory, and then call the hook with `ptr = 0`.
#[no_mangle]
pub extern "C" fn alloc(size: u32) -> u32 {
    alloc_bytes(size as usize) as u32
}

/// Frees a buffer previously returned by [`alloc`]. `size` must be the size
/// that was passed to `alloc`.
#[no_mangle]
pub extern "C" fn dealloc(ptr: u32, size: u32) {
    dealloc_bytes(ptr as *mut u8, size as usize);
}

// The pointer-level halves exist so the allocator contract can be tested on the
// host, where a real pointer does not fit in the u32 the wasm ABI uses.

fn alloc_bytes(size: usize) -> *mut u8 {
    if size == 0 {
        // Zero bytes needs no allocation, and a zero-sized layout is UB to
        // pass to the allocator. No caller dereferences this.
        return ptr::null_mut();
    }
    let Ok(layout) = Layout::array::<u8>(size) else {
        // Only reachable for a size beyond isize::MAX. Unrecoverable, and
        // there is no ABI value to report it with.
        std::process::abort();
    };
    let p = unsafe { std::alloc::alloc(layout) };
    if p.is_null() {
        // Out of memory. Returning null here would be reported to the host as
        // a pass-through by __result, which means the plugin's output is
        // dropped and the ORIGINAL request continues upstream — a redaction
        // plugin would fail open. Trap so failure_mode applies.
        std::alloc::handle_alloc_error(layout);
    }
    p
}

fn dealloc_bytes(p: *mut u8, size: usize) {
    if p.is_null() || size == 0 {
        return;
    }
    if let Ok(layout) = Layout::array::<u8>(size) {
        unsafe { std::alloc::dealloc(p, layout) }
    }
}

/// Borrows a host-provided buffer as a slice.
///
/// # Safety
///
/// `ptr..ptr+len` must denote readable guest memory for the returned borrow's
/// lifetime. The memory must not be mutated or freed during that lifetime.
/// Only the generated hook wrapper should call this function.
///
/// The leading underscores mark it as ABI plumbing rather than API.
#[doc(hidden)]
pub unsafe fn __input<'a>(ptr: u32, len: u32) -> &'a [u8] {
    if ptr == 0 || len == 0 {
        return &[];
    }
    unsafe { slice::from_raw_parts(ptr as *const u8, len as usize) }
}

/// Copies `bytes` into a freshly allocated host-visible buffer and packs the
/// pointer and length into the u64 the ABI returns.
#[doc(hidden)]
pub fn __result(bytes: &[u8]) -> u64 {
    // Zero is reserved by the ABI for a DELIBERATE pass-through. An empty
    // payload is the only thing that means, so it is the only thing that
    // returns 0 here — allocation failure traps inside alloc_bytes rather than
    // arriving as a null this function could not tell apart from "no change".
    if bytes.is_empty() {
        return 0;
    }
    let (dst, len) = copy_to_owned_buffer(bytes);
    pack(dst as u32, len as u32)
}

/// Copies `bytes` into a fresh buffer, returning the pointer and length.
///
/// Split out from [`__result`] so it can be tested: off wasm32 a real pointer
/// does not fit in the u32 the ABI packs it into, so a test that round-trips
/// through `pack` would reconstruct a truncated pointer and segfault.
fn copy_to_owned_buffer(bytes: &[u8]) -> (*mut u8, usize) {
    if bytes.is_empty() {
        return (ptr::null_mut(), 0);
    }
    // Non-null: alloc_bytes traps on failure rather than returning null.
    let dst = alloc_bytes(bytes.len());
    // copy_nonoverlapping rather than building a &mut [u8] first. The buffer is
    // UNINITIALISED, and constructing a reference to uninitialised memory is
    // undefined behaviour even when nothing reads it before the write — which
    // is exactly what `slice::from_raw_parts_mut(..).copy_from_slice(..)` did.
    unsafe { ptr::copy_nonoverlapping(bytes.as_ptr(), dst, bytes.len()) };
    (dst, bytes.len())
}

/// Packs a pointer and length into the single u64 an ABI hook returns:
/// pointer in the high 32 bits, length in the low 32.
fn pack(ptr: u32, len: u32) -> u64 {
    ((ptr as u64) << 32) | len as u64
}

#[repr(i32)]
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum MetricKind {
    Counter = 0,
    Histogram = 1,
    Gauge = 2,
}
pub const METRIC_COUNTER: MetricKind = MetricKind::Counter;
pub const METRIC_HISTOGRAM: MetricKind = MetricKind::Histogram;
pub const METRIC_GAUGE: MetricKind = MetricKind::Gauge;

/// Emits a metric on a best-effort void host import.
pub fn emit_metric(name: &str, kind: MetricKind, value: f64, labels: &serde_json::Value) {
    let labels = labels.to_string();
    #[cfg(target_arch = "wasm32")]
    unsafe {
        host_emit_metric(
            kind as i32,
            name.as_ptr() as u32,
            name.len() as u32,
            value,
            labels.as_ptr() as u32,
            labels.len() as u32,
        )
    }
    #[cfg(not(target_arch = "wasm32"))]
    let _ = (name, kind, value, labels);
}

pub fn debug(message: &str) {
    log(message, LogLevel::Debug);
}
pub fn info(message: &str) {
    log(message, LogLevel::Info);
}
pub fn counter(name: &str, value: f64, labels: &serde_json::Value) {
    emit_metric(name, MetricKind::Counter, value, labels);
}
pub fn histogram(name: &str, value: f64, labels: &serde_json::Value) {
    emit_metric(name, MetricKind::Histogram, value, labels);
}
pub fn gauge(name: &str, value: f64, labels: &serde_json::Value) {
    emit_metric(name, MetricKind::Gauge, value, labels);
}

pub fn decode_host_call_result(bytes: &[u8]) -> Result<Vec<u8>, HostCallError> {
    use pbv1::host_call_result::Result as ResultArm;
    use prost::Message;

    validate_host_call_result_wire(bytes)?;
    let result = pbv1::HostCallResult::decode(bytes)
        .map_err(|err| HostCallError::Protocol(format!("decode HostCallResult: {err}")))?;
    match result.result {
        Some(ResultArm::Value(value)) => Ok(value),
        Some(ResultArm::Error(error)) => {
            if !matches!(error.code, 1..=6) {
                return Err(HostCallError::Protocol(format!(
                    "HostError code {} is not classified by this SDK",
                    error.code
                )));
            }
            Err(HostCallError::Refused(error))
        }
        None => Err(HostCallError::Protocol(
            "HostCallResult requires a result arm".to_owned(),
        )),
    }
}

fn validate_host_call_result_wire(mut bytes: &[u8]) -> Result<(), HostCallError> {
    let mut arms = 0u8;
    while !bytes.is_empty() {
        let (key, key_len) = read_varint(bytes)?;
        bytes = &bytes[key_len..];
        let field = key >> 3;
        let wire = key & 7;
        if !matches!(field, 1 | 2) || wire != 2 {
            return Err(HostCallError::Protocol(
                "HostCallResult carries an unknown or malformed result arm".to_owned(),
            ));
        }
        arms = arms.saturating_add(1);
        let (length, length_len) = read_varint(bytes)?;
        bytes = &bytes[length_len..];
        let length = usize::try_from(length).map_err(|_| {
            HostCallError::Protocol("HostCallResult field length overflows usize".to_owned())
        })?;
        if length > bytes.len() {
            return Err(HostCallError::Protocol(
                "HostCallResult field is truncated".to_owned(),
            ));
        }
        bytes = &bytes[length..];
    }
    if arms != 1 {
        return Err(HostCallError::Protocol(
            "HostCallResult must carry exactly one result arm".to_owned(),
        ));
    }
    Ok(())
}

fn read_varint(bytes: &[u8]) -> Result<(u64, usize), HostCallError> {
    let mut value = 0u64;
    for (index, byte) in bytes.iter().copied().take(10).enumerate() {
        if index == 9 && byte > 1 {
            break;
        }
        value |= u64::from(byte & 0x7f) << (index * 7);
        if byte & 0x80 == 0 {
            return Ok((value, index + 1));
        }
    }
    Err(HostCallError::Protocol(
        "HostCallResult contains an invalid varint".to_owned(),
    ))
}

/// Invokes a host command with protobuf arguments and decodes the ABI-v1
/// `HostCallResult` envelope. Empty successful values remain distinguishable
/// from typed refusals; callers must branch on [`HostCallError::Refused`]'s
/// code, never its diagnostic message.
pub fn host_call<M: prost::Message>(
    command: &str,
    arguments: &M,
) -> Result<Vec<u8>, HostCallError> {
    let arguments = arguments.encode_to_vec();
    let contract = capability_contract::command(command)
        .ok_or_else(|| HostCallError::Protocol(format!("unknown host command {command}")))?;
    if contract.arguments == "import" || contract.arguments.ends_with("-json") {
        return Err(HostCallError::Protocol(format!(
            "{command} is not a protobuf host-call command"
        )));
    }
    validate_host_arguments(command, &arguments)?;
    call_encoded(command, &arguments)
}

fn call_encoded(command: &str, arguments: &[u8]) -> Result<Vec<u8>, HostCallError> {
    #[cfg(not(target_arch = "wasm32"))]
    if let Some(result) =
        NATIVE_HOST.with(|slot| slot.borrow().as_ref().map(|f| f(command, arguments)))
    {
        return result.and_then(|frame| decode_host_call_result(&frame));
    }
    #[cfg(not(target_arch = "wasm32"))]
    return Err(HostCallError::Protocol(
        "no native host transport installed".into(),
    ));
    #[cfg(target_arch = "wasm32")]
    {
        let packed = unsafe {
            raw_host_call(
                command.as_ptr() as u32,
                command.len() as u32,
                arguments.as_ptr() as u32,
                arguments.len() as u32,
            )
        };
        if packed == 0 {
            return Err(HostCallError::Protocol(
                "host_call returned no HostCallResult frame".to_owned(),
            ));
        }
        let ptr = (packed >> 32) as u32;
        let len = packed as u32;
        // SAFETY: raw_host_call returns a host-owned result buffer valid until
        // dealloc below; copy it before releasing that allocation.
        let bytes = unsafe { __input(ptr, len) }.to_vec();
        dealloc(ptr, len);
        decode_host_call_result(&bytes)
    }
}

/// Calls one catalogued JSON extension through the same ABI transport as typed
/// protobuf helpers. Both arguments and JSON results use strict object shape.
pub fn extension_call(command: &str, arguments: &[u8]) -> Result<serde_json::Value, HostCallError> {
    let contract = capability_contract::command(command)
        .ok_or_else(|| HostCallError::Protocol(format!("unknown host command {command}")))?;
    if !contract.arguments.ends_with("-json") {
        return Err(HostCallError::Protocol(format!(
            "{command} is not a JSON extension"
        )));
    }
    let input = strict_json(arguments).map_err(|e| {
        HostCallError::Protocol(format!("{command} arguments are invalid JSON: {e}"))
    })?;
    if !input.is_object() {
        return Err(HostCallError::Protocol(format!(
            "{command} arguments must be a JSON object"
        )));
    }
    let value = call_encoded(command, arguments)?;
    if contract.result == "empty" && value.is_empty() {
        return Ok(serde_json::Value::Null);
    }
    let output = strict_json(&value)
        .map_err(|e| HostCallError::Protocol(format!("{command} result is invalid JSON: {e}")))?;
    if !output.is_object() {
        return Err(HostCallError::Protocol(format!(
            "{command} result must be a JSON object"
        )));
    }
    Ok(output)
}

fn validate_host_arguments(command: &str, bytes: &[u8]) -> Result<(), HostCallError> {
    macro_rules! decode {
        ($ty:ty) => {
            <$ty>::decode(bytes)
                .map_err(|e| HostCallError::Protocol(format!("decode {command} arguments: {e}")))?
        };
    }
    let bad = |message: &str| HostCallError::Protocol(format!("{command} arguments: {message}"));
    match command {
        "env.meta_get" => {
            if decode!(pbv1::MetaGetArgs).key.is_empty() {
                return Err(bad("key is required"));
            }
        }
        "env.meta_set" => {
            if decode!(pbv1::MetaSetArgs).key.is_empty() {
                return Err(bad("key is required"));
            }
        }
        "env.state_get" | "env.state_get_versioned" => {
            if decode!(pbv1::StateGetArgs).key.is_empty() {
                return Err(bad("key is required"));
            }
        }
        "env.state_set" => {
            if decode!(pbv1::StateSetArgs).key.is_empty() {
                return Err(bad("key is required"));
            }
        }
        "env.state_delete" => {
            if decode!(pbv1::StateDeleteArgs).key.is_empty() {
                return Err(bad("key is required"));
            }
        }
        "env.cache_get" | "env.shared_cache_get" => {
            if decode!(pbv1::CacheGetArgs).key.is_empty() {
                return Err(bad("key is required"));
            }
        }
        "env.cache_set" | "env.shared_cache_set" => {
            let a = decode!(pbv1::CacheSetArgs);
            if a.key.is_empty() {
                return Err(bad("key is required"));
            }
            if a.ttl_ms == Some(0) {
                return Err(bad("ttl_ms must be positive"));
            }
        }
        "env.cache_delete" | "env.shared_cache_delete" => {
            if decode!(pbv1::CacheDeleteArgs).key.is_empty() {
                return Err(bad("key is required"));
            }
        }
        "env.state_compare_and_set" => {
            if decode!(pbv1::StateCompareAndSetArgs).key.is_empty() {
                return Err(bad("key is required"));
            }
        }
        "env.state_compare_and_delete" => {
            let a = decode!(pbv1::StateCompareAndDeleteArgs);
            if a.key.is_empty() || a.expected_version.is_empty() {
                return Err(bad("key and expected_version are required"));
            }
        }
        "env.state_scan" => {
            if !(1..=256).contains(&decode!(pbv1::StateScanArgs).limit) {
                return Err(bad("limit must be 1..256"));
            }
        }
        "env.model_complete" => {
            let a = decode!(pbv1::ModelCompleteArgs);
            if !valid_resource_name(&a.service)
                || a.messages.is_empty()
                || a.max_tokens == Some(0)
                || a.temperature.is_some_and(|v| !v.is_finite())
            {
                return Err(bad("request violates the model contract"));
            }
        }
        "env.http_request" => {
            let a = decode!(pbv1::OutboundHttpRequestArgs);
            let method_ok =
                !a.method.is_empty() && a.method.bytes().all(|b| b.is_ascii_uppercase());
            let path_ok =
                a.path.starts_with('/') && !a.path.starts_with("//") && !a.path.contains('#');
            if !valid_resource_name(&a.endpoint)
                || !method_ok
                || !path_ok
                || !valid_http_headers(&a.headers)
            {
                return Err(bad("request violates the HTTP contract"));
            }
        }
        "env.model_pricing"
            if !valid_resource_name(&decode!(pbv1::ModelPricingGetArgs).resource) =>
        {
            return Err(bad("resource is invalid"));
        }
        "env.cache_policy"
            if !valid_resource_name(&decode!(pbv1::PromptCachePolicyGetArgs).resource) =>
        {
            return Err(bad("resource is invalid"));
        }
        _ => {}
    }
    Ok(())
}

fn valid_http_headers(headers: &[pbv1::HttpHeader]) -> bool {
    let mut seen = std::collections::HashSet::new();
    headers.iter().all(|header| {
        let name = header.name.to_ascii_lowercase();
        !header.name.is_empty()
            && header.name.is_ascii()
            && header
                .name
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || b"!#$%&'*+-.^_`|~".contains(&b))
            && seen.insert(name)
            && header
                .values
                .iter()
                .all(|value| !value.contains(['\r', '\n']))
    })
}

fn decode_host_value<R: prost::Message + Default>(
    bytes: &[u8],
    protobuf_name: &str,
) -> Result<R, HostCallError> {
    __validate_wire_message(bytes, protobuf_name).map_err(HostCallError::Protocol)?;
    R::decode(bytes).map_err(|e| HostCallError::Protocol(format!("decode {protobuf_name}: {e}")))
}

/// Resolves one operator-bound credential slot. Treat the returned bytes as a
/// secret and do not place them in logs or diagnostic errors.
pub fn get_credential(slot: &str) -> Result<Vec<u8>, HostCallError> {
    host_call(
        "env.credential_get",
        &pbv1::CredentialGetArgs {
            slot: slot.to_owned(),
        },
    )
}

pub fn meta_get(key: &str) -> Result<Option<String>, HostCallError> {
    match host_call("env.meta_get", &pbv1::MetaGetArgs { key: key.into() }) {
        Err(HostCallError::Refused(e)) if e.code == pbv1::ErrorCode::NotFound as i32 => Ok(None),
        Err(e) => Err(e),
        Ok(v) => match v {
            v if v.is_empty() => Ok(Some(String::new())),
            v => String::from_utf8(v)
                .map(Some)
                .map_err(|_| HostCallError::Protocol("meta value is not UTF-8".into())),
        },
    }
}
pub fn meta_set(key: &str, value: &str) -> Result<(), HostCallError> {
    host_call(
        "env.meta_set",
        &pbv1::MetaSetArgs {
            key: key.into(),
            value: value.into(),
        },
    )
    .map(|_| ())
}
pub fn meta_append(block_index: i32, fragment: &[u8]) -> Result<Vec<u8>, HostCallError> {
    host_call(
        "env.meta_append",
        &pbv1::MetaAppendArgs {
            block_index,
            fragment: fragment.to_vec(),
        },
    )
}
pub fn state_get(key: &str) -> Result<Option<String>, HostCallError> {
    match host_call("env.state_get", &pbv1::StateGetArgs { key: key.into() }) {
        Err(HostCallError::Refused(e)) if e.code == pbv1::ErrorCode::NotFound as i32 => Ok(None),
        Err(e) => Err(e),
        Ok(v) => match v {
            v if v.is_empty() => Ok(Some(String::new())),
            v => String::from_utf8(v)
                .map(Some)
                .map_err(|_| HostCallError::Protocol("state value is not UTF-8".into())),
        },
    }
}
pub fn state_set(key: &str, value: &str) -> Result<(), HostCallError> {
    host_call(
        "env.state_set",
        &pbv1::StateSetArgs {
            key: key.into(),
            value: value.into(),
        },
    )
    .map(|_| ())
}
pub fn state_delete(key: &str) -> Result<(), HostCallError> {
    host_call(
        "env.state_delete",
        &pbv1::StateDeleteArgs { key: key.into() },
    )
    .map(|_| ())
}
pub fn cache_get(key: &str) -> Result<Option<String>, HostCallError> {
    cache_get_named("env.cache_get", key)
}
pub fn cache_set(key: &str, value: &str, ttl_ms: Option<u64>) -> Result<(), HostCallError> {
    host_call(
        "env.cache_set",
        &pbv1::CacheSetArgs {
            key: key.into(),
            value: value.into(),
            ttl_ms,
        },
    )
    .map(|_| ())
}
pub fn cache_delete(key: &str) -> Result<(), HostCallError> {
    host_call(
        "env.cache_delete",
        &pbv1::CacheDeleteArgs { key: key.into() },
    )
    .map(|_| ())
}
pub fn state_keys() -> Result<Vec<String>, HostCallError> {
    let b = host_call("env.state_keys", &EmptyArgs {})?;
    let value = strict_json(&b)
        .map_err(|e| HostCallError::Protocol(format!("state keys are invalid JSON: {e}")))?;
    let array = value
        .as_array()
        .ok_or_else(|| HostCallError::Protocol("state keys must be a JSON array".into()))?;
    array
        .iter()
        .map(|item| {
            item.as_str()
                .map(str::to_owned)
                .ok_or_else(|| HostCallError::Protocol("state key must be a string".into()))
        })
        .collect()
}
pub fn state_get_versioned(key: &str) -> Result<Option<pbv1::StateValue>, HostCallError> {
    match host_call(
        "env.state_get_versioned",
        &pbv1::StateGetArgs { key: key.into() },
    ) {
        Ok(b) => Ok(Some(decode_host_value(&b, ".torana.v1.StateValue")?)),
        Err(HostCallError::Refused(e)) if e.code == pbv1::ErrorCode::NotFound as i32 => Ok(None),
        Err(e) => Err(e),
    }
}
pub fn state_compare_and_set(
    key: &str,
    value: &str,
    expected_version: Option<&str>,
) -> Result<pbv1::StateMutationResult, HostCallError> {
    let b = host_call(
        "env.state_compare_and_set",
        &pbv1::StateCompareAndSetArgs {
            key: key.into(),
            value: value.into(),
            expected_version: expected_version.map(str::to_owned),
        },
    )?;
    let result: pbv1::StateMutationResult =
        decode_host_value(&b, ".torana.v1.StateMutationResult")?;
    if result.applied && result.version.is_none() {
        return Err(HostCallError::Protocol(
            "applied state mutation requires version".into(),
        ));
    }
    Ok(result)
}
pub fn state_compare_and_delete(
    key: &str,
    expected_version: &str,
) -> Result<pbv1::StateMutationResult, HostCallError> {
    let b = host_call(
        "env.state_compare_and_delete",
        &pbv1::StateCompareAndDeleteArgs {
            key: key.into(),
            expected_version: expected_version.into(),
        },
    )?;
    decode_host_value(&b, ".torana.v1.StateMutationResult")
}
pub fn state_scan(
    prefix: &str,
    cursor: &str,
    limit: u32,
) -> Result<pbv1::StateScanResult, HostCallError> {
    if !(1..=256).contains(&limit) {
        return Err(HostCallError::Protocol(
            "state scan limit must be 1..256".into(),
        ));
    }
    let b = host_call(
        "env.state_scan",
        &pbv1::StateScanArgs {
            prefix: prefix.into(),
            cursor: cursor.into(),
            limit,
        },
    )?;
    let result: pbv1::StateScanResult = decode_host_value(&b, ".torana.v1.StateScanResult")?;
    if result.entries.iter().any(|entry| {
        entry.key.is_empty() || entry.value.as_ref().is_none_or(|v| v.version.is_empty())
    }) {
        return Err(HostCallError::Protocol(
            "state scan returned an invalid entry".into(),
        ));
    }
    Ok(result)
}
pub fn shared_cache_get(key: &str) -> Result<Option<String>, HostCallError> {
    cache_get_named("env.shared_cache_get", key)
}
pub fn shared_cache_set(key: &str, value: &str, ttl_ms: Option<u64>) -> Result<(), HostCallError> {
    host_call(
        "env.shared_cache_set",
        &pbv1::CacheSetArgs {
            key: key.into(),
            value: value.into(),
            ttl_ms,
        },
    )
    .map(|_| ())
}
pub fn shared_cache_delete(key: &str) -> Result<(), HostCallError> {
    host_call(
        "env.shared_cache_delete",
        &pbv1::CacheDeleteArgs { key: key.into() },
    )
    .map(|_| ())
}
fn cache_get_named(command: &str, key: &str) -> Result<Option<String>, HostCallError> {
    match host_call(command, &pbv1::CacheGetArgs { key: key.into() }) {
        Err(HostCallError::Refused(e)) if e.code == pbv1::ErrorCode::NotFound as i32 => Ok(None),
        Err(e) => Err(e),
        Ok(v) if v.is_empty() => Ok(Some(String::new())),
        Ok(v) => String::from_utf8(v)
            .map(Some)
            .map_err(|_| HostCallError::Protocol("cache value is not UTF-8".into())),
    }
}
pub fn respond_request(response: pbv1::SyntheticResponse) -> Result<(), HostCallError> {
    validate_synthetic_response(&response).map_err(HostCallError::Protocol)?;
    host_call("env.respond_request", &response).map(|_| ())
}

fn validate_synthetic_response(response: &pbv1::SyntheticResponse) -> Result<(), String> {
    let message = response
        .message
        .as_ref()
        .ok_or("synthetic response requires a message")?;
    if message.blocks.is_empty() {
        return Err("synthetic response requires at least one block".into());
    }
    let mut has_tools = false;
    for block in &message.blocks {
        let Some(kind) = block.kind.as_ref() else {
            return Err("synthetic response contains an empty block".into());
        };
        match kind {
            pbv1::response_block::Kind::Text(_) => {}
            pbv1::response_block::Kind::ToolCall(call) => {
                has_tools = true;
                if !call.id.is_empty() || !call.signature.is_empty() {
                    return Err("synthetic tool IDs/signatures are host-owned".into());
                }
                if call.name.is_empty() {
                    return Err("synthetic tool name is required".into());
                }
                if call.arguments_json.is_empty() {
                    return Err("synthetic tool arguments are required".into());
                }
                json_object(&call.arguments_json, "tool arguments_json")?;
            }
        }
    }
    let expected = if has_tools { "tool_calls" } else { "stop" };
    if response.finish_reason != expected {
        return Err("synthetic finish_reason disagrees with tool-call presence".into());
    }
    Ok(())
}
pub fn respond_text(content: &str) -> Result<(), HostCallError> {
    respond_request(pbv1::SyntheticResponse {
        message: Some(pbv1::ResponseMessage {
            blocks: vec![pbv1::ResponseBlock {
                kind: Some(pbv1::response_block::Kind::Text(pbv1::ResponseTextBlock {
                    text: content.into(),
                })),
            }],
        }),
        finish_reason: "stop".into(),
    })
}
pub fn get_resource_info(kind: &str, name: &str) -> Result<pbv1::ResourceInfo, HostCallError> {
    let b = host_call(
        "env.resource_info",
        &pbv1::ResourceInfoArgs {
            kind: kind.into(),
            name: name.into(),
        },
    )?;
    decode_host_value(&b, ".torana.v1.ResourceInfo")
}
pub fn block_request(status: i32, code: &str, message: &str) -> Result<(), HostCallError> {
    host_call(
        "env.block_request",
        &pbv1::BlockRequestArgs {
            status,
            code: code.into(),
            message: message.into(),
        },
    )
    .map(|_| ())
}
pub fn route_request(provider: &str, model: &str) -> Result<(), HostCallError> {
    host_call(
        "env.route_request",
        &pbv1::RouteRequestArgs {
            provider: provider.into(),
            model: model.into(),
        },
    )
    .map(|_| ())
}
pub fn set_identity(identity: &str) -> Result<(), HostCallError> {
    host_call(
        "env.set_identity",
        &pbv1::SetIdentityArgs {
            identity: identity.into(),
        },
    )
    .map(|_| ())
}

#[derive(Clone)]
pub struct AssembledToolCall {
    pub index: i32,
    pub id: String,
    pub name: String,
    pub signature: String,
    pub invocation_kind: pbv1::ToolInvocationKind,
    pub arguments: String,
    pub input_text: Option<String>,
}
pub struct StreamFeed {
    pub emit: Vec<pbv1::StreamEvent>,
    pub suppress: bool,
    pub complete: Option<AssembledToolCall>,
}
pub struct StreamAssembler;
impl Default for StreamAssembler {
    fn default() -> Self {
        Self::new()
    }
}
impl StreamAssembler {
    pub fn new() -> Self {
        Self
    }
    pub fn feed(&self, event: pbv1::StreamEvent) -> Result<StreamFeed, HostCallError> {
        let original = event.clone();
        use pbv1::stream_event::Event;
        match event.event {
            Some(Event::ContentBlockStart(s)) => {
                if let Some(pbv1::content_block_start::Block::ToolCall(r)) = s.block {
                    let mut h = r.encode_to_vec();
                    let mut f = (h.len() as u32).to_be_bytes().to_vec();
                    f.append(&mut h);
                    meta_append(s.index, &f)?;
                    return Ok(StreamFeed {
                        emit: vec![],
                        suppress: true,
                        complete: None,
                    });
                }
            }
            Some(Event::ToolCallDelta(d)) => {
                let f = d.input_text_delta.unwrap_or(d.arguments_delta).into_bytes();
                if !f.is_empty() {
                    meta_append(d.index, &f)?;
                }
                return Ok(StreamFeed {
                    emit: vec![],
                    suppress: true,
                    complete: None,
                });
            }
            Some(Event::ContentBlockStop(s)) => {
                let b = meta_append(s.index, &[])?;
                if b.len() < 4 {
                    return Ok(StreamFeed {
                        emit: vec![],
                        suppress: true,
                        complete: None,
                    });
                }
                let n = u32::from_be_bytes(b[..4].try_into().unwrap()) as usize;
                if n + 4 > b.len() {
                    return Err(HostCallError::Protocol("corrupt tool frame".into()));
                }
                let r = pbv1::ToolCallRef::decode(&b[4..4 + n])
                    .map_err(|e| HostCallError::Protocol(e.to_string()))?;
                let args = String::from_utf8(b[4 + n..].to_vec())
                    .map_err(|_| HostCallError::Protocol("tool arguments not UTF-8".into()))?;
                let input = (r.invocation_kind == pbv1::ToolInvocationKind::Freeform as i32)
                    .then_some(args.clone());
                let arguments = if input.is_none() { args } else { String::new() };
                return Ok(StreamFeed {
                    emit: vec![],
                    suppress: true,
                    complete: Some(AssembledToolCall {
                        index: s.index,
                        id: r.id,
                        name: r.name,
                        signature: r.signature,
                        invocation_kind: pbv1::ToolInvocationKind::try_from(r.invocation_kind)
                            .unwrap_or(pbv1::ToolInvocationKind::Function),
                        arguments,
                        input_text: input,
                    }),
                });
            }
            _ => {}
        }
        Ok(StreamFeed {
            emit: vec![original],
            suppress: false,
            complete: None,
        })
    }
}

pub struct StreamHandler<F> {
    assembler: StreamAssembler,
    on_tool: F,
}
impl<F, E> StreamHandler<F>
where
    F: Fn(AssembledToolCall) -> Result<Option<String>, E>,
    E: std::fmt::Display,
{
    pub fn new(on_tool: F) -> Self {
        Self {
            assembler: StreamAssembler::new(),
            on_tool,
        }
    }
    pub fn handle(&self, event: pbv1::StreamEvent) -> Result<Vec<pbv1::StreamEvent>, String> {
        let fed = self.assembler.feed(event).map_err(|e| e.to_string())?;
        if let Some(call) = fed.complete {
            let original = if call.invocation_kind == pbv1::ToolInvocationKind::Freeform {
                call.input_text.clone().unwrap_or_default()
            } else {
                call.arguments.clone()
            };
            let payload = (self.on_tool)(call.clone())
                .map_err(|e| e.to_string())?
                .unwrap_or(original.clone());
            let sig = if payload == original {
                call.signature
            } else {
                String::new()
            };
            let r = pbv1::ToolCallRef {
                id: call.id,
                name: call.name,
                signature: sig,
                invocation_kind: call.invocation_kind as i32,
            };
            let mut out = Vec::new();
            out.push(pbv1::StreamEvent {
                event: Some(pbv1::stream_event::Event::ContentBlockStart(
                    pbv1::ContentBlockStart {
                        index: call.index,
                        block: Some(pbv1::content_block_start::Block::ToolCall(r)),
                    },
                )),
            });
            let mut d = pbv1::ToolCallDelta {
                index: call.index,
                ..Default::default()
            };
            if call.invocation_kind == pbv1::ToolInvocationKind::Freeform {
                d.input_text_delta = Some(payload)
            } else {
                d.arguments_delta = payload
            }
            out.push(pbv1::StreamEvent {
                event: Some(pbv1::stream_event::Event::ToolCallDelta(d)),
            });
            out.push(pbv1::StreamEvent {
                event: Some(pbv1::stream_event::Event::ContentBlockStop(
                    pbv1::ContentBlockStop { index: call.index },
                )),
            });
            Ok(out)
        } else if fed.suppress {
            Ok(Vec::new())
        } else {
            Ok(fed.emit)
        }
    }
}

#[derive(Clone, PartialEq, prost::Message)]
struct EmptyArgs {}
pub fn plugin_config<T: serde::de::DeserializeOwned>() -> Result<T, HostCallError> {
    let bytes = host_call("env.plugin_config", &EmptyArgs {})?;
    let value = strict_json(&bytes)
        .map_err(|e| HostCallError::Protocol(format!("plugin config is invalid JSON: {e}")))?;
    if !value.is_object() {
        return Err(HostCallError::Protocol(
            "plugin config must be a JSON object".into(),
        ));
    }
    serde_json::from_value(value)
        .map_err(|e| HostCallError::Protocol(format!("plugin config has wrong shape: {e}")))
}

pub fn append_file(path: &str, data: &[u8]) -> Result<(), HostCallError> {
    host_call(
        "env.file_append",
        &pbv1::FileAppendArgs {
            path: path.to_owned(),
            data: data.to_vec(),
        },
    )
    .map(|_| ())
}

pub fn read_file(path: &str) -> Result<Vec<u8>, HostCallError> {
    host_call(
        "env.file_read",
        &pbv1::FileReadArgs {
            path: path.to_owned(),
        },
    )
}

pub fn write_file(path: &str, data: &[u8]) -> Result<(), HostCallError> {
    host_call(
        "env.file_write",
        &pbv1::FileWriteArgs {
            path: path.to_owned(),
            data: data.to_vec(),
        },
    )
    .map(|_| ())
}

pub fn list_files(prefix: &str) -> Result<Vec<String>, HostCallError> {
    let value = host_call(
        "env.file_list",
        &pbv1::FileListArgs {
            prefix: prefix.to_owned(),
        },
    )?;
    decode_host_value::<pbv1::FileListResult>(&value, ".torana.v1.FileListResult")
        .map(|result| result.paths)
}

pub fn delete_file(path: &str) -> Result<(), HostCallError> {
    host_call(
        "env.file_delete",
        &pbv1::FileDeleteArgs {
            path: path.to_owned(),
        },
    )
    .map(|_| ())
}

pub fn http_request(
    request: &pbv1::OutboundHttpRequestArgs,
) -> Result<pbv1::OutboundHttpResponse, HostCallError> {
    let value = host_call("env.http_request", request)?;
    let response: pbv1::OutboundHttpResponse =
        decode_host_value(&value, ".torana.v1.OutboundHTTPResponse")?;
    if !(100..=599).contains(&response.status) {
        return Err(HostCallError::Protocol(format!(
            "OutboundHTTPResponse status {} is invalid",
            response.status
        )));
    }
    if !valid_http_headers(&response.headers) {
        return Err(HostCallError::Protocol(
            "OutboundHTTPResponse headers are invalid".into(),
        ));
    }
    Ok(response)
}

/// Invokes one operator-bound model-service slot. Provider, URL, model,
/// credentials, and hard budgets are owned by the binding, not by the plugin.
pub fn model_complete(
    request: &pbv1::ModelCompleteArgs,
) -> Result<pbv1::ModelCompleteResult, HostCallError> {
    if !valid_resource_name(&request.service)
        || request.messages.is_empty()
        || request
            .messages
            .iter()
            .any(|message| message.role.is_empty())
        || request.max_tokens == Some(0)
        || request.temperature.is_some_and(|value| !value.is_finite())
    {
        return Err(HostCallError::Protocol(
            "ModelCompleteArgs violates the SDK contract".to_owned(),
        ));
    }
    let value = host_call("env.model_complete", request)?;
    let result: pbv1::ModelCompleteResult =
        decode_host_value(&value, ".torana.v1.ModelCompleteResult")?;
    if result.message.as_ref().is_none_or(|message| {
        message.blocks.is_empty() || message.blocks.iter().any(|block| block.kind.is_none())
    }) || result.usage.as_ref().is_some_and(|usage| {
        usage.input_tokens < 0
            || usage.output_tokens < 0
            || usage.cache_read_tokens < 0
            || usage.cache_write_tokens < 0
    }) {
        return Err(HostCallError::Protocol(
            "ModelCompleteResult violates the SDK contract".into(),
        ));
    }
    Ok(result)
}

pub fn model_complete_text(request: &pbv1::ModelCompleteArgs) -> Result<String, HostCallError> {
    let result = model_complete(request)?;
    let message = result
        .message
        .ok_or_else(|| HostCallError::Protocol("model result has no message".into()))?;
    let mut text = String::new();
    for block in message.blocks {
        match block.kind {
            Some(pbv1::response_block::Kind::Text(t)) => text.push_str(&t.text),
            Some(_) => {
                return Err(HostCallError::Protocol(
                    "model result contains non-text block".into(),
                ))
            }
            None => {
                return Err(HostCallError::Protocol(
                    "model result contains empty block".into(),
                ))
            }
        }
    }
    Ok(text)
}

/// Resolves one operator-bound pricing resource. `None` means an unknown rate;
/// `Some(0.0)` is an explicitly free rate.
pub fn get_model_pricing(resource: &str) -> Result<pbv1::ModelPricing, HostCallError> {
    if !valid_resource_name(resource) {
        return Err(HostCallError::Protocol(
            "model pricing resource is required".to_owned(),
        ));
    }
    let value = host_call(
        "env.model_pricing",
        &pbv1::ModelPricingGetArgs {
            resource: resource.to_owned(),
        },
    )?;
    let pricing: pbv1::ModelPricing = decode_host_value(&value, ".torana.v1.ModelPricing")?;
    for rate in [
        pricing.input_usd_per_mtok,
        pricing.output_usd_per_mtok,
        pricing.cache_read_usd_per_mtok,
        pricing.cache_write_usd_per_mtok,
    ] {
        if rate.is_some_and(|value| !value.is_finite() || value < 0.0) {
            return Err(HostCallError::Protocol(
                "ModelPricing rates must be finite and non-negative".to_owned(),
            ));
        }
    }
    Ok(pricing)
}

/// Resolves one operator-bound prompt-cache-policy resource. The plugin names
/// only its declared slot; provider, model, routing, prices, and lifetime
/// semantics are owned by the binding.
pub fn get_prompt_cache_policy(resource: &str) -> Result<pbv1::PromptCachePolicy, HostCallError> {
    if !valid_resource_name(resource) {
        return Err(HostCallError::Protocol(
            "prompt cache policy resource is required".to_owned(),
        ));
    }
    let value = host_call(
        "env.cache_policy",
        &pbv1::PromptCachePolicyGetArgs {
            resource: resource.to_owned(),
        },
    )?;
    let policy: pbv1::PromptCachePolicy =
        decode_host_value(&value, ".torana.v1.PromptCachePolicy")?;
    validate_prompt_cache_policy(&policy)?;
    Ok(policy)
}

fn valid_resource_name(value: &str) -> bool {
    let bytes = value.as_bytes();
    !bytes.is_empty()
        && bytes.len() <= 64
        && bytes.iter().enumerate().all(|(index, byte)| {
            byte.is_ascii_alphanumeric() || (index > 0 && matches!(byte, b'.' | b'_' | b'-'))
        })
}

fn validate_prompt_cache_policy(policy: &pbv1::PromptCachePolicy) -> Result<(), HostCallError> {
    for rate in [
        policy.cache_read_usd_per_mtok,
        policy.cache_write_usd_per_mtok,
    ] {
        if rate.is_some_and(|value| !value.is_finite() || value < 0.0) {
            return Err(HostCallError::Protocol(
                "PromptCachePolicy rates must be finite and non-negative".to_owned(),
            ));
        }
    }
    if policy.cache_read_usd_per_mtok.is_none()
        && policy.cache_write_usd_per_mtok.is_none()
        && policy.tiers.is_empty()
    {
        return Err(HostCallError::Protocol(
            "PromptCachePolicy carries no prices or tiers".to_owned(),
        ));
    }
    if policy.warm_interval_seconds == Some(0) {
        return Err(HostCallError::Protocol(
            "PromptCachePolicy warm_interval_seconds must be positive when present".to_owned(),
        ));
    }
    if policy.warm_interval_seconds.is_some() && !policy.refresh_on_read {
        return Err(HostCallError::Protocol(
            "PromptCachePolicy warm_interval_seconds requires refresh_on_read".to_owned(),
        ));
    }
    let mut ttls = std::collections::BTreeSet::new();
    for tier in &policy.tiers {
        if tier.ttl_seconds == 0
            || tier
                .write_multiplier
                .is_some_and(|value| !value.is_finite() || value < 0.0)
            || !ttls.insert(tier.ttl_seconds)
        {
            return Err(HostCallError::Protocol(
                "PromptCachePolicy contains an invalid tier".to_owned(),
            ));
        }
        let marker: serde_json::Value = strict_json(&tier.marker_json).map_err(|_| {
            HostCallError::Protocol("PromptCachePolicy marker_json is invalid JSON".to_owned())
        })?;
        if !marker.is_object() {
            return Err(HostCallError::Protocol(
                "PromptCachePolicy marker_json must be an object".to_owned(),
            ));
        }
    }
    if let Some(interval) = policy.warm_interval_seconds {
        let shortest = policy
            .tiers
            .iter()
            .map(|tier| tier.ttl_seconds)
            .min()
            .unwrap_or(0);
        if shortest == 0 || interval >= shortest {
            return Err(HostCallError::Protocol(
                "PromptCachePolicy warm_interval_seconds must be below the shortest tier"
                    .to_owned(),
            ));
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    // CI has run `cargo test` on this crate all along, against zero tests — so
    // the green tick meant only that the crate compiled. These cover the ABI
    // plumbing that every hook goes through.
    //
    // They exercise the pointer-level halves rather than the extern "C"
    // wrappers, because off wasm32 a real pointer does not fit in the u32 the
    // ABI uses and `ptr as u32` would truncate it.

    #[test]
    fn alloc_dealloc_round_trip() {
        for size in [1usize, 7, 64, 4096] {
            let p = alloc_bytes(size);
            assert!(!p.is_null(), "alloc_bytes({size}) returned null");

            // Write and read back every byte. This does NOT prove the layout
            // was large enough — it cannot, since both use the same `size` —
            // but it does exercise the alloc/write/read/free cycle, and a
            // dealloc whose layout disagreed with the allocation corrupts the
            // allocator, which the repeated cycles below surface.
            unsafe { ptr::write_bytes(p, 0xAB, size) };
            let seen = unsafe { slice::from_raw_parts(p, size) };
            assert!(seen.iter().all(|&b| b == 0xAB));

            // Frees with a layout built the same way it was allocated. This is
            // the pairing the Vec::with_capacity version only got right by
            // coincidence.
            dealloc_bytes(p, size);
        }
    }

    // Repeated alloc/free at varying sizes. A dealloc whose Layout disagreed
    // with the allocation corrupts allocator bookkeeping, and the corruption
    // shows up on a later allocation rather than at the bad free — so the loop
    // is the point.
    #[test]
    fn repeated_alloc_free_cycles_do_not_corrupt_the_allocator() {
        for round in 0..64 {
            let size = 1 + (round * 37) % 4096;
            let p = alloc_bytes(size);
            assert!(!p.is_null());
            unsafe { ptr::write_bytes(p, round as u8, size) };
            let seen = unsafe { slice::from_raw_parts(p, size) };
            assert!(
                seen.iter().all(|&b| b == round as u8),
                "round {round} readback"
            );
            dealloc_bytes(p, size);
        }
    }

    #[test]
    fn empty_payload_is_the_only_pass_through() {
        // The ABI reserves 0 for a deliberate no-op. Anything non-empty must
        // produce a real buffer; allocation failure traps instead of landing
        // here as a 0 the host would read as "plugin made no change".
        assert_eq!(__result(&[]), 0);
        assert_ne!(__result(&[0u8]), 0);
    }

    #[test]
    fn zero_size_allocation_is_a_null_pointer() {
        // Zero is the ABI's "no buffer" signal, and a zero-sized layout would
        // be undefined behaviour to pass to the allocator.
        assert!(alloc_bytes(0).is_null());
        assert_eq!(alloc(0), 0);
    }

    #[test]
    fn dealloc_tolerates_null_and_zero() {
        dealloc_bytes(ptr::null_mut(), 16);
        dealloc_bytes(ptr::null_mut(), 0);
        dealloc(0, 16);
    }

    #[test]
    fn pack_puts_the_pointer_high_and_the_length_low() {
        assert_eq!(pack(0x0000_1234, 0x0000_0056), 0x0000_1234_0000_0056);
        // The full u32 range must survive: a sign-extending cast would corrupt
        // any pointer above 2GiB, which is reachable in a 4GiB wasm memory.
        assert_eq!(pack(0xFFFF_FFFF, 0xFFFF_FFFF), 0xFFFF_FFFF_FFFF_FFFF);
        assert_eq!(pack(0x8000_0000, 1), 0x8000_0000_0000_0001);
    }

    #[test]
    fn empty_result_is_pass_through() {
        // Zero is reserved by the ABI for "the plugin made no change".
        assert_eq!(__result(&[]), 0);
    }

    #[test]
    fn result_copies_the_payload_verbatim() {
        // Every byte value, so a copy that dropped or mangled one is visible.
        let payload: Vec<u8> = (0u8..=255).collect();

        let (p, len) = copy_to_owned_buffer(&payload);
        assert!(!p.is_null());
        assert_eq!(len, payload.len());

        let copied = unsafe { slice::from_raw_parts(p, len) };
        assert_eq!(copied, payload.as_slice());

        dealloc_bytes(p, len);
    }

    #[test]
    fn copying_nothing_yields_no_buffer() {
        let (p, len) = copy_to_owned_buffer(&[]);
        assert!(p.is_null());
        assert_eq!(len, 0);
    }

    #[test]
    fn input_of_an_empty_or_null_buffer_is_an_empty_slice() {
        // SAFETY: zero-length/null inputs are explicitly accepted without a
        // dereference by the ABI helper.
        unsafe {
            assert!(__input(0, 0).is_empty());
            assert!(__input(0, 10).is_empty());
            assert!(__input(10, 0).is_empty());
        }
    }

    fn pass(_input: pbv1::HookInput) -> Result<Option<pbv1::HookResult>, String> {
        Ok(None)
    }

    fn replace(input: pbv1::HookInput) -> Result<Option<pbv1::HookResult>, String> {
        let request = match input.payload {
            Some(pbv1::hook_input::Payload::ChatRequest(request)) => request,
            _ => return Err("wrong payload".to_owned()),
        };
        Ok(Some(pbv1::HookResult {
            action: Some(pbv1::hook_result::Action::ReplaceRequest(request)),
        }))
    }

    fn wrong_action(_input: pbv1::HookInput) -> Result<Option<pbv1::HookResult>, String> {
        Ok(Some(pbv1::HookResult {
            action: Some(pbv1::hook_result::Action::Suppress(pbv1::Suppress {})),
        }))
    }

    fn before_request_input() -> Vec<u8> {
        use prost::Message;
        pbv1::HookInput {
            contract_revision: 1,
            request_id: 7,
            execution: None,
            payload: Some(pbv1::hook_input::Payload::ChatRequest(pbv1::ChatRequest {
                model: "rust-v1".to_owned(),
                ..Default::default()
            })),
        }
        .encode_to_vec()
    }

    #[test]
    fn v1_pass_through_is_exactly_empty_output() {
        assert_eq!(
            __dispatch_v1(&before_request_input(), HOOK_BEFORE_REQUEST, pass).unwrap(),
            Vec::<u8>::new()
        );
    }

    #[test]
    fn v1_replacement_round_trips_through_the_single_action_envelope() {
        use prost::Message;
        let output = __dispatch_v1(&before_request_input(), HOOK_BEFORE_REQUEST, replace).unwrap();
        let result = pbv1::HookResult::decode(output.as_slice()).unwrap();
        let Some(pbv1::hook_result::Action::ReplaceRequest(request)) = result.action else {
            panic!("expected replace_request")
        };
        assert_eq!(request.model, "rust-v1");
    }

    #[test]
    fn v1_dispatch_rejects_undeclared_and_mismatched_hooks() {
        let err = __dispatch_v1(&before_request_input(), HOOK_ON_TICK, pass).unwrap_err();
        assert!(err.contains("unregistered"), "{err}");
        let err =
            __dispatch_v1(&before_request_input(), HOOK_BEFORE_REQUEST, wrong_action).unwrap_err();
        assert!(err.contains("does not match"), "{err}");
    }

    #[test]
    fn v1_host_call_result_keeps_empty_success_distinct_from_refusal() {
        use prost::Message;
        let success = pbv1::HostCallResult {
            result: Some(pbv1::host_call_result::Result::Value(Vec::new())),
        };
        assert_eq!(
            decode_host_call_result(&success.encode_to_vec()).unwrap(),
            Vec::<u8>::new()
        );

        let refusal = pbv1::HostCallResult {
            result: Some(pbv1::host_call_result::Result::Error(pbv1::HostError {
                code: pbv1::ErrorCode::PermissionDenied as i32,
                message: "diagnostic only".to_owned(),
            })),
        };
        assert_eq!(
            decode_host_call_result(&refusal.encode_to_vec()),
            Err(HostCallError::Refused(pbv1::HostError {
                code: pbv1::ErrorCode::PermissionDenied as i32,
                message: "diagnostic only".to_owned(),
            }))
        );
    }

    #[test]
    fn v1_host_call_result_rejects_empty_and_unclassified_frames() {
        use prost::Message;
        let err = decode_host_call_result(&[]).unwrap_err();
        assert!(matches!(err, HostCallError::Protocol(_)));

        for code in [pbv1::ErrorCode::Unspecified as i32, 99] {
            let frame = pbv1::HostCallResult {
                result: Some(pbv1::host_call_result::Result::Error(pbv1::HostError {
                    code,
                    message: String::new(),
                })),
            }
            .encode_to_vec();
            assert!(matches!(
                decode_host_call_result(&frame),
                Err(HostCallError::Protocol(_))
            ));
        }

        // Unknown top-level field 3, length-delimited empty payload. Prost
        // normally discards it, but the v1 contract treats it as a future
        // result arm this build cannot classify.
        assert!(matches!(
            decode_host_call_result(&[0x1a, 0x00]),
            Err(HostCallError::Protocol(_))
        ));
    }

    fn native_value(value: Vec<u8>) -> Result<Vec<u8>, HostCallError> {
        Ok(pbv1::HostCallResult {
            result: Some(pbv1::host_call_result::Result::Value(value)),
        }
        .encode_to_vec())
    }

    #[test]
    fn option_getters_distinguish_not_found_from_empty() {
        use std::cell::Cell;
        let calls = Cell::new(0);
        let _guard = install_native_host(move |_, _| {
            let call = calls.get();
            calls.set(call + 1);
            if call == 0 {
                Ok(pbv1::HostCallResult {
                    result: Some(pbv1::host_call_result::Result::Error(pbv1::HostError {
                        code: pbv1::ErrorCode::NotFound as i32,
                        message: "missing".into(),
                    })),
                }
                .encode_to_vec())
            } else {
                native_value(Vec::new())
            }
        });
        assert_eq!(meta_get("key").unwrap(), None);
        assert_eq!(meta_get("key").unwrap(), Some(String::new()));
    }

    #[test]
    fn state_keys_uses_empty_args_and_host_json_shape() {
        let _guard = install_native_host(|command, arguments| {
            assert_eq!(command, "env.state_keys");
            assert!(
                arguments.is_empty(),
                "Empty protobuf arguments must encode empty"
            );
            native_value(br#"["a","empty"]"#.to_vec())
        });
        assert_eq!(state_keys().unwrap(), vec!["a", "empty"]);
    }

    #[test]
    fn plugin_config_requires_an_object() {
        let _guard = install_native_host(|command, _| {
            assert_eq!(command, "env.plugin_config");
            native_value(br#"[]"#.to_vec())
        });
        assert!(
            matches!(plugin_config::<serde_json::Value>(), Err(HostCallError::Protocol(message)) if message.contains("JSON object"))
        );
    }

    #[test]
    fn invalid_arguments_do_not_reach_the_transport() {
        let _guard = install_native_host(|_, _| panic!("invalid call reached transport"));
        assert!(matches!(cache_get(""), Err(HostCallError::Protocol(_))));
        let invalid = pbv1::OutboundHttpRequestArgs {
            endpoint: "api".into(),
            method: "post".into(),
            path: "/".into(),
            headers: vec![],
            body: vec![],
            timeout_ms: 0,
        };
        assert!(matches!(
            http_request(&invalid),
            Err(HostCallError::Protocol(_))
        ));
    }

    #[test]
    fn typed_results_reject_unknown_nested_fields() {
        // status=200, headers[0] is an HTTPHeader containing unknown field 99.
        let malformed = vec![0x08, 0xc8, 0x01, 0x12, 0x03, 0x9a, 0x06, 0x00];
        let _guard = install_native_host(move |_, _| native_value(malformed.clone()));
        let request = pbv1::OutboundHttpRequestArgs {
            endpoint: "api".into(),
            method: "GET".into(),
            path: "/".into(),
            headers: vec![],
            body: vec![],
            timeout_ms: 0,
        };
        assert!(
            matches!(http_request(&request), Err(HostCallError::Protocol(message)) if message.contains("unknown field"))
        );
    }

    #[test]
    fn catalog_rejects_unknown_commands_and_drives_json_extensions() {
        let _guard = install_native_host(|command, arguments| {
            assert_eq!(command, "verify_virtual_key");
            assert_eq!(arguments, br#"{"key":"v"}"#);
            native_value(br#"{"valid":true}"#.to_vec())
        });
        assert!(
            matches!(host_call("env.future", &EmptyArgs {}), Err(HostCallError::Protocol(message)) if message.contains("unknown host command"))
        );
        assert_eq!(
            extension_call("verify_virtual_key", br#"{"key":"v"}"#).unwrap()["valid"],
            true
        );
        assert!(matches!(
            extension_call("verify_virtual_key", b"[]"),
            Err(HostCallError::Protocol(_))
        ));
    }

    #[test]
    fn resource_names_match_the_typed_host_contract() {
        for valid in ["cache", "request-cache", "a.b_c", "A1"] {
            assert!(valid_resource_name(valid), "{valid}");
        }
        for invalid in ["", "-cache", "../cache", "cache/name", "caché"] {
            assert!(!valid_resource_name(invalid), "{invalid}");
        }
        assert!(!valid_resource_name(&"a".repeat(65)));
    }

    #[test]
    fn prompt_cache_policy_validation_rejects_unusable_results() {
        let valid = pbv1::PromptCachePolicy {
            cache_read_usd_per_mtok: Some(0.1),
            cache_write_usd_per_mtok: Some(1.25),
            refresh_on_read: true,
            tiers: vec![pbv1::PromptCacheTier {
                ttl_seconds: 300,
                write_multiplier: Some(1.25),
                marker_json: br#"{"type":"ephemeral"}"#.to_vec(),
            }],
            warm_interval_seconds: Some(240),
        };
        validate_prompt_cache_policy(&valid).unwrap();

        let mut invalid = valid.clone();
        invalid.tiers[0].marker_json = b"[]".to_vec();
        assert!(validate_prompt_cache_policy(&invalid).is_err());
        let mut invalid = valid.clone();
        invalid.tiers[0].ttl_seconds = 0;
        assert!(validate_prompt_cache_policy(&invalid).is_err());
        let mut invalid = valid;
        invalid.warm_interval_seconds = Some(300);
        assert!(validate_prompt_cache_policy(&invalid).is_err());
    }
}
