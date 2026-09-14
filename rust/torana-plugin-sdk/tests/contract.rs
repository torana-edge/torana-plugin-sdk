use torana_plugin_sdk::{
    __dispatch_v1, pbv1, prost::Message, HOOK_AFTER_RESPONSE, HOOK_BEFORE_REQUEST,
    HOOK_ON_HTTP_REQUEST,
};

fn pass(_: pbv1::HookInput) -> Result<Option<pbv1::HookResult>, String> {
    Ok(None)
}

#[test]
fn rejects_missing_after_response_and_invalid_http_result() {
    let input = pbv1::HookInput {
        payload: Some(pbv1::hook_input::Payload::AfterResponse(
            pbv1::AfterResponse {
                response: None,
                mutable: true,
            },
        )),
        ..Default::default()
    }
    .encode_to_vec();
    assert!(__dispatch_v1(&input, HOOK_AFTER_RESPONSE, pass).is_err());
    fn bad(_: pbv1::HookInput) -> Result<Option<pbv1::HookResult>, String> {
        Ok(Some(pbv1::HookResult {
            action: Some(pbv1::hook_result::Action::ServeHttp(pbv1::HttpResponse {
                status: 0,
                ..Default::default()
            })),
        }))
    }
    let input = pbv1::HookInput {
        payload: Some(pbv1::hook_input::Payload::HttpRequest(Default::default())),
        ..Default::default()
    }
    .encode_to_vec();
    assert!(__dispatch_v1(&input, HOOK_ON_HTTP_REQUEST, bad).is_err());
}

#[test]
fn rejects_unknown_nested_request_fields_before_prost_decode() {
    let request = vec![0xa2, 0x06, 0x01, b'x']; // field 100, bytes "x"
    let mut input = vec![0x22, request.len() as u8];
    input.extend_from_slice(&request);
    assert!(__dispatch_v1(&input, HOOK_BEFORE_REQUEST, pass).is_err());
}

#[test]
fn rejects_duplicate_host_result_arms() {
    // value="a", value="b"; protobuf's last-arm-wins behavior is unsafe.
    assert!(matches!(
        torana_plugin_sdk::decode_host_call_result(&[0x0a, 1, b'a', 0x0a, 1, b'b']),
        Err(torana_plugin_sdk::HostCallError::Protocol(_))
    ));
}

#[test]
fn strict_json_rejects_duplicate_top_level_and_nested_keys() {
    let req = |json: &[u8]| pbv1::ChatRequest {
        provider_extensions_json: json.to_vec(),
        ..Default::default()
    };
    assert!(torana_plugin_sdk::replace_request(req(br#"{"a":1,"a":2}"#)).is_err());
    assert!(torana_plugin_sdk::replace_request(req(br#"{"x":{"a":1,"\u0061":2}}"#)).is_err());
}

#[test]
fn strict_json_rejects_lone_surrogates() {
    let req = pbv1::ChatRequest {
        provider_extensions_json: br#"{"x":"\uD800"}"#.to_vec(),
        ..Default::default()
    };
    assert!(torana_plugin_sdk::replace_request(req).is_err());
}

#[test]
fn safety_settings_must_be_array() {
    let req = pbv1::ChatRequest {
        safety_settings_json: br#"{}"#.to_vec(),
        ..Default::default()
    };
    assert!(torana_plugin_sdk::replace_request(req).is_err());
}

#[test]
fn message_requires_at_least_one_block() {
    let req = pbv1::ChatRequest {
        messages: vec![pbv1::Message {
            role: "user".into(),
            blocks: vec![],
        }],
        ..Default::default()
    };
    assert!(torana_plugin_sdk::replace_request(req).is_err());
}

#[test]
fn deep_unknown_fields_are_rejected_before_decode() {
    // HookInput -> ChatRequest -> Message -> RequestBlock, unknown field 100.
    let block = vec![0xa2, 0x06, 0x01, b'x'];
    let message = vec![
        0x12,
        1,
        b'u',
        0x1a,
        block.len() as u8,
        0x1a,
        block.len() as u8,
    ];
    let mut raw = vec![0x22, message.len() as u8];
    raw.extend_from_slice(&message);
    assert!(__dispatch_v1(&raw, HOOK_BEFORE_REQUEST, pass).is_err());
}

#[test]
fn native_injection_drives_public_helpers() {
    use std::sync::{Arc, Mutex};
    let seen = Arc::new(Mutex::new(Vec::new()));
    let copy = seen.clone();
    let _guard = torana_plugin_sdk::install_native_host(move |cmd, args| {
        copy.lock().unwrap().push((cmd.to_owned(), args.to_vec()));
        Ok(pbv1::HostCallResult {
            result: Some(pbv1::host_call_result::Result::Value(b"ok".to_vec())),
        }
        .encode_to_vec())
    });
    assert_eq!(torana_plugin_sdk::meta_set("k", "v").unwrap(), ());
    assert_eq!(
        torana_plugin_sdk::cache_set("k", "v", Some(1000)).unwrap(),
        ()
    );
    assert_eq!(
        seen.lock()
            .unwrap()
            .iter()
            .map(|x| x.0.as_str())
            .collect::<Vec<_>>(),
        vec!["env.meta_set", "env.cache_set"]
    );
}

#[test]
fn native_injection_preserves_typed_refusal() {
    let _guard = torana_plugin_sdk::install_native_host(|_, _| {
        Ok(pbv1::HostCallResult {
            result: Some(pbv1::host_call_result::Result::Error(pbv1::HostError {
                code: pbv1::ErrorCode::PermissionDenied as i32,
                message: "denied".into(),
            })),
        }
        .encode_to_vec())
    });
    assert!(matches!(
        torana_plugin_sdk::state_set("k", "v"),
        Err(torana_plugin_sdk::HostCallError::Refused(_))
    ));
}

#[test]
fn typed_plugin_dispatch_invokes_family_callback() {
    struct P;
    impl torana_plugin_sdk::Plugin for P {
        const SUPPORTED_HOOKS: u32 = HOOK_BEFORE_REQUEST;
        fn before_request(
            r: pbv1::ChatRequest,
        ) -> Result<torana_plugin_sdk::RequestResult, String> {
            Ok(torana_plugin_sdk::RequestResult::replace(r)?)
        }
    }
    let input = pbv1::HookInput {
        payload: Some(pbv1::hook_input::Payload::ChatRequest(pbv1::ChatRequest {
            model: "typed".into(),
            ..Default::default()
        })),
        ..Default::default()
    };
    let result = torana_plugin_sdk::__plugin_dispatch::<P>(input).unwrap();
    assert!(matches!(
        result.unwrap().action,
        Some(pbv1::hook_result::Action::ReplaceRequest(_))
    ));
}

#[test]
fn shared_wire_vectors_match_rust_validation() {
    let raw = include_str!("../../../pb/v1/wire_vectors.json");
    let vectors: Vec<serde_json::Value> = serde_json::from_str(raw).unwrap();
    for v in vectors {
        let name = v["name"].as_str().unwrap();
        let ty = v["message_type"].as_str().unwrap();
        let hex = v["wire_hex"].as_str().unwrap();
        let expected = v["valid"].as_bool().unwrap();
        let bytes = (0..hex.len())
            .step_by(2)
            .map(|i| u8::from_str_radix(&hex[i..i + 2], 16).unwrap())
            .collect::<Vec<_>>();
        let actual = if ty == "torana.v1.HostCallResult" {
            torana_plugin_sdk::decode_host_call_result(&bytes).is_ok()
        } else if ty == "torana.v1.OutputFormat" {
            match pbv1::OutputFormat::decode(bytes.as_slice()) {
                Ok(f) => {
                    matches!(f.mode, 0 | 1)
                        && (f.schema_json.is_empty()
                            || serde_json::from_slice::<serde_json::Value>(&f.schema_json)
                                .map(|v| v.is_object())
                                .unwrap_or(false))
                }
                Err(_) => false,
            }
        } else if ty == "torana.v1.HookInput" {
            torana_plugin_sdk::__validate_wire_message(&bytes, ".torana.v1.HookInput").is_ok() && pbv1::HookInput::decode(bytes.as_slice()).map(|i| !matches!(i.payload, Some(pbv1::hook_input::Payload::AfterResponse(ref r)) if r.response.is_none())).unwrap_or(false)
        } else {
            torana_plugin_sdk::__validate_wire_message(&bytes, &format!(".{ty}")).is_ok()
        };
        assert_eq!(actual, expected, "{name}");
    }
}

#[test]
fn stream_assembler_assembles_function_and_freeform_calls() {
    use std::sync::{Arc, Mutex};
    let buffers = Arc::new(Mutex::new(std::collections::HashMap::<i32, Vec<u8>>::new()));
    let state = buffers.clone();
    let _guard = torana_plugin_sdk::install_native_host(move |cmd, args| {
        if cmd != "env.meta_append" {
            return Ok(pbv1::HostCallResult {
                result: Some(pbv1::host_call_result::Result::Value(vec![])),
            }
            .encode_to_vec());
        }
        let a = pbv1::MetaAppendArgs::decode(args).unwrap();
        let mut m = state.lock().unwrap();
        let v = m.entry(a.block_index).or_default();
        if a.fragment.is_empty() {
            return Ok(pbv1::HostCallResult {
                result: Some(pbv1::host_call_result::Result::Value(v.clone())),
            }
            .encode_to_vec());
        }
        v.extend(a.fragment);
        Ok(pbv1::HostCallResult {
            result: Some(pbv1::host_call_result::Result::Value(vec![])),
        }
        .encode_to_vec())
    });
    let asm = torana_plugin_sdk::StreamAssembler::new();
    let ref0 = pbv1::ToolCallRef {
        id: "f".into(),
        name: "fn".into(),
        invocation_kind: pbv1::ToolInvocationKind::Function as i32,
        ..Default::default()
    };
    let start = pbv1::StreamEvent {
        event: Some(pbv1::stream_event::Event::ContentBlockStart(
            pbv1::ContentBlockStart {
                index: 0,
                block: Some(pbv1::content_block_start::Block::ToolCall(ref0)),
            },
        )),
    };
    assert!(asm.feed(start).unwrap().suppress);
    let d = pbv1::StreamEvent {
        event: Some(pbv1::stream_event::Event::ToolCallDelta(
            pbv1::ToolCallDelta {
                index: 0,
                arguments_delta: "{}".into(),
                ..Default::default()
            },
        )),
    };
    assert!(asm.feed(d).unwrap().suppress);
    let stop = pbv1::StreamEvent {
        event: Some(pbv1::stream_event::Event::ContentBlockStop(
            pbv1::ContentBlockStop { index: 0 },
        )),
    };
    let out = asm.feed(stop).unwrap();
    assert_eq!(out.complete.unwrap().arguments, "{}");
}

#[test]
fn stream_assembler_propagates_meta_refusal() {
    let _guard = torana_plugin_sdk::install_native_host(|_, _| {
        Ok(pbv1::HostCallResult {
            result: Some(pbv1::host_call_result::Result::Error(pbv1::HostError {
                code: pbv1::ErrorCode::PermissionDenied as i32,
                message: "denied".into(),
            })),
        }
        .encode_to_vec())
    });
    let asm = torana_plugin_sdk::StreamAssembler::new();
    let ev = pbv1::StreamEvent {
        event: Some(pbv1::stream_event::Event::ContentBlockStart(
            pbv1::ContentBlockStart {
                index: 1,
                block: Some(pbv1::content_block_start::Block::ToolCall(
                    Default::default(),
                )),
            },
        )),
    };
    assert!(matches!(
        asm.feed(ev),
        Err(torana_plugin_sdk::HostCallError::Refused(_))
    ));
}

#[test]
fn stream_handler_preserves_or_clears_signature_and_supports_freeform() {
    use std::sync::{Arc, Mutex};
    let buffers = Arc::new(Mutex::new(std::collections::HashMap::<i32, Vec<u8>>::new()));
    let state = buffers.clone();
    let _guard = torana_plugin_sdk::install_native_host(move |cmd, args| {
        let a = pbv1::MetaAppendArgs::decode(args).unwrap();
        let mut m = state.lock().unwrap();
        let v = m.entry(a.block_index).or_default();
        if a.fragment.is_empty() {
            return Ok(pbv1::HostCallResult {
                result: Some(pbv1::host_call_result::Result::Value(v.clone())),
            }
            .encode_to_vec());
        }
        v.extend(a.fragment);
        Ok(pbv1::HostCallResult {
            result: Some(pbv1::host_call_result::Result::Value(vec![])),
        }
        .encode_to_vec())
    });
    fn events(kind: pbv1::ToolInvocationKind, sig: &str) -> Vec<pbv1::StreamEvent> {
        vec![
            pbv1::StreamEvent {
                event: Some(pbv1::stream_event::Event::ContentBlockStart(
                    pbv1::ContentBlockStart {
                        index: 2,
                        block: Some(pbv1::content_block_start::Block::ToolCall(
                            pbv1::ToolCallRef {
                                id: "id".into(),
                                name: "name".into(),
                                signature: sig.into(),
                                invocation_kind: kind as i32,
                            },
                        )),
                    },
                )),
            },
            pbv1::StreamEvent {
                event: Some(pbv1::stream_event::Event::ToolCallDelta(
                    pbv1::ToolCallDelta {
                        index: 2,
                        arguments_delta: "{}".into(),
                        ..Default::default()
                    },
                )),
            },
            pbv1::StreamEvent {
                event: Some(pbv1::stream_event::Event::ContentBlockStop(
                    pbv1::ContentBlockStop { index: 2 },
                )),
            },
        ]
    }
    let unchanged = torana_plugin_sdk::StreamHandler::new(|_| Ok::<_, String>(None));
    for e in events(pbv1::ToolInvocationKind::Function, "sig") {
        let _ = unchanged.handle(e).unwrap();
    }
    let edited =
        torana_plugin_sdk::StreamHandler::new(|_| Ok::<_, String>(Some("{\"x\":1}".into())));
    let mut out = Vec::new();
    for e in events(pbv1::ToolInvocationKind::Function, "sig") {
        out = edited.handle(e).unwrap();
    }
    let Some(pbv1::stream_event::Event::ContentBlockStart(s)) = out[0].event.as_ref() else {
        panic!()
    };
    let Some(pbv1::content_block_start::Block::ToolCall(r)) = s.block.as_ref() else {
        panic!()
    };
    assert!(r.signature.is_empty());
    let free =
        torana_plugin_sdk::StreamHandler::new(|call: torana_plugin_sdk::AssembledToolCall| {
            Ok::<_, String>(call.input_text)
        });
    let mut out = Vec::new();
    for e in events(pbv1::ToolInvocationKind::Freeform, "") {
        out = free.handle(e).unwrap();
    }
    assert_eq!(out.len(), 3);
}

#[test]
fn plugin_config_is_strict_and_typed() {
    #[derive(serde::Deserialize, Debug, PartialEq)]
    struct C {
        enabled: bool,
    }
    let _guard = torana_plugin_sdk::install_native_host(|_, _| {
        Ok(pbv1::HostCallResult {
            result: Some(pbv1::host_call_result::Result::Value(
                br#"{"enabled":true}"#.to_vec(),
            )),
        }
        .encode_to_vec())
    });
    assert_eq!(
        torana_plugin_sdk::plugin_config::<C>().unwrap(),
        C { enabled: true }
    );
    drop(_guard);
    let _guard = torana_plugin_sdk::install_native_host(|_, _| {
        Ok(pbv1::HostCallResult {
            result: Some(pbv1::host_call_result::Result::Value(
                br#"{"x":1,"x":2}"#.to_vec(),
            )),
        }
        .encode_to_vec())
    });
    assert!(torana_plugin_sdk::plugin_config::<C>().is_err());
}
