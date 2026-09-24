//go:build !wasip1

package sdktest

import (
	"encoding/json"
	"errors"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
)

// SetResponseMeta simulates the host-owned response metadata seen by an
// after-response hook. It does not send these fields to a model or harness.
func SetResponseMeta(resp *pbv1.ChatResponse, meta map[string]any) error {
	if resp == nil {
		return errors.New("sdktest: nil response")
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	resp.ToranaMetaJson = raw
	return nil
}

// SetRequestMeta simulates host-owned metadata for before-request hooks.
func SetRequestMeta(req *pbv1.ChatRequest, meta map[string]any) error {
	if req == nil {
		return errors.New("sdktest: nil request")
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	req.ToranaMetaJson = raw
	return nil
}
