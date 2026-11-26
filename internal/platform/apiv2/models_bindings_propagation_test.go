// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/bootstrap"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const modelsBindingsNamespace = "test-ns"

type modelsBindingsClient struct{}

func (modelsBindingsClient) ChatCompletion(context.Context, *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}

func (modelsBindingsClient) ChatCompletionStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk)
	close(ch)
	return ch, nil
}

type modelsBindingsEnv struct {
	*entrypointAPI
	broker   broker.Broker
	registry *bindings.BindingsRegistry
}

func newModelsBindingsEnv(t *testing.T) *modelsBindingsEnv {
	t.Helper()
	core, deps := newTestCore(t)
	for _, svc := range deps.BackgroundServices {
		if poller, ok := svc.(*platpkg.OutboxPoller); ok {
			if err := poller.Start(t.Context()); err != nil {
				t.Fatalf("start outbox poller: %v", err)
			}
			t.Cleanup(func() { _ = poller.Stop(context.Background()) })
		}
	}
	platform := entplatform.NewManageablePlatform(core, deps, slog.Default())
	startControlApp(t, core, "platform", func(method string, path []string, body json.RawMessage) (any, error) {
		return platform.HandleManagement(t.Context(), method, path, body, nil)
	})

	brk := core.Services().Broker()
	registry := bindings.New(
		bindings.WithClientBuilder(func(context.Context, map[string]any, *slog.Logger) (llm.Client, error) {
			return modelsBindingsClient{}, nil
		}),
		bindings.WithFetcher(bootstrap.NewPlatformModelFetcher(brk, modelsBindingsNamespace, slog.Default())),
		bindings.WithRetryBackoff(20*time.Millisecond, 100*time.Millisecond),
	)
	t.Cleanup(registry.Close)
	awe.NewExecutor(core.Services()).SetBindings(registry)
	watcher := bindings.NewWatcher(registry, brk, modelsBindingsNamespace, slog.Default())
	if err := watcher.Start(t.Context()); err != nil {
		t.Fatalf("start model bindings watcher: %v", err)
	}
	t.Cleanup(func() { _ = watcher.Stop(context.Background()) })

	return &modelsBindingsEnv{entrypointAPI: &entrypointAPI{apiClient: newAPIClient(t, core)}, broker: brk, registry: registry}
}

func (e *modelsBindingsEnv) create(alias, modelName, secret string) Model {
	e.t.Helper()
	return expectCreated[Model](e.t, e.call(http.MethodPost, "/models", map[string]any{
		"alias":      alias,
		"provider":   "openai",
		"modelName":  modelName,
		"apiBase":    "https://llm.example.com/v1",
		"authConfig": map[string]any{"type": "apikey", "api_key": secret},
	}), "models")
}

func (e *modelsBindingsEnv) bound(key string) (map[string]any, bool) {
	models, _ := e.registry.Snapshot()["models"].(map[string]any)
	binding, ok := models[key].(map[string]any)
	return binding, ok
}

func (e *modelsBindingsEnv) waitFor(what string, done func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			e.t.Fatalf("timed out waiting for %s; bindings = %v", what, e.registry.Snapshot()["models"])
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (e *modelsBindingsEnv) waitForModel(key, model string) {
	e.t.Helper()
	e.waitFor(key+" bound to "+model, func() bool {
		binding, ok := e.bound(key)
		return ok && binding["model"] == model
	})
}

func (e *modelsBindingsEnv) subscribe(alias string) <-chan []byte {
	e.t.Helper()
	sub, err := e.broker.CreateDirectSubscription()
	if err != nil {
		e.t.Fatalf("create subscription: %v", err)
	}
	if err := e.broker.Subscribe(sub, a2a.ModelConfigUpdateTopic(modelsBindingsNamespace, alias)); err != nil {
		e.t.Fatalf("subscribe: %v", err)
	}
	ctx, cancel := context.WithCancel(e.t.Context())
	e.t.Cleanup(cancel)
	payloads := make(chan []byte, 8)
	go func() {
		for {
			msg, err := e.broker.Receive(ctx, sub)
			if err != nil || msg == nil {
				return
			}
			msg.Ack()
			select {
			case payloads <- msg.Payload:
			case <-ctx.Done():
				return
			}
		}
	}()
	return payloads
}

func (e *modelsBindingsEnv) expectSignalWithoutCredentials(payloads <-chan []byte, alias, secret string) {
	e.t.Helper()
	var payload []byte
	select {
	case payload = <-payloads:
	case <-time.After(5 * time.Second):
		e.t.Fatalf("no change signal for %s", alias)
	}
	var parsed map[string]any
	if err := json.Unmarshal(payload, &parsed); err != nil {
		e.t.Fatalf("parse change signal: %v", err)
	}
	if parsed["alias"] != alias {
		e.t.Errorf("change signal alias = %v, want %s", parsed["alias"], alias)
	}
	if model, present := parsed["model_config"]; present && model != nil {
		e.t.Errorf("change signal carries model_config: %s", payload)
	}
	if bytes.Contains(payload, []byte(secret)) {
		e.t.Errorf("change signal carries the model's secret: %s", payload)
	}
}

func TestModels_CreateReachesAgentBindingsAndBroadcastsNoCredentials(t *testing.T) {
	env := newModelsBindingsEnv(t)
	signals := env.subscribe("propagation-planning")

	env.create("propagation-planning", "openai/gpt-4", "sk-secret")

	env.waitForModel("propagation-planning", "openai/gpt-4")
	env.expectSignalWithoutCredentials(signals, "propagation-planning", "sk-secret")
}

func TestModels_AnAliasWithASlashReachesAgentBindings(t *testing.T) {
	env := newModelsBindingsEnv(t)
	created := env.create("openai/gpt-4o", "openai/gpt-4", "sk-v1")
	env.waitForModel("openai/gpt-4o", "openai/gpt-4")

	expectStatus(t, env.call(http.MethodPatch, "/models/"+created.ID, map[string]any{"modelName": "openai/gpt-5"}), http.StatusOK)

	env.waitForModel("openai/gpt-4o", "openai/gpt-5")
}
