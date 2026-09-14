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
