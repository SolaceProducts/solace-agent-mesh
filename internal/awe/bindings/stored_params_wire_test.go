// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bindings

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func streamingProvider(t *testing.T) (*httptest.Server, func() []map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"id":"c","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"finish_reason":"stop","delta":{"content":"ok"}}]}`+"\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	return server, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any(nil), bodies...)
	}
}

func platformModelConfig(baseURL, modelName string, storedParams map[string]any) map[string]any {
	raw := map[string]any{"model": "openai/" + modelName, "api_base": baseURL, "api_key": "sk-test"}
	for key, value := range storedParams {
		raw[key] = value
	}
	samapp.WithModelParams(raw, storedParams)
	return raw
}

func drain(t *testing.T, stream <-chan llm.StreamChunk) {
	t.Helper()
	for chunk := range stream {
		if chunk.Err != nil {
			t.Fatalf("stream: %v", chunk.Err)
		}
	}
}

func agentTurn() *llm.ChatRequest {
	return &llm.ChatRequest{Messages: []llm.ChatMessage{{Role: "user", Content: "Hi"}}, WithStoredParams: true}
}

func TestBoundModel_AgentTurnCarriesTheStoredParams(t *testing.T) {
	t.Parallel()
	provider, sent := streamingProvider(t)
	registry := New()
	t.Cleanup(registry.Close)

	stored := map[string]any{"temperature": 0.2, "top_p": 0.9, "cache_strategy": "5m"}
	if err := registry.UpsertModel(t.Context(), "general", platformModelConfig(provider.URL, "gpt-4o", stored)); err != nil {
		t.Fatal(err)
	}
	client, err := registry.ModelClient("general")
	if err != nil {
		t.Fatal(err)
	}

	stream, err := client.ChatCompletionStream(t.Context(), agentTurn())
	if err != nil {
		t.Fatal(err)
	}
	drain(t, stream)

	bodies := sent()
	if len(bodies) != 1 {
		t.Fatalf("provider received %d requests, want 1", len(bodies))
	}
	body := bodies[0]
	if body["temperature"] != 0.2 || body["top_p"] != 0.9 {
		t.Errorf("stored temperature and top_p did not reach the provider (body: %v)", body)
	}
	for _, notAParam := range []string{"cache_strategy", samapp.ModelParamsKey, samapp.ModelConfigIDKey, "api_key", "api_base"} {
		if _, carries := body[notAParam]; carries {
			t.Errorf("%s reached the provider's request body (body: %v)", notAParam, body)
		}
	}
}

func TestBoundModel_TaskModelOverrideCarriesTheOverrideModelsStoredParams(t *testing.T) {
	t.Parallel()
	provider, sent := streamingProvider(t)
	registry := New()
	t.Cleanup(registry.Close)

	if err := registry.UpsertModel(t.Context(), "general",
		platformModelConfig(provider.URL, "gpt-4o", map[string]any{"temperature": 0.2})); err != nil {
		t.Fatal(err)
	}
	if err := registry.UpsertModel(t.Context(), "planning",
		platformModelConfig(provider.URL, "gpt-4o-mini", map[string]any{"top_p": 0.5})); err != nil {
		t.Fatal(err)
	}
	client, err := registry.ModelClient("general")
	if err != nil {
		t.Fatal(err)
	}
	overrideRaw, err := registry.ResolveRawOrFetch(t.Context(), "planning")
	if err != nil {
		t.Fatal(err)
	}
	override, err := samapp.ParseModelConfig(overrideRaw, nil)
	if err != nil {
		t.Fatal(err)
	}

	req := agentTurn()
	req.ModelOverride = override
	stream, err := client.ChatCompletionStream(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	drain(t, stream)

	body := sent()[0]
	if body["model"] != "gpt-4o-mini" || body["top_p"] != 0.5 {
		t.Errorf("the override model's stored top_p did not reach the provider (body: %v)", body)
	}
	if _, carries := body["temperature"]; carries {
		t.Errorf("the agent's own model's stored temperature reached the override model (body: %v)", body)
	}
}

func TestBoundModel_StoredParamsNamingRequestFieldsDoNotChangeTheAgentsRequest(t *testing.T) {
	t.Parallel()
	provider, sent := streamingProvider(t)
	registry := New()
	t.Cleanup(registry.Close)

	stored := map[string]any{"stream": false, "tools": []any{}, "tool_choice": "none", "top_p": 0.9}
	if err := registry.UpsertModel(t.Context(), "general", platformModelConfig(provider.URL, "gpt-4o", stored)); err != nil {
		t.Fatal(err)
	}
	client, err := registry.ModelClient("general")
	if err != nil {
		t.Fatal(err)
	}

	req := agentTurn()
	req.Tools = []llm.ToolDefinition{{Name: "get_time", Description: "t", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}}
	req.ToolChoice = "auto"
	stream, err := client.ChatCompletionStream(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	drain(t, stream)

	body := sent()[0]
	if body["stream"] != true || body["tool_choice"] != "auto" {
		t.Errorf("stream and tool_choice on the wire = %v, %v; want true, auto", body["stream"], body["tool_choice"])
	}
	if tools, _ := body["tools"].([]any); len(tools) != 1 {
		t.Errorf("tools on the wire = %v, want the agent's one tool", body["tools"])
	}
	if body["top_p"] != 0.9 {
		t.Errorf("top_p = %v, want the stored 0.9", body["top_p"])
	}
}
