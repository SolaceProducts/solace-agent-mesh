// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
)

func TestBuiltinToolsetConfigs_VolumeBindingsRoundTrip(t *testing.T) {
	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	agentRepo := NewAgentRepo(db)
	ctx := t.Context()

	agent := &Agent{Name: "agent-volume-bindings", Description: new(string), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent create: %v", err)
	}

	binds := `{"workspace":"ws_slot"}`
	cfg := `{"some_tool":{"k":"v"}}`
	if err := repo.SetBuiltinToolsetConfigsForAgent(ctx, agent.ID, []AgentBuiltinToolsetConfig{
		{AgentID: agent.ID, ToolsetName: "builtin_volume_tools", VolumeBindings: &binds},
		{AgentID: agent.ID, ToolsetName: "builtin_research_tools", ConfigValues: &cfg},
		{AgentID: agent.ID, ToolsetName: "both_tools", ConfigValues: &cfg, VolumeBindings: &binds},
	}); err != nil {
		t.Fatalf("SetBuiltinToolsetConfigsForAgent: %v", err)
	}

	rows, err := repo.GetBuiltinToolsetConfigsForAgent(ctx, agent.ID)
	if err != nil {
		t.Fatalf("GetBuiltinToolsetConfigsForAgent: %v", err)
	}
	byName := make(map[string]AgentBuiltinToolsetConfig, len(rows))
	for _, r := range rows {
		byName[r.ToolsetName] = r
	}
	if len(byName) != 3 {
		t.Fatalf("want 3 rows, got %d: %+v", len(byName), rows)
	}

	if got := byName["builtin_volume_tools"]; got.VolumeBindings == nil || *got.VolumeBindings != binds {
		t.Errorf("volume_tools VolumeBindings = %v, want %q", got.VolumeBindings, binds)
	} else if got.ConfigValues != nil {
		t.Errorf("volume_tools ConfigValues = %v, want nil", *got.ConfigValues)
	}

	if got := byName["builtin_research_tools"]; got.VolumeBindings != nil {
		t.Errorf("research VolumeBindings = %v, want nil", *got.VolumeBindings)
	} else if got.ConfigValues == nil || *got.ConfigValues != cfg {
		t.Errorf("research ConfigValues = %v, want %q", got.ConfigValues, cfg)
	}

	if got := byName["both_tools"]; got.VolumeBindings == nil || *got.VolumeBindings != binds || got.ConfigValues == nil || *got.ConfigValues != cfg {
		t.Errorf("both_tools = %+v, want both fields set", got)
	}

	batch, err := repo.GetBuiltinToolsetConfigsForAgents(ctx, []string{agent.ID})
	if err != nil {
		t.Fatalf("GetBuiltinToolsetConfigsForAgents: %v", err)
	}
	if len(batch[agent.ID]) != 3 {
		t.Fatalf("batch want 3 rows, got %d", len(batch[agent.ID]))
	}
}
