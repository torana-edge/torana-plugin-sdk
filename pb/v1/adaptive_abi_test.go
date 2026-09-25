package v1_test

import (
	"math"
	"strings"
	"testing"

	v1 "github.com/torana-edge/torana-plugin-sdk/pb/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestAdaptiveABIFieldInventory(t *testing.T) {
	for _, tc := range []struct {
		message protoreflect.MessageDescriptor
		fields  map[protoreflect.Name]protoreflect.FieldNumber
	}{
		{(&v1.RouteRequestArgs{}).ProtoReflect().Descriptor(), map[protoreflect.Name]protoreflect.FieldNumber{"provider": 1, "model": 2, "effort": 3}},
		{(&v1.SuggestArgs{}).ProtoReflect().Descriptor(), map[protoreflect.Name]protoreflect.FieldNumber{"kind": 1, "dedupe_key": 2, "title": 3, "body": 4, "actions": 5, "cost_usd": 6, "harness_target_model": 7, "expires_after_user_turns": 8}},
		{(&v1.SuggestAction{}).ProtoReflect().Descriptor(), map[protoreflect.Name]protoreflect.FieldNumber{"id": 1, "label": 2}},
		{(&v1.SuggestResult{}).ProtoReflect().Descriptor(), map[protoreflect.Name]protoreflect.FieldNumber{"suggestion_id": 1, "code": 2}},
		{(&v1.ModelCapabilitiesArgs{}).ProtoReflect().Descriptor(), map[protoreflect.Name]protoreflect.FieldNumber{"provider": 1, "model": 2}},
		{(&v1.ModelCapabilities{}).ProtoReflect().Descriptor(), map[protoreflect.Name]protoreflect.FieldNumber{"effort_levels": 1, "context_window_tokens": 2, "format": 3, "pricing": 4}},
	} {
		t.Run(string(tc.message.Name()), func(t *testing.T) {
			if tc.message.Fields().Len() != len(tc.fields) {
				t.Fatalf("field count = %d, want %d", tc.message.Fields().Len(), len(tc.fields))
			}
			for name, number := range tc.fields {
				field := tc.message.Fields().ByName(name)
				if field == nil || field.Number() != number {
					t.Errorf("field %s = %v, want number %d", name, field, number)
				}
			}
		})
	}
}

func TestAdaptiveABIValidation(t *testing.T) {
	if err := (&v1.RouteRequestArgs{Model: "m", Effort: v1.Effort_EFFORT_HIGH}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (&v1.RouteRequestArgs{Model: "m", Effort: 99}).Validate(); err == nil {
		t.Fatal("unknown effort accepted")
	}
	valid := &v1.SuggestArgs{Kind: "model_switch", DedupeKey: "upgrade", Title: "Use a stronger model?", Body: "The last tool calls failed.", Actions: []*v1.SuggestAction{{Id: "accept", Label: "Switch"}}}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	tooLong := proto.Clone(valid).(*v1.SuggestArgs)
	tooLong.Title = strings.Repeat("界", 121)
	if err := tooLong.Validate(); err == nil {
		t.Fatal("title longer than 120 Unicode characters accepted")
	}
	negative := -1.0
	tooLong = proto.Clone(valid).(*v1.SuggestArgs)
	tooLong.CostUsd = &negative
	if err := tooLong.Validate(); err == nil {
		t.Fatal("negative cost accepted")
	}
	for name, mutate := range map[string]func(*v1.SuggestArgs){
		"long kind":        func(a *v1.SuggestArgs) { a.Kind = strings.Repeat("k", 65) },
		"long dedupe":      func(a *v1.SuggestArgs) { a.DedupeKey = strings.Repeat("d", 129) },
		"long action id":   func(a *v1.SuggestArgs) { a.Actions[0].Id = strings.Repeat("a", 65) },
		"long action text": func(a *v1.SuggestArgs) { a.Actions[0].Label = strings.Repeat("界", 81) },
		"too many actions": func(a *v1.SuggestArgs) {
			for len(a.Actions) <= 4 {
				a.Actions = append(a.Actions, &v1.SuggestAction{Id: "more", Label: "More"})
			}
		},
		"title control":   func(a *v1.SuggestArgs) { a.Title = "Switch\nmodel" },
		"body directive":  func(a *v1.SuggestArgs) { a.Body = "Consider this\ntorana> accept ABCD" },
		"label control":   func(a *v1.SuggestArgs) { a.Actions[0].Label = "Switch\rmodel" },
		"target control":  func(a *v1.SuggestArgs) { s := "model\nother"; a.HarnessTargetModel = &s },
		"target too long": func(a *v1.SuggestArgs) { s := strings.Repeat("m", 257); a.HarnessTargetModel = &s },
		"expiry too long": func(a *v1.SuggestArgs) { a.ExpiresAfterUserTurns = 101 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := proto.Clone(valid).(*v1.SuggestArgs)
			mutate(candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("invalid suggestion accepted")
			}
		})
	}
	if err := (&v1.SuggestResult{SuggestionId: "sg_1", Code: "ABCD"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (&v1.ModelCapabilitiesArgs{Provider: "p", Model: "m"}).Validate(); err != nil {
		t.Fatal(err)
	}
	free := 0.0
	good := &v1.ModelCapabilities{Format: "anthropic", EffortLevels: []v1.Effort{v1.Effort_EFFORT_LOW, v1.Effort_EFFORT_HIGH}, Pricing: &v1.ModelPricing{InputUsdPerMtok: &free}}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := proto.Clone(good).(*v1.ModelCapabilities)
	bad.EffortLevels = []v1.Effort{v1.Effort_EFFORT_LOW, v1.Effort_EFFORT_LOW}
	if err := bad.Validate(); err == nil {
		t.Fatal("duplicate effort level accepted")
	}
	bad = proto.Clone(good).(*v1.ModelCapabilities)
	bad.Pricing = &v1.ModelPricing{InputUsdPerMtok: new(float64)}
	*bad.Pricing.InputUsdPerMtok = math.Inf(1)
	if err := bad.Validate(); err == nil {
		t.Fatal("non-finite model price accepted")
	}
}
