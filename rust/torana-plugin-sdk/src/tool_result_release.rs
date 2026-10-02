use crate::{extension_call, HostCallError};

/// Permission for one host-resolved result. Only the operator can approve it.
#[derive(Debug, serde::Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ToolResultReleaseInfo {
    pub reference: String,
    pub approved: bool,
}

/// Read or register a withheld result in the current before-request input.
/// Position is not authority: the host derives scope and content itself.
pub fn tool_result_release(
    message: usize,
    block: usize,
    register: bool,
) -> Result<ToolResultReleaseInfo, HostCallError> {
    let args = serde_json::json!({"message": message, "block": block, "register": register});
    let value = extension_call("torana_tool_result_release", args.to_string().as_bytes())?;
    let result: ToolResultReleaseInfo = serde_json::from_value(value)
        .map_err(|_| HostCallError::Protocol("invalid tool-result release response".into()))?;
    if result.approved && result.reference.is_empty() {
        return Err(HostCallError::Protocol(
            "approved result has no reference".into(),
        ));
    }
    Ok(result)
}

#[cfg(test)]
mod tests {
    use super::*;
    use prost::Message;
    #[test]
    fn register_is_not_approval_and_unknown_fields_are_refused() {
        let _host = crate::install_native_host(|command, input| {
            assert_eq!(command, "torana_tool_result_release");
            assert_eq!(
                serde_json::from_slice::<serde_json::Value>(input).unwrap()["register"],
                true
            );
            Ok(crate::pbv1::HostCallResult {
                result: Some(crate::pbv1::host_call_result::Result::Value(
                    br#"{"reference":"tr_test","approved":false}"#.to_vec(),
                )),
            }
            .encode_to_vec())
        });
        let info = tool_result_release(1, 2, true).unwrap();
        assert!(!info.approved);
        assert_eq!(info.reference, "tr_test");
        assert!(serde_json::from_str::<ToolResultReleaseInfo>(
            r#"{"reference":"x","approved":true,"content":"secret"}"#
        )
        .is_err());
    }
}
