// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/bootstrap"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

type modelsHotRotationClient struct {
	id        string
	apiKey    string
	modelName string
}

func (c *modelsHotRotationClient) ChatCompletion(context.Context, *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: c.id}, nil
}

func (c *modelsHotRotationClient) ChatCompletionStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk)
	close(ch)
	return ch, nil
}

type modelsHotRotationExecutor struct {
	t   *testing.T
	reg *bindings.BindingsRegistry

	mu    sync.Mutex
	built map[string]*modelsHotRotationClient
}

func newModelsHotRotationAPI(t *testing.T) (*apiClient, *modelsHotRotationExecutor) {
	t.Helper()
	core, deps := newTestCore(t)
	modelsStartOutboxPoller(t, deps)
	platform := entplatform.NewManageablePlatform(core, deps, slog.Default())
	startControlApp(t, core, "platform", func(method string, path []string, body json.RawMessage) (any, error) {
		return platform.HandleManagement(t.Context(), method, path, body, nil)
	})

	exe := &modelsHotRotationExecutor{t: t, built: map[string]*modelsHotRotationClient{}}
	brk := core.Services().Broker()
	exe.reg = bindings.New(
		bindings.WithClientBuilder(exe.build),
		bindings.WithFetcher(bootstrap.NewPlatformModelFetcher(brk, core.Config().Namespace, slog.Default())),
		bindings.WithLogger(slog.Default()),
	)
	t.Cleanup(exe.reg.Close)
	watcher := bindings.NewWatcher(exe.reg, brk, core.Config().Namespace, slog.Default())
	if err := watcher.Start(t.Context()); err != nil {
		t.Fatalf("start model change watcher: %v", err)
	}
	t.Cleanup(func() { _ = watcher.Stop(context.Background()) })
	return newAPIClient(t, core), exe
}

func (e *modelsHotRotationExecutor) build(_ context.Context, raw map[string]any, _ *slog.Logger) (llm.Client, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	apiKey, _ := raw["api_key"].(string)
	modelName, _ := raw["model"].(string)
	c := &modelsHotRotationClient{id: fmt.Sprintf("client-%d", len(e.built)+1), apiKey: apiKey, modelName: modelName}
	e.built[c.id] = c
	return c, nil
}

func (e *modelsHotRotationExecutor) serving(c llm.Client) *modelsHotRotationClient {
	e.t.Helper()
	resp, err := c.ChatCompletion(e.t.Context(), &llm.ChatRequest{})
	if err != nil {
		e.t.Fatalf("chat through the binding: %v", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	built, ok := e.built[resp.Content]
	if !ok {
		e.t.Fatalf("the chat was served by %q, which the builder never made", resp.Content)
	}
	return built
}

func (e *modelsHotRotationExecutor) resolve(key string) *modelsHotRotationClient {
	e.t.Helper()
	c, err := e.reg.ResolveOrFetch(e.t.Context(), key)
	if err != nil {
		e.t.Fatalf("resolve %q: %v", key, err)
	}
	return e.serving(c)
}

func (e *modelsHotRotationExecutor) waitForBinding(key string, want func(*modelsHotRotationClient) bool) {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := e.reg.ModelClient(key); err == nil && want(e.serving(c)) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	e.t.Fatalf("the binding under %q never took the change", key)
}

func (e *modelsHotRotationExecutor) waitForUnbound(key string) {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		models, _ := e.reg.Snapshot()["models"].(map[string]any)
		if _, bound := models[key]; !bound {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	e.t.Fatalf("the binding under %q was never dropped", key)
}

func (e *modelsHotRotationExecutor) expectUnbound(key string) {
	e.t.Helper()
	c, err := e.reg.ResolveOrFetch(e.t.Context(), key)
	if err == nil {
		e.t.Fatalf("resolve %q returned a client (%T): agents still chat on a model that is gone", key, c)
	}
	if !errors.Is(err, bindings.ErrAliasUnbound) {
		e.t.Errorf("resolve %q: %v, want ErrAliasUnbound", key, err)
	}
}

func modelsHotRotationCreate(api *apiClient, alias, apiKey string) Model {
	api.t.Helper()
	rec := api.call(http.MethodPost, "/models", map[string]any{
		"alias": alias, "provider": "openai", "modelName": "gpt-4o",
		"apiBase":    "https://example.com/v1",
		"authConfig": map[string]any{"type": "apikey", "api_key": apiKey},
	})
	return expectCreated[Model](api.t, rec, "models")
}

func TestModels_UpdateRebuildsTheClientAgentsResolve(t *testing.T) {
	api, exe := newModelsHotRotationAPI(t)
	newModelName := platpkg.ResolveLiteLLMModelName("openai", "", "gpt-5")
	tests := []struct {
		name       string
		patch      map[string]any
		wantAPIKey string
	}{
		{
			name:       "key and model name",
			patch:      map[string]any{"modelName": "gpt-5", "authConfig": map[string]any{"type": "apikey", "api_key": "sk-v2"}},
			wantAPIKey: "sk-v2",
		},
		{
			name:       "model name only",
			patch:      map[string]any{"modelName": "gpt-5"},
			wantAPIKey: "sk-v1",
		},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			alias := fmt.Sprintf("rotating-%d", i)
			created := modelsHotRotationCreate(api, alias, "sk-v1")
			before := exe.resolve(alias)
			if before.apiKey != "sk-v1" || before.modelName == newModelName {
				t.Fatalf("the first lookup built %+v, want the saved config", before)
			}

			expectStatus(t, api.call(http.MethodPatch, "/models/"+created.ID, tc.patch), http.StatusOK)

			exe.waitForBinding(alias, func(c *modelsHotRotationClient) bool { return c.modelName == newModelName })
			after := exe.resolve(alias)
			if after.apiKey != tc.wantAPIKey {
				t.Errorf("after the update the agent's client uses key %q, want %q", after.apiKey, tc.wantAPIKey)
			}
			if after == before {
				t.Error("the lookup returned the client built before the update: it was not rebuilt")
			}
		})
	}
}

func TestModels_UpdateRebuildsTheClientBoundByID(t *testing.T) {
	api, exe := newModelsHotRotationAPI(t)
	const alias = "rotating-by-id"
	created := modelsHotRotationCreate(api, alias, "sk-v1")
	beforeByID := exe.resolve(created.ID)
	if beforeByID.apiKey != "sk-v1" || exe.resolve(alias).apiKey != "sk-v1" {
		t.Fatal("the first lookups did not build the saved config")
	}

	expectStatus(t, api.call(http.MethodPatch, "/models/"+created.ID, map[string]any{
		"authConfig": map[string]any{"type": "apikey", "api_key": "sk-v2"},
	}), http.StatusOK)

	rotated := func(c *modelsHotRotationClient) bool { return c.apiKey == "sk-v2" }
	exe.waitForBinding(alias, rotated)
	exe.waitForBinding(created.ID, rotated)
	if afterByID := exe.resolve(created.ID); afterByID == beforeByID {
		t.Error("the id-keyed lookup returned the client built before the update")
	}
}

func TestModels_DeleteUnbindsTheModelUnderItsAliasAndID(t *testing.T) {
	api, exe := newModelsHotRotationAPI(t)
	const alias = "deleted-model"
	created := modelsHotRotationCreate(api, alias, "sk-v1")
	for _, key := range []string{alias, created.ID} {
		if c := exe.resolve(key); c.apiKey != "sk-v1" {
			t.Fatalf("the first lookup of %q built key %q, want sk-v1", key, c.apiKey)
		}
	}

	expectStatus(t, api.call(http.MethodDelete, "/models/"+created.ID, nil), http.StatusNoContent)

	for _, key := range []string{alias, created.ID} {
		exe.waitForUnbound(key)
		exe.expectUnbound(key)
	}
}

func TestModels_RenameBindsTheNewAliasAndDropsTheOld(t *testing.T) {
	api, exe := newModelsHotRotationAPI(t)
	created := modelsHotRotationCreate(api, "old-alias", "sk-v1")
	exe.resolve("old-alias")
	exe.resolve(created.ID)

	expectStatus(t, api.call(http.MethodPatch, "/models/"+created.ID, map[string]any{"alias": "new-alias"}), http.StatusOK)

	exe.waitForUnbound("old-alias")
	exe.expectUnbound("old-alias")
	if c := exe.resolve("new-alias"); c.apiKey != "sk-v1" {
		t.Errorf("the new alias resolves to key %q, want sk-v1", c.apiKey)
	}
	if c := exe.resolve(created.ID); c.apiKey != "sk-v1" {
		t.Errorf("the id resolves to key %q after the rename, want sk-v1", c.apiKey)
	}
}

func TestModels_RemovedParamLeavesTheModelAndTheAgent(t *testing.T) {
	api, exe := newModelsHotRotationAPI(t)
	tests := []struct {
		name    string
		created map[string]any
		saved   map[string]any
	}{
		{name: "one of two", created: map[string]any{"top_p": 0.9, "presence_penalty": 0.5}, saved: map[string]any{"presence_penalty": 0.5}},
		{name: "the only one", created: map[string]any{"top_p": 0.9}, saved: map[string]any{}},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			alias := fmt.Sprintf("params-%d", i)
			created := expectCreated[Model](t, api.call(http.MethodPost, "/models", map[string]any{
				"alias": alias, "provider": "openai", "modelName": "gpt-4o",
				"apiBase":     "https://example.com/v1",
				"authConfig":  map[string]any{"type": "apikey", "api_key": "sk-v1"},
				"modelParams": tc.created,
			}), "models")
			exe.resolve(alias)
			if raw, ok := exe.awaitRawConfig(alias, func(raw map[string]any) bool { return raw["top_p"] == 0.9 }); !ok {
				t.Fatalf("before the save the agent holds %v, want top_p", raw)
			}

			expectStatus(t, api.call(http.MethodPatch, "/models/"+created.ID, webUISaveOfEveryFieldWithTheParamsStillListed(alias, tc.saved)), http.StatusOK)

			rec := api.call(http.MethodGet, "/models/"+created.ID, nil)
			expectStatus(t, rec, http.StatusOK)
			if diff := cmp.Diff(tc.saved, decodeData[Model](t, rec).ModelParams, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("stored modelParams (-want +got):\n%s", diff)
			}
			hasOnlySaved := func(raw map[string]any) bool {
				_, kept := raw["top_p"]
				return !kept && paramsInclude(raw, tc.saved)
			}
			if raw, ok := exe.awaitRawConfig(alias, hasOnlySaved); !ok {
				t.Errorf("the agent still holds %v after the save, want modelParams %v", raw, tc.saved)
			}
		})
	}
}

func webUISaveOfEveryFieldWithTheParamsStillListed(alias string, paramsStillListed map[string]any) map[string]any {
	return map[string]any{
		"alias": alias, "provider": "openai", "modelName": "gpt-4o",
		"apiBase": "https://example.com/v1", "description": nil,
		"authType": "apikey", "authConfig": map[string]any{"type": "apikey"},
		"modelParams": paramsStillListed, "maxInputTokens": nil,
	}
}

func paramsInclude(params, want map[string]any) bool {
	for k, v := range want {
		if params[k] != v {
			return false
		}
	}
	return true
}

func (e *modelsHotRotationExecutor) awaitRawConfig(key string, want func(map[string]any) bool) (map[string]any, bool) {
	var raw map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		models, _ := e.reg.Snapshot()["models"].(map[string]any)
		raw, _ = models[key].(map[string]any)
		if raw != nil && want(raw) {
			return raw, true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return raw, false
}
