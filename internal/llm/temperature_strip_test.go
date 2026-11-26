// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestTemperatureStrippedForAdaptiveOnlyModels(t *testing.T) {
	tests := []struct {
		model    string
		wantTemp bool
	}{
		{model: "claude-sonnet-5"},
		{model: "claude-opus-4-8"},
		{model: "claude-fable-5"},
		{model: "claude-sonnet-4-6", wantTemp: true},
		{model: "claude-haiku-4-5", wantTemp: true},
	}

	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			temp := 0.7
			sent := captureAnthropicRequestWith(t, tc.model, func(req *ChatRequest) {
				req.Temperature = &temp
			})
			_, present := sent["temperature"]
			if present != tc.wantTemp {
				t.Errorf("temperature present = %v, want %v (body: %v)",
					present, tc.wantTemp, sent)
			}
		})
	}
}

func TestLiveTemperatureOnSonnet5(t *testing.T) {
	if os.Getenv("SAM_LIVE_LLM_TEST") == "" {
		t.Skip("set SAM_LIVE_LLM_TEST=1 to call the real API")
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY is not set")
	}

	model := os.Getenv("SAM_LIVE_MODEL")
	if model == "" {
		model = "claude-sonnet-5"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider:   "anthropic",
		Model:      model,
		MaxRetries: new(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	temp := 0.7
	resp, err := client.ChatCompletion(ctx, &ChatRequest{
		Model:       model,
		Messages:    []ChatMessage{{Role: "user", Content: "Reply with the single word: ok"}},
		MaxTokens:   256,
		Temperature: &temp,
	})
	if err != nil {
		t.Fatalf("live call with a temperature failed on %s: %v", model, err)
	}
	t.Logf("model=%s content=%q", model, resp.Content)
}
