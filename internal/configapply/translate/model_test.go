// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

const modelYAMLFixture = `
alias: planning
description: "High-reasoning model used by the orchestrator."
provider: anthropic
modelName: claude-opus-4-7
apiBase: https://api.anthropic.com
authConfig:
  apiKey: sk-ant-test-key
modelParams:
  temperature: 0.2
  max_tokens: 8192
maxInputTokens: 200000
`

func TestForwardModelV2_FullSpec(t *testing.T) {
	in := decodeYAMLMap(t, modelYAMLFixture)

	got, err := ForwardModelV2(in, ModelV2Hooks{})
	if err != nil {
		t.Fatalf("ForwardModelV2: %v", err)
	}

	maxTokens := 200000
	want := wire.CreateModelRequest{
		Alias:       "planning",
		Description: "High-reasoning model used by the orchestrator.",
		Provider:    "anthropic",
		ModelName:   "claude-opus-4-7",
		APIBase:     "https://api.anthropic.com",
		AuthConfig: map[string]any{
			"apiKey": "sk-ant-test-key",
		},
		ModelParams: map[string]any{
			"temperature": 0.2,
			"max_tokens":  8192,
		},
		MaxInputTokens: &maxTokens,
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("ForwardModelV2 mismatch (-want +got):\n%s", diff)
	}
}

func TestForwardModelV2_MinimalSpec(t *testing.T) {
	in := map[string]any{
		"alias":     "fast",
		"provider":  "openai",
		"modelName": "gpt-5-mini",
		"authConfig": map[string]any{
			"apiKey": "sk-openai-test",
		},
	}

	got, err := ForwardModelV2(in, ModelV2Hooks{})
	if err != nil {
		t.Fatalf("ForwardModelV2: %v", err)
	}

	want := wire.CreateModelRequest{
		Alias:     "fast",
		Provider:  "openai",
		ModelName: "gpt-5-mini",
		AuthConfig: map[string]any{
			"apiKey": "sk-openai-test",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("ForwardModelV2 mismatch (-want +got):\n%s", diff)
	}
}

func TestForwardModelV2_TypeError(t *testing.T) {
	in := map[string]any{
		"alias":          "bad",
		"maxInputTokens": "not-an-int",
	}
	if _, err := ForwardModelV2(in, ModelV2Hooks{}); err == nil {
		t.Fatal("expected type-coercion error, got nil")
	}
}

func TestReverseModelV2_FullResponse(t *testing.T) {
	apiBase := "https://api.anthropic.com"
	desc := "High-reasoning model used by the orchestrator."
	maxTokens := 200000
	provider, modelName := "anthropic", "claude-opus-4-7"
	createdBy, updatedBy := "alice", "bob"

	in := wire.ModelResponse{
		ID:          "model-abc-123",
		Type:        "model",
		CreatedBy:   &createdBy,
		UpdatedBy:   &updatedBy,
		CreatedTime: testInstant(1),
		UpdatedTime: testInstant(2),

		Alias:     "planning",
		Provider:  &provider,
		ModelName: &modelName,
		APIBase:   &apiBase,
		AuthConfig: map[string]any{
			"apiKey": "sk-ant-test-key",
		},
		ModelParams: map[string]any{
			"temperature": 0.2,
			"max_tokens":  8192,
		},
		Description:    &desc,
		MaxInputTokens: &maxTokens,
	}

	got, err := ReverseModelV2(in, ModelV2Hooks{})
	if err != nil {
		t.Fatalf("ReverseModelV2: %v", err)
	}

	want := map[string]any{
		"alias":     "planning",
		"provider":  "anthropic",
		"modelName": "claude-opus-4-7",
		"apiBase":   "https://api.anthropic.com",
		"authConfig": map[string]any{
			"apiKey": "sk-ant-test-key",
		},
		"modelParams": map[string]any{
			"temperature": 0.2,
			"max_tokens":  8192,
		},
		"description":    "High-reasoning model used by the orchestrator.",
		"maxInputTokens": 200000,
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("ReverseModelV2 mismatch (-want +got):\n%s", diff)
	}
}

func TestReverseModelV2_OmitsNilOptionalFields(t *testing.T) {
	provider, modelName := "openai", "gpt-5-mini"
	in := wire.ModelResponse{
		ID:        "model-xyz",
		Alias:     "fast",
		Provider:  &provider,
		ModelName: &modelName,
		AuthConfig: map[string]any{
			"apiKey": "sk-openai-test",
		},
	}

	got, err := ReverseModelV2(in, ModelV2Hooks{})
	if err != nil {
		t.Fatalf("ReverseModelV2: %v", err)
	}

	want := map[string]any{
		"alias":     "fast",
		"provider":  "openai",
		"modelName": "gpt-5-mini",
		"authConfig": map[string]any{
			"apiKey": "sk-openai-test",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("ReverseModelV2 mismatch (-want +got):\n%s", diff)
	}
}

func TestReverseModelV2_ModelTypeOmitsChatDefault(t *testing.T) {
	cases := []struct {
		name        string
		modelType   string
		wantPulled  string
		wantForward string
	}{
		{name: "empty", modelType: "", wantPulled: "", wantForward: ""},
		{name: "chat", modelType: "chat", wantPulled: "", wantForward: ""},
		{name: "decision", modelType: "decision", wantPulled: "decision", wantForward: "decision"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReverseModelV2(wire.ModelResponse{ModelType: tc.modelType}, ModelV2Hooks{})
			if err != nil {
				t.Fatalf("ReverseModelV2: %v", err)
			}
			if pulled, _ := got["modelType"].(string); pulled != tc.wantPulled {
				t.Fatalf("pulled modelType = %q, want %q", pulled, tc.wantPulled)
			}

			req, err := ForwardModelV2(got, ModelV2Hooks{})
			if err != nil {
				t.Fatalf("ForwardModelV2: %v", err)
			}
			if req.ModelType != tc.wantForward {
				t.Fatalf("forward ModelType = %q, want %q", req.ModelType, tc.wantForward)
			}
		})
	}
}

func TestModelV2RoundTrip_Forward_Reverse(t *testing.T) {
	in := decodeYAMLMap(t, modelYAMLFixture)

	req, err := ForwardModelV2(in, ModelV2Hooks{})
	if err != nil {
		t.Fatalf("ForwardModelV2: %v", err)
	}

	resp := responseFromRequest(req)

	got, err := ReverseModelV2(resp, ModelV2Hooks{})
	if err != nil {
		t.Fatalf("ReverseModelV2: %v", err)
	}

	if diff := cmp.Diff(in, got); diff != "" {
		t.Fatalf("round-trip mismatch (-input +reverse(forward(input))):\n%s", diff)
	}
}

func responseFromRequest(req wire.CreateModelRequest) wire.ModelResponse {
	provider, modelName, createdBy := req.Provider, req.ModelName, "test"
	resp := wire.ModelResponse{
		ID:             "synthesized-test-id",
		Type:           "model",
		Alias:          req.Alias,
		Provider:       &provider,
		ModelName:      &modelName,
		AuthConfig:     req.AuthConfig,
		ModelParams:    req.ModelParams,
		MaxInputTokens: req.MaxInputTokens,
		CreatedBy:      &createdBy,
		UpdatedBy:      &createdBy,
		CreatedTime:    testInstant(1),
		UpdatedTime:    testInstant(2),
	}
	if req.APIBase != "" {
		s := req.APIBase
		resp.APIBase = &s
	}
	if req.Description != "" {
		s := req.Description
		resp.Description = &s
	}
	return resp
}

func testInstant(day int) apiv2.Instant {
	return apiv2.Instant(time.Date(2026, time.January, day, 0, 0, 0, 0, time.UTC).UnixMilli())
}

func decodeYAMLMap(t *testing.T, src string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := yaml.Unmarshal([]byte(src), &out); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	return out
}

func TestModelPricingRoundTripsThroughTheTranslators(t *testing.T) {
	in := decodeYAMLMap(t, modelYAMLFixture+"pricing:\n  input: \"2.50\"\n  output: \"10\"\n")
	req, err := ForwardModelV2(in, ModelV2Hooks{})
	if err != nil {
		t.Fatalf("ForwardModelV2: %v", err)
	}
	if req.Pricing == nil || req.Pricing.Input == nil || *req.Pricing.Input != "2.50" {
		t.Fatalf("ForwardModelV2 dropped pricing: %+v", req.Pricing)
	}

	input, output := "2.5", "10"
	out, err := ReverseModelV2(wire.ModelResponse{
		Alias:   "planning",
		Pricing: &apiv2.Pricing{Input: &input, Output: &output},
	}, ModelV2Hooks{})
	if err != nil {
		t.Fatalf("ReverseModelV2: %v", err)
	}
	pricing, ok := out["pricing"].(map[string]any)
	if !ok || pricing["input"] != "2.5" || pricing["output"] != "10" {
		t.Errorf("ReverseModelV2 pricing = %#v, want the stored rates", out["pricing"])
	}
}
