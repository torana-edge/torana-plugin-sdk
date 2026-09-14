//! Rust bindings and WASI Preview 1 trampolines for Torana Plugin ABI v1.
//!
//! The SDK deliberately exposes only the host calls granted by Torana. A
//! plugin cannot gain a capability by importing a function that the operator
//! did not grant.

use core::alloc::Layout;
use core::{ptr, slice};

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

/// A deterministic host implementation for native unit tests. Production
/// guests use the WASI import path; tests can inject a command router without
/// linking a WASM host.
pub struct NativeHost<F> {
    call: F,
}

impl<F> NativeHost<F>
where
    F: Fn(&str, &[u8]) -> Result<Vec<u8>, HostCallError>,
{
    pub fn new(call: F) -> Self {
        Self { call }
    }

    pub fn call<M: prost::Message>(
        &self,
        command: &str,
        arguments: &M,
    ) -> Result<Vec<u8>, HostCallError> {
        (self.call)(command, &arguments.encode_to_vec())
    }

    pub fn call_value<M: prost::Message, R: prost::Message + Default>(
        &self,
        command: &str,
        arguments: &M,
    ) -> Result<R, HostCallError> {
        let bytes = self.call(command, arguments)?;
        R::decode(bytes.as_slice())
            .map_err(|e| HostCallError::Protocol(format!("decode {command} result: {e}")))
    }

    pub fn meta_get(&self, key: &str) -> Result<Vec<u8>, HostCallError> {
        self.call_result("env.meta_get", &pbv1::MetaGetArgs { key: key.into() })
    }
    pub fn meta_set(&self, key: &str, value: &str) -> Result<(), HostCallError> {
        self.call_result(
            "env.meta_set",
            &pbv1::MetaSetArgs {
                key: key.into(),
                value: value.into(),
            },
        )
        .map(|_| ())
    }
    pub fn cache_get(&self, key: &str) -> Result<Vec<u8>, HostCallError> {
        self.call_result("env.cache_get", &pbv1::CacheGetArgs { key: key.into() })
    }
    pub fn cache_set(&self, key: &str, value: &str) -> Result<(), HostCallError> {
        self.call_result(
            "env.cache_set",
            &pbv1::CacheSetArgs {
                key: key.into(),
                value: value.into(),
                ttl_ms: None,
            },
        )
        .map(|_| ())
    }
    pub fn state_get(&self, key: &str) -> Result<Vec<u8>, HostCallError> {
        self.call_result("env.state_get", &pbv1::StateGetArgs { key: key.into() })
    }
    pub fn state_set(&self, key: &str, value: &str) -> Result<(), HostCallError> {
        self.call_result(
            "env.state_set",
            &pbv1::StateSetArgs {
                key: key.into(),
                value: value.into(),
            },
        )
        .map(|_| ())
    }
    pub fn state_delete(&self, key: &str) -> Result<(), HostCallError> {
        self.call_result(
            "env.state_delete",
            &pbv1::StateDeleteArgs { key: key.into() },
        )
        .map(|_| ())
    }
    fn call_result<M: prost::Message>(
        &self,
        command: &str,
        args: &M,
    ) -> Result<Vec<u8>, HostCallError> {
        let frame = self.call(command, args)?;
        decode_host_call_result(&frame)
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
pub fn replace_request(request: pbv1::ChatRequest) -> Result<pbv1::HookResult, String> {
    validate_chat_request(&request)?;
    Ok(pbv1::HookResult {
        action: Some(pbv1::hook_result::Action::ReplaceRequest(request)),
    })
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

fn json_object(raw: &[u8], field: &str) -> Result<(), String> {
    if raw.is_empty() {
        return Ok(());
    }
    let value: serde_json::Value = serde_json::from_slice(raw)
        .map_err(|e| format!("torana sdk: {field} is invalid JSON: {e}"))?;
    if !value.is_object() {
        return Err(format!("torana sdk: {field} must be a JSON object"));
    }
    Ok(())
}
fn json_array(raw: &[u8], field: &str) -> Result<(), String> {
    if raw.is_empty() {
        return Ok(());
    }
    let value: serde_json::Value = serde_json::from_slice(raw)
        .map_err(|e| format!("torana sdk: {field} is invalid JSON: {e}"))?;
    if !value.is_array() {
        return Err(format!("torana sdk: {field} must be a JSON array"));
    }
    Ok(())
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
    validate_wire_message(raw, ".torana.v1.HookInput")?;
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
        Some(pbv1::hook_input::Payload::StreamEvent(_))
        | Some(pbv1::hook_input::Payload::TickRequest(_)) => Ok(()),
        None => Err("torana sdk: HookInput requires a payload".into()),
    }
}

fn validate_wire_message(mut bytes: &[u8], name: &str) -> Result<(), String> {
    use prost::Message;
    use prost_types::{field_descriptor_proto::Type, FileDescriptorSet};
    let set = FileDescriptorSet::decode(
        include_bytes!(concat!(env!("OUT_DIR"), "/torana.descriptor.bin")).as_slice(),
    )
    .map_err(|e| format!("torana sdk: descriptor decode: {e}"))?;
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
        let expected = match field.r#type.and_then(Type::from_i32) {
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
                validate_wire_message(&bytes[p..p + len], ty)?;
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

pub const LOG_DEBUG: i32 = 0;
pub const LOG_INFO: i32 = 1;

#[link(wasm_import_module = "env")]
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
pub fn log(message: &str, level: i32) {
    if message.is_empty() {
        return;
    }
    unsafe { host_log(level, message.as_ptr() as u32, message.len() as u32) }
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

pub const METRIC_COUNTER: i32 = 0;
pub const METRIC_HISTOGRAM: i32 = 1;
pub const METRIC_GAUGE: i32 = 2;

pub fn emit_metric(name: &str, kind: i32, value: f64, labels: &serde_json::Value) {
    let labels = labels.to_string();
    unsafe {
        host_emit_metric(
            kind,
            name.as_ptr() as u32,
            name.len() as u32,
            value,
            labels.as_ptr() as u32,
            labels.len() as u32,
        )
    }
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
    use prost::Message;
    let value = host_call(
        "env.file_list",
        &pbv1::FileListArgs {
            prefix: prefix.to_owned(),
        },
    )?;
    pbv1::FileListResult::decode(value.as_slice())
        .map(|result| result.paths)
        .map_err(|error| HostCallError::Protocol(format!("decode FileListResult: {error}")))
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
    use prost::Message;
    let value = host_call("env.http_request", request)?;
    let response = pbv1::OutboundHttpResponse::decode(value.as_slice()).map_err(|error| {
        HostCallError::Protocol(format!("decode OutboundHTTPResponse: {error}"))
    })?;
    if !(100..=599).contains(&response.status) {
        return Err(HostCallError::Protocol(format!(
            "OutboundHTTPResponse status {} is invalid",
            response.status
        )));
    }
    Ok(response)
}

/// Invokes one operator-bound model-service slot. Provider, URL, model,
/// credentials, and hard budgets are owned by the binding, not by the plugin.
pub fn model_complete(
    request: &pbv1::ModelCompleteArgs,
) -> Result<pbv1::ModelCompleteResult, HostCallError> {
    use prost::Message;
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
    pbv1::ModelCompleteResult::decode(value.as_slice())
        .map_err(|error| HostCallError::Protocol(format!("decode ModelCompleteResult: {error}")))
}

/// Resolves one operator-bound pricing resource. `None` means an unknown rate;
/// `Some(0.0)` is an explicitly free rate.
pub fn get_model_pricing(resource: &str) -> Result<pbv1::ModelPricing, HostCallError> {
    use prost::Message;
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
    let pricing = pbv1::ModelPricing::decode(value.as_slice())
        .map_err(|error| HostCallError::Protocol(format!("decode ModelPricing: {error}")))?;
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
    use prost::Message;
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
    let policy = pbv1::PromptCachePolicy::decode(value.as_slice())
        .map_err(|error| HostCallError::Protocol(format!("decode PromptCachePolicy: {error}")))?;
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
        let marker: serde_json::Value =
            serde_json::from_slice(&tier.marker_json).map_err(|_| {
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
