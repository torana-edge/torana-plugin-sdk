package plugin_sdk

import (
	"bytes"
	"encoding/json"
	"fmt"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

// SuggestionOutcome is host-owned feedback on a suggestion previously issued
// by this plugin. It is never sent to the model provider.
type SuggestionOutcome struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Action string `json:"action,omitempty"`
	Via    string `json:"via,omitempty"`
}

func requestMeta(req *pbv1.ChatRequest) (map[string]json.RawMessage, error) {
	if req == nil || len(req.ToranaMetaJson) == 0 {
		return nil, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(req.ToranaMetaJson, &envelope); err != nil {
		return nil, fmt.Errorf("torana: request metadata: %w", err)
	}
	if envelope == nil {
		return nil, fmt.Errorf("torana: request metadata must be an object")
	}
	return envelope, nil
}

// Suggestions returns outcomes scoped by the host to the calling plugin.
func Suggestions(req *pbv1.ChatRequest) ([]SuggestionOutcome, error) {
	meta, err := requestMeta(req)
	if err != nil {
		return nil, err
	}
	raw, ok := meta["_suggestions"]
	if !ok {
		return nil, nil
	}
	if raw = bytes.TrimSpace(raw); len(raw) == 0 || raw[0] != '[' {
		return nil, fmt.Errorf("torana: suggestions metadata must be an array")
	}
	var outcomes []SuggestionOutcome
	if err := json.Unmarshal(raw, &outcomes); err != nil {
		return nil, fmt.Errorf("torana: suggestions metadata: %w", err)
	}
	for _, item := range outcomes {
		if item.ID == "" || item.Status == "" {
			return nil, fmt.Errorf("torana: suggestion outcome needs id and status")
		}
	}
	return outcomes, nil
}

// ToranaMCP reports the host's MCP-connection observation, when available.
// An absent field means the host has made no observation.
func ToranaMCP(req *pbv1.ChatRequest) (status string, present bool, err error) {
	meta, err := requestMeta(req)
	if err != nil {
		return "", false, err
	}
	raw, ok := meta["_torana_mcp"]
	if !ok {
		return "", false, nil
	}
	if err := json.Unmarshal(raw, &status); err != nil || status == "" {
		return "", false, fmt.Errorf("torana: MCP metadata must be a non-empty string")
	}
	return status, true, nil
}
