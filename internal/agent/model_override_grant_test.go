// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type noGrantTokens struct{}

func (noGrantTokens) GrantToken(context.Context, string, string) (llm.GrantToken, error) {
	return llm.GrantToken{}, nil
}

func TestTaskModelOverrideCarriesTheProcessGrantSource(t *testing.T) {
	grants := noGrantTokens{}
	reg := bindings.New(
		bindings.WithClientBuilder(func(context.Context, map[string]any, *slog.Logger) (llm.Client, error) {
			return &bindingsStubClient{}, nil
		}),
		bindings.WithGrantTokens(grants),
	)
	if err := reg.UpsertModel(t.Context(), "signed-in", map[string]any{"model": "openai/free-model", llm.GrantIDKey: "grant-1"}); err != nil {
		t.Fatalf("UpsertModel: %v", err)
	}
	agent := New(AgentConfig{Name: "own-model-agent", Model: "gpt-4o"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.bindings = reg
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	o, reason := agent.resolveTaskModelOverride(t.Context(), map[string]any{
		"model_override": map[string]any{"model_id": "signed-in"},
	}, slog.New(slog.DiscardHandler))
	if o == nil {
		t.Fatalf("override not resolved: %q", reason)
	}
	if o.Config.GrantID != "grant-1" || o.Config.Grants != llm.GrantTokens(grants) {
		t.Errorf("override grant %q, source %v; want grant-1 with the process's source", o.Config.GrantID, o.Config.Grants)
	}
}
