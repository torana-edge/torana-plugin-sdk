package plugin_sdk

import (
	"bytes"
	"encoding/json"
	"fmt"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

// RouteAppliedInfo describes the host's handling of one plugin route verdict.
// Provider and Model are the route immediately after the verdict; ServedBy and
// ServedModel describe the eventual upstream attempt, which may have failed over.
type RouteAppliedInfo struct {
	Provider      string  `json:"provider"`
	Model         string  `json:"model"`
	VerdictPlugin string  `json:"verdict_plugin"`
	Refused       *string `json:"refused"`
	ServedBy      string  `json:"served_by"`
	ServedModel   string  `json:"served_model"`
	Failover      bool    `json:"failover"`
}

// RouteApplied reads the optional, host-owned routing observation. A false
// presence means no plugin route verdict was attempted. Callers should not
// infer success from a missing observation.
func RouteApplied(resp *pbv1.ChatResponse) (RouteAppliedInfo, bool, error) {
	var zero RouteAppliedInfo
	if resp == nil || len(resp.ToranaMetaJson) == 0 {
		return zero, false, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(resp.ToranaMetaJson, &envelope); err != nil {
		return zero, false, fmt.Errorf("route applied metadata: %w", err)
	}
	raw, present := envelope["_route_applied"]
	if !present {
		return zero, false, nil
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return zero, false, fmt.Errorf("route applied metadata must be an object")
	}
	if err := json.Unmarshal(raw, &zero); err != nil {
		return RouteAppliedInfo{}, false, fmt.Errorf("route applied metadata: %w", err)
	}
	return zero, true, nil
}
