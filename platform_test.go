package plugin_sdk

import (
	"reflect"
	"testing"

	pbv1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"google.golang.org/protobuf/proto"
)

func framedValue(t *testing.T, value []byte) []byte {
	t.Helper()
	b, err := proto.Marshal(&pbv1.HostCallResult{Result: &pbv1.HostCallResult_Value{Value: value}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPlatformHelpersUseTypedCommands(t *testing.T) {
	var commands []string
	h := &TestHost{HostCall: func(command string, raw []byte) ([]byte, error) {
		commands = append(commands, command)
		switch command {
		case "env.credential_get":
			var args pbv1.CredentialGetArgs
			if err := proto.Unmarshal(raw, &args); err != nil || args.Slot != "github" {
				t.Fatalf("credential args = %q, %v", args.Slot, err)
			}
			return framedValue(t, []byte("secret")), nil
		case "env.file_append", "env.file_write", "env.file_delete":
			return framedValue(t, nil), nil
		case "env.file_read":
			return framedValue(t, []byte("line\n")), nil
		case "env.file_list":
			value, err := proto.Marshal(&pbv1.FileListResult{Paths: []string{"usage.jsonl"}})
			if err != nil {
				t.Fatal(err)
			}
			return framedValue(t, value), nil
		case "env.http_request":
			value, err := proto.Marshal(&pbv1.OutboundHTTPResponse{Status: 200, Body: []byte("ok")})
			if err != nil {
				t.Fatal(err)
			}
			return framedValue(t, value), nil
		case "env.model_complete":
			var args pbv1.ModelCompleteArgs
			if err := proto.Unmarshal(raw, &args); err != nil || args.Service != "summarizer" || len(args.Messages) != 1 {
				t.Fatalf("model complete service/messages = %q/%d, %v", args.Service, len(args.Messages), err)
			}
			value, err := proto.Marshal(&pbv1.ModelCompleteResult{Message: &pbv1.ResponseMessage{Blocks: []*pbv1.ResponseBlock{{Kind: &pbv1.ResponseBlock_Text{Text: &pbv1.ResponseTextBlock{Text: "summary"}}}}}, ReportedModel: "small-1"})
			if err != nil {
				t.Fatal(err)
			}
			return framedValue(t, value), nil
		case "env.model_pricing":
			var args pbv1.ModelPricingGetArgs
			if err := proto.Unmarshal(raw, &args); err != nil || args.Resource != "request-model" {
				t.Fatalf("model pricing resource = %q, %v", args.Resource, err)
			}
			zero := 0.0
			value, err := proto.Marshal(&pbv1.ModelPricing{InputUsdPerMtok: &zero})
			if err != nil {
				t.Fatal(err)
			}
			return framedValue(t, value), nil
		case "env.cache_policy":
			var args pbv1.PromptCachePolicyGetArgs
			if err := proto.Unmarshal(raw, &args); err != nil || args.Resource != "request-cache" {
				t.Fatalf("prompt cache policy resource = %q, %v", args.Resource, err)
			}
			read, write, multiplier := 0.1, 1.25, 1.25
			value, err := proto.Marshal(&pbv1.PromptCachePolicy{
				CacheReadUsdPerMtok: &read, CacheWriteUsdPerMtok: &write, RefreshOnRead: true,
				Tiers: []*pbv1.PromptCacheTier{
					{TtlSeconds: 300, WriteMultiplier: &multiplier, MarkerJson: []byte(`{"ttl":"5m"}`)},
					{TtlSeconds: 3600, MarkerJson: []byte(`{"ttl":"1h"}`)},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			return framedValue(t, value), nil
		default:
			t.Fatalf("unexpected command %q", command)
			return nil, nil
		}
	}}
	WithTestHost(h, func() {
		if value, err := GetCredential("github"); err != nil || string(value) != "secret" {
			t.Fatalf("GetCredential = %q, %v", value, err)
		}
		if err := AppendFile("usage.jsonl", []byte("{}\n")); err != nil {
			t.Fatalf("AppendFile = %v", err)
		}
		if value, err := ReadFile("usage.jsonl"); err != nil || string(value) != "line\n" {
			t.Fatalf("ReadFile = %q, %v", value, err)
		}
		if err := WriteFile("state.json", []byte("{}")); err != nil {
			t.Fatalf("WriteFile = %v", err)
		}
		if paths, err := ListFiles(""); err != nil || !reflect.DeepEqual(paths, []string{"usage.jsonl"}) {
			t.Fatalf("ListFiles = %v, %v", paths, err)
		}
		if err := DeleteFile("state.json"); err != nil {
			t.Fatalf("DeleteFile = %v", err)
		}
		response, err := HTTPRequest(&pbv1.OutboundHTTPRequestArgs{Endpoint: "github", Method: "GET", Path: "/user"})
		if err != nil || response.Status != 200 || string(response.Body) != "ok" {
			t.Fatalf("HTTPRequest = %#v, %v", response, err)
		}
		completion, err := ModelComplete(&pbv1.ModelCompleteArgs{Service: "summarizer", Messages: []*pbv1.Message{{Role: "user", Blocks: []*pbv1.RequestBlock{{Kind: &pbv1.RequestBlock_Text{Text: &pbv1.RequestTextBlock{Text: "long text"}}}}}}})
		if err != nil || completion.Message == nil || completion.Message.Blocks[0].GetText().Text != "summary" || completion.ReportedModel != "small-1" {
			t.Fatalf("ModelComplete = %#v, %v", completion, err)
		}
		pricing, err := GetModelPricing("request-model")
		if err != nil || pricing.InputUsdPerMtok == nil || *pricing.InputUsdPerMtok != 0 {
			t.Fatalf("GetModelPricing = %#v, %v", pricing, err)
		}
		policy, err := GetPromptCachePolicy("request-cache")
		if err != nil || !policy.RefreshOnRead || len(policy.Tiers) != 2 {
			t.Fatalf("GetPromptCachePolicy = %#v, %v", policy, err)
		}
		if tier, ok := LongestPromptCacheTier(policy); !ok || tier.TtlSeconds != 3600 {
			t.Fatalf("LongestPromptCacheTier = %#v, %v", tier, ok)
		}
		if ttl, ok := ShortestPromptCacheTTL(policy); !ok || ttl != 300 {
			t.Fatalf("ShortestPromptCacheTTL = %d, %v", ttl, ok)
		}
		if refreshes, ok := PromptCacheBreakEvenRefreshes(policy); !ok || refreshes != 11 {
			t.Fatalf("PromptCacheBreakEvenRefreshes = %d, %v", refreshes, ok)
		}
	})
	want := []string{"env.credential_get", "env.file_append", "env.file_read", "env.file_write", "env.file_list", "env.file_delete", "env.http_request", "env.model_complete", "env.model_pricing", "env.cache_policy"}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %v, want %v", commands, want)
	}
}

func TestPlatformHelpersRejectInvalidArgumentsBeforeHost(t *testing.T) {
	calls := 0
	WithTestHost(&TestHost{HostCall: func(string, []byte) ([]byte, error) {
		calls++
		return nil, nil
	}}, func() {
		if _, err := GetCredential("../secret"); err == nil {
			t.Fatal("invalid credential slot accepted")
		}
		if err := AppendFile("../escape", nil); err == nil {
			t.Fatal("traversal path accepted")
		}
		if _, err := HTTPRequest(&pbv1.OutboundHTTPRequestArgs{Endpoint: "api", Method: "GET", Path: "https://evil.example/"}); err == nil {
			t.Fatal("absolute URL accepted")
		}
		if _, err := ModelComplete(&pbv1.ModelCompleteArgs{Service: "summarizer"}); err == nil {
			t.Fatal("empty model prompt accepted")
		}
		if _, err := GetModelPricing("../price"); err == nil {
			t.Fatal("invalid pricing resource accepted")
		}
		if _, err := GetPromptCachePolicy("../cache"); err == nil {
			t.Fatal("invalid prompt-cache policy resource accepted")
		}
	})
	if calls != 0 {
		t.Fatalf("invalid arguments made %d host calls", calls)
	}
}

func TestPlatformPermissionsAreRequestable(t *testing.T) {
	for _, permission := range []string{
		"env.credential_get", "env.file_append", "env.file_read", "env.file_write",
		"env.file_list", "env.file_delete", "env.http_request", "env.model_complete", "env.model_pricing", "env.cache_policy",
	} {
		if !IsPermission(permission) {
			t.Errorf("%q is not requestable", permission)
		}
	}
}

func TestModelResourceHelpersRejectMalformedHostValues(t *testing.T) {
	negative := -1.0
	rows := []struct {
		name    string
		command string
		value   proto.Message
		invoke  func() error
	}{
		{
			name: "negative usage", command: "env.model_complete",
			value: &pbv1.ModelCompleteResult{Usage: &pbv1.Usage{InputTokens: -1}},
			invoke: func() error {
				_, err := ModelComplete(&pbv1.ModelCompleteArgs{Service: "scanner", Messages: []*pbv1.Message{{Role: "user", Blocks: []*pbv1.RequestBlock{{Kind: &pbv1.RequestBlock_Text{Text: &pbv1.RequestTextBlock{Text: "x"}}}}}}})
				return err
			},
		},
		{
			name: "negative pricing", command: "env.model_pricing",
			value: &pbv1.ModelPricing{OutputUsdPerMtok: &negative},
			invoke: func() error {
				_, err := GetModelPricing("request-model")
				return err
			},
		},
		{
			name: "invalid prompt cache tier marker", command: "env.cache_policy",
			value: &pbv1.PromptCachePolicy{Tiers: []*pbv1.PromptCacheTier{{
				TtlSeconds: 300, MarkerJson: []byte(`[]`),
			}}},
			invoke: func() error {
				_, err := GetPromptCachePolicy("request-cache")
				return err
			},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			value, err := proto.Marshal(row.value)
			if err != nil {
				t.Fatal(err)
			}
			WithTestHost(&TestHost{HostCall: func(command string, _ []byte) ([]byte, error) {
				if command != row.command {
					t.Fatalf("command = %q, want %q", command, row.command)
				}
				return framedValue(t, value), nil
			}}, func() {
				if err := row.invoke(); err == nil {
					t.Fatal("malformed host value accepted")
				}
			})
		})
	}
}

func TestModelCompleteTextRefusesPartialToolProjection(t *testing.T) {
	result := &pbv1.ModelCompleteResult{Message: &pbv1.ResponseMessage{Blocks: []*pbv1.ResponseBlock{
		{Kind: &pbv1.ResponseBlock_Text{Text: &pbv1.ResponseTextBlock{Text: "preface"}}},
		{Kind: &pbv1.ResponseBlock_ToolCall{ToolCall: &pbv1.ToolCall{Name: "lookup", ArgumentsJson: []byte(`{}`)}}},
	}}}
	raw, err := proto.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	WithTestHost(&TestHost{HostCall: func(string, []byte) ([]byte, error) { return framedValue(t, raw), nil }}, func() {
		text, err := ModelCompleteText(&pbv1.ModelCompleteArgs{Service: "summarizer", Messages: []*pbv1.Message{{Role: "user", Blocks: []*pbv1.RequestBlock{{Kind: &pbv1.RequestBlock_Text{Text: &pbv1.RequestTextBlock{Text: "summarize"}}}}}}})
		if err == nil || text != "" {
			t.Fatalf("partial text projection = %q, %v", text, err)
		}
	})
}
