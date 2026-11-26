// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"
	"time"
)

func createTestAgentDeployment(ctx context.Context, t *testing.T, agentRepo *AgentRepo, deployRepo *AgentDeploymentRepo, agentName string) (*Agent, *AgentDeployment) {
	t.Helper()
	desc := "test agent"
	agent := &Agent{Name: agentName, Description: &desc, CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent %q: %v", agentName, err)
	}
	d := &AgentDeployment{
		AgentID:   agent.ID,
		Status:    "pending",
		Action:    "deploy",
		CreatedBy: "user1",
	}
	if err := deployRepo.Create(ctx, d); err != nil {
		t.Fatalf("create deployment for %q: %v", agentName, err)
	}
	return agent, d
}

func createTestA2AAgent(ctx context.Context, t *testing.T, repo *A2AAgentRepo, name string) *A2AAgent {
	t.Helper()
	authType := "none"
	authConfig := "{}"
	a := &A2AAgent{
		AgentURL:              "https://example.com/" + name,
		Name:                  name,
		AgentCardLocation:     "well_known",
		AgentAuthType:         &authType,
		AgentAuthConfig:       &authConfig,
		RequestTimeoutSeconds: 60,
		CreatedBy:             "user1",
		UpdatedBy:             "user1",
	}
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("create a2a agent %q: %v", name, err)
	}
	return a
}

func TestA2AAgentDeploymentRepo_CRUD(t *testing.T) {
	db := TestDB(t)
	agentRepo := NewAgentRepo(db)
	deployRepo := NewAgentDeploymentRepo(db)
	a2aRepo := NewA2AAgentRepo(db)
	repo := NewA2AAgentDeploymentRepo(db)
	ctx := context.Background()

	_, deployment := createTestAgentDeployment(ctx, t, agentRepo, deployRepo, "a2a-deploy-agent")
	a2aAgent := createTestA2AAgent(ctx, t, a2aRepo, "a2a-crud-agent")

	link := &A2AAgentDeployment{
		A2AAgentID:        a2aAgent.ID,
		AgentDeploymentID: deployment.ID,
		Action:            strPtr("deploy"),
		ConfigSnapshot:    strPtr(`{"url":"https://example.com"}`),
	}
	if err := repo.Create(ctx, link); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.GetByDeploymentID(ctx, deployment.ID)
	if err != nil {
		t.Fatalf("get by deployment id: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.A2AAgentID != a2aAgent.ID {
		t.Errorf("a2a_agent_id = %q, want %q", got.A2AAgentID, a2aAgent.ID)
	}
	if got.Action == nil || *got.Action != "deploy" {
		t.Errorf("action = %v, want %q", got.Action, "deploy")
	}
}

func TestA2AAgentDeploymentRepo_GetByDeploymentID_NotFound(t *testing.T) {
	db := TestDB(t)
	repo := NewA2AAgentDeploymentRepo(db)
	ctx := context.Background()

	got, err := repo.GetByDeploymentID(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for not found")
	}
}

func TestA2AAgentDeploymentRepo_ListByA2AAgentID(t *testing.T) {
	db := TestDB(t)
	agentRepo := NewAgentRepo(db)
	deployRepo := NewAgentDeploymentRepo(db)
	a2aRepo := NewA2AAgentRepo(db)
	repo := NewA2AAgentDeploymentRepo(db)
	ctx := context.Background()

	a2aAgent := createTestA2AAgent(ctx, t, a2aRepo, "a2a-list-agent")

	baseTime := time.Now().UnixMilli()
	for i := range 2 {
		desc := "agent for a2a list"
		agent := &Agent{Name: "a2a-list-dep-agent-" + string(rune('a'+i)), Description: &desc, CreatedBy: "u", UpdatedBy: "u"}
		if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
			t.Fatalf("create agent %d: %v", i, err)
		}
		d := &AgentDeployment{
			AgentID:     agent.ID,
			Status:      "pending",
			Action:      "deploy",
			CreatedBy:   "user1",
			CreatedTime: baseTime + int64(i*1000),
		}
		if err := deployRepo.Create(ctx, d); err != nil {
			t.Fatalf("create deployment %d: %v", i, err)
		}
		link := &A2AAgentDeployment{
			A2AAgentID:        a2aAgent.ID,
			AgentDeploymentID: d.ID,
			Action:            strPtr("deploy"),
		}
		if err := repo.Create(ctx, link); err != nil {
			t.Fatalf("create link %d: %v", i, err)
		}
	}

	list, total, err := repo.ListByA2AAgentID(ctx, a2aAgent.ID, 1, 10)
	if err != nil {
		t.Fatalf("list by a2a agent id: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(list) != 2 {
		t.Errorf("len = %d, want 2", len(list))
	}

	listPage, totalPage, err := repo.ListByA2AAgentID(ctx, a2aAgent.ID, 1, 1)
	if err != nil {
		t.Fatalf("list by a2a agent id paginated: %v", err)
	}
	if totalPage != 2 {
		t.Errorf("total paginated = %d, want 2", totalPage)
	}
	if len(listPage) != 1 {
		t.Errorf("len paginated = %d, want 1", len(listPage))
	}
}

func TestA2AAgentDeploymentRepo_ListAllPaginated(t *testing.T) {
	db := TestDB(t)
	agentRepo := NewAgentRepo(db)
	deployRepo := NewAgentDeploymentRepo(db)
	a2aRepo := NewA2AAgentRepo(db)
	repo := NewA2AAgentDeploymentRepo(db)
	ctx := context.Background()

	a2aAgent1 := createTestA2AAgent(ctx, t, a2aRepo, "a2a-all-agent-1")
	a2aAgent2 := createTestA2AAgent(ctx, t, a2aRepo, "a2a-all-agent-2")

	for i, a2aAgent := range []*A2AAgent{a2aAgent1, a2aAgent2} {
		desc := "agent for a2a all"
		agent := &Agent{Name: "a2a-all-dep-agent-" + string(rune('a'+i)), Description: &desc, CreatedBy: "u", UpdatedBy: "u"}
		if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
			t.Fatalf("create agent %d: %v", i, err)
		}
		d := &AgentDeployment{
			AgentID:   agent.ID,
			Status:    "pending",
			Action:    "deploy",
			CreatedBy: "user1",
		}
		if err := deployRepo.Create(ctx, d); err != nil {
			t.Fatalf("create deployment %d: %v", i, err)
		}
		link := &A2AAgentDeployment{
			A2AAgentID:        a2aAgent.ID,
			AgentDeploymentID: d.ID,
			Action:            strPtr("deploy"),
		}
		if err := repo.Create(ctx, link); err != nil {
			t.Fatalf("create link %d: %v", i, err)
		}
	}

	list, total, err := repo.ListAllPaginated(ctx, 1, 10)
	if err != nil {
		t.Fatalf("list all paginated: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(list) != 2 {
		t.Errorf("len = %d, want 2", len(list))
	}

	listPage, totalPage, err := repo.ListAllPaginated(ctx, 1, 1)
	if err != nil {
		t.Fatalf("list all paginated page: %v", err)
	}
	if totalPage != 2 {
		t.Errorf("total paginated = %d, want 2", totalPage)
	}
	if len(listPage) != 1 {
		t.Errorf("len paginated = %d, want 1", len(listPage))
	}
}
