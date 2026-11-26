// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
)

func TestOnlyOneRowIsTheFirstAgentDeploy(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()

	agent := &Agent{Name: "long-lived-agent", CreatedBy: "a", UpdatedBy: "a"}
	if err := NewAgentRepo(db).Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	repo := NewDeploymentRepo(db)
	history := NewDeployHistoryRepo(db)

	steps := []struct {
		action, status string
		wantFirst      bool
	}{
		{"deploy", "failed", false},
		{"deploy", "success", true},
		{"update", "success", false},
		{"undeploy", "success", false},
		{"deploy", "success", false},
	}
	ids := make([]string, len(steps))
	for i, step := range steps {
		row := &AgentDeployment{AgentID: agent.ID, Action: step.action, Status: step.status, CreatedBy: "a"}
		if err := repo.Create(ctx, row); err != nil {
			t.Fatalf("create %s/%s: %v", step.action, step.status, err)
		}
		ids[i] = row.ID
	}

	for i, step := range steps {
		first, err := history.IsFirstAgentDeploy(ctx, ids[i])
		if err != nil {
			t.Fatalf("IsFirstAgentDeploy: %v", err)
		}
		if first != step.wantFirst {
			t.Errorf("row %d (%s/%s): first = %v, want %v", i, step.action, step.status, first, step.wantFirst)
		}
	}
}

func TestEachAgentHasItsOwnFirstDeploy(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewDeploymentRepo(db)
	history := NewDeployHistoryRepo(db)

	for _, name := range []string{"agent-one", "agent-two"} {
		agent := &Agent{Name: name, CreatedBy: "a", UpdatedBy: "a"}
		if err := NewAgentRepo(db).Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
			t.Fatalf("create agent: %v", err)
		}
		row := &AgentDeployment{AgentID: agent.ID, Action: "deploy", Status: "success", CreatedBy: "a"}
		if err := repo.Create(ctx, row); err != nil {
			t.Fatalf("create deployment: %v", err)
		}
		first, err := history.IsFirstAgentDeploy(ctx, row.ID)
		if err != nil {
			t.Fatalf("IsFirstAgentDeploy: %v", err)
		}
		if !first {
			t.Errorf("%s: its own first deploy was not reported as one", name)
		}
	}
}

func TestAnUnknownRowIsNotAFirstDeploy(t *testing.T) {
	db := TestDB(t)
	history := NewDeployHistoryRepo(db)
	for _, id := range []string{"", "3f2e1d00-0000-4000-8000-000000000000"} {
		if first, err := history.IsFirstAgentDeploy(t.Context(), id); err != nil || first {
			t.Errorf("agent id %q: first = %v (err %v), want false", id, first, err)
		}
		if first, err := history.IsFirstWorkflowDeploy(t.Context(), id); err != nil || first {
			t.Errorf("workflow id %q: first = %v (err %v), want false", id, first, err)
		}
	}
}

func TestOnlyOneRowIsTheFirstWorkflowDeploy(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()

	workflow := &Workflow{Name: "long-lived-workflow", AppConfig: "{}", CreatedBy: "a", UpdatedBy: "a"}
	if err := NewWorkflowRepo(db).Create(ctx, workflow, WorkflowBindings{}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	repo := NewWorkflowDeploymentRepo(db)
	history := NewDeployHistoryRepo(db)

	steps := []struct {
		action, status string
		wantFirst      bool
	}{
		{"deploy", "failed", false},
		{"deploy", "success", true},
		{"undeploy", "success", false},
		{"deploy", "success", false},
	}
	for _, step := range steps {
		row := &WorkflowDeployment{WorkflowID: workflow.ID, Action: step.action, Status: step.status, CreatedBy: "a"}
		if err := repo.Create(ctx, row); err != nil {
			t.Fatalf("create %s/%s: %v", step.action, step.status, err)
		}
		first, err := history.IsFirstWorkflowDeploy(ctx, row.ID)
		if err != nil {
			t.Fatalf("IsFirstWorkflowDeploy: %v", err)
		}
		if first != step.wantFirst {
			t.Errorf("%s/%s: first = %v, want %v", step.action, step.status, first, step.wantFirst)
		}
	}
}
