// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
)

func TestReportModelCtx_ResolvesFromBindings(t *testing.T) {
	reg := newBindingsRegistryForTest(t, nil)
	if err := reg.UpsertModel(t.Context(), "general", map[string]any{
		"id":    "general-v1",
		"model": "openai/gpt-4o",
	}); err != nil {
		t.Fatalf("UpsertModel: %v", err)
	}

	agent := New(AgentConfig{
		Name:       "test-agent",
		ModelAlias: "general",
	})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.bindings = reg
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	got := agent.reportModelCtx(t.Context())
	if got != "openai/gpt-4o" {
		t.Errorf("reportModelCtx = %q, want openai/gpt-4o", got)
	}

	agent.resolvedModelName.Store(nil)
	got = agent.reportModelCtx(t.Context())
	if got != "openai/gpt-4o" {
		t.Errorf("reportModelCtx (after cache reset) = %q, want openai/gpt-4o", got)
	}
}

func TestReportModel_PrefersStaticConfig(t *testing.T) {
	agent := New(AgentConfig{
		Name:          "test-agent",
		Model:         "gpt-4",
		OriginalModel: "openai/gpt-4",
	})
	if got := agent.reportModel(); got != "openai/gpt-4" {
		t.Errorf("reportModel (both set) = %q, want openai/gpt-4", got)
	}

	agent2 := New(AgentConfig{
		Name:  "test-agent",
		Model: "gpt-4",
	})
	if got := agent2.reportModel(); got != "gpt-4" {
		t.Errorf("reportModel (only Model) = %q, want gpt-4", got)
	}
}

func TestReportModel_FallsBackToCachedResolvedName(t *testing.T) {
	agent := New(AgentConfig{
		Name:       "test-agent",
		ModelAlias: "general",
	})
	resolved := "openai/gpt-4o"
	agent.resolvedModelName.Store(&resolved)

	if got := agent.reportModel(); got != resolved {
		t.Errorf("reportModel (cached) = %q, want %q", got, resolved)
	}
}

func TestReportModelCtx_NoBindingsNoCache(t *testing.T) {
	agent := New(AgentConfig{Name: "test-agent"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if got := agent.reportModelCtx(t.Context()); got != "" {
		t.Errorf("reportModelCtx (nothing configured) = %q, want empty", got)
	}
}

func TestRefreshResolvedModelName_IgnoresMissingModel(t *testing.T) {
	reg := newBindingsRegistryForTest(t, map[string]string{"general": "general-v1"})
	agent := New(AgentConfig{Name: "test-agent", ModelAlias: "general"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.bindings = reg
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	agent.refreshResolvedModelName(t.Context(), reg)
	if v := agent.resolvedModelName.Load(); v != nil {
		t.Errorf("cache = %q, want nil (no model key in raw config)", *v)
	}
}

var _ = bindings.WithClientBuilder
