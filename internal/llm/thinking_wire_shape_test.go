// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func captureAnthropicRequest(t *testing.T, model string, reasoning *ReasoningConfig) map[string]any {
	t.Helper()
	return captureAnthropicRequestWith(t, model, func(req *ChatRequest) {
		req.Reasoning = reasoning
	})
}

func captureAnthropicRequestWith(t *testing.T, model string, mutate func(*ChatRequest)) map[string]any {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "test-key-123")

	bodies := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		select {
		case bodies <- body:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_test",
			"type": "message",
			"role": "assistant",
			"model": "` + model + `",
			"content": [{"type": "text", "text": "ok"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 1, "output_tokens": 1}
		}`))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider:   "anthropic",
		Model:      model,
		BaseURL:    srv.URL,
		MaxRetries: new(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Shutdown)

	req := &ChatRequest{
		Model:     model,
		Messages:  []ChatMessage{{Role: "user", Content: "Hi"}},
		MaxTokens: 2048,
	}
	mutate(req)
	if _, err := client.ChatCompletion(ctx, req); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	select {
	case raw := <-bodies:
		var sent map[string]any
		if err := json.Unmarshal(raw, &sent); err != nil {
			t.Fatalf("unmarshal captured body: %v\n%s", err, raw)
		}
		return sent
	case <-time.After(15 * time.Second):
		t.Fatal("no request reached the stand-in API")
		return nil
	}
}

func TestThinkingWireShapeByModel(t *testing.T) {
	const budget = 1600
	tests := []struct {
		model    string
		adaptive bool
	}{
		{model: "claude-opus-4-8", adaptive: true},
		{model: "claude-opus-4-7", adaptive: true},
		{model: "claude-sonnet-5", adaptive: true},
		{model: "claude-fable-5", adaptive: true},
		{model: "anthropic.claude-opus-4-8-v1:0", adaptive: true},
		{model: "claude-opus-4-6"},
		{model: "claude-haiku-4-5"},
		{model: "claude-3-5-sonnet-20241022"},
	}

	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			sent := captureAnthropicRequest(t, tc.model, &ReasoningConfig{BudgetTokens: budget})
			thinking, ok := sent["thinking"].(map[string]any)
			if !ok {
				t.Fatalf("no thinking block in the request: %v", sent)
			}
			if tc.adaptive {
				if thinking["type"] != "adaptive" {
					t.Errorf("thinking.type = %v, want adaptive", thinking["type"])
				}
				if _, present := thinking["budget_tokens"]; present {
					t.Errorf("budget_tokens present for an adaptive-only model: %v", thinking)
				}
				return
			}
			if thinking["type"] != "enabled" {
				t.Errorf("thinking.type = %v, want enabled", thinking["type"])
			}
			if got := thinking["budget_tokens"]; got != float64(budget) {
				t.Errorf("budget_tokens = %v, want %d", got, budget)
			}
		})
	}
}

func TestEffortWireShapeByModel(t *testing.T) {
	tests := []struct {
		model    string
		adaptive bool
	}{
		{model: "claude-opus-4-8", adaptive: true},
		{model: "claude-opus-4-7", adaptive: true},
		{model: "claude-sonnet-5", adaptive: true},
		{model: "claude-fable-5", adaptive: true},
		{model: "claude-opus-4-6", adaptive: true},
		{model: "claude-haiku-4-5"},
		{model: "claude-3-5-sonnet-20241022"},
	}

	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			sent := captureAnthropicRequest(t, tc.model, &ReasoningConfig{Effort: "high"})
			thinking, ok := sent["thinking"].(map[string]any)
			if !ok {
				t.Fatalf("no thinking block in the request: %v", sent)
			}
			if tc.adaptive {
				if thinking["type"] != "adaptive" {
					t.Errorf("thinking.type = %v, want adaptive: effort on this model "+
						"used to be converted into a budget it rejects", thinking["type"])
				}
				outputConfig, ok := sent["output_config"].(map[string]any)
				if !ok || outputConfig["effort"] != "high" {
					t.Errorf("effort did not reach output_config, so this row would pass with the level corrupted: %v", sent["output_config"])
				}
				return
			}
			if thinking["type"] != "enabled" {
				t.Errorf("thinking.type = %v, want enabled", thinking["type"])
			}
			if _, present := thinking["budget_tokens"]; !present {
				t.Errorf("no budget derived from effort: %v", thinking)
			}
		})
	}
}

func TestEffortOnALowMaxTokensAgentFailsLoudly(t *testing.T) {
	for _, tc := range []struct {
		model     string
		wantError bool
	}{
		{model: "claude-3-5-sonnet-20241022", wantError: true},
		{model: "claude-opus-4-8"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			err := requestWithSmallMaxTokens(t, tc.model)
			if tc.wantError {
				if err == nil {
					t.Fatal("expected a refusal: no budget can be derived below the minimum")
				}
				if !strings.Contains(err.Error(), "max_tokens") {
					t.Errorf("error = %v, want it to name max_tokens so the cause "+
						"is findable from the message alone", err)
				}
				return
			}
			if err != nil {
				t.Errorf("an adaptive model does not derive a budget, so a small "+
					"max_tokens must not fail: %v", err)
			}
		})
	}
}

func requestWithSmallMaxTokens(t *testing.T, model string) error {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "test-key-123")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant",
			"model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",
			"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider:   "anthropic",
		Model:      model,
		BaseURL:    srv.URL,
		MaxRetries: new(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Shutdown)

	_, err = client.ChatCompletion(ctx, &ChatRequest{
		Model:     model,
		Messages:  []ChatMessage{{Role: "user", Content: "Hi"}},
		MaxTokens: 512,
		Reasoning: &ReasoningConfig{Effort: "high"},
	})
	return err
}

func TestEffortWinsOverBudgetOnTheWire(t *testing.T) {
	sent := captureAnthropicRequest(t, "claude-3-5-sonnet-20241022",
		&ReasoningConfig{Effort: "low", BudgetTokens: 4096})
	thinking, ok := sent["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("no thinking block: %v", sent)
	}
	if got := thinking["budget_tokens"]; got == float64(4096) {
		t.Errorf("budget_tokens = %v: the caller's budget survived, so effort was discarded", got)
	}
	if _, present := thinking["budget_tokens"]; !present {
		t.Errorf("no budget derived from effort on a non-adaptive model: %v", thinking)
	}
}

func TestEffortIsTheOnlyLeverOnAnAdaptiveModel(t *testing.T) {
	const model = "claude-opus-4-8"

	budgetOnly := captureAnthropicRequest(t, model, &ReasoningConfig{BudgetTokens: 1600})
	if _, present := budgetOnly["output_config"]; present {
		t.Errorf("a budget must not invent an effort: %v", budgetOnly["output_config"])
	}

	for _, effort := range []string{"low", "high"} {
		t.Run(effort, func(t *testing.T) {
			sent := captureAnthropicRequest(t, model, &ReasoningConfig{Effort: effort})
			outputConfig, ok := sent["output_config"].(map[string]any)
			if !ok {
				t.Fatalf("no output_config on the wire, so nothing carries the effort: %v", sent)
			}
			if outputConfig["effort"] != effort {
				t.Errorf("output_config.effort = %v, want %q", outputConfig["effort"], effort)
			}
		})
	}
}

func TestUnsetMaxTokensSendsTheModelCeilingNotBifrost4096(t *testing.T) {
	tests := []struct {
		model string
		want  float64
	}{
		{model: "claude-sonnet-5-5", want: 128_000},
		{model: "claude-fable-5-1", want: 128_000},
		{model: "claude-sonnet-4-6", want: 128_000},
		{model: "claude-haiku-4-5", want: 64_000},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			sent := captureAnthropicRequestWith(t, tt.model, func(req *ChatRequest) {
				req.MaxTokens = 0
			})
			if sent["max_tokens"] != tt.want {
				t.Errorf("max_tokens = %v, want %v", sent["max_tokens"], tt.want)
			}
		})
	}
}

func TestModelOverrideUnsetMaxTokensSendsTheOverrideModelsCeiling(t *testing.T) {
	const parentModel = "claude-sonnet-4-6"
	override := &BifrostConfig{Provider: "anthropic", Model: "claude-sonnet-5-5"}
	tests := []struct {
		name         string
		reqMaxTokens int
		want         float64
	}{
		{name: "unset takes the override model's ceiling", want: 128_000},
		{name: "explicit request value wins", reqMaxTokens: 3000, want: 3000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sent := captureAnthropicRequestWith(t, parentModel, func(req *ChatRequest) {
				req.MaxTokens = tt.reqMaxTokens
				req.ModelOverride = override
			})
			if sent["max_tokens"] != tt.want {
				t.Errorf("max_tokens = %v, want %v", sent["max_tokens"], tt.want)
			}
		})
	}
}
