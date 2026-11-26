// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type bindingsStubClient struct {
	id string
}

func servedByBinding(t *testing.T, c llm.Client) string {
	t.Helper()
	resp, err := c.ChatCompletion(t.Context(), &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("call through binding: %v", err)
	}
	return resp.Content
}

func (s *bindingsStubClient) ChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: s.id}, nil
}

func (s *bindingsStubClient) ChatCompletionStream(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk, 1)
	ch <- llm.StreamChunk{ContentDelta: s.id, FinishReason: "stop"}
	close(ch)
	return ch, nil
}

func newBindingsRegistryForTest(t *testing.T, preload map[string]string) *bindings.BindingsRegistry {
	t.Helper()
	reg := bindings.New(
		bindings.WithClientBuilder(func(_ context.Context, raw map[string]any, _ *slog.Logger) (llm.Client, error) {
			id, _ := raw["id"].(string)
			return &bindingsStubClient{id: id}, nil
		}),
	)
	for alias, id := range preload {
		if err := reg.UpsertModel(t.Context(), alias, map[string]any{"id": id}); err != nil {
			t.Fatalf("preload %q: %v", alias, err)
		}
	}
	return reg
}

func TestAgentInit_ModelAliasUnbound_SucceedsBecauseBindingsArriveAfterStart(t *testing.T) {
	reg := newBindingsRegistryForTest(t, nil)

	agent := New(AgentConfig{
		Name:       "test-agent",
		Model:      "does-not-matter",
		ModelAlias: "planning",
	})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.bindings = reg

	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init should succeed even with unbound alias (lazy resolution at call time); got: %v", err)
	}

	client := agent.llmClient(t.Context())
	if _, err := client.ChatCompletion(t.Context(), &llm.ChatRequest{}); err == nil {
		t.Fatal("expected ChatCompletion to fail when alias is unbound and no fetcher is configured")
	} else if !strings.Contains(err.Error(), "planning") {
		t.Errorf("error should mention alias name, got: %v", err)
	}
}

func TestAgentInit_ModelAliasBound_Succeeds(t *testing.T) {
	reg := newBindingsRegistryForTest(t, map[string]string{"planning": "planning-v1"})

	agent := New(AgentConfig{
		Name:       "test-agent",
		Model:      "planning-v1",
		ModelAlias: "planning",
	})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.bindings = reg

	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
}

func TestAgentLLMClient_UsesBindingsRegistry(t *testing.T) {
	reg := newBindingsRegistryForTest(t, map[string]string{"planning": "planning-binding"})

	agent := New(AgentConfig{
		Name:       "test-agent",
		Model:      "default-model",
		ModelAlias: "planning",
	})
	defaultLLM := newMockLLM(&llm.ChatResponse{Content: "default"})
	svc := newMockServices(defaultLLM, newMemSessionStore())
	svc.bindings = reg
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	client := agent.llmClient(t.Context())
	if got := servedByBinding(t, client); got != "planning-binding" {
		t.Errorf("bound client id = %q, want planning-binding (registry-supplied)", got)
	}
}

func TestAgentLLMClient_FallsBackToServicesLLM(t *testing.T) {
	agent := New(AgentConfig{
		Name:  "test-agent",
		Model: "static-model",
	})
	defaultLLM := newMockLLM(&llm.ChatResponse{Content: "static"})
	svc := newMockServices(defaultLLM, newMemSessionStore())
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	client := agent.llmClient(t.Context())
	if client != defaultLLM {
		t.Errorf("llmClient = %T, want the Services.LLM() default client", client)
	}
}

func TestAgentLLMClient_LazyFetchViaFetcher(t *testing.T) {
	var fetchCount int
	reg := bindings.New(
		bindings.WithClientBuilder(func(_ context.Context, raw map[string]any, _ *slog.Logger) (llm.Client, error) {
			id, _ := raw["id"].(string)
			return &bindingsStubClient{id: id}, nil
		}),
		bindings.WithFetcher(func(_ context.Context, alias string) (map[string]any, error) {
			fetchCount++
			if alias != "planning" {
				return nil, errors.New("unexpected alias")
			}
			return map[string]any{"id": "fetched-planning"}, nil
		}),
	)

	agent := New(AgentConfig{
		Name:       "test-agent",
		Model:      "planning-v1",
		ModelAlias: "planning",
	})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.bindings = reg
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	c1 := agent.llmClient(t.Context())
	if got := servedByBinding(t, c1); got != "fetched-planning" {
		t.Errorf("fetched client id = %q, want fetched-planning (fetcher-supplied)", got)
	}
	if fetchCount != 1 {
		t.Errorf("fetchCount = %d, want 1", fetchCount)
	}

	_ = agent.llmClient(t.Context())
	if fetchCount != 1 {
		t.Errorf("fetchCount after second call = %d, want 1 (cached)", fetchCount)
	}
}

func TestAgentLLMClient_BindingDeletedAtRuntime(t *testing.T) {
	reg := newBindingsRegistryForTest(t, map[string]string{"planning": "planning-v1"})

	agent := New(AgentConfig{
		Name:       "test-agent",
		Model:      "planning-v1",
		ModelAlias: "planning",
	})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.bindings = reg
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	reg.DeleteModel("planning")

	client := agent.llmClient(t.Context())
	if _, err := client.ChatCompletion(t.Context(), &llm.ChatRequest{}); err == nil {
		t.Fatal("expected ChatCompletion to fail when the alias is unbound")
	} else if !strings.Contains(err.Error(), "planning") {
		t.Errorf("error should mention alias, got: %v", err)
	}
}

func TestAgentLLMClient_BindingDeletedStreamErrors(t *testing.T) {
	reg := newBindingsRegistryForTest(t, map[string]string{"planning": "planning-v1"})

	agent := New(AgentConfig{
		Name:       "test-agent",
		Model:      "planning-v1",
		ModelAlias: "planning",
	})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.bindings = reg
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	reg.DeleteModel("planning")

	client := agent.llmClient(t.Context())
	if _, err := client.ChatCompletionStream(t.Context(), &llm.ChatRequest{}); err == nil {
		t.Fatal("expected ChatCompletionStream to fail when the alias is unbound")
	}
}

func TestAgentInternalLLMCall_RoutesByModel(t *testing.T) {
	reg := newBindingsRegistryForTest(t, map[string]string{"planning": "binding-resp"})

	phaseLLM := newMockLLM(&llm.ChatResponse{Content: "phase-resp"})
	defaultLLM := newMockLLM(&llm.ChatResponse{Content: "default-resp"})

	agent := New(AgentConfig{
		Name:       "test-agent",
		Model:      "planning-v1",
		ModelAlias: "planning",
	})
	svc := newMockServices(defaultLLM, newMemSessionStore())
	svc.llmMap["phase-model"] = phaseLLM
	svc.bindings = reg
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	got, err := agent.internalLLMCall(t.Context(), internalCallSinks{}, "", "sys", "user", 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if got != "binding-resp" {
		t.Errorf("empty model = %q, want binding-resp", got)
	}

	got, err = agent.internalLLMCall(t.Context(), internalCallSinks{}, "planning-v1", "sys", "user", 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if got != "binding-resp" {
		t.Errorf("own model = %q, want binding-resp", got)
	}

	got, err = agent.internalLLMCall(t.Context(), internalCallSinks{}, "phase-model", "sys", "user", 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if got != "phase-resp" {
		t.Errorf("phase model = %q, want phase-resp", got)
	}
}

func TestResolveModelOverrideMetadata_PrefersBindingsRegistry(t *testing.T) {
	fetched := map[string]any{"model": "openai/gpt-4o-mini", "id": "override-target"}
	reg := bindings.New(
		bindings.WithClientBuilder(func(_ context.Context, _ map[string]any, _ *slog.Logger) (llm.Client, error) {
			return &bindingsStubClient{id: "override"}, nil
		}),
		bindings.WithFetcher(func(_ context.Context, alias string) (map[string]any, error) {
			if alias != "override-alias" {
				return nil, errors.New("unexpected alias")
			}
			return fetched, nil
		}),
	)

	resolver := &stubResolver{resp: map[string]any{"model": "legacy/should-not-see"}}

	md := map[string]any{"model_override": map[string]any{"model_id": "override-alias"}}
	cfg, _, errReason := resolveModelOverrideMetadata(
		t.Context(), md, reg, resolver, discardLogger(),
	)
	if errReason != "" {
		t.Fatalf("unexpected errReason: %q", errReason)
	}
	if cfg == nil {
		t.Fatal("expected resolved config from bindings registry")
	}
	if got, _ := cfg["model"].(string); got != "openai/gpt-4o-mini" {
		t.Errorf("resolved model = %q, want openai/gpt-4o-mini (from bindings path, not legacy resolver)", got)
	}
	if resolver.callCount.Load() != 0 {
		t.Errorf("legacy resolver was invoked %d times, must be bypassed when bindings is available",
			resolver.callCount.Load())
	}
	if got, _ := md["model_override"].(map[string]any); got == nil || got["id"] != "override-target" {
		t.Errorf("metadata.model_override not replaced with resolved config: %v", md["model_override"])
	}
}
