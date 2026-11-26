// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestTrimTrailingV1(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"https://host.example.com/v1", "https://host.example.com"},
		{"https://host.example.com/v1/", "https://host.example.com"},
		{"https://host.example.com/v1///", "https://host.example.com"},
		{"https://host.example.com/api/v1", "https://host.example.com/api"},
		{"https://host.example.com", "https://host.example.com"},
		{"https://host.example.com/", "https://host.example.com/"},
		{"https://host.example.com/v1beta", "https://host.example.com/v1beta"},
		{"https://host.example.com/openv1", "https://host.example.com/openv1"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := TrimTrailingV1(tt.in); got != tt.want {
			t.Errorf("TrimTrailingV1(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestUsesOpenAIV1BaseURL(t *testing.T) {
	for _, p := range []string{
		"openai", "openrouter", "groq", "cerebras", "nebius", "xai",
		"mistral", "huggingface", "ollama", "anthropic",
	} {
		if !UsesOpenAIV1BaseURL(p) {
			t.Errorf("UsesOpenAIV1BaseURL(%q) = false, want true", p)
		}
	}
	for _, p := range []string{
		"vertex", "bedrock", "azure", "google", "cohere", "replicate",
		"perplexity", "fireworks", "vllm", "sgl", "parasail", "",
	} {
		if UsesOpenAIV1BaseURL(p) {
			t.Errorf("UsesOpenAIV1BaseURL(%q) = true, want false", p)
		}
	}
}

func TestNewConfigAccount(t *testing.T) {
	cfg := BifrostConfig{
		Provider: "anthropic",
		Model:    "claude-sonnet-4-20250514",
		BaseURL:  "https://example.com",
	}
	acc := newConfigAccount(cfg, "")
	if acc == nil {
		t.Fatal("expected non-nil configAccount")
	}
	if len(acc.providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(acc.providers))
	}
	if string(acc.providers[0]) != "anthropic" {
		t.Errorf("provider = %q, want %q", acc.providers[0], "anthropic")
	}
}

func TestGetConfiguredProviders(t *testing.T) {
	tests := []struct {
		name     string
		provider string
	}{
		{"openai", "openai"},
		{"anthropic", "anthropic"},
		{"google", "google"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acc := newConfigAccount(BifrostConfig{Provider: tt.provider}, "")
			providers, err := acc.GetConfiguredProviders()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(providers) != 1 {
				t.Fatalf("got %d providers, want 1", len(providers))
			}
			if string(providers[0]) != tt.provider {
				t.Errorf("provider = %q, want %q", providers[0], tt.provider)
			}
		})
	}
}

func TestGetKeysForProvider(t *testing.T) {
	tests := []struct {
		name       string
		cfg        BifrostConfig
		envVar     string
		envValue   string
		wantErr    bool
		wantKeyID  string
		wantEnvRef string
	}{
		{
			name:       "default env var for openai",
			cfg:        BifrostConfig{Provider: "openai"},
			envVar:     "OPENAI_API_KEY",
			envValue:   "sk-test-123",
			wantKeyID:  "openai-key",
			wantEnvRef: "env.OPENAI_API_KEY",
		},
		{
			name:       "custom env var",
			cfg:        BifrostConfig{Provider: "openai", APIKeyEnvVar: "MY_CUSTOM_KEY"},
			envVar:     "MY_CUSTOM_KEY",
			envValue:   "custom-key-456",
			wantKeyID:  "openai-key",
			wantEnvRef: "env.MY_CUSTOM_KEY",
		},
		{
			name:    "missing env var returns error",
			cfg:     BifrostConfig{Provider: "openai", APIKeyEnvVar: "NONEXISTENT_KEY_FOR_TEST"},
			envVar:  "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVar != "" {
				t.Setenv(tt.envVar, tt.envValue)
			}
			if tt.wantErr {
				t.Setenv("NONEXISTENT_KEY_FOR_TEST", "")
				t.Setenv(strings.ToUpper(tt.cfg.Provider)+"_API_KEY", "")
			}

			acc := newConfigAccount(tt.cfg, "")
			keys, err := acc.GetKeysForProvider(context.Background(), schemas.ModelProvider(tt.cfg.Provider))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(keys) != 1 {
				t.Fatalf("got %d keys, want 1", len(keys))
			}
			if keys[0].ID != tt.wantKeyID {
				t.Errorf("key ID = %q, want %q", keys[0].ID, tt.wantKeyID)
			}
		})
	}
}

func TestGetConfigForProvider(t *testing.T) {
	tests := []struct {
		name           string
		cfg            BifrostConfig
		wantBaseURL    string
		wantTimeoutSec int
	}{
		{
			name:           "default timeout (600s) when not set",
			cfg:            BifrostConfig{Provider: "openai"},
			wantTimeoutSec: 600,
		},
		{
			name:           "custom timeout",
			cfg:            BifrostConfig{Provider: "openai", RequestTimeoutSeconds: 120},
			wantTimeoutSec: 120,
		},
		{
			name:        "custom base URL without /v1 is unchanged",
			cfg:         BifrostConfig{Provider: "openai", BaseURL: "https://my-gateway.example.com"},
			wantBaseURL: "https://my-gateway.example.com",
		},
		{
			name:        "openai base URL trailing /v1 is stripped (Bifrost re-appends it)",
			cfg:         BifrostConfig{Provider: "openai", BaseURL: "https://my-gateway.example.com/v1"},
			wantBaseURL: "https://my-gateway.example.com",
		},
		{
			name:        "openai base URL trailing /v1/ is stripped",
			cfg:         BifrostConfig{Provider: "openai", BaseURL: "https://my-gateway.example.com/v1/"},
			wantBaseURL: "https://my-gateway.example.com",
		},
		{
			name:        "openai-compatible non-openai provider (groq) /v1 is stripped",
			cfg:         BifrostConfig{Provider: "groq", BaseURL: "https://api.groq.com/openai/v1"},
			wantBaseURL: "https://api.groq.com/openai",
		},
		{
			name:        "anthropic base URL trailing /v1 is stripped (Bifrost re-appends /v1/messages)",
			cfg:         BifrostConfig{Provider: "anthropic", BaseURL: "https://api.anthropic.com/v1"},
			wantBaseURL: "https://api.anthropic.com",
		},
		{
			name:        "anthropic base URL trailing /v1/ is stripped",
			cfg:         BifrostConfig{Provider: "anthropic", BaseURL: "https://api.anthropic.com/v1/"},
			wantBaseURL: "https://api.anthropic.com",
		},
		{
			name:        "anthropic base URL without /v1 is unchanged",
			cfg:         BifrostConfig{Provider: "anthropic", BaseURL: "https://api.anthropic.com"},
			wantBaseURL: "https://api.anthropic.com",
		},
		{
			name:        "bespoke-path provider (perplexity, no /v1 chat path) base URL is preserved",
			cfg:         BifrostConfig{Provider: "perplexity", BaseURL: "https://api.perplexity.ai/v1"},
			wantBaseURL: "https://api.perplexity.ai/v1",
		},
		{
			name: "no base URL uses default",
			cfg:  BifrostConfig{Provider: "openai"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acc := newConfigAccount(tt.cfg, "")
			pcfg, err := acc.GetConfigForProvider(schemas.ModelProvider(tt.cfg.Provider))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if pcfg == nil {
				t.Fatal("expected non-nil ProviderConfig")
			}
			if tt.wantBaseURL != "" && pcfg.NetworkConfig.BaseURL != tt.wantBaseURL {
				t.Errorf("BaseURL = %q, want %q", pcfg.NetworkConfig.BaseURL, tt.wantBaseURL)
			}
			if tt.wantTimeoutSec > 0 && pcfg.NetworkConfig.DefaultRequestTimeoutInSeconds != tt.wantTimeoutSec {
				t.Errorf("timeout = %d, want %d", pcfg.NetworkConfig.DefaultRequestTimeoutInSeconds, tt.wantTimeoutSec)
			}
		})
	}
}

func TestGetConfigForProviderDisablesBifrostRetries(t *testing.T) {
	for name, cfg := range map[string]BifrostConfig{
		"unset":      {Provider: "openai"},
		"configured": {Provider: "openai", MaxRetries: new(5)},
	} {
		acc := newConfigAccount(cfg, "")
		pcfg, err := acc.GetConfigForProvider(schemas.ModelProvider(cfg.Provider))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if pcfg.NetworkConfig.MaxRetries != 0 {
			t.Errorf("%s: NetworkConfig.MaxRetries = %d, want 0 (SAM owns the retry loop)",
				name, pcfg.NetworkConfig.MaxRetries)
		}
	}
}

func TestMaxAttempts(t *testing.T) {
	tests := []struct {
		name            string
		cfg             BifrostConfig
		want            int
		wantRateLimited int
	}{
		{
			name:            "unset uses package default",
			cfg:             BifrostConfig{Provider: "openai"},
			want:            defaultLLMMaxRetries + 1,
			wantRateLimited: defaultRateLimitRetries + 1,
		},
		{
			name:            "explicit zero disables retries",
			cfg:             BifrostConfig{Provider: "openai", MaxRetries: new(0)},
			want:            1,
			wantRateLimited: 1,
		},
		{
			name:            "explicit value honored verbatim",
			cfg:             BifrostConfig{Provider: "openai", MaxRetries: new(5)},
			want:            6,
			wantRateLimited: 6,
		},
		{
			name:            "negative clamped to 0 (never skips the request entirely)",
			cfg:             BifrostConfig{Provider: "openai", MaxRetries: new(-1)},
			want:            1,
			wantRateLimited: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &BifrostClient{cfg: tt.cfg}
			if got := c.maxAttempts(false); got != tt.want {
				t.Errorf("maxAttempts(false) = %d, want %d", got, tt.want)
			}
			if got := c.maxAttempts(true); got != tt.wantRateLimited {
				t.Errorf("maxAttempts(true) = %d, want %d (an explicit num_retries wins)", got, tt.wantRateLimited)
			}
		})
	}
}

func TestRetryBudget(t *testing.T) {
	tests := []struct {
		name string
		cfg  BifrostConfig
		want time.Duration
	}{
		{
			name: "unset uses package default",
			cfg:  BifrostConfig{},
			want: defaultRetryBudget,
		},
		{
			name: "explicit zero admits no retry",
			cfg:  BifrostConfig{RetryBudgetSeconds: new(0)},
			want: 0,
		},
		{
			name: "explicit value honored verbatim",
			cfg:  BifrostConfig{RetryBudgetSeconds: new(30)},
			want: 30 * time.Second,
		},
		{
			name: "negative clamped to 0",
			cfg:  BifrostConfig{RetryBudgetSeconds: new(-5)},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &BifrostClient{cfg: tt.cfg}
			if got := c.retryBudget(); got != tt.want {
				t.Errorf("retryBudget() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetConfigForProviderConcurrency(t *testing.T) {
	tests := []struct {
		name           string
		cfg            BifrostConfig
		wantConcurrent int
		wantBuffer     int
	}{
		{
			name:           "unset uses package defaults",
			cfg:            BifrostConfig{Provider: "openai"},
			wantConcurrent: defaultLLMConcurrency,
			wantBuffer:     schemas.DefaultBufferSize,
		},
		{
			name:           "explicit concurrency honored",
			cfg:            BifrostConfig{Provider: "openai", Concurrency: 50},
			wantConcurrent: 50,
			wantBuffer:     schemas.DefaultBufferSize,
		},
		{
			name:           "explicit buffer size honored",
			cfg:            BifrostConfig{Provider: "openai", Concurrency: 50, BufferSize: 200},
			wantConcurrent: 50,
			wantBuffer:     200,
		},
		{
			name:           "non-positive falls back to defaults",
			cfg:            BifrostConfig{Provider: "openai", Concurrency: 0, BufferSize: -1},
			wantConcurrent: defaultLLMConcurrency,
			wantBuffer:     schemas.DefaultBufferSize,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acc := newConfigAccount(tt.cfg, "")
			pcfg, err := acc.GetConfigForProvider(schemas.ModelProvider(tt.cfg.Provider))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := pcfg.ConcurrencyAndBufferSize.Concurrency; got != tt.wantConcurrent {
				t.Errorf("Concurrency = %d, want %d", got, tt.wantConcurrent)
			}
			if got := pcfg.ConcurrencyAndBufferSize.BufferSize; got != tt.wantBuffer {
				t.Errorf("BufferSize = %d, want %d", got, tt.wantBuffer)
			}
		})
	}
}

func mustBuildRequest(t *testing.T, c *BifrostClient, req *ChatRequest) *schemas.BifrostChatRequest {
	t.Helper()
	bfReq, err := c.buildRequest(req)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	return bfReq
}

func TestBuildRequestModelFallback(t *testing.T) {
	tests := []struct {
		name      string
		cfgModel  string
		reqModel  string
		wantModel string
		wantErr   bool
	}{
		{name: "uses request model when set", cfgModel: "default-model", reqModel: "custom-model", wantModel: "custom-model"},
		{name: "falls back to config model when empty", cfgModel: "default-model", wantModel: "default-model"},
		{name: "rejects the request when both are empty", wantErr: true},
		{name: "rejects a whitespace-only config model", cfgModel: " ", wantErr: true},
		{name: "rejects a whitespace-only request model", cfgModel: "default-model", reqModel: "\t", wantErr: true},
		{name: "request model carries a blank config model", reqModel: "gpt-4", wantModel: "gpt-4"},
		{name: "passes a model with surrounding space through verbatim", cfgModel: " padded-model ", wantModel: " padded-model "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &BifrostClient{
				cfg: BifrostConfig{Provider: "openai", Model: tt.cfgModel},
			}
			req := &ChatRequest{
				Model:    tt.reqModel,
				Messages: []ChatMessage{{Role: "user", Content: "hi"}},
			}
			bfReq, err := client.buildRequest(req)
			if tt.wantErr {
				if !errors.Is(err, ErrNoModel) {
					t.Fatalf("error = %v, want ErrNoModel", err)
				}
				if bfReq != nil {
					t.Errorf("request = %+v, want nil", bfReq)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildRequest: %v", err)
			}
			if bfReq.Model != tt.wantModel {
				t.Errorf("model = %q, want %q", bfReq.Model, tt.wantModel)
			}
		})
	}
}

func TestBuildRequestSanitizesInvalidUTF8(t *testing.T) {
	client := &BifrostClient{
		cfg: BifrostConfig{Provider: "anthropic", Model: "test-model"},
	}
	broken := "snippet\xc2..."
	req := &ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: broken},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "search", Arguments: "{\"query\":\"caf\xc3\"}"}}},
			{Role: "tool", ToolCallID: "c1", ContentBlocks: []ContentBlock{{Type: "text", Text: broken}}},
		},
	}
	bfReq := mustBuildRequest(t, client, req)

	got := *bfReq.Input[0].Content.ContentStr
	if got != "snippet�..." {
		t.Errorf("message content = %q, want %q", got, "snippet�...")
	}
	blockText := *bfReq.Input[2].Content.ContentBlocks[0].Text
	if !utf8.ValidString(blockText) {
		t.Errorf("block text = %q, still invalid UTF-8", blockText)
	}
	args := bfReq.Input[1].ToolCalls[0].Function.Arguments
	if !utf8.ValidString(args) {
		t.Errorf("tool arguments = %q, still invalid UTF-8", args)
	}
	if args != "{\"query\":\"caf�\"}" {
		t.Errorf("tool arguments = %q, want %q", args, "{\"query\":\"caf�\"}")
	}
}

func TestBuildRequestToolChoice(t *testing.T) {
	client := &BifrostClient{
		cfg: BifrostConfig{Provider: "openai", Model: "test-model"},
	}

	tests := []struct {
		name       string
		toolChoice string
		wantNil    bool
		wantChoice string
	}{
		{"empty tool choice means no params.ToolChoice", "", true, ""},
		{"auto", "auto", false, "auto"},
		{"none", "none", false, "none"},
		{"required", "required", false, "required"},
		{"specific tool name", "my_tool", false, "my_tool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &ChatRequest{
				Messages:   []ChatMessage{{Role: "user", Content: "hi"}},
				ToolChoice: tt.toolChoice,
			}
			bfReq := mustBuildRequest(t, client, req)
			if tt.wantNil {
				if bfReq.Params != nil && bfReq.Params.ToolChoice != nil {
					t.Error("expected nil ToolChoice")
				}
				return
			}
			if bfReq.Params == nil || bfReq.Params.ToolChoice == nil {
				t.Fatal("expected non-nil ToolChoice")
			}
			if bfReq.Params.ToolChoice.ChatToolChoiceStr == nil {
				t.Fatal("expected non-nil ChatToolChoiceStr")
			}
			if *bfReq.Params.ToolChoice.ChatToolChoiceStr != tt.wantChoice {
				t.Errorf("ToolChoice = %q, want %q", *bfReq.Params.ToolChoice.ChatToolChoiceStr, tt.wantChoice)
			}
		})
	}
}

func TestBuildRequestMaxTokens(t *testing.T) {
	const unset = 0
	tests := []struct {
		name         string
		provider     string
		model        string
		reqMaxTokens int
		want         int
	}{
		{name: "zero means no param", provider: "openai", model: "test-model", want: unset},
		{name: "positive value sets param", provider: "openai", model: "test-model", reqMaxTokens: 1024, want: 1024},
		{name: "request value beats the family ceiling", provider: "anthropic", model: "claude-sonnet-5-5", reqMaxTokens: 1024, want: 1024},
		{name: "negative request value falls through to the family ceiling", provider: "anthropic", model: "claude-sonnet-5-5", reqMaxTokens: -1, want: 128_000},
		{name: "anthropic sonnet 5.5 gets the family ceiling", provider: "anthropic", model: "claude-sonnet-5-5", want: 128_000},
		{name: "bedrock fable 5.1 gets the family ceiling", provider: "bedrock", model: "global.anthropic.claude-fable-5-1", want: 128_000},
		{name: "vertex opus 5.5 gets the family ceiling", provider: "vertex", model: "claude-opus-5-5", want: 128_000},
		{name: "azure haiku 5.5 gets the family ceiling", provider: "azure", model: "claude-haiku-5-5", want: 128_000},
		{name: "older claude is left to bifrost", provider: "anthropic", model: "claude-sonnet-4-6", want: unset},
		{name: "openai-compatible proxy is left to the proxy", provider: "openai", model: "claude-sonnet-5-5", want: unset},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &BifrostClient{
				cfg: BifrostConfig{Provider: tt.provider, Model: tt.model},
			}
			req := &ChatRequest{
				Messages:  []ChatMessage{{Role: "user", Content: "hi"}},
				MaxTokens: tt.reqMaxTokens,
			}
			bfReq := mustBuildRequest(t, client, req)
			if tt.want == unset {
				if bfReq.Params != nil && bfReq.Params.MaxCompletionTokens != nil {
					t.Fatalf("MaxCompletionTokens = %d, want nil", *bfReq.Params.MaxCompletionTokens)
				}
				return
			}
			if bfReq.Params == nil || bfReq.Params.MaxCompletionTokens == nil {
				t.Fatalf("MaxCompletionTokens = nil, want %d", tt.want)
			}
			if got := *bfReq.Params.MaxCompletionTokens; got != tt.want {
				t.Errorf("MaxCompletionTokens = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBuildRequestTemperature(t *testing.T) {
	client := &BifrostClient{
		cfg: BifrostConfig{Provider: "openai", Model: "test-model"},
	}

	temp := 0.7
	tests := []struct {
		name    string
		temp    *float64
		wantSet bool
	}{
		{"nil means no param", nil, false},
		{"non-nil sets param", &temp, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &ChatRequest{
				Messages:    []ChatMessage{{Role: "user", Content: "hi"}},
				Temperature: tt.temp,
			}
			bfReq := mustBuildRequest(t, client, req)
			if !tt.wantSet {
				if bfReq.Params != nil && bfReq.Params.Temperature != nil {
					t.Error("expected nil Temperature")
				}
				return
			}
			if bfReq.Params == nil || bfReq.Params.Temperature == nil {
				t.Fatal("expected non-nil Temperature")
			}
			if *bfReq.Params.Temperature != temp {
				t.Errorf("Temperature = %f, want %f", *bfReq.Params.Temperature, temp)
			}
		})
	}
}

func TestBuildRequestStopSequences(t *testing.T) {
	client := &BifrostClient{
		cfg: BifrostConfig{Provider: "openai", Model: "test-model"},
	}

	tests := []struct {
		name    string
		stops   []string
		wantSet bool
	}{
		{"nil means no param", nil, false},
		{"empty means no param", []string{}, false},
		{"non-empty sets param", []string{"STOP", "END"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &ChatRequest{
				Messages:      []ChatMessage{{Role: "user", Content: "hi"}},
				StopSequences: tt.stops,
			}
			bfReq := mustBuildRequest(t, client, req)
			if !tt.wantSet {
				if bfReq.Params != nil && len(bfReq.Params.Stop) > 0 {
					t.Error("expected empty Stop")
				}
				return
			}
			if bfReq.Params == nil {
				t.Fatal("expected non-nil Params")
			}
			if len(bfReq.Params.Stop) != len(tt.stops) {
				t.Fatalf("Stop len = %d, want %d", len(bfReq.Params.Stop), len(tt.stops))
			}
			for i, s := range tt.stops {
				if bfReq.Params.Stop[i] != s {
					t.Errorf("Stop[%d] = %q, want %q", i, bfReq.Params.Stop[i], s)
				}
			}
		})
	}
}

func TestBuildRequestReasoning(t *testing.T) {
	client := &BifrostClient{
		cfg: BifrostConfig{Provider: "anthropic", Model: "test-model"},
	}

	tests := []struct {
		name      string
		reasoning *ReasoningConfig
		wantSet   bool
	}{
		{"nil reasoning means no param", nil, false},
		{"zero budget means no param", &ReasoningConfig{BudgetTokens: 0}, false},
		{"positive budget sets param", &ReasoningConfig{BudgetTokens: 4096}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &ChatRequest{
				Messages:  []ChatMessage{{Role: "user", Content: "hi"}},
				Reasoning: tt.reasoning,
			}
			bfReq := mustBuildRequest(t, client, req)
			if !tt.wantSet {
				if bfReq.Params != nil && bfReq.Params.Reasoning != nil {
					t.Error("expected nil Reasoning")
				}
				return
			}
			if bfReq.Params == nil || bfReq.Params.Reasoning == nil {
				t.Fatal("expected non-nil Reasoning")
			}
			if bfReq.Params.Reasoning.MaxTokens == nil {
				t.Fatal("expected non-nil Reasoning.MaxTokens")
			}
			if *bfReq.Params.Reasoning.MaxTokens != 4096 {
				t.Errorf("Reasoning.MaxTokens = %d, want 4096", *bfReq.Params.Reasoning.MaxTokens)
			}
		})
	}
}

func TestBuildRequestNoParams(t *testing.T) {
	client := &BifrostClient{
		cfg: BifrostConfig{Provider: "openai", Model: "test-model"},
	}
	req := &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}
	bfReq := mustBuildRequest(t, client, req)
	if bfReq.Params != nil {
		t.Error("expected nil Params when no optional fields set")
	}
}

func TestBuildRequestMultipleParams(t *testing.T) {
	client := &BifrostClient{
		cfg: BifrostConfig{Provider: "openai", Model: "test-model"},
	}
	temp := 0.5
	req := &ChatRequest{
		Messages:      []ChatMessage{{Role: "user", Content: "hi"}},
		MaxTokens:     2048,
		Temperature:   &temp,
		StopSequences: []string{"END"},
		ToolChoice:    "auto",
		Tools: []ToolDefinition{
			{Name: "search", Description: "search the web"},
		},
	}
	bfReq := mustBuildRequest(t, client, req)
	if bfReq.Params == nil {
		t.Fatal("expected non-nil Params")
	}
	if bfReq.Params.MaxCompletionTokens == nil || *bfReq.Params.MaxCompletionTokens != 2048 {
		t.Error("MaxCompletionTokens not set correctly")
	}
	if bfReq.Params.Temperature == nil || *bfReq.Params.Temperature != 0.5 {
		t.Error("Temperature not set correctly")
	}
	if len(bfReq.Params.Stop) != 1 || bfReq.Params.Stop[0] != "END" {
		t.Error("Stop not set correctly")
	}
	if bfReq.Params.ToolChoice == nil {
		t.Error("ToolChoice not set")
	}
	if len(bfReq.Params.Tools) != 1 {
		t.Error("Tools not set correctly")
	}
}

func TestParseNonStreamResponseNil(t *testing.T) {
	client := &BifrostClient{}
	resp := client.parseNonStreamResponse(nil)
	if resp == nil {
		t.Fatal("expected non-nil response for nil input")
	}
	if resp.Content != "" {
		t.Errorf("Content = %q, want empty", resp.Content)
	}
	if resp.FinishReason != "" {
		t.Errorf("FinishReason = %q, want empty", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("ToolCalls len = %d, want 0", len(resp.ToolCalls))
	}
	if resp.Usage != nil {
		t.Error("expected nil Usage")
	}
}

func TestParseNonStreamResponseEmptyChoices(t *testing.T) {
	client := &BifrostClient{}
	resp := client.parseNonStreamResponse(&schemas.BifrostChatResponse{
		Choices: []schemas.BifrostResponseChoice{},
	})
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Content != "" {
		t.Errorf("Content = %q, want empty", resp.Content)
	}
}

func TestParseNonStreamResponseWithReasoningTokens(t *testing.T) {
	client := &BifrostClient{}
	resp := client.parseNonStreamResponse(&schemas.BifrostChatResponse{
		Choices: []schemas.BifrostResponseChoice{
			{
				FinishReason: schemas.Ptr("stop"),
			},
		},
		Usage: &schemas.BifrostLLMUsage{
			PromptTokens:     50,
			CompletionTokens: 100,
			TotalTokens:      150,
			CompletionTokensDetails: &schemas.ChatCompletionTokensDetails{
				ReasoningTokens: 30,
			},
		},
	})
	if resp.Usage == nil {
		t.Fatal("expected non-nil Usage")
	}
	if resp.Usage.ReasoningTokens != 30 {
		t.Errorf("ReasoningTokens = %d, want 30", resp.Usage.ReasoningTokens)
	}
}

func TestParseNonStreamResponseFinishReasonNil(t *testing.T) {
	client := &BifrostClient{}
	resp := client.parseNonStreamResponse(&schemas.BifrostChatResponse{
		Choices: []schemas.BifrostResponseChoice{
			{},
		},
	})
	if resp.FinishReason != "" {
		t.Errorf("FinishReason = %q, want empty", resp.FinishReason)
	}
}

func TestParseStreamChunkEmptyChoices(t *testing.T) {
	client := &BifrostClient{}
	sc := client.parseStreamChunk(&schemas.BifrostChatResponse{
		Choices: []schemas.BifrostResponseChoice{},
	})
	if sc.ContentDelta != "" {
		t.Errorf("ContentDelta = %q, want empty", sc.ContentDelta)
	}
	if sc.FinishReason != "" {
		t.Errorf("FinishReason = %q, want empty", sc.FinishReason)
	}
}

func TestParseStreamChunkWithUsage(t *testing.T) {
	client := &BifrostClient{}
	sc := client.parseStreamChunk(&schemas.BifrostChatResponse{
		Choices: []schemas.BifrostResponseChoice{
			{FinishReason: schemas.Ptr("stop")},
		},
		Usage: &schemas.BifrostLLMUsage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
			PromptTokensDetails: &schemas.ChatPromptTokensDetails{
				CachedReadTokens: 5,
			},
			CompletionTokensDetails: &schemas.ChatCompletionTokensDetails{
				ReasoningTokens: 8,
			},
		},
	})
	if sc.Usage == nil {
		t.Fatal("expected non-nil Usage")
	}
	if sc.Usage.CachedTokens != 5 {
		t.Errorf("CachedTokens = %d, want 5", sc.Usage.CachedTokens)
	}
	if sc.Usage.ReasoningTokens != 8 {
		t.Errorf("ReasoningTokens = %d, want 8", sc.Usage.ReasoningTokens)
	}
}

func TestParseStreamChunkWithReasoningDelta(t *testing.T) {
	client := &BifrostClient{}
	reasoning := "thinking step 1"
	sc := client.parseStreamChunk(&schemas.BifrostChatResponse{
		Choices: []schemas.BifrostResponseChoice{
			{
				ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
					Delta: &schemas.ChatStreamResponseChoiceDelta{
						Reasoning: &reasoning,
					},
				},
			},
		},
	})
	if sc.ReasoningDelta != reasoning {
		t.Errorf("ReasoningDelta = %q, want %q", sc.ReasoningDelta, reasoning)
	}
}

func TestParseStreamChunkWithToolCallDeltas(t *testing.T) {
	client := &BifrostClient{}
	sc := client.parseStreamChunk(&schemas.BifrostChatResponse{
		Choices: []schemas.BifrostResponseChoice{
			{
				ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
					Delta: &schemas.ChatStreamResponseChoiceDelta{
						ToolCalls: []schemas.ChatAssistantMessageToolCall{
							{
								Index: 0,
								ID:    schemas.Ptr("call-1"),
								Function: schemas.ChatAssistantMessageToolCallFunction{
									Name:      schemas.Ptr("my_tool"),
									Arguments: `{"key":"val`,
								},
							},
							{
								Index: 1,
								Function: schemas.ChatAssistantMessageToolCallFunction{
									Arguments: `ue"}`,
								},
							},
						},
					},
				},
			},
		},
	})
	if len(sc.ToolCallDeltas) != 2 {
		t.Fatalf("ToolCallDeltas len = %d, want 2", len(sc.ToolCallDeltas))
	}
	if sc.ToolCallDeltas[0].ID != "call-1" {
		t.Errorf("delta[0].ID = %q, want %q", sc.ToolCallDeltas[0].ID, "call-1")
	}
	if sc.ToolCallDeltas[0].Name != "my_tool" {
		t.Errorf("delta[0].Name = %q, want %q", sc.ToolCallDeltas[0].Name, "my_tool")
	}
	if sc.ToolCallDeltas[1].Index != 1 {
		t.Errorf("delta[1].Index = %d, want 1", sc.ToolCallDeltas[1].Index)
	}
	if sc.ToolCallDeltas[1].ID != "" {
		t.Errorf("delta[1].ID = %q, want empty", sc.ToolCallDeltas[1].ID)
	}
}

func TestConvertBifrostToolCallsEmpty(t *testing.T) {
	result := convertBifrostToolCalls(nil)
	if result != nil {
		t.Errorf("expected nil for nil input, got %v", result)
	}

	result = convertBifrostToolCalls([]schemas.ChatAssistantMessageToolCall{})
	if result != nil {
		t.Errorf("expected nil for empty input, got %v", result)
	}
}

func TestConvertBifrostToolCallsMultiple(t *testing.T) {
	tcs := []schemas.ChatAssistantMessageToolCall{
		{
			ID:   schemas.Ptr("call-1"),
			Type: schemas.Ptr("function"),
			Function: schemas.ChatAssistantMessageToolCallFunction{
				Name:      schemas.Ptr("tool_a"),
				Arguments: `{"x":1}`,
			},
		},
		{
			ID:   schemas.Ptr("call-2"),
			Type: schemas.Ptr("function"),
			Function: schemas.ChatAssistantMessageToolCallFunction{
				Name:      schemas.Ptr("tool_b"),
				Arguments: `{"y":2}`,
			},
		},
		{
			Function: schemas.ChatAssistantMessageToolCallFunction{
				Arguments: `{}`,
			},
		},
	}

	result := convertBifrostToolCalls(tcs)
	if len(result) != 3 {
		t.Fatalf("got %d tool calls, want 3", len(result))
	}
	if result[0].ID != "call-1" || result[0].Name != "tool_a" || result[0].Arguments != `{"x":1}` {
		t.Errorf("result[0] = %+v", result[0])
	}
	if result[1].ID != "call-2" || result[1].Name != "tool_b" || result[1].Arguments != `{"y":2}` {
		t.Errorf("result[1] = %+v", result[1])
	}
	if result[2].ID != "" {
		t.Errorf("result[2].ID = %q, want empty", result[2].ID)
	}
	if result[2].Name != "" {
		t.Errorf("result[2].Name = %q, want empty", result[2].Name)
	}
}

func TestErrorIsRetryable(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		want       bool
	}{
		{"429 is retryable", 429, true},
		{"500 is retryable", 500, true},
		{"502 is retryable", 502, true},
		{"400 is not retryable", 400, false},
		{"200 is not retryable", 200, false},
		{"0 is not retryable", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Error{Message: "test", StatusCode: tt.statusCode}
			if got := e.IsRetryable(); got != tt.want {
				t.Errorf("IsRetryable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConvertMessageWhitespaceOnlyContent(t *testing.T) {
	msg := ChatMessage{Role: "tool", Content: "   \n\t  ", ToolCallID: "tc-1"}
	cm := convertMessage(msg)
	if cm.Content != nil {
		t.Error("expected nil Content for whitespace-only message")
	}
	if cm.ChatToolMessage == nil {
		t.Error("expected ChatToolMessage to be set")
	}
}

func TestSanitizeToolArguments(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"valid object passes through", `{"a":1}`, `{"a":1}`},
		{"empty object passes through", `{}`, `{}`},
		{"nested object passes through", `{"a":{"b":1}}`, `{"a":{"b":1}}`},
		{"truncated json coerced", `{"method":"GET","path":"/query`, `{}`},
		{"empty string coerced", "", `{}`},
		{"whitespace coerced", "  \n\t", `{}`},
		{"non-json coerced", "not json", `{}`},
		{"array coerced", `[1,2]`, `{}`},
		{"scalar coerced", `42`, `{}`},
		{"json string coerced", `"hi"`, `{}`},
		{"null coerced", `null`, `{}`},
	}
	for _, tc := range cases {
		if got := sanitizeToolArguments(tc.in); got != tc.want {
			t.Errorf("%s: sanitizeToolArguments(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestConvertMessageSanitizesTruncatedToolArgs(t *testing.T) {
	msg := ChatMessage{
		Role: "assistant",
		ToolCalls: []ToolCall{
			{ID: "toolu_trunc", Name: "web_request", Arguments: `{"method":"GET","path":"/query`},
			{ID: "toolu_ok", Name: "calc", Arguments: `{"x":1}`},
		},
	}
	cm := convertMessage(msg)
	if len(cm.ToolCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(cm.ToolCalls))
	}
	if got := cm.ToolCalls[0].Function.Arguments; got != "{}" {
		t.Errorf("truncated tool args: got %q, want {}", got)
	}
	if got := cm.ToolCalls[1].Function.Arguments; got != `{"x":1}` {
		t.Errorf("valid tool args should pass through: got %q", got)
	}
}

func TestWarnOnInvalidToolArgs(t *testing.T) {
	var buf bytes.Buffer
	c := &BifrostClient{logger: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))}

	c.warnOnInvalidToolArgs([]ChatMessage{{
		Role: "assistant",
		ToolCalls: []ToolCall{
			{ID: "t1", Name: "calc", Arguments: `{"x":1}`},
			{ID: "t2", Name: "noop", Arguments: ""},
		},
	}})
	if buf.Len() != 0 {
		t.Fatalf("expected no warn for valid/empty args, got: %s", buf.String())
	}

	c.warnOnInvalidToolArgs([]ChatMessage{{
		Role: "assistant",
		ToolCalls: []ToolCall{
			{ID: "t3", Name: "web_request", Arguments: `{"method":"GET`},
			{ID: "t4", Name: "list", Arguments: `[1,2]`},
		},
	}})
	out := buf.String()
	if !strings.Contains(out, "coercing invalid tool-call arguments") {
		t.Errorf("expected a coercion warn, got: %s", out)
	}
	if !strings.Contains(out, "toolCallsAffected=2") {
		t.Errorf("expected toolCallsAffected=2, got: %s", out)
	}
	if n := strings.Count(out, "level=WARN"); n != 1 {
		t.Errorf("expected exactly one aggregated WARN, got %d: %s", n, out)
	}
}
