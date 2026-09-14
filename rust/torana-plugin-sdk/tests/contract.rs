use torana_plugin_sdk::{__dispatch_v1, pbv1, prost::Message, HOOK_AFTER_RESPONSE, HOOK_BEFORE_REQUEST, HOOK_ON_HTTP_REQUEST};

fn pass(_: pbv1::HookInput) -> Result<Option<pbv1::HookResult>, String> { Ok(None) }

#[test]
fn rejects_missing_after_response_and_invalid_http_result() {
    let input = pbv1::HookInput { payload: Some(pbv1::hook_input::Payload::AfterResponse(pbv1::AfterResponse { response: None, mutable: true })), ..Default::default() }.encode_to_vec();
    assert!(__dispatch_v1(&input, HOOK_AFTER_RESPONSE, pass).is_err());
    fn bad(_: pbv1::HookInput) -> Result<Option<pbv1::HookResult>, String> {
        Ok(Some(pbv1::HookResult { action: Some(pbv1::hook_result::Action::ServeHttp(pbv1::HttpResponse { status: 0, ..Default::default() })) }))
    }
    let input = pbv1::HookInput { payload: Some(pbv1::hook_input::Payload::HttpRequest(Default::default())), ..Default::default() }.encode_to_vec();
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
