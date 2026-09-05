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

// GetPromptCachePolicy resolves one operator-bound prompt-cache policy. The
// plugin supplies only its declared resource name; provider, model, routing,
// prices, and lifetime semantics belong to the operator binding.
func GetPromptCachePolicy(resource string) (*pbv1.PromptCachePolicy, *pbv1.HostError, error) {
	request := &pbv1.PromptCachePolicyGetArgs{Resource: resource}
	if err := request.Validate(); err != nil {
		return nil, nil, err
	}
	value, herr, err := HostCall("env.cache_policy", request)
	if err != nil || herr != nil {
		return nil, herr, err
	}
	var policy pbv1.PromptCachePolicy
	if err := proto.Unmarshal(value, &policy); err != nil {
		return nil, nil, fmt.Errorf("torana: decode prompt cache policy: %w", err)
	}
	if err := policy.Validate(); err != nil {
		return nil, nil, fmt.Errorf("torana: prompt cache policy: %w", err)
	}
	return &policy, nil, nil
}

// LongestPromptCacheTier returns the longest configured tier. Fewer than two
// tiers means there is no tier-selection decision to make.
func LongestPromptCacheTier(policy *pbv1.PromptCachePolicy) (*pbv1.PromptCacheTier, bool) {
	if policy == nil || policy.Validate() != nil || len(policy.Tiers) < 2 {
		return nil, false
	}
	best := policy.Tiers[0]
	for _, tier := range policy.Tiers[1:] {
		if tier.TtlSeconds > best.TtlSeconds {
			best = tier
		}
	}
	return proto.Clone(best).(*pbv1.PromptCacheTier), true
}

// ShortestPromptCacheTTL returns the shortest configured lifetime in seconds.
func ShortestPromptCacheTTL(policy *pbv1.PromptCachePolicy) (uint32, bool) {
	if policy == nil || policy.Validate() != nil || len(policy.Tiers) == 0 {
		return 0, false
	}
	shortest := policy.Tiers[0].TtlSeconds
	for _, tier := range policy.Tiers[1:] {
		if tier.TtlSeconds < shortest {
			shortest = tier.TtlSeconds
		}
	}
	return shortest, true
}

// PromptCacheBreakEvenRefreshes returns floor(write/read - 1). Unknown, free,
// or otherwise unusable prices return false so callers decline instead of
// guessing about operator spend.
func PromptCacheBreakEvenRefreshes(policy *pbv1.PromptCachePolicy) (int, bool) {
	if policy == nil || policy.Validate() != nil || policy.CacheReadUsdPerMtok == nil || policy.CacheWriteUsdPerMtok == nil ||
		*policy.CacheReadUsdPerMtok <= 0 || *policy.CacheWriteUsdPerMtok < *policy.CacheReadUsdPerMtok {
		return 0, false
	}
	ratio := *policy.CacheWriteUsdPerMtok / *policy.CacheReadUsdPerMtok
	maxInt := int(^uint(0) >> 1)
	if ratio > float64(maxInt) {
		return 0, false
	}
	return int(ratio) - 1, true
}
