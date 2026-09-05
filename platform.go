package plugin_sdk

import (
	"fmt"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"google.golang.org/protobuf/proto"
)

// GetCredential resolves one credential slot declared by this plugin and
// bound by the operator. The returned bytes must be treated as secret.
func GetCredential(slot string) ([]byte, *pbv1.HostError, error) {
	value, herr, err := HostCall("env.credential_get", &pbv1.CredentialGetArgs{Slot: slot})
	return append([]byte(nil), value...), herr, err
}

// AppendFile atomically appends bytes to a declared plugin-private file.
func AppendFile(path string, data []byte) (*pbv1.HostError, error) {
	_, herr, err := HostCall("env.file_append", &pbv1.FileAppendArgs{Path: path, Data: data})
	return herr, err
}

// ReadFile reads a declared plugin-private file up to the host-approved limit.
func ReadFile(path string) ([]byte, *pbv1.HostError, error) {
	value, herr, err := HostCall("env.file_read", &pbv1.FileReadArgs{Path: path})
	return append([]byte(nil), value...), herr, err
}

// WriteFile atomically replaces a declared plugin-private file.
func WriteFile(path string, data []byte) (*pbv1.HostError, error) {
	_, herr, err := HostCall("env.file_write", &pbv1.FileWriteArgs{Path: path, Data: data})
	return herr, err
}

// ListFiles lists declared files under prefix in stable lexical order.
func ListFiles(prefix string) ([]string, *pbv1.HostError, error) {
	value, herr, err := HostCall("env.file_list", &pbv1.FileListArgs{Prefix: prefix})
	if err != nil || herr != nil {
		return nil, herr, err
	}
	var result pbv1.FileListResult
	if err := proto.Unmarshal(value, &result); err != nil {
		return nil, nil, fmt.Errorf("torana: decode file list: %w", err)
	}
	return append([]string(nil), result.Paths...), nil, nil
}

// DeleteFile removes a declared plugin-private file. Missing files succeed.
func DeleteFile(path string) (*pbv1.HostError, error) {
	_, herr, err := HostCall("env.file_delete", &pbv1.FileDeleteArgs{Path: path})
	return herr, err
}

// HTTPRequest performs one request through an operator-approved endpoint slot.
func HTTPRequest(request *pbv1.OutboundHTTPRequestArgs) (*pbv1.OutboundHTTPResponse, *pbv1.HostError, error) {
	value, herr, err := HostCall("env.http_request", request)
	if err != nil || herr != nil {
		return nil, herr, err
	}
	var response pbv1.OutboundHTTPResponse
	if err := proto.Unmarshal(value, &response); err != nil {
		return nil, nil, fmt.Errorf("torana: decode outbound http response: %w", err)
	}
	if err := response.Validate(); err != nil {
		return nil, nil, fmt.Errorf("torana: outbound http response: %w", err)
	}
	return &response, nil, nil
}

// ModelComplete invokes an operator-bound model-service slot. The plugin
// supplies only a provider-neutral prompt and bounded generation preferences;
// the binding owns provider, URL, model, credentials, and hard budgets.
func ModelComplete(request *pbv1.ModelCompleteArgs) (*pbv1.ModelCompleteResult, *pbv1.HostError, error) {
	value, herr, err := HostCall("env.model_complete", request)
	if err != nil || herr != nil {
		return nil, herr, err
	}
	var result pbv1.ModelCompleteResult
	if err := proto.Unmarshal(value, &result); err != nil {
		return nil, nil, fmt.Errorf("torana: decode model completion: %w", err)
	}
	if err := result.Validate(); err != nil {
		return nil, nil, fmt.Errorf("torana: model completion: %w", err)
	}
	return &result, nil, nil
}

// GetModelPricing resolves one operator-bound pricing resource. Pointer fields
// preserve absent (unknown) versus explicitly-zero rates.
func GetModelPricing(resource string) (*pbv1.ModelPricing, *pbv1.HostError, error) {
	value, herr, err := HostCall("env.model_pricing", &pbv1.ModelPricingGetArgs{Resource: resource})
	if err != nil || herr != nil {
		return nil, herr, err
	}
	var pricing pbv1.ModelPricing
	if err := proto.Unmarshal(value, &pricing); err != nil {
		return nil, nil, fmt.Errorf("torana: decode model pricing: %w", err)
	}
	if err := pricing.Validate(); err != nil {
		return nil, nil, fmt.Errorf("torana: model pricing: %w", err)
	}
	return &pricing, nil, nil
}
