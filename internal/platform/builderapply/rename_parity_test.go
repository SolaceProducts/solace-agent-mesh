// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"slices"
	"strings"
	"testing"
)

type renameParityCase struct {
	name       string
	seed       func(t *testing.T, svc *Service) map[string]string
	build      func(ids map[string]string) []BuildComponentInput
	want       map[string]renameOutcome
	wantFailed []string
}

type renameOutcome struct {
	action      string
	resource    string
	warning     string
	renamedFrom string
}

func seedAgents(names ...string) func(t *testing.T, svc *Service) map[string]string {
	return func(t *testing.T, svc *Service) map[string]string {
		ids := map[string]string{}
		for _, name := range names {
			ids[name] = seedAgent(t, svc, name).ID
		}
		return ids
	}
}

func renameParityCases() []renameParityCase {
	const unknownID = "01a15453-e34d-7330-a292-50320e36ef02"
	return []renameParityCase{
		{
			name: "the id alone",
			seed: seedAgents("old_agent"),
			build: func(ids map[string]string) []BuildComponentInput {
				return []BuildComponentInput{withResourceID(agentComponent("new_agent"), ids["old_agent"])}
			},
			want: map[string]renameOutcome{"agent/new_agent": {actionUpdate, "old_agent", "in place", "old_agent"}},
		},
		{
			name: "the name alone",
			seed: seedAgents("old_agent"),
			build: func(map[string]string) []BuildComponentInput {
				return []BuildComponentInput{agentComponent("old_agent")}
			},
			want: map[string]renameOutcome{"agent/old_agent": {actionUnchanged, "old_agent", "", ""}},
		},
		{
			name: "the name and the id naming the same resource",
			seed: seedAgents("old_agent"),
			build: func(ids map[string]string) []BuildComponentInput {
				return []BuildComponentInput{withResourceID(agentComponent("old_agent"), ids["old_agent"])}
			},
			want: map[string]renameOutcome{"agent/old_agent": {actionUnchanged, "old_agent", "", ""}},
		},
		{
			name: "the name held by another resource than the id names",
			seed: seedAgents("agent_a", "agent_b"),
			build: func(ids map[string]string) []BuildComponentInput {
				return []BuildComponentInput{withResourceID(agentComponent("agent_b"), ids["agent_a"])}
			},
			want: map[string]renameOutcome{"agent/agent_b": {actionUnchanged, "agent_b", `the id of the stored agent "agent_a" for this component, which is not used: the stored agent that carries this name is written instead`, ""}},
		},
		{
			name: "a new component taking the name the renamed one leaves",
			seed: seedAgents("old_agent"),
			build: func(ids map[string]string) []BuildComponentInput {
				return []BuildComponentInput{withResourceID(agentComponent("new_agent"), ids["old_agent"]), agentComponent("old_agent")}
			},
			want: map[string]renameOutcome{
				"agent/old_agent": {actionUnchanged, "old_agent", "", ""},
				"agent/new_agent": {actionCreate, "", `the id of the stored agent "old_agent" for this component, which is not used: a new agent is created`, ""},
			},
		},
		{
			name: "a chain of renames",
			seed: seedAgents("agent_a", "agent_b"),
			build: func(ids map[string]string) []BuildComponentInput {
				return []BuildComponentInput{withResourceID(agentComponent("agent_b"), ids["agent_a"]), withResourceID(agentComponent("agent_c"), ids["agent_b"])}
			},
			want: map[string]renameOutcome{
				"agent/agent_b": {actionUnchanged, "agent_b", `the stored agent that carries this name is written instead`, ""},
				"agent/agent_c": {actionCreate, "", `which is not used: a new agent is created`, ""},
			},
		},
		{
			name: "an id no stored agent has",
			seed: seedAgents("old_agent"),
			build: func(map[string]string) []BuildComponentInput {
				return []BuildComponentInput{withResourceID(agentComponent("new_agent"), unknownID)}
			},
			want: map[string]renameOutcome{"agent/new_agent": {actionCreate, "", "that no stored agent has, which is not used: a new agent is created", ""}},
		},
		{
			name: "a remote agent created under the name a rename takes",
			seed: seedAgents("old_agent"),
			build: func(ids map[string]string) []BuildComponentInput {
				return []BuildComponentInput{a2aComponent("new_agent", nil), withResourceID(agentComponent("new_agent"), ids["old_agent"])}
			},
			want: map[string]renameOutcome{
				"a2a_agent/new_agent": {actionCreate, "", "", ""},
				"agent/new_agent":     {actionUpdate, "old_agent", "in place", "old_agent"},
			},
			wantFailed: []string{"agent/new_agent"},
		},
		{
			name: "a workflow",
			seed: func(t *testing.T, svc *Service) map[string]string {
				seedAgent(t, svc, "test_agent")
				if resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{workflowComponent("order_wf", "OrderFlow", "test_agent")}}, testActor); err != nil || resp.Summary.Failed != 0 {
					t.Fatalf("seed workflow: %v %+v", err, resp)
				}
				return map[string]string{"OrderFlow": workflowNamed(t, svc, "OrderFlow").ID}
			},
			build: func(ids map[string]string) []BuildComponentInput {
				return []BuildComponentInput{withResourceID(workflowComponent("order_pipeline", "OrderPipeline", "test_agent"), ids["OrderFlow"])}
			},
			want: map[string]renameOutcome{"workflow/order_pipeline": {actionUpdate, "OrderFlow", `renames "OrderFlow" to "OrderPipeline" in place`, "OrderFlow"}},
		},
		{
			name: "an id no stored agent has, while the name is held",
			seed: seedAgents("old_agent"),
			build: func(map[string]string) []BuildComponentInput {
				return []BuildComponentInput{withResourceID(agentComponent("old_agent"), unknownID)}
			},
			want: map[string]renameOutcome{"agent/old_agent": {actionUnchanged, "old_agent", "that no stored agent has, which is not used: the stored agent that carries this name is written instead", ""}},
		},
		{
			name: "two renames that swap names",
			seed: seedAgents("agent_a", "agent_b"),
			build: func(ids map[string]string) []BuildComponentInput {
				return []BuildComponentInput{withResourceID(agentComponent("agent_a"), ids["agent_b"]), withResourceID(agentComponent("agent_b"), ids["agent_a"])}
			},
			want: map[string]renameOutcome{
				"agent/agent_a": {actionUnchanged, "agent_a", "which is not used: the stored agent that carries this name is written instead", ""},
				"agent/agent_b": {actionUnchanged, "agent_b", "which is not used: the stored agent that carries this name is written instead", ""},
			},
		},
		{
			name: "a change of case only, which the name pairs",
			seed: seedAgents("refund_orchestrator"),
			build: func(map[string]string) []BuildComponentInput {
				return []BuildComponentInput{agentComponent("Refund_Orchestrator")}
			},
			want: map[string]renameOutcome{"agent/Refund_Orchestrator": {actionUpdate, "refund_orchestrator", "ignoring case and spaces", "refund_orchestrator"}},
		},
		{
			name: "a workflow paired by its legacy name whose recorded id no stored workflow has",
			seed: func(t *testing.T, svc *Service) map[string]string {
				return map[string]string{"legacy": seedStoredWorkflow(t, svc, "mule_detection_workflow", flatMuleDetectionWorkflow()).ID}
			},
			build: func(map[string]string) []BuildComponentInput {
				return []BuildComponentInput{withResourceID(flatMuleDetectionWorkflow(), unknownID)}
			},
			want: map[string]renameOutcome{"workflow/MuleDetectionWorkflow": {actionUpdate, "legacy", "that no stored workflow has, which is not used", "mule_detection_workflow"}},
		},
		{
			name: "a workflow whose recorded id no stored workflow has, while the name is held",
			seed: func(t *testing.T, svc *Service) map[string]string {
				seedAgent(t, svc, "test_agent")
				if resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{workflowComponent("order_wf", "OrderFlow", "test_agent")}}, testActor); err != nil || resp.Summary.Failed != 0 {
					t.Fatalf("seed workflow: %v %+v", err, resp)
				}
				return map[string]string{"OrderFlow": workflowNamed(t, svc, "OrderFlow").ID}
			},
			build: func(map[string]string) []BuildComponentInput {
				return []BuildComponentInput{withResourceID(workflowComponent("order_wf", "OrderFlow", "test_agent"), unknownID)}
			},
			want: map[string]renameOutcome{"workflow/order_wf": {actionUnchanged, "OrderFlow", "that no stored workflow has, which is not used: the stored workflow that carries this name is written instead", ""}},
		},
	}
}

func TestRenameByID_PreviewAndApplyMakeTheSameDecision(t *testing.T) {
	t.Parallel()
	for _, tc := range renameParityCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			ids := tc.seed(t, svc)
			request := ApplyBuildRequest{Components: tc.build(ids)}

			dry, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: request.Components}, testActor)
			if err != nil {
				t.Fatalf("preview: %v", err)
			}
			applied, err := svc.Apply(t.Context(), request, testActor)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}

			for _, planned := range dry.Operations {
				real := opFor(t, applied, planned.Kind, planned.Name)
				key := planned.Kind + "/" + planned.Name
				if planned.Action != real.Action {
					t.Errorf("%s: the preview plans %q and the apply does %q", key, planned.Action, real.Action)
				}
				if planned.Action != actionCreate && planned.ResourceID != real.ResourceID {
					t.Errorf("%s: the preview acts on %s and the apply on %s", key, planned.ResourceID, real.ResourceID)
				}
				if (planned.Status == statusFailed) != (real.Status == statusFailed) {
					t.Errorf("%s: the preview says %q and the apply %q", key, planned.Status, real.Status)
				}
				if !slices.Equal(planned.Warnings, real.Warnings) {
					t.Errorf("%s: the preview warns %v and the apply %v", key, planned.Warnings, real.Warnings)
				}
			}

			for key, want := range tc.want {
				kind, name := splitKey(key)
				row := opFor(t, dry, kind, name)
				if row.Action != want.action {
					t.Errorf("%s: action = %q, want %q", key, row.Action, want.action)
				}
				if want.resource != "" && row.ResourceID != ids[want.resource] {
					t.Errorf("%s: acts on %s, want %q (%s)", key, row.ResourceID, want.resource, ids[want.resource])
				}
				if row.RenamedFrom != want.renamedFrom {
					t.Errorf("%s: renamedFrom = %q, want %q", key, row.RenamedFrom, want.renamedFrom)
				}
				if appliedRow := opFor(t, applied, kind, name); appliedRow.RenamedFrom != row.RenamedFrom {
					t.Errorf("%s: the preview renames from %q and the apply from %q", key, row.RenamedFrom, appliedRow.RenamedFrom)
				}
				if want.warning != "" && !mentions(row.Warnings, want.warning) {
					t.Errorf("%s: warnings = %v, want one containing %q", key, row.Warnings, want.warning)
				}
				if want.warning == "" && len(row.Warnings) != 0 {
					t.Errorf("%s: warnings = %v, want none", key, row.Warnings)
				}
			}
			for _, key := range tc.wantFailed {
				kind, name := splitKey(key)
				if opFor(t, dry, kind, name).Status != statusFailed || opFor(t, applied, kind, name).Status != statusFailed {
					t.Errorf("%s: want both the preview and the apply to reject it", key)
				}
			}
		})
	}
}

func splitKey(key string) (kind, name string) {
	kind, name, _ = strings.Cut(key, "/")
	return kind, name
}
