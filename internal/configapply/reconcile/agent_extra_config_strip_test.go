// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

func agentResolvedWithExtraConfig(extra map[string]any) resolve.ResolvedResource {
	return resolve.ResolvedResource{
		Name:        "gated",
		Description: "an agent whose manifest carries an agent-level hil block",
		SourceFile:  "agents/gated.yaml",
		Spec: map[string]any{
			"systemPrompt":             "p",
			"additionalConfigurations": extra,
		},
	}
}

func agentActualWithExtraConfig(t *testing.T, extra map[string]any) []wire.AgentResponse {
	t.Helper()
	return []wire.AgentResponse{{
		ID:                       "agent-1",
		Name:                     "gated",
		Description:              "an agent whose manifest carries an agent-level hil block",
		SystemPrompt:             "p",
		AdditionalConfigurations: extra,
	}}
}

func TestPlanAgents_StrippedKeyDoesNotDrift(t *testing.T) {
	desired := []resolve.ResolvedResource{agentResolvedWithExtraConfig(map[string]any{
		"hil":               map[string]any{"enabled": true, "timeout": "45m"},
		"supportsStreaming": true,
	})}
	actual := agentActualWithExtraConfig(t, map[string]any{"supportsStreaming": true})

	plan, err := planAgents(t.Context(), nil, desired, actual, translate.AgentHooks{})
	if err != nil {
		t.Fatalf("planAgents: %v", err)
	}
	if len(plan.Updates) != 0 {
		t.Errorf("a key the platform strips on write must not read as drift, got updates: %+v", plan.Updates)
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("expected one unchanged entry, got %+v", plan.Unchanged)
	}
}

func TestPlanAgents_HilOnlyExtraConfigDoesNotDrift(t *testing.T) {
	desired := []resolve.ResolvedResource{agentResolvedWithExtraConfig(map[string]any{
		"hil": map[string]any{"enabled": true},
	})}
	actual := agentActualWithExtraConfig(t, map[string]any{})

	plan, err := planAgents(t.Context(), nil, desired, actual, translate.AgentHooks{})
	if err != nil {
		t.Fatalf("planAgents: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("expected one unchanged entry, got updates=%+v unchanged=%+v", plan.Updates, plan.Unchanged)
	}
}

func TestPlanAgents_LegacyStoredHilStillUpdatesOnce(t *testing.T) {
	extra := map[string]any{"hil": map[string]any{"enabled": true}, "supportsStreaming": true}
	desired := []resolve.ResolvedResource{agentResolvedWithExtraConfig(extra)}
	actual := agentActualWithExtraConfig(t, extra)

	plan, err := planAgents(t.Context(), nil, desired, actual, translate.AgentHooks{})
	if err != nil {
		t.Fatalf("planAgents: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Fatalf("a stored legacy hil block must plan one cleanup update, got %+v", plan.Updates)
	}
}

func TestPlanAgents_RealExtraConfigChangeStillUpdates(t *testing.T) {
	desired := []resolve.ResolvedResource{agentResolvedWithExtraConfig(map[string]any{
		"hil":               map[string]any{"enabled": true},
		"supportsStreaming": false,
	})}
	actual := agentActualWithExtraConfig(t, map[string]any{"supportsStreaming": true})

	plan, err := planAgents(t.Context(), nil, desired, actual, translate.AgentHooks{})
	if err != nil {
		t.Fatalf("planAgents: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Errorf("a changed live key must still plan an update, got %+v", plan.Updates)
	}
}
