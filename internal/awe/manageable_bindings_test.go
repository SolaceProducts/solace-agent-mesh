// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type stubModelClient struct{}

func (stubModelClient) ChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}

func (stubModelClient) ChatCompletionStream(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk)
	close(ch)
	return ch, nil
}

func newExecutorWithStubBuilder() *Executor {
	exe := NewExecutor(newTestServices())
	exe.bindings = bindings.New(
		bindings.WithClientBuilder(func(_ context.Context, _ map[string]any, _ *slog.Logger) (llm.Client, error) {
			return stubModelClient{}, nil
		}),
	)
	return exe
}

func TestHandleGetBindings_Empty(t *testing.T) {
	exe := newExecutorWithStubBuilder()
	result, err := exe.HandleManagement(context.Background(), "get", []string{"bindings"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	snap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map[string]any", result)
	}
	models, _ := snap["models"].(map[string]any)
	if len(models) != 0 {
		t.Errorf("expected empty models map, got %v", models)
	}
}

func TestHandlePatchBindings_ValidSparsePatch(t *testing.T) {
	exe := newExecutorWithStubBuilder()
	body := json.RawMessage(`{"models":{"planning":{"model":"openai/gpt-4"}}}`)
	result, err := exe.HandleManagement(context.Background(), "patch", []string{"bindings"}, body, nil)
	if err != nil {
		t.Fatal(err)
	}
	snap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map[string]any", result)
	}
	models, _ := snap["models"].(map[string]any)
	if _, ok := models["planning"]; !ok {
		t.Errorf("planning alias not present in snapshot: %v", snap)
	}
}

func TestHandlePatchBindings_MultiFieldSparsePatch(t *testing.T) {
	exe := newExecutorWithStubBuilder()
	body := json.RawMessage(`{"models":{"planning":{"model":"openai/gpt-4"},"general":{"model":"openai/gpt-3.5"}}}`)
	_, err := exe.HandleManagement(context.Background(), "patch", []string{"bindings"}, body, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := exe.HandleManagement(context.Background(), "get", []string{"bindings"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	models := result.(map[string]any)["models"].(map[string]any)
	if _, ok := models["planning"]; !ok {
		t.Error("planning alias missing")
	}
	if _, ok := models["general"]; !ok {
		t.Error("general alias missing")
	}
}

func TestHandlePatchBindings_EmptyBody(t *testing.T) {
	exe := newExecutorWithStubBuilder()
	_, err := exe.HandleManagement(context.Background(), "patch", []string{"bindings"}, nil, nil)
	if err == nil {
		t.Fatal("expected error for empty body")
	}
}

func TestHandlePatchBindings_InvalidJSON(t *testing.T) {
	exe := newExecutorWithStubBuilder()
	_, err := exe.HandleManagement(context.Background(), "patch", []string{"bindings"}, json.RawMessage(`not json`), nil)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestHandlePatchBindings_UnknownTopLevelField(t *testing.T) {
	exe := newExecutorWithStubBuilder()
	body := json.RawMessage(`{"tools":{"foo":{}}}`)
	_, err := exe.HandleManagement(context.Background(), "patch", []string{"bindings"}, body, nil)
	if err == nil {
		t.Fatal("expected error for unknown top-level field")
	}
}

func TestHandlePatchBindings_EmptyModels(t *testing.T) {
	exe := newExecutorWithStubBuilder()
	body := json.RawMessage(`{"models":{}}`)
	_, err := exe.HandleManagement(context.Background(), "patch", []string{"bindings"}, body, nil)
	if err == nil {
		t.Fatal("expected error when models is empty")
	}
}

func TestHandlePatchBindings_EmptyAlias(t *testing.T) {
	exe := newExecutorWithStubBuilder()
	body := json.RawMessage(`{"models":{"":{"model":"x"}}}`)
	_, err := exe.HandleManagement(context.Background(), "patch", []string{"bindings"}, body, nil)
	if err == nil {
		t.Fatal("expected error for empty alias")
	}
}

func TestHandlePatchBindings_BuilderFailure(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.bindings = bindings.New(
		bindings.WithClientBuilder(func(_ context.Context, _ map[string]any, _ *slog.Logger) (llm.Client, error) {
			return nil, errors.New("forced failure")
		}),
	)
	body := json.RawMessage(`{"models":{"planning":{"model":"x"}}}`)
	_, err := exe.HandleManagement(context.Background(), "patch", []string{"bindings"}, body, nil)
	if err == nil {
		t.Fatal("expected error from builder failure")
	}
}

func TestHandlePatchBindings_NullCfgDeletes(t *testing.T) {
	exe := newExecutorWithStubBuilder()
	upsert := json.RawMessage(`{"models":{"planning":{"model":"openai/gpt-4"}}}`)
	if _, err := exe.HandleManagement(context.Background(), "patch", []string{"bindings"}, upsert, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	del := json.RawMessage(`{"models":{"planning":null}}`)
	result, err := exe.HandleManagement(context.Background(), "patch", []string{"bindings"}, del, nil)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	models := result.(map[string]any)["models"].(map[string]any)
	if _, ok := models["planning"]; ok {
		t.Errorf("expected planning to be deleted, still present: %v", models)
	}
}

func TestHandlePatchBindings_NullCfgDeleteMissingIsNoop(t *testing.T) {
	exe := newExecutorWithStubBuilder()
	del := json.RawMessage(`{"models":{"never-set":null}}`)
	if _, err := exe.HandleManagement(context.Background(), "patch", []string{"bindings"}, del, nil); err != nil {
		t.Fatalf("delete of missing alias should be a no-op, got error: %v", err)
	}
}

func TestHandleBindings_MethodNotAllowed(t *testing.T) {
	exe := newExecutorWithStubBuilder()
	_, err := exe.HandleManagement(context.Background(), "delete", []string{"bindings"}, nil, nil)
	if err == nil {
		t.Fatal("expected error for unsupported method on /bindings")
	}
}

func TestHandleGetInstanceConfig_FoundWithCfg(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		return newMockInstance(cfg.GetString("name"), "agent"), nil
	})
	body := json.RawMessage(`{"kind":"agent","name":"my-agent","foo":"bar"}`)
	if _, err := exe.HandleManagement(context.Background(), "post", []string{"instances"}, body, nil); err != nil {
		t.Fatal(err)
	}

	result, err := exe.HandleManagement(context.Background(), "get", []string{"instances", "my-agent", "config"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfgMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map[string]any", result)
	}
	if cfgMap["name"] != "my-agent" || cfgMap["foo"] != "bar" {
		t.Errorf("config map = %v, missing fields", cfgMap)
	}
}

func TestHandleGetInstanceConfig_FoundNoCfg(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.AddInstance(newMockInstance("bare", "agent"))

	result, err := exe.HandleManagement(context.Background(), "get", []string{"instances", "bare", "config"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map[string]any", result)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map for cfg-less instance, got %v", m)
	}
}

func TestHandleGetInstanceConfig_NotFound(t *testing.T) {
	exe := NewExecutor(newTestServices())
	_, err := exe.HandleManagement(context.Background(), "get", []string{"instances", "missing", "config"}, nil, nil)
	if err == nil {
		t.Fatal("expected error for missing instance")
	}
	if !errors.Is(err, control.ErrResourceNotFound) {
		t.Errorf("want ErrResourceNotFound, got %v", err)
	}
}

func TestHandlePatchInstanceConfig_RejectsUnknownField(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.AddInstance(newMockInstance("a", "agent"))

	body := json.RawMessage(`{"instructions":"foo"}`)
	_, err := exe.HandleManagement(context.Background(), "patch", []string{"instances", "a", "config"}, body, nil)
	if err == nil {
		t.Fatal("expected rejection for any field in PR1")
	}
}

func TestHandlePatchInstanceConfig_NotFound(t *testing.T) {
	exe := NewExecutor(newTestServices())
	body := json.RawMessage(`{"x":1}`)
	_, err := exe.HandleManagement(context.Background(), "patch", []string{"instances", "missing", "config"}, body, nil)
	if err == nil {
		t.Fatal("expected error for missing instance")
	}
	if !errors.Is(err, control.ErrResourceNotFound) {
		t.Errorf("want ErrResourceNotFound, got %v", err)
	}
}

func TestHandlePatchInstanceConfig_EmptyBody(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.AddInstance(newMockInstance("a", "agent"))

	_, err := exe.HandleManagement(context.Background(), "patch", []string{"instances", "a", "config"}, nil, nil)
	if err == nil {
		t.Fatal("expected error for empty body")
	}
}

func TestHandlePatchInstanceConfig_EmptyPatch(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.AddInstance(newMockInstance("a", "agent"))

	body := json.RawMessage(`{}`)
	_, err := exe.HandleManagement(context.Background(), "patch", []string{"instances", "a", "config"}, body, nil)
	if err == nil {
		t.Fatal("expected error for empty patch")
	}
}

func TestHandlePatchInstanceConfig_InvalidJSON(t *testing.T) {
	exe := NewExecutor(newTestServices())
	exe.AddInstance(newMockInstance("a", "agent"))

	_, err := exe.HandleManagement(context.Background(), "patch", []string{"instances", "a", "config"}, json.RawMessage(`bad`), nil)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func newExecutorPullingFrom(t *testing.T, rows map[string]map[string]any) *Executor {
	t.Helper()
	reg := bindings.New(
		bindings.WithClientBuilder(func(_ context.Context, _ map[string]any, _ *slog.Logger) (llm.Client, error) {
			return stubModelClient{}, nil
		}),
		bindings.WithFetcher(func(_ context.Context, key string) (map[string]any, error) {
			return rows[key], nil
		}),
	)
	t.Cleanup(reg.Close)
	exe := NewExecutor(newTestServices())
	exe.SetBindings(reg)
	return exe
}

func waitForBoundModel(t *testing.T, exe *Executor, alias, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		models, _ := exe.bindings.Snapshot()["models"].(map[string]any)
		if cfg, _ := models[alias].(map[string]any); cfg != nil && cfg["model"] == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("binding %q never reached model %q: %v", alias, want, models)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestExecutor_PullsAModelChangeItDoesNotHoldYet(t *testing.T) {
	t.Parallel()
	exe := newExecutorPullingFrom(t, map[string]map[string]any{"research": {"model": "v1"}})

	exe.bindings.Notify("research")

	waitForBoundModel(t, exe, "research", "v1")
}

func TestHandlePatchBindings_PullsEachKeyAgain(t *testing.T) {
	t.Parallel()
	exe := newExecutorPullingFrom(t, map[string]map[string]any{"general": {"model": "v2"}})
	body, _ := json.Marshal(map[string]any{"models": map[string]any{"general": map[string]any{"model": "v1"}}})

	if _, err := exe.HandleManagement(t.Context(), "patch", []string{"bindings"}, body, nil); err != nil {
		t.Fatalf("patch: %v", err)
	}

	waitForBoundModel(t, exe, "general", "v2")
}

func TestHandlePatchBindings_StaleDeleteIsPulledBack(t *testing.T) {
	t.Parallel()
	exe := newExecutorPullingFrom(t, map[string]map[string]any{"research": {"model": "v1"}})
	body, _ := json.Marshal(map[string]any{"models": map[string]any{"research": nil}})

	if _, err := exe.HandleManagement(t.Context(), "patch", []string{"bindings"}, body, nil); err != nil {
		t.Fatalf("patch: %v", err)
	}

	waitForBoundModel(t, exe, "research", "v1")
}
