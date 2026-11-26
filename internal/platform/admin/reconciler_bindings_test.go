// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/bootstrap"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/discovery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type stubBindingsClient struct{}

func (stubBindingsClient) ChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}

func (stubBindingsClient) ChatCompletionStream(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk)
	close(ch)
	return ch, nil
}

func installStubBuilder(t *testing.T, h *e2eHarness) {
	t.Helper()
	reg := bindings.New(
		bindings.WithClientBuilder(func(_ context.Context, _ map[string]any, _ *slog.Logger) (llm.Client, error) {
			return stubBindingsClient{}, nil
		}),
		bindings.WithFetcher(bootstrap.NewPlatformModelFetcher(h.brk, h.namespace, slog.Default())),
		bindings.WithRetryBackoff(20*time.Millisecond, 100*time.Millisecond),
	)
	t.Cleanup(reg.Close)
	h.aweExe.SetBindings(reg)
	startBindingsWatcher(t, h, reg)
}

func startBindingsWatcher(t *testing.T, h *e2eHarness, reg *bindings.BindingsRegistry) {
	t.Helper()
	watcher := bindings.NewWatcher(reg, h.brk, h.namespace, slog.Default())
	if err := watcher.Start(h.ctx); err != nil {
		t.Fatalf("start model bindings watcher: %v", err)
	}
	t.Cleanup(func() { _ = watcher.Stop(context.Background()) })
}

func bindingsSnapshot(t *testing.T, h *e2eHarness) map[string]any {
	t.Helper()
	snap := h.aweExe.Bindings().Snapshot()
	models, _ := snap["models"].(map[string]any)
	return models
}

func waitForAlias(t *testing.T, h *e2eHarness, alias string, wantPresent bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, ok := bindingsSnapshot(t, h)[alias]
		if ok == wantPresent {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timeout: binding %q present=%v after %v, got present=%v", alias, wantPresent, timeout, !wantPresent)
}

func TestReconciler_PushAllBindingsOnAWEStarted(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	installStubBuilder(t, h)

	if _, err := h.modelConfigSvc.Create(h.ctx, &CreateModelConfigRequest{
		Alias:      "reconcile-a",
		Provider:   "openai",
		ModelName:  "openai/gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-a"},
	}, "test-seed"); err != nil {
		t.Fatalf("seed A: %v", err)
	}
	if _, err := h.modelConfigSvc.Create(h.ctx, &CreateModelConfigRequest{
		Alias:      "reconcile-b",
		Provider:   "openai",
		ModelName:  "openai/gpt-3.5",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-b"},
	}, "test-seed"); err != nil {
		t.Fatalf("seed B: %v", err)
	}

	if n := len(bindingsSnapshot(t, h)); n != 0 {
		t.Fatalf("expected no initial bindings, got %d", n)
	}

	reconciler := NewReconciler(
		h.agentRepo, h.deployRepo, h.connectorRepo,
		h.workflowRepo, h.gatewayRepo, h.gwDeployRepo,
		h.yamlSvc, h.modelConfigSvc, h.controlClient, h.brk, h.namespace, slog.Default(),
	)
	reconciler.SetDeployerRegistry(NewDefaultDeployerRegistry(h.yamlSvc, h.connectorRepo, nil, nil, h.a2aRepo, h.namespace, slog.Default()))
	reconciler.answerAWE(h.ctx, &broker.Message{
		ReplyTo:        a2a.AWEAgentsReplyTopic(h.namespace, "first"),
		UserProperties: map[string]string{control.FirstAskProperty: "true", discovery.ProcessIDProperty: "test-awe"},
	})

	waitForAlias(t, h, "reconcile-a", true, 3*time.Second)
	waitForAlias(t, h, "reconcile-b", true, 3*time.Second)

	models := bindingsSnapshot(t, h)
	if got := models["reconcile-a"].(map[string]any)["model"]; got != "openai/gpt-4" {
		t.Errorf("reconcile-a model = %v, want openai/gpt-4", got)
	}
	if got := models["reconcile-b"].(map[string]any)["model"]; got != "openai/gpt-3.5" {
		t.Errorf("reconcile-b model = %v, want openai/gpt-3.5", got)
	}
}

func TestReconciler_PushAllBindingsOnSTRStarted(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	installStubBuilder(t, h)

	if _, err := h.modelConfigSvc.Create(h.ctx, &CreateModelConfigRequest{
		Alias:      "str-reseed",
		Provider:   "openai",
		ModelName:  "openai/gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-str"},
	}, "test-seed"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if n := len(bindingsSnapshot(t, h)); n != 0 {
		t.Fatalf("expected no initial bindings, got %d", n)
	}

	reconciler := NewReconciler(
		h.agentRepo, h.deployRepo, h.connectorRepo,
		h.workflowRepo, h.gatewayRepo, h.gwDeployRepo,
		h.yamlSvc, h.modelConfigSvc, h.controlClient, h.brk, h.namespace, slog.Default(),
	)
	reconciler.SetDeployerRegistry(NewDefaultDeployerRegistry(h.yamlSvc, h.connectorRepo, nil, nil, h.a2aRepo, h.namespace, slog.Default()))
	serveCtx, stopServing := context.WithCancel(h.ctx)
	t.Cleanup(stopServing)
	go func() { _ = reconciler.serve(serveCtx, leader.Term{Number: 1}) }()
	time.Sleep(500 * time.Millisecond)

	topic := a2a.LifecycleTopic(h.namespace, "str", "started")
	msg := &broker.Message{Topic: topic, Payload: []byte("{}")}
	if err := h.brk.Publish(h.ctx, topic, msg); err != nil {
		t.Fatalf("publish str lifecycle/started: %v", err)
	}

	waitForAlias(t, h, "str-reseed", true, 3*time.Second)
}
