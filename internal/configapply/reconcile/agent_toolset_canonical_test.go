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

func agentResolvedWithToolsetSpelling(toolset string) resolve.ResolvedResource {
	return resolve.ResolvedResource{
		Name:        "drifty",
		Description: "an agent authored with an alias toolset spelling",
		SourceFile:  "agents/drifty.yaml",
		Spec: map[string]any{
			"systemPrompt":         "p",
			"toolsets":             []string{toolset},
			"toolsetExcludedTools": map[string]any{toolset: []any{"web_search_google"}},
		},
	}
}

func TestPlanAgents_AliasToolsetDoesNotDrift(t *testing.T) {
	desired := []resolve.ResolvedResource{agentResolvedWithToolsetSpelling("web_tools")}
	actual := []wire.AgentResponse{{
		ID:                   "agent-1",
		Name:                 "drifty",
		Description:          "an agent authored with an alias toolset spelling",
		SystemPrompt:         "p",
		Toolsets:             []string{"builtin_web_request_tools"},
		ToolsetExcludedTools: map[string][]string{"builtin_web_request_tools": {"web_search_google"}},
	}}

	plan, err := planAgents(t.Context(), nil, desired, actual, translate.AgentHooks{})
	if err != nil {
		t.Fatalf("planAgents: %v", err)
	}
	if len(plan.Updates) != 0 {
		t.Errorf("an alias spelling must not read as drift, got updates: %+v", plan.Updates)
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("expected one unchanged entry, got %+v", plan.Unchanged)
	}
}

func TestPlanAgents_CanonicalToolsetStillUnchanged(t *testing.T) {
	desired := []resolve.ResolvedResource{agentResolvedWithToolsetSpelling("builtin_web_request_tools")}
	actual := []wire.AgentResponse{{
		ID:                   "agent-1",
		Name:                 "drifty",
		Description:          "an agent authored with an alias toolset spelling",
		SystemPrompt:         "p",
		Toolsets:             []string{"builtin_web_request_tools"},
		ToolsetExcludedTools: map[string][]string{"builtin_web_request_tools": {"web_search_google"}},
	}}

	plan, err := planAgents(t.Context(), nil, desired, actual, translate.AgentHooks{})
	if err != nil {
		t.Fatalf("planAgents: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("expected one unchanged entry, got updates=%+v unchanged=%+v", plan.Updates, plan.Unchanged)
	}
}

func TestPlanAgents_RealToolsetChangeStillUpdates(t *testing.T) {
	desired := []resolve.ResolvedResource{agentResolvedWithToolsetSpelling("web_tools")}
	actual := []wire.AgentResponse{{
		ID:           "agent-1",
		Name:         "drifty",
		Description:  "an agent authored with an alias toolset spelling",
		SystemPrompt: "p",
		Toolsets:     []string{"builtin_research_tools"},
	}}

	plan, err := planAgents(t.Context(), nil, desired, actual, translate.AgentHooks{})
	if err != nil {
		t.Fatalf("planAgents: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Errorf("a different toolset must still plan an update, got %+v", plan.Updates)
	}
}
