// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"slices"
	"testing"
)

func builtinToolsetNames(rows []AgentBuiltinToolsetConfig) []string {
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.ToolsetName)
	}
	return names
}

func TestBuiltinToolsetConfigs_Ordered(t *testing.T) {
	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	agentRepo := NewAgentRepo(db)
	ctx := t.Context()

	var agentIDs []string
	for _, name := range []string{"agent-order-a", "agent-order-b"} {
		agent := &Agent{Name: name, Description: new(string), CreatedBy: "u", UpdatedBy: "u"}
		if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
			t.Fatalf("agent create: %v", err)
		}
		agentIDs = append(agentIDs, agent.ID)
	}
	slices.Sort(agentIDs)

	insertionOrder := []string{"zeta", "mango", "alpha"}
	wantOrder := []string{"alpha", "mango", "zeta"}

	for _, agentID := range slices.Backward(agentIDs) {
		var configs []AgentBuiltinToolsetConfig
		for _, toolset := range insertionOrder {
			configs = append(configs, AgentBuiltinToolsetConfig{AgentID: agentID, ToolsetName: toolset})
		}
		if err := repo.SetBuiltinToolsetConfigsForAgent(ctx, agentID, configs); err != nil {
			t.Fatalf("SetBuiltinToolsetConfigsForAgent: %v", err)
		}
	}

	for _, agentID := range agentIDs {
		rows, err := repo.GetBuiltinToolsetConfigsForAgent(ctx, agentID)
		if err != nil {
			t.Fatalf("GetBuiltinToolsetConfigsForAgent: %v", err)
		}
		if got := builtinToolsetNames(rows); !slices.Equal(got, wantOrder) {
			t.Errorf("single read for %s = %v, want %v", agentID, got, wantOrder)
		}
	}

	batch, err := repo.GetBuiltinToolsetConfigsForAgents(ctx, agentIDs)
	if err != nil {
		t.Fatalf("GetBuiltinToolsetConfigsForAgents: %v", err)
	}
	for _, agentID := range agentIDs {
		if got := builtinToolsetNames(batch[agentID]); !slices.Equal(got, wantOrder) {
			t.Errorf("batch read for %s = %v, want %v", agentID, got, wantOrder)
		}
	}
}
