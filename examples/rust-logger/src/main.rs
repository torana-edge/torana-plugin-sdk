use torana_plugin_sdk::{
    export_plugin_v1, log, pbv1, Plugin, RequestResult, HOOK_BEFORE_REQUEST, LOG_INFO,
};
struct Logger;
impl Plugin for Logger {
    const SUPPORTED_HOOKS: u32 = HOOK_BEFORE_REQUEST;
    fn before_request(request: pbv1::ChatRequest) -> Result<RequestResult, String> {
        log(&format!("received request for {}", request.model), LOG_INFO);
        Ok(RequestResult::pass())
    }
}
export_plugin_v1!(Logger);

fn main() {}
