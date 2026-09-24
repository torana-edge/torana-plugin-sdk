//go:build !wasip1

package sdktest

import (
	"encoding/json"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

// SetResponseMeta simulates the host-owned response metadata seen by an
// after-response hook. It does not send these fields to a model or harness.
func SetResponseMeta(resp *pbv1.ChatResponse, meta map[string]any) error {
	raw, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	resp.ToranaMetaJson = raw
	return nil
}
