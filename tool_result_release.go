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

// ToolResultRelease reads a human-approved exception for the result at the
// supplied position in the current before-request input. register=true records
// a withheld result so a model can request human review. Torana resolves the
// conversation, plugin digest, call ID and content; a guest supplies no scope
// or content. This call never returns the withheld content or grants consent.
func ToolResultRelease(message, block int, register bool) (ToolResultReleaseInfo, error) {
	if message < 0 || block < 0 {
		return ToolResultReleaseInfo{}, fmt.Errorf("tool-result position must be nonnegative")
	}
	args, _ := json.Marshal(struct {
		Message  int  `json:"message"`
		Block    int  `json:"block"`
		Register bool `json:"register"`
	}{message, block, register})
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
