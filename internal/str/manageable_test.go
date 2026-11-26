// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type fakeLLMClient struct{ id string }

func (f *fakeLLMClient) ChatCompletion(context.Context, *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: f.id}, nil
}

func (f *fakeLLMClient) ChatCompletionStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, nil
}

func bindingsRegistryReturning(client llm.Client) *bindings.BindingsRegistry {
	return bindings.New(bindings.WithClientBuilder(
		func(context.Context, map[string]any, *slog.Logger) (llm.Client, error) {
			return client, nil
		}))
}

func aliasAwareBindingsRegistry() *bindings.BindingsRegistry {
	return bindings.New(bindings.WithClientBuilder(
		func(_ context.Context, cfg map[string]any, _ *slog.Logger) (llm.Client, error) {
			id, _ := cfg["model"].(string)
			return &fakeLLMClient{id: id}, nil
		}))
}

func seedAliases(t *testing.T, reg *bindings.BindingsRegistry, aliases ...string) {
	t.Helper()
	for _, a := range aliases {
		if err := reg.UpsertModel(context.Background(), a, map[string]any{"model": a}); err != nil {
			t.Fatalf("seed alias %q: %v", a, err)
		}
	}
}

func newBindingsWorker(reg *bindings.BindingsRegistry) *Worker {
	return NewWorker(&testServices{bindings: reg}, WorkerConfig{WorkerID: "t", Namespace: "test-ns"})
}

func newBindingsWorkerWithAlias(reg *bindings.BindingsRegistry, alias string) *Worker {
	return NewWorker(&testServices{bindings: reg},
		WorkerConfig{WorkerID: "t", Namespace: "test-ns", ToolModelAlias: alias})
}

func snapshotHasAlias(res any, alias string) bool {
	snap, ok := res.(map[string]any)
	if !ok {
		return false
	}
	models, ok := snap["models"].(map[string]any)
	if !ok {
		return false
	}
	return models[alias] != nil
}

func TestWorkerPatchBindings_InstallsModel(t *testing.T) {
	t.Parallel()
	bound := &fakeLLMClient{id: "bound"}
	reg := bindingsRegistryReturning(bound)
	w := newBindingsWorker(reg)

	body := json.RawMessage(`{"models":{"general":{"model":"openai/x","api_key":"sk-new"}}}`)
	res, err := w.HandleManagement(context.Background(), "patch", []string{"bindings"}, body, nil)
	if err != nil {
		t.Fatalf("patch bindings: %v", err)
	}
	if !snapshotHasAlias(res, "general") {
		t.Fatalf("patch response snapshot missing general: %#v", res)
	}

	got, err := reg.ModelClient("general")
	if err != nil {
		t.Fatalf("ModelClient(general) after push: %v", err)
	}
	if clientID(t, got) != bound.id {
		t.Fatalf("ModelClient(general) serves %q, want the pushed client", clientID(t, got))
	}
}

func TestWorkerPatchBindings_InstallsOnlyResolvedAlias(t *testing.T) {
	t.Parallel()
	reg := bindingsRegistryReturning(&fakeLLMClient{id: "bound"})
	w := newBindingsWorker(reg)

	body := json.RawMessage(`{"models":{` +
		`"general":{"model":"m"},` +
		`"other-alias":{"model":"n"},` +
		`"11111111-1111-1111-1111-111111111111":{"model":"byid"}}}`)
	if _, err := w.HandleManagement(context.Background(), "patch", []string{"bindings"}, body, nil); err != nil {
		t.Fatalf("patch bindings: %v", err)
	}

	if _, err := reg.ModelClient(defaultToolModelAlias); err != nil {
		t.Fatalf("resolved alias %q not installed: %v", defaultToolModelAlias, err)
	}
	for _, skipped := range []string{"other-alias", "11111111-1111-1111-1111-111111111111"} {
		if _, err := reg.ModelClient(skipped); err == nil {
			t.Fatalf("alias %q should have been skipped but is installed", skipped)
		}
	}
}

func TestWorkerPatchBindings_InstallsConfiguredAlias(t *testing.T) {
	t.Parallel()
	reg := bindingsRegistryReturning(&fakeLLMClient{id: "bound"})
	w := newBindingsWorkerWithAlias(reg, "planning")

	body := json.RawMessage(`{"models":{"general":{"model":"m"},"planning":{"model":"n"}}}`)
	if _, err := w.HandleManagement(context.Background(), "patch", []string{"bindings"}, body, nil); err != nil {
		t.Fatalf("patch bindings: %v", err)
	}

	if _, err := reg.ModelClient("planning"); err != nil {
		t.Fatalf("configured alias planning not installed: %v", err)
	}
	if _, err := reg.ModelClient(defaultToolModelAlias); err == nil {
		t.Fatalf("alias %q should have been skipped for a planning-configured worker", defaultToolModelAlias)
	}
}

func TestWorkerPatchBindings_RotatesAlreadyInstalledAlias(t *testing.T) {
	t.Parallel()
	reg := aliasAwareBindingsRegistry()
	w := newBindingsWorker(reg)

	seedAliases(t, reg, "fast")
	before, err := reg.ModelClient("fast")
	if err != nil {
		t.Fatalf("seeded alias fast should resolve: %v", err)
	}
	if got := clientID(t, before); got != "fast" {
		t.Fatalf("pre-rotation client = %q, want fast", got)
	}

	body := json.RawMessage(`{"models":{"fast":{"model":"fast-rotated"},"unrelated":{"model":"z"}}}`)
	if _, err := w.HandleManagement(context.Background(), "patch", []string{"bindings"}, body, nil); err != nil {
		t.Fatalf("patch bindings: %v", err)
	}

	after, afterErr := reg.ModelClient("fast")
	if afterErr != nil {
		t.Fatalf("already-installed alias fast should accept a rotation: %v", afterErr)
	}
	if got := clientID(t, after); got != "fast-rotated" {
		t.Errorf("post-rotation client = %q, want fast-rotated — the rotation was dropped", got)
	}
	if _, err := reg.ModelClient("unrelated"); err == nil {
		t.Error("alias unrelated was never in use and should still be skipped")
	}
}

func clientID(t *testing.T, c llm.Client) string {
	t.Helper()
	resp, err := c.ChatCompletion(context.Background(), &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("call through client: %v", err)
	}
	return resp.Content
}

func TestWorkerGetBindings_ReturnsSnapshot(t *testing.T) {
	t.Parallel()
	reg := bindingsRegistryReturning(&fakeLLMClient{id: "bound"})
	w := newBindingsWorker(reg)

	if _, err := w.HandleManagement(context.Background(), "patch", []string{"bindings"},
		json.RawMessage(`{"models":{"general":{"model":"m"}}}`), nil); err != nil {
		t.Fatalf("seed patch: %v", err)
	}
	res, err := w.HandleManagement(context.Background(), "get", []string{"bindings"}, nil, nil)
	if err != nil {
		t.Fatalf("get bindings: %v", err)
	}
	if !snapshotHasAlias(res, "general") {
		t.Fatalf("get snapshot missing general: %#v", res)
	}
}

func TestWorkerPatchBindings_Rejects(t *testing.T) {
	t.Parallel()
	w := newBindingsWorker(bindingsRegistryReturning(&fakeLLMClient{id: "x"}))
	cases := map[string]json.RawMessage{
		"empty body":  nil,
		"no models":   json.RawMessage(`{"models":{}}`),
		"empty alias": json.RawMessage(`{"models":{"":{"model":"m"}}}`),
		"unknown key": json.RawMessage(`{"nope":{}}`),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := w.HandleManagement(context.Background(), "patch", []string{"bindings"}, body, nil); err == nil {
				t.Fatalf("expected error for %s", name)
			}
		})
	}
}

func TestResolveToolLLMClient_PrefersBindingElseFallback(t *testing.T) {
	t.Parallel()
	bound := &fakeLLMClient{id: "bound"}
	fallback := &fakeLLMClient{id: "fallback"}
	reg := bindingsRegistryReturning(bound)
	w := newBindingsWorker(reg)

	got, _, err := w.resolveToolLLMClient(context.Background(), defaultToolModelAlias, fallback, false)
	if got != fallback {
		t.Fatalf("unbound resolve = %#v, want fallback", got)
	}
	if err == nil {
		t.Error("unbound resolve should report an error alongside the fallback client")
	}

	if _, patchErr := w.HandleManagement(context.Background(), "patch", []string{"bindings"},
		json.RawMessage(`{"models":{"general":{"model":"m"}}}`), nil); patchErr != nil {
		t.Fatalf("patch: %v", patchErr)
	}
	got, _, err = w.resolveToolLLMClient(context.Background(), defaultToolModelAlias, fallback, false)
	if got == fallback || clientID(t, got) != bound.id {
		t.Fatalf("bound resolve = %#v, want the general binding", got)
	}
	if err != nil {
		t.Errorf("bound resolve returned error %v, want nil", err)
	}
}

func TestResolveToolLLMClient_NilBindingsUsesFallback(t *testing.T) {
	t.Parallel()
	fallback := &fakeLLMClient{id: "fallback"}
	w := NewWorker(&testServices{}, WorkerConfig{WorkerID: "t", Namespace: "test-ns"})
	got, _, err := w.resolveToolLLMClient(context.Background(), defaultToolModelAlias, fallback, false)
	if got != fallback {
		t.Fatalf("nil-bindings resolve = %#v, want fallback", got)
	}
	if !errors.Is(err, errNoBindingsRegistry) {
		t.Errorf("nil-bindings resolve error = %v, want errNoBindingsRegistry", err)
	}
}

func TestNewToolLLMCallback_RoutesThroughBinding(t *testing.T) {
	t.Parallel()
	bound := &fakeLLMClient{id: "bound"}
	env := &fakeLLMClient{id: "env"}
	reg := bindingsRegistryReturning(bound)
	w := newBindingsWorker(reg)

	cb := w.newToolLLMCallback(env)
	if cb == nil {
		t.Fatal("newToolLLMCallback returned nil despite an env client")
	}

	if got, err := cb(context.Background(), "", "tool", nil, "sys", "user", 0); err != nil || got != "env" {
		t.Fatalf("pre-push callback = (%q, %v), want (env, nil)", got, err)
	}

	if _, err := w.HandleManagement(context.Background(), "patch", []string{"bindings"},
		json.RawMessage(`{"models":{"general":{"model":"m"}}}`), nil); err != nil {
		t.Fatalf("patch: %v", err)
	}
	if got, err := cb(context.Background(), "", "tool", nil, "sys", "user", 0); err != nil || got != "bound" {
		t.Fatalf("post-push callback = (%q, %v), want (bound, nil)", got, err)
	}
}

func TestNewToolLLMCallback_AliasPrecedence(t *testing.T) {
	t.Parallel()
	reg := aliasAwareBindingsRegistry()
	seedAliases(t, reg, defaultToolModelAlias, "planning", "fast")

	configured := newBindingsWorkerWithAlias(reg, "planning")
	unconfigured := newBindingsWorker(reg)

	if alias, operatorSet := configured.toolModelAlias(); alias != "planning" || !operatorSet {
		t.Errorf("configured toolModelAlias() = (%q, %v), want (planning, true)", alias, operatorSet)
	}
	if alias, operatorSet := unconfigured.toolModelAlias(); alias != defaultToolModelAlias || operatorSet {
		t.Errorf("unconfigured toolModelAlias() = (%q, %v), want (%q, false)", alias, operatorSet, defaultToolModelAlias)
	}

	tests := []struct {
		name            string
		worker          *Worker
		invocationAlias string
		want            string
	}{
		{
			name:            "invocation alias beats the operator alias",
			worker:          configured,
			invocationAlias: "fast",
			want:            "fast",
		},
		{
			name:   "operator alias applies with no invocation override",
			worker: configured,
			want:   "planning",
		},
		{
			name:            "invocation alias beats the built-in default",
			worker:          unconfigured,
			invocationAlias: "fast",
			want:            "fast",
		},
		{
			name:   "built-in default applies when nobody chose",
			worker: unconfigured,
			want:   defaultToolModelAlias,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cb := tc.worker.newToolLLMCallback(&fakeLLMClient{id: "env"})
			got, err := cb(context.Background(), tc.invocationAlias, "tool", nil, "sys", "user", 0)
			if err != nil {
				t.Fatalf("callback error: %v", err)
			}
			if got != tc.want {
				t.Errorf("resolved alias = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestShippedDefaultAliasStillFallsBack(t *testing.T) {
	t.Parallel()

	w := NewWorker(&testServices{}, WorkerConfig{
		WorkerID: "t", Namespace: "test-ns", ToolModelAlias: defaultToolModelAlias,
	})

	alias, operatorSet := w.toolModelAlias()
	if alias != defaultToolModelAlias {
		t.Errorf("alias = %q, want %q", alias, defaultToolModelAlias)
	}
	if operatorSet {
		t.Error("the shipped default must not count as an operator choice; " +
			"that promotes every default deployment to the fail-loudly tier")
	}
	if _, tier := w.resolveToolAlias(""); tier != tierDefault {
		t.Errorf("tier = %v, want tierDefault so the model: block fallback still applies", tier)
	}

	cb := w.newToolLLMCallback(&fakeLLMClient{id: "env"})
	if cb == nil {
		t.Fatal("callback should exist with an env client")
	}
	got, err := cb(context.Background(), "", "tool", nil, "sys", "user", 0)
	if err != nil {
		t.Fatalf("default-alias call errored on a platform-less STR: %v", err)
	}
	if got != "env" {
		t.Errorf("resolved client = %q, want the env fallback", got)
	}
}

func TestNewToolLLMCallback_OperatorAliasFailsLoudly(t *testing.T) {
	t.Parallel()
	reg := aliasAwareBindingsRegistry()
	seedAliases(t, reg, defaultToolModelAlias)
	w := newBindingsWorkerWithAlias(reg, "typoed-alias")

	cb := w.newToolLLMCallback(&fakeLLMClient{id: "env"})
	got, err := cb(context.Background(), "", "tool", nil, "sys", "user", 0)
	if err == nil {
		t.Fatalf("unresolvable tool_model_alias returned (%q, nil); want an error", got)
	}
	if !strings.Contains(err.Error(), "typoed-alias") {
		t.Errorf("error %q should name the configured alias", err)
	}
	if got == "env" {
		t.Error("unresolvable operator alias silently fell back to the env client")
	}
}

func TestNewToolLLMCallback_ExplicitAliasFailsWithoutRegistry(t *testing.T) {
	t.Parallel()
	w := NewWorker(&testServices{}, WorkerConfig{WorkerID: "t", Namespace: "test-ns"})

	cb := w.newToolLLMCallback(&fakeLLMClient{id: "env"})
	if cb == nil {
		t.Fatal("callback should exist when an env client is present")
	}
	got, err := cb(context.Background(), "wants-a-specific-model", "tool", nil, "sys", "user", 0)
	if err == nil {
		t.Fatalf("explicit alias with no registry returned (%q, nil); want an error", got)
	}
	if got == "env" {
		t.Error("explicit alias was silently served by the env client")
	}

	if got, err := cb(context.Background(), "", "tool", nil, "sys", "user", 0); err != nil || got != "env" {
		t.Errorf("default tier with no registry = (%q, %v), want (env, nil)", got, err)
	}
}

func TestNewToolLLMCallback_ExplicitAliasFailsLoudly(t *testing.T) {
	t.Parallel()
	reg := bindingsRegistryReturning(&fakeLLMClient{id: "bound"})
	w := newBindingsWorker(reg)

	cb := w.newToolLLMCallback(&fakeLLMClient{id: "env"})
	got, err := cb(context.Background(), "does-not-exist", "tool", nil, "sys", "user", 0)
	if err == nil {
		t.Fatalf("unresolvable explicit alias returned (%q, nil); want an error", got)
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error %q should name the requested alias", err)
	}
	if got == "env" {
		t.Error("unresolvable explicit alias fell back to the env client instead of erroring")
	}
}

func TestNewToolLLMCallback_NilWhenNoClientOrBindings(t *testing.T) {
	t.Parallel()
	w := NewWorker(&testServices{}, WorkerConfig{WorkerID: "t", Namespace: "test-ns"})
	if cb := w.newToolLLMCallback(nil); cb != nil {
		t.Fatal("newToolLLMCallback should be nil with no env client and no bindings")
	}
}

func TestWorkerFollowsModelChangesLikeAPush(t *testing.T) {
	t.Parallel()
	var pulls sync.Map
	fetch := func(_ context.Context, key string) (map[string]any, error) {
		pulls.Store(key, true)
		return map[string]any{"model": key}, nil
	}
	reg := bindings.New(
		bindings.WithFetcher(fetch),
		bindings.WithClientBuilder(func(_ context.Context, cfg map[string]any, _ *slog.Logger) (llm.Client, error) {
			id, _ := cfg["model"].(string)
			return &fakeLLMClient{id: id}, nil
		}),
	)
	t.Cleanup(reg.Close)
	w := newBindingsWorkerWithAlias(reg, "tools")
	w.followModelChanges()
	w.tempRejects.Mark("tools")

	reg.Notify("tools")
	reg.Notify("research")

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := reg.ModelClient("tools"); err == nil && !w.tempRejects.Rejects("tools") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("tool model alias was not installed with its temperature verdict forgotten")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, pulled := pulls.Load("research"); pulled {
		t.Error("STR pulled an alias it neither holds nor uses")
	}
}

func TestWorkerPatchBindings_StaleDeleteIsPulledBack(t *testing.T) {
	t.Parallel()
	reg := bindings.New(
		bindings.WithFetcher(func(_ context.Context, key string) (map[string]any, error) {
			return map[string]any{"model": key}, nil
		}),
		bindings.WithClientBuilder(func(_ context.Context, cfg map[string]any, _ *slog.Logger) (llm.Client, error) {
			id, _ := cfg["model"].(string)
			return &fakeLLMClient{id: id}, nil
		}),
	)
	t.Cleanup(reg.Close)
	seedAliases(t, reg, "tools")
	w := newBindingsWorkerWithAlias(reg, "tools")

	if _, err := w.HandleManagement(t.Context(), "patch", []string{"bindings"}, json.RawMessage(`{"models":{"tools":null}}`), nil); err != nil {
		t.Fatalf("patch: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if c, err := reg.ModelClient("tools"); err == nil && clientID(t, c) == "tools" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("tools was not pulled back after a stale delete")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWorkerPatchBindings_StaleUpsertIsPulledBack(t *testing.T) {
	t.Parallel()
	reg := bindings.New(
		bindings.WithFetcher(func(_ context.Context, key string) (map[string]any, error) {
			return map[string]any{"model": key}, nil
		}),
		bindings.WithClientBuilder(func(_ context.Context, cfg map[string]any, _ *slog.Logger) (llm.Client, error) {
			id, _ := cfg["model"].(string)
			return &fakeLLMClient{id: id}, nil
		}),
	)
	t.Cleanup(reg.Close)
	seedAliases(t, reg, "tools")
	w := newBindingsWorkerWithAlias(reg, "tools")

	if _, err := w.HandleManagement(t.Context(), "patch", []string{"bindings"}, json.RawMessage(`{"models":{"tools":{"model":"stale"}}}`), nil); err != nil {
		t.Fatalf("patch: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if c, err := reg.ModelClient("tools"); err == nil && clientID(t, c) == "tools" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("tools was not pulled back after a stale upsert")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
