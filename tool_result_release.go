package plugin_sdk

import (
	"encoding/json"
	"fmt"

	"github.com/torana-edge/torana-plugin-sdk/strictjson"
)

// ToolResultReleaseInfo is host-owned permission for one exact result. Neither
// registering nor reading this permission approves disclosure.
type ToolResultReleaseInfo struct {
	Reference string `json:"reference"`
	Approved  bool   `json:"approved"`
}

// ToolResultReleaseReason contains only bounded classifications, never values,
// scanner prose or tool arguments. Lines refer to returned output, not files.
type ToolResultReleaseReason struct {
	Kind     string                     `json:"kind"`
	Findings []ToolResultReleaseFinding `json:"findings,omitempty"`
}

type ToolResultReleaseFinding struct {
	Type string `json:"type"`
	Line int    `json:"line"`
}

func (reason ToolResultReleaseReason) Validate() error {
	if reason.Kind == "scan_failure" && len(reason.Findings) == 0 {
		return nil
	}
	if reason.Kind != "findings" || len(reason.Findings) == 0 || len(reason.Findings) > 20 {
		return fmt.Errorf("invalid result review reason")
	}
	for _, finding := range reason.Findings {
		switch finding.Type {
		case "email", "phone", "address", "government_id", "us_ssn", "credit_card", "bank_number", "api_key", "password", "private_key", "access_token", "aws_access_key", "unspecified":
		default:
			return fmt.Errorf("invalid result review category")
		}
		if finding.Line < 0 || finding.Line > 1000000000 {
			return fmt.Errorf("invalid result review line")
		}
	}
	return nil
}

// DecodeToolResultReleaseReason enforces the same closed metadata contract in
// hosts and guests; free-form messages and sensitive values cannot be stored.
func DecodeToolResultReleaseReason(raw []byte) (*ToolResultReleaseReason, error) {
	object, err := strictjson.DecodeObjectStrict(raw, "kind", "findings")
	if err != nil || object["kind"] == nil {
		return nil, fmt.Errorf("invalid result review reason")
	}
	if findings, present := object["findings"]; present {
		var entries []json.RawMessage
		if json.Unmarshal(findings, &entries) != nil || len(entries) > 20 {
			return nil, fmt.Errorf("invalid result review findings")
		}
		for _, entry := range entries {
			fields, err := strictjson.DecodeObjectStrict(entry, "type", "line")
			if err != nil || len(fields) != 2 {
				return nil, fmt.Errorf("invalid result review finding")
			}
		}
	}
	var reason ToolResultReleaseReason
	if json.Unmarshal(raw, &reason) != nil {
		return nil, fmt.Errorf("invalid result review reason")
	}
	return &reason, reason.Validate()
}

// ToolResultRelease reads a human-approved exception for the result at the
// supplied position in the current before-request input. A non-nil reason
// registers a withheld result for review; nil only reads an existing decision.
// Torana resolves the
// conversation, plugin digest, call ID and content; a guest supplies no scope
// or content. This call never returns the withheld content or grants consent.
func ToolResultRelease(message, block int, reason *ToolResultReleaseReason) (ToolResultReleaseInfo, error) {
	if message < 0 || block < 0 {
		return ToolResultReleaseInfo{}, fmt.Errorf("tool-result position must be nonnegative")
	}
	if reason != nil {
		if err := reason.Validate(); err != nil {
			return ToolResultReleaseInfo{}, err
		}
	}
	args, _ := json.Marshal(struct {
		Message  int                      `json:"message"`
		Block    int                      `json:"block"`
		Register bool                     `json:"register"`
		Reason   *ToolResultReleaseReason `json:"reason,omitempty"`
	}{message, block, reason != nil, reason})
	raw, err := HostCallExtension("torana_tool_result_release", args)
	if err != nil {
		return ToolResultReleaseInfo{}, err
	}
	object, err := strictjson.DecodeObjectStrict(raw, "reference", "approved")
	if err != nil || len(object) != 2 {
		return ToolResultReleaseInfo{}, fmt.Errorf("invalid tool-result release response")
	}
	var result ToolResultReleaseInfo
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	if result.Approved && result.Reference == "" {
		return ToolResultReleaseInfo{}, fmt.Errorf("approved result has no reference")
	}
	return result, nil
}
