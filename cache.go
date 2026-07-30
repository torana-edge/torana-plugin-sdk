package plugin_sdk

import (
	"encoding/json"

	pbv2 "github.com/torana-edge/torana-plugin-sdk/pb/v2"
)

// Prompt-cache compliance helpers for v2 messages.

func CacheControl(msg *pbv2.Message) map[string]any {
	if msg == nil || len(msg.CacheControlJson) == 0 {
		return nil
	}
	var cc map[string]any
	if err := json.Unmarshal(msg.CacheControlJson, &cc); err != nil {
		return nil
	}
	return cc
}

func SetCacheBreakpoint(msg *pbv2.Message, cc map[string]any) {
	if msg == nil {
		return
	}
	if len(cc) == 0 {
		msg.CacheControlJson = nil
		return
	}
	if b, err := json.Marshal(cc); err == nil {
		msg.CacheControlJson = b
	}
}

func MoveCacheBreakpoint(from, to *pbv2.Message) {
	if from == nil || to == nil || len(from.CacheControlJson) == 0 {
		return
	}
	to.CacheControlJson = from.CacheControlJson
	from.CacheControlJson = nil
}
