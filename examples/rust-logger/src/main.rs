use torana_plugin_sdk::{
    export_plugin_v1, log, pbv1, HOOK_BEFORE_REQUEST, LOG_INFO,
};

fn dispatch(input: pbv1::HookInput) -> Result<Option<pbv1::HookResult>, String> {
    let request = match input.payload {
        Some(pbv1::hook_input::Payload::ChatRequest(request)) => request,
        _ => return Err("rust-logger received an undeclared hook".to_owned()),
    };
    log(&format!("received request for {}", request.model), LOG_INFO);
    Ok(None) // pass-through
}

export_plugin_v1!(HOOK_BEFORE_REQUEST, dispatch);

fn main() {}
