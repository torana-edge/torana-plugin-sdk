package plugin_sdk

import (
	"fmt"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"google.golang.org/protobuf/proto"
)

// GetCredential resolves one credential slot declared by this plugin and
// bound by the operator. The returned bytes must be treated as secret.
func GetCredential(slot string) ([]byte, error) {
	value, err := checkedHostCallRequired("env.credential_get", &pbv1.CredentialGetArgs{Slot: slot})
	return append([]byte(nil), value...), err
}

// AppendFile atomically appends bytes to a declared plugin-private file.
func AppendFile(path string, data []byte) error {
	return checkedHostCall("env.file_append", &pbv1.FileAppendArgs{Path: path, Data: data})
}

// ReadFile reads a declared plugin-private file up to the host-approved limit.
func ReadFile(path string) ([]byte, error) {
	value, err := checkedHostCallRequired("env.file_read", &pbv1.FileReadArgs{Path: path})
	return append([]byte(nil), value...), err
}

// WriteFile atomically replaces a declared plugin-private file.
func WriteFile(path string, data []byte) error {
	return checkedHostCall("env.file_write", &pbv1.FileWriteArgs{Path: path, Data: data})
}

// ListFiles lists declared files under prefix in stable lexical order.
func ListFiles(prefix string) ([]string, error) {
	value, err := checkedHostCallRequired("env.file_list", &pbv1.FileListArgs{Prefix: prefix})
	if err != nil {
		return nil, err
	}
	var result pbv1.FileListResult
	if err := proto.Unmarshal(value, &result); err != nil {
		return nil, fmt.Errorf("torana: decode file list: %w", err)
	}
	return append([]string(nil), result.Paths...), nil
}

// DeleteFile removes a declared plugin-private file. Missing files succeed.
func DeleteFile(path string) error {
	return checkedHostCall("env.file_delete", &pbv1.FileDeleteArgs{Path: path})
}

// HTTPRequest performs one request through an operator-approved endpoint slot.
func HTTPRequest(request *pbv1.OutboundHTTPRequestArgs) (*pbv1.OutboundHTTPResponse, error) {
	value, err := checkedHostCallRequired("env.http_request", request)
	if err != nil {
		return nil, err
	}
	var response pbv1.OutboundHTTPResponse
	if err := pbv1.ValidateWire(value, response.ProtoReflect().Descriptor()); err != nil {
		return nil, fmt.Errorf("torana: outbound http response wire: %w", err)
	}
	if err := proto.Unmarshal(value, &response); err != nil {
		return nil, fmt.Errorf("torana: decode outbound http response: %w", err)
	}
	if err := response.Validate(); err != nil {
		return nil, fmt.Errorf("torana: outbound http response: %w", err)
	}
	return &response, nil
}

// ModelComplete invokes an operator-bound model-service slot with canonical
// messages, tools, and optional OutputFormat. The binding owns provider, URL,
// model, credentials, timeout, and hard budgets; it may clamp preferences.
// The result contains a canonical ResponseMessage plus provider-reported model,
// finish reason, and usage when available.
func ModelComplete(request *pbv1.ModelCompleteArgs) (*pbv1.ModelCompleteResult, error) {
	value, err := checkedHostCallRequired("env.model_complete", request)
	if err != nil {
		return nil, err
	}
	var result pbv1.ModelCompleteResult
	if err := pbv1.ValidateWire(value, result.ProtoReflect().Descriptor()); err != nil {
		return nil, fmt.Errorf("torana: model completion wire: %w", err)
	}
	if err := proto.Unmarshal(value, &result); err != nil {
		return nil, fmt.Errorf("torana: decode model completion: %w", err)
	}
	if err := result.Validate(); err != nil {
		return nil, fmt.Errorf("torana: model completion: %w", err)
	}
	return &result, nil
}

// ModelCompleteText concatenates a completion made solely of text blocks. It
// returns an error for tool calls, unknown blocks, empty blocks, or a missing
// message so structured output is never silently discarded.
func ModelCompleteText(request *pbv1.ModelCompleteArgs) (string, error) {
	result, err := ModelComplete(request)
	if err != nil {
		return "", err
	}
	if result == nil || result.Message == nil {
		return "", fmt.Errorf("torana: model completion has no message")
	}
	var out string
	for _, block := range result.Message.Blocks {
		if text := block.GetText(); text != nil {
			out += text.Text
		} else {
			return "", fmt.Errorf("torana: model completion contains a non-text block; use ModelComplete")
		}
	}
	return out, nil
}

// GetModelPricing resolves one operator-bound pricing resource. Pointer fields
// preserve absent (unknown) versus explicitly-zero rates.
func GetModelPricing(resource string) (*pbv1.ModelPricing, error) {
	value, err := checkedHostCallRequired("env.model_pricing", &pbv1.ModelPricingGetArgs{Resource: resource})
	if err != nil {
		return nil, err
	}
	var pricing pbv1.ModelPricing
	if err := pbv1.ValidateWire(value, pricing.ProtoReflect().Descriptor()); err != nil {
		return nil, fmt.Errorf("torana: model pricing wire: %w", err)
	}
	if err := proto.Unmarshal(value, &pricing); err != nil {
		return nil, fmt.Errorf("torana: decode model pricing: %w", err)
	}
	if err := pricing.Validate(); err != nil {
		return nil, fmt.Errorf("torana: model pricing: %w", err)
	}
	return &pricing, nil
}

// GetPromptCachePolicy resolves one operator-bound prompt-cache policy. The
// plugin supplies only its declared resource name; provider, model, routing,
// prices, and lifetime semantics belong to the operator binding.
func GetPromptCachePolicy(resource string) (*pbv1.PromptCachePolicy, error) {
	request := &pbv1.PromptCachePolicyGetArgs{Resource: resource}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	value, err := checkedHostCallRequired("env.cache_policy", request)
	if err != nil {
		return nil, err
	}
	var policy pbv1.PromptCachePolicy
	if err := pbv1.ValidateWire(value, policy.ProtoReflect().Descriptor()); err != nil {
		return nil, fmt.Errorf("torana: prompt cache policy wire: %w", err)
	}
	if err := proto.Unmarshal(value, &policy); err != nil {
		return nil, fmt.Errorf("torana: decode prompt cache policy: %w", err)
	}
	if err := policy.Validate(); err != nil {
		return nil, fmt.Errorf("torana: prompt cache policy: %w", err)
	}
	return &policy, nil
}

// GetResourceInfo returns the effective operations and hard limits for one
// declared operator-bound resource. It exposes no URL, credential, or secret
// configuration, and it does not grant authority to use the resource.
func GetResourceInfo(kind, name string) (*pbv1.ResourceInfo, error) {
	request := &pbv1.ResourceInfoArgs{Kind: kind, Name: name}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	raw, err := checkedHostCallRequired("env.resource_info", request)
	if err != nil {
		return nil, err
	}
	var info pbv1.ResourceInfo
	if err := pbv1.ValidateWire(raw, info.ProtoReflect().Descriptor()); err != nil {
		return nil, fmt.Errorf("torana: resource info wire: %w", err)
	}
	if err := proto.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("torana: decode resource info: %w", err)
	}
	if err := info.Validate(); err != nil {
		return nil, fmt.Errorf("torana: resource info: %w", err)
	}
	info.Operations = append([]string(nil), info.Operations...)
	return &info, nil
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
