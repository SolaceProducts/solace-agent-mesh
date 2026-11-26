// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

func pruningModelPlan() *PlanResult {
	return &PlanResult{Models: &kindPlan{
		Kind:    "model",
		Deletes: []modelOp{{Name: "old-m", ID: "id-old-m"}},
	}}
}

func decisionAppConfig(nested bool, key, alias string) map[string]any {
	decision := map[string]any{key: alias}
	if nested {
		return map[string]any{"workflow": map[string]any{"decision": decision, "nodes": []any{}}}
	}
	return map[string]any{"decision": decision, "nodes": []any{}}
}

func TestCheckPrunedModelReferences_Conflicts(t *testing.T) {
	cases := []struct {
		name     string
		agents   *agentKindPlan
		wfs      *workflowKindPlan
		wantKind string
		wantName string
	}{
		{
			name:     "agent create by alias",
			agents:   &agentKindPlan{Creates: []agentOp{{Name: "fresh", Create: wire.CreateAgentRequest{ModelProvider: []string{"old-m"}}}}},
			wantKind: "agent", wantName: "fresh",
		},
		{
			name:     "agent update by platform id",
			agents:   &agentKindPlan{Updates: []agentOp{{Name: "keeper", Update: wire.UpdateAgentRequest{ModelProvider: []string{"id-old-m"}}}}},
			wantKind: "agent", wantName: "keeper",
		},
		{
			name:     "unchanged agent",
			agents:   &agentKindPlan{Unchanged: []agentOp{{Name: "idle", Actual: &wire.AgentResponse{ModelProvider: []string{"old-m"}}}}},
			wantKind: "agent", wantName: "idle",
		},
		{
			name:     "workflow create with a flat decision block",
			wfs:      &workflowKindPlan{Creates: []workflowOp{{Name: "triage", Create: wire.CreateWorkflowRequest{AppConfig: decisionAppConfig(false, "model_alias", "old-m")}}}},
			wantKind: "workflow", wantName: "triage",
		},
		{
			name:     "workflow update with a nested decision block",
			wfs:      &workflowKindPlan{Updates: []workflowOp{{Name: "triage", Update: wire.UpdateWorkflowRequest{AppConfig: decisionAppConfig(true, "model_alias", "old-m")}}}},
			wantKind: "workflow", wantName: "triage",
		},
		{
			name:     "unchanged workflow naming the model by camelCase key",
			wfs:      &workflowKindPlan{Unchanged: []workflowOp{{Name: "triage", Create: wire.CreateWorkflowRequest{AppConfig: decisionAppConfig(false, "modelAlias", "old-m")}}}},
			wantKind: "workflow", wantName: "triage",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := pruningModelPlan()
			plan.Agents, plan.Workflows = tc.agents, tc.wfs
			err := checkPrunedModelReferences(plan)
			want := `model "old-m" is deleted by this apply but ` + tc.wantKind + ` "` + tc.wantName + `" still uses it; point it at another model or keep the model`
			if err == nil || err.Error() != want {
				t.Fatalf("checkPrunedModelReferences = %v, want %q", err, want)
			}
			warnings := prunedModelWarnings(plan)
			if len(warnings) != 1 || !strings.Contains(warnings[0], `model "old-m" is not in the manifest`) || !strings.Contains(warnings[0], tc.wantName) {
				t.Errorf("prunedModelWarnings = %q", warnings)
			}
		})
	}
}

func TestCheckPrunedModelReferences_NoConflict(t *testing.T) {
	cases := []struct {
		name   string
		plan   func() *PlanResult
		agents *agentKindPlan
		wfs    *workflowKindPlan
	}{
		{
			name:   "references point at a kept model",
			plan:   pruningModelPlan,
			agents: &agentKindPlan{Creates: []agentOp{{Name: "fresh", Create: wire.CreateAgentRequest{ModelProvider: []string{"new-m"}}}}},
			wfs:    &workflowKindPlan{Updates: []workflowOp{{Name: "triage", Update: wire.UpdateWorkflowRequest{AppConfig: decisionAppConfig(true, "model_alias", "new-m")}}}},
		},
		{
			name:   "the referencing agent is deleted too",
			plan:   pruningModelPlan,
			agents: &agentKindPlan{Deletes: []agentOp{{Name: "gone", ID: "a-1"}}},
		},
		{
			name: "no model deletes planned",
			plan: func() *PlanResult {
				return &PlanResult{Models: &kindPlan{Kind: "model"}}
			},
			agents: &agentKindPlan{Creates: []agentOp{{Name: "fresh", Create: wire.CreateAgentRequest{ModelProvider: []string{"old-m"}}}}},
		},
		{
			name:   "models kind not declared",
			plan:   func() *PlanResult { return &PlanResult{} },
			agents: &agentKindPlan{Creates: []agentOp{{Name: "fresh", Create: wire.CreateAgentRequest{ModelProvider: []string{"old-m"}}}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := tc.plan()
			plan.Agents, plan.Workflows = tc.agents, tc.wfs
			if err := checkPrunedModelReferences(plan); err != nil {
				t.Fatalf("checkPrunedModelReferences: %v", err)
			}
			if warnings := prunedModelWarnings(plan); len(warnings) != 0 {
				t.Errorf("prunedModelWarnings = %q, want none", warnings)
			}
		})
	}
}

func TestCheckPrunedModelReferences_ReportsEveryConflict(t *testing.T) {
	plan := pruningModelPlan()
	plan.Agents = &agentKindPlan{
		Creates: []agentOp{{Name: "fresh", Create: wire.CreateAgentRequest{ModelProvider: []string{"old-m"}}}},
		Updates: []agentOp{{Name: "keeper", Update: wire.UpdateAgentRequest{ModelProvider: []string{"old-m"}}}},
	}
	plan.Workflows = &workflowKindPlan{Creates: []workflowOp{{Name: "triage", Create: wire.CreateWorkflowRequest{AppConfig: decisionAppConfig(false, "model_alias", "old-m")}}}}
	err := checkPrunedModelReferences(plan)
	if err == nil {
		t.Fatal("checkPrunedModelReferences: want an error")
	}
	for _, name := range []string{`agent "fresh"`, `agent "keeper"`, `workflow "triage"`} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name %s", err, name)
		}
	}
}
