use torana_plugin_sdk::{export_before_request, log, pb, LOG_INFO};

fn observe(request: &mut pb::ChatRequest) -> bool {
    log(&format!("received request for {}", request.model), LOG_INFO);
    false // pass-through
}

export_before_request!(observe);

fn main() {}
