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

func TestAgentDeploymentRepo_CRUD(t *testing.T) {
	db := TestDB(t)
	agentRepo := NewAgentRepo(db)
	repo := NewAgentDeploymentRepo(db)
	ctx := context.Background()

	desc := "Test agent for deployments"
	agent := &Agent{Name: "deploy-agent", Description: &desc, CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	snapshot := `{"name":"deploy-agent"}`
	d := &AgentDeployment{
		AgentID:             agent.ID,
		Status:              "pending",
		Action:              "deploy",
		AgentConfigSnapshot: &snapshot,
		CreatedBy:           "user1",
	}
	err := repo.Create(ctx, d)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if d.ID == "" {
		t.Fatal("expected ID to be set")
	}

	got, err := repo.GetByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected deployment")
	}
	if got.Status != "pending" {
		t.Errorf("status = %q, want %q", got.Status, "pending")
	}
	if got.Action != "deploy" {
		t.Errorf("action = %q, want %q", got.Action, "deploy")
	}
}

func TestAgentDeploymentRepo_GetByID_NotFound(t *testing.T) {
	db := TestDB(t)
	repo := NewAgentDeploymentRepo(db)
	ctx := context.Background()

	got, err := repo.GetByID(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for missing deployment")
	}
}

func TestAgentDeploymentRepo_ListPaginated(t *testing.T) {
	db := TestDB(t)
	agentRepo := NewAgentRepo(db)
	repo := NewAgentDeploymentRepo(db)
	ctx := context.Background()

	desc := "agent for list test"
	agent := &Agent{Name: "list-agent", Description: &desc, CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	baseTime := time.Now().UnixMilli()
	for i := range 3 {
		d := &AgentDeployment{
			AgentID:     agent.ID,
			Status:      "pending",
			Action:      "deploy",
			CreatedBy:   "user1",
			CreatedTime: baseTime + int64(i*1000),
		}
		if err := repo.Create(ctx, d); err != nil {
			t.Fatalf("create deployment %d: %v", i, err)
		}
	}

	list, total, err := repo.ListPaginated(ctx, 1, 2, agent.ID)
	if err != nil {
		t.Fatalf("list paginated: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(list) != 2 {
		t.Errorf("len = %d, want 2", len(list))
	}

	listAll, totalAll, err := repo.ListPaginated(ctx, 1, 10, "")
	if err != nil {
		t.Fatalf("list paginated all: %v", err)
	}
	if totalAll != 3 {
		t.Errorf("total all = %d, want 3", totalAll)
	}
	if len(listAll) != 3 {
		t.Errorf("len all = %d, want 3", len(listAll))
	}
}

func TestAgentDeploymentRepo_UpdateStatus(t *testing.T) {
	db := TestDB(t)
	agentRepo := NewAgentRepo(db)
	repo := NewAgentDeploymentRepo(db)
	ctx := context.Background()

	desc := "agent for update status"
	agent := &Agent{Name: "update-status-agent", Description: &desc, CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	d := &AgentDeployment{
		AgentID:   agent.ID,
		Status:    "pending",
		Action:    "deploy",
		CreatedBy: "user1",
	}
	if err := repo.Create(ctx, d); err != nil {
		t.Fatalf("create: %v", err)
	}

	errMsg := "something went wrong"
	if err := repo.UpdateStatus(ctx, d.ID, "success", &errMsg); err != nil {
		t.Fatalf("update status: %v", err)
	}

	got, err := repo.GetByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != "success" {
		t.Errorf("status = %q, want %q", got.Status, "success")
	}
	if got.ErrorMessage == nil || *got.ErrorMessage != errMsg {
		t.Errorf("error_message = %v, want %q", got.ErrorMessage, errMsg)
	}
}

func TestAgentDeploymentRepo_GetLatestByAgentID(t *testing.T) {
	db := TestDB(t)
	agentRepo := NewAgentRepo(db)
	repo := NewAgentDeploymentRepo(db)
	ctx := context.Background()

	desc := "agent for latest"
	agent := &Agent{Name: "latest-agent", Description: &desc, CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	baseTime := time.Now().UnixMilli()
	older := &AgentDeployment{
		AgentID:     agent.ID,
		Status:      "success",
		Action:      "deploy",
		CreatedBy:   "user1",
		CreatedTime: baseTime,
	}
	if err := repo.Create(ctx, older); err != nil {
		t.Fatalf("create older: %v", err)
	}

	newer := &AgentDeployment{
		AgentID:     agent.ID,
		Status:      "pending",
		Action:      "update",
		CreatedBy:   "user1",
		CreatedTime: baseTime + 5000,
	}
	if err := repo.Create(ctx, newer); err != nil {
		t.Fatalf("create newer: %v", err)
	}

	latest, err := repo.GetLatestByAgentID(ctx, agent.ID)
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if latest == nil {
		t.Fatal("expected latest deployment")
	}
	if latest.ID != newer.ID {
		t.Errorf("latest id = %q, want %q", latest.ID, newer.ID)
	}
}

func TestAgentDeploymentRepo_ListByStatus(t *testing.T) {
	db := TestDB(t)
	agentRepo := NewAgentRepo(db)
	repo := NewAgentDeploymentRepo(db)
	ctx := context.Background()

	desc := "agent for list-by-status"
	agent := &Agent{Name: "status-agent", Description: &desc, CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	for _, status := range []string{"pending", "pending", "success"} {
		d := &AgentDeployment{
			AgentID:   agent.ID,
			Status:    status,
			Action:    "deploy",
			CreatedBy: "user1",
		}
		if err := repo.Create(ctx, d); err != nil {
			t.Fatalf("create %s deployment: %v", status, err)
		}
	}

	pending, err := repo.ListByStatus(ctx, "pending")
	if err != nil {
		t.Fatalf("list by status: %v", err)
	}
	if len(pending) != 2 {
		t.Errorf("pending count = %d, want 2", len(pending))
	}

	success, err := repo.ListByStatus(ctx, "success")
	if err != nil {
		t.Fatalf("list by status success: %v", err)
	}
	if len(success) != 1 {
		t.Errorf("success count = %d, want 1", len(success))
	}
}

func TestAgentDeploymentRepo_OnlyTheDesiredRowKeepsItsDeployedPayloads(t *testing.T) {
	db := TestDB(t)
	agentRepo := NewAgentRepo(db)
	repo := NewAgentDeploymentRepo(db)
	ctx := t.Context()
	agent := &Agent{Name: "payload-agent", CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	snapshot := `{"name":"payload-agent"}`
	validation, runtimeErr := "validation_error", "runtime_error"
	row := func(name, status string, reason *string, at int64) *AgentDeployment {
		payloads := `[{"secret":"` + name + `"}]`
		d := &AgentDeployment{AgentID: agent.ID, Status: status, Action: "deploy", AgentConfigSnapshot: &snapshot, FailureReason: reason, DeployedPayloads: &payloads, CreatedBy: "u", CreatedTime: at}
		if err := repo.Create(ctx, d); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return d
	}
	stored := func(d *AgentDeployment) bool {
		got, err := repo.GetByID(ctx, d.ID)
		if err != nil || got == nil {
			t.Fatalf("read %s: %v", d.ID, err)
		}
		return got.DeployedPayloads != nil
	}
	sweep := func() {
		if _, err := repo.ClearSupersededPayloads(ctx); err != nil {
			t.Fatalf("clear superseded payloads: %v", err)
		}
	}

	first := row("first", "success", nil, 1)
	refused := row("refused", "failed", &validation, 2)
	sweep()
	if !stored(first) || stored(refused) {
		t.Fatalf("after a refused deploy: first kept %v, refused kept %v; want the desired row kept and the refused row without payloads", stored(first), stored(refused))
	}
	failedStart := row("failed start", "failed", &runtimeErr, 3)
	second := row("second", "success", nil, 4)
	if !stored(first) || !stored(failedStart) {
		t.Fatal("a new deployment cleared older rows on insert; the leader's sweep clears them")
	}
	sweep()
	if stored(first) || stored(failedStart) || !stored(second) {
		t.Fatalf("after the sweep: first %v, failed start %v, second %v; want only the newest desired row to keep payloads", stored(first), stored(failedStart), stored(second))
	}
}
