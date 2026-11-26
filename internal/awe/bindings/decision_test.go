// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bindings

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func decisionRaw() map[string]any {
	return map[string]any{
		"model":             "jev-1.13.0",
		"api_base":          "https://api.typesafe.ai",
		"api_key":           "ts-secret",
		samapp.ModelTypeKey: samapp.DecisionModelType,
	}
}

func TestDecisionBindingBuildsNoChatClient(t *testing.T) {
	var builds atomic.Int32
	r := New(WithClientBuilder(stubBuilder(&builds)))

	if err := r.UpsertModel(t.Context(), "jev", decisionRaw()); err != nil {
		t.Fatalf("UpsertModel: %v", err)
	}
	if builds.Load() != 0 {
		t.Errorf("a decision binding built %d chat clients", builds.Load())
	}

	if _, err := r.ModelClient("jev"); !errors.Is(err, ErrNotChatModel) {
		t.Errorf("ModelClient err = %v, want ErrNotChatModel", err)
	}
	if _, err := r.ResolveOrFetch(t.Context(), "jev"); !errors.Is(err, ErrNotChatModel) {
		t.Errorf("ResolveOrFetch err = %v, want ErrNotChatModel", err)
	}
	raw, err := r.ResolveRawOrFetch(t.Context(), "jev")
	if err != nil {
		t.Fatalf("ResolveRawOrFetch: %v", err)
	}
	if raw["api_key"] != "ts-secret" || raw["api_base"] != "https://api.typesafe.ai" {
		t.Errorf("raw = %v", raw)
	}
}

func TestDecisionBindingResolvesThroughTheFetcher(t *testing.T) {
	var builds, fetches atomic.Int32
	r := New(
		WithClientBuilder(stubBuilder(&builds)),
		WithFetcher(func(context.Context, string) (map[string]any, error) {
			fetches.Add(1)
			return decisionRaw(), nil
		}),
	)

	for range 2 {
		raw, err := r.ResolveRawOrFetch(t.Context(), "jev")
		if err != nil {
			t.Fatalf("ResolveRawOrFetch: %v", err)
		}
		if !samapp.IsDecisionModel(raw) {
			t.Errorf("raw lost its model type: %v", raw)
		}
	}
	if fetches.Load() != 1 {
		t.Errorf("fetches = %d, want the second resolve served from the registry", fetches.Load())
	}
	if builds.Load() != 0 {
		t.Errorf("builds = %d, want none", builds.Load())
	}
}

func TestBindingSwitchingTypeUpdatesItsClient(t *testing.T) {
	r := New(WithClientBuilder(stubBuilder(nil)))
	chat := map[string]any{"model": "openai/gpt-4o", "api_key": "sk"}

	if err := r.UpsertModel(t.Context(), "m", chat); err != nil {
		t.Fatal(err)
	}
	if err := r.UpsertModel(t.Context(), "m", decisionRaw()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ModelClient("m"); !errors.Is(err, ErrNotChatModel) {
		t.Errorf("after becoming a decision model, ModelClient err = %v", err)
	}
	if err := r.UpsertModel(t.Context(), "m", chat); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ModelClient("m"); err != nil {
		t.Errorf("after becoming a chat model again, ModelClient err = %v", err)
	}
}

func TestConcurrentTypeSwitchKeepsDecisionAndClientConsistent(t *testing.T) {
	r := New(WithClientBuilder(stubBuilder(nil)))
	chat := map[string]any{"model": "openai/gpt-4o", "api_key": "sk"}
	const upsertsPerKind = 50

	var wg sync.WaitGroup
	for range upsertsPerKind {
		wg.Go(func() {
			if err := r.UpsertModel(t.Context(), "m", chat); err != nil {
				t.Errorf("chat UpsertModel: %v", err)
			}
		})
		wg.Go(func() {
			if err := r.UpsertModel(t.Context(), "m", decisionRaw()); err != nil {
				t.Errorf("decision UpsertModel: %v", err)
			}
		})
	}
	wg.Wait()

	r.mu.RLock()
	b := r.models["m"]
	decision := b.decision
	hasClient := b.current.Load() != nil
	r.mu.RUnlock()

	if decision == hasClient {
		t.Errorf("decision = %v with client installed = %v, want exactly one", decision, hasClient)
	}
}
