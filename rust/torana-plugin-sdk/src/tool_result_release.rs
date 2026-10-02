use crate::{extension_call, HostCallError};

/// Permission for one host-resolved result. Only the operator can approve it.
#[derive(Debug, serde::Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ToolResultReleaseInfo {
    pub reference: String,
    pub approved: bool,
}

#[derive(Debug, serde::Serialize, serde::Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ToolResultReleaseReason {
    pub kind: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub findings: Vec<ToolResultReleaseFinding>,
}

#[derive(Debug, serde::Serialize, serde::Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ToolResultReleaseFinding {
    pub r#type: String,
    pub line: u32,
}

impl ToolResultReleaseReason {
    pub fn validate(&self) -> Result<(), HostCallError> {
        if self.kind == "scan_failure" && self.findings.is_empty() {
            return Ok(());
        }
        if self.kind != "findings" || self.findings.is_empty() || self.findings.len() > 20 {
            return Err(HostCallError::Protocol(
                "invalid result review reason".into(),
            ));
        }
        for finding in &self.findings {
            if finding.line > 1_000_000_000
                || !matches!(
                    finding.r#type.as_str(),
                    "email"
                        | "phone"
                        | "address"
                        | "government_id"
                        | "us_ssn"
                        | "credit_card"
                        | "bank_number"
                        | "api_key"
                        | "password"
                        | "private_key"
                        | "access_token"
                        | "aws_access_key"
                        | "unspecified"
                )
            {
                return Err(HostCallError::Protocol(
                    "invalid result review finding".into(),
                ));
            }
        }
        Ok(())
    }
}

/// Read or register a withheld result in the current before-request input.
/// Position is not authority: the host derives scope and content itself.
pub fn tool_result_release(
    message: usize,
    block: usize,
    reason: Option<&ToolResultReleaseReason>,
) -> Result<ToolResultReleaseInfo, HostCallError> {
    let mut args =
        serde_json::json!({"message": message, "block": block, "register": reason.is_some()});
    if let Some(reason) = reason {
        reason.validate()?;
        args["reason"] = serde_json::to_value(reason)
            .map_err(|_| HostCallError::Protocol("invalid result review reason".into()))?;
    }
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
    fn review_reason_is_bounded_and_value_free() {
        for raw in [
            r#"{"kind":"findings","findings":[{"type":"api_key","line":2,"value":"secret"}]}"#,
            r#"{"kind":"scan_failure","findings":null}"#,
            r#"{"kind":"scan_failure","message":"secret"}"#,
        ] {
            assert!(serde_json::from_str::<ToolResultReleaseReason>(raw).is_err());
        }
        let reason: ToolResultReleaseReason =
            serde_json::from_str(r#"{"kind":"findings","findings":[{"type":"api_key","line":2}]}"#)
                .unwrap();
        assert!(reason.validate().is_ok());
        let invalid = ToolResultReleaseReason {
            kind: "findings".into(),
            findings: vec![],
        };
        assert!(invalid.validate().is_err());
    }
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
        let reason = ToolResultReleaseReason {
            kind: "scan_failure".into(),
            findings: vec![],
        };
        let info = tool_result_release(1, 2, Some(&reason)).unwrap();
        assert!(!info.approved);
        assert_eq!(info.reference, "tr_test");
        assert!(serde_json::from_str::<ToolResultReleaseInfo>(
            r#"{"reference":"x","approved":true,"content":"secret"}"#
        )
        .is_err());
    }
}
