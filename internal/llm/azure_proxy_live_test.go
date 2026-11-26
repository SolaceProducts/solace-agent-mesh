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

func TestLiveAzureThroughProxy(t *testing.T) {
	if os.Getenv("SAM_LIVE_LLM_TEST") == "" {
		t.Skip("set SAM_LIVE_LLM_TEST=1 to call a real endpoint")
	}
	endpoint := os.Getenv("SAM_AZURE_ENDPOINT")
	if endpoint == "" || os.Getenv("AZURE_API_KEY") == "" {
		t.Skip("SAM_AZURE_ENDPOINT and AZURE_API_KEY are required")
	}
	model := os.Getenv("SAM_AZURE_MODEL")
	if model == "" {
		model = "azure-gpt-5-mini"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider:   "azure",
		Model:      model,
		BaseURL:    endpoint,
		MaxRetries: new(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	resp, err := client.ChatCompletion(ctx, &ChatRequest{
		Model:     model,
		Messages:  []ChatMessage{{Role: "user", Content: "Reply with the single word: ok"}},
		MaxTokens: 256,
	})
	if err != nil {
		t.Fatalf("azure-through-proxy call failed for %s at %s: %v", model, endpoint, err)
	}
	t.Logf("model=%s content=%q", model, resp.Content)
}
