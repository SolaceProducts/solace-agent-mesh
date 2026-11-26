// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"math"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func cacheKey(t *testing.T, ov *BifrostConfig) string {
	t.Helper()
	key, err := overrideCacheKey(ov)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestOverrideCacheKey_ConfigThatDoesNotMarshalHasNoKey(t *testing.T) {
	nan := &BifrostConfig{Model: "m", RequestDefaults: RequestDefaults{ExtraParams: map[string]any{"top_p": math.NaN()}}}
	if key, err := overrideCacheKey(nan); err == nil {
		t.Fatalf("overrideCacheKey = %q, want an error: every config that does not marshal would share that key", key)
	}
}

func TestSubReqForOverride_ReplacesModelAndClearsOverride(t *testing.T) {
	req := &ChatRequest{
		Model:     "default",
		Messages:  []ChatMessage{{Role: "user", Content: "hi"}},
		MaxTokens: 100,
		ModelOverride: &BifrostConfig{
			Model: "override-model",
		},
	}
	sub := subReqForOverride(req)
	if sub.Model != "override-model" {
		t.Errorf("sub.Model = %q, want override-model", sub.Model)
	}
	if sub.ModelOverride != nil {
		t.Errorf("sub.ModelOverride must be nil to prevent recursion, got %+v", sub.ModelOverride)
	}
	if sub.MaxTokens != 100 {
		t.Errorf("sub.MaxTokens = %d, want 100", sub.MaxTokens)
	}
	if req.Model != "default" {
		t.Errorf("original req.Model mutated: %q", req.Model)
	}
	if req.ModelOverride == nil {
		t.Error("original req.ModelOverride must not be cleared")
	}
}

func TestSubReqForOverride_EmptyOverrideModel_PreservesReqModel(t *testing.T) {
	req := &ChatRequest{
		Model:         "default",
		ModelOverride: &BifrostConfig{Model: ""},
	}
	sub := subReqForOverride(req)
	if sub.Model != "default" {
		t.Errorf("sub.Model = %q, want default", sub.Model)
	}
}

func TestOverrideCacheKey_StableForSameConfig(t *testing.T) {
	a := &BifrostConfig{
		Model:    "gpt-4o",
		Provider: "openai",
		ExtraHeaders: map[string]string{
			"X-A": "1",
			"X-B": "2",
		},
		ProviderConfig: map[string]any{"region": "us"},
	}
	b := &BifrostConfig{
		Model:    "gpt-4o",
		Provider: "openai",
		ExtraHeaders: map[string]string{
			"X-B": "2",
			"X-A": "1",
		},
		ProviderConfig: map[string]any{"region": "us"},
	}
	if cacheKey(t, a) != cacheKey(t, b) {
		t.Error("overrideCacheKey must be stable across equivalent configs with different map orderings")
	}
}

func TestOverrideCacheKey_DifferentConfigs(t *testing.T) {
	a := &BifrostConfig{Model: "gpt-4o"}
	b := &BifrostConfig{Model: "claude-sonnet"}
	if cacheKey(t, a) == cacheKey(t, b) {
		t.Error("overrideCacheKey must differ for different models")
	}
}

func TestOverrideCacheKey_DifferentTimeouts(t *testing.T) {
	a := &BifrostConfig{Model: "gpt-4o", RequestTimeoutSeconds: 60}
	b := &BifrostConfig{Model: "gpt-4o", RequestTimeoutSeconds: 120}
	if cacheKey(t, a) == cacheKey(t, b) {
		t.Error("overrideCacheKey must differ across request timeouts")
	}
}

func TestOverrideCacheKey_DifferentStreamIdleTimeouts(t *testing.T) {
	a := &BifrostConfig{Model: "gpt-4o"}
	b := &BifrostConfig{Model: "gpt-4o", StreamIdleTimeoutSeconds: 600}
	if cacheKey(t, a) == cacheKey(t, b) {
		t.Error("overrideCacheKey must differ across stream idle timeouts")
	}
}

func TestOverrideCacheKey_DifferentRetrySettings(t *testing.T) {
	base := &BifrostConfig{Model: "gpt-4o"}
	retries := &BifrostConfig{Model: "gpt-4o", MaxRetries: new(0)}
	budget := &BifrostConfig{Model: "gpt-4o", RetryBudgetSeconds: new(30)}
	if cacheKey(t, base) == cacheKey(t, retries) {
		t.Error("overrideCacheKey must differ across num_retries")
	}
	if cacheKey(t, base) == cacheKey(t, budget) {
		t.Error("overrideCacheKey must differ across llm_retry_budget_seconds")
	}
}

func TestMergeOverrideConfig_InheritsAndOverrides(t *testing.T) {
	parent := &BifrostConfig{
		Provider:                 "openai",
		Model:                    "default",
		APIKeyEnvVar:             "PARENT_KEY",
		RequestTimeoutSeconds:    30,
		StreamIdleTimeoutSeconds: 200,
		ExtraHeaders:             map[string]string{"X-Parent": "yes"},
	}
	ov := &BifrostConfig{
		Model:        "override-model",
		APIKeyEnvVar: "OV_KEY",
		ExtraHeaders: map[string]string{"X-Ov": "yes"},
	}
	got := mergeOverrideConfig(parent, ov)
	if got.Provider != "openai" {
		t.Errorf("Provider = %q, want inherit openai", got.Provider)
	}
	if got.Model != "override-model" {
		t.Errorf("Model = %q, want override-model", got.Model)
	}
	if got.APIKeyEnvVar != "OV_KEY" {
		t.Errorf("APIKeyEnvVar = %q, want OV_KEY", got.APIKeyEnvVar)
	}
	if got.RequestTimeoutSeconds != 30 {
		t.Errorf("RequestTimeoutSeconds = %d, want inherit 30", got.RequestTimeoutSeconds)
	}
	if got.StreamIdleTimeoutSeconds != 200 {
		t.Errorf("StreamIdleTimeoutSeconds = %d, want inherit 200", got.StreamIdleTimeoutSeconds)
	}
	if got.ExtraHeaders["X-Parent"] != "yes" || got.ExtraHeaders["X-Ov"] != "yes" {
		t.Errorf("ExtraHeaders not merged: %+v", got.ExtraHeaders)
	}
}

func TestMergeOverrideConfig_StreamIdleTimeoutReachesProvider(t *testing.T) {
	parent := &BifrostConfig{Provider: "anthropic", Model: "default", StreamIdleTimeoutSeconds: 200}
	ov := &BifrostConfig{Model: "claude-opus", StreamIdleTimeoutSeconds: 900}
	sub := mergeOverrideConfig(parent, ov)
	pcfg, err := testConfigAccount(sub).GetConfigForProvider(schemas.Anthropic)
	if err != nil {
		t.Fatalf("GetConfigForProvider: %v", err)
	}
	if got := pcfg.NetworkConfig.StreamIdleTimeoutInSeconds; got != 900 {
		t.Errorf("override StreamIdleTimeoutInSeconds = %d, want 900", got)
	}
}
