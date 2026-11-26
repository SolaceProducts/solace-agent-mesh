// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
)

func TestBuiltinToolsetConfigs_NetworkBindingsRoundTrip(t *testing.T) {
	db := TestDB(t)
	repo := NewToolPackageRepo(db)
	agentRepo := NewAgentRepo(db)
	ctx := t.Context()

	agent := &Agent{Name: "agent-network-bindings", Description: new(string), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(ctx, agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("agent create: %v", err)
	}

	netBinds := `{"anthropic_api":"code_egress","outbound":"code_egress"}`
	volBinds := `{"workspace":"ws_slot"}`
	cfg := `{"some_tool":{"k":"v"}}`
	if err := repo.SetBuiltinToolsetConfigsForAgent(ctx, agent.ID, []AgentBuiltinToolsetConfig{
		{AgentID: agent.ID, ToolsetName: "network_only", NetworkBindings: &netBinds},
		{AgentID: agent.ID, ToolsetName: "config_only", ConfigValues: &cfg},
		{AgentID: agent.ID, ToolsetName: "all_three", ConfigValues: &cfg, VolumeBindings: &volBinds, NetworkBindings: &netBinds},
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

	if got := byName["network_only"]; got.NetworkBindings == nil || *got.NetworkBindings != netBinds {
		t.Errorf("network_only NetworkBindings = %v, want %q", got.NetworkBindings, netBinds)
	} else if got.ConfigValues != nil || got.VolumeBindings != nil {
		t.Errorf("network_only should have nil config/volume, got cfg=%v vol=%v", got.ConfigValues, got.VolumeBindings)
	}

	if got := byName["config_only"]; got.NetworkBindings != nil {
		t.Errorf("config_only NetworkBindings = %v, want nil", *got.NetworkBindings)
	}

	if got := byName["all_three"]; got.NetworkBindings == nil || *got.NetworkBindings != netBinds ||
		got.VolumeBindings == nil || *got.VolumeBindings != volBinds || got.ConfigValues == nil || *got.ConfigValues != cfg {
		t.Errorf("all_three = %+v, want all fields set", got)
	}

	batch, err := repo.GetBuiltinToolsetConfigsForAgents(ctx, []string{agent.ID})
	if err != nil {
		t.Fatalf("GetBuiltinToolsetConfigsForAgents: %v", err)
	}
	var sawNetwork bool
	for _, r := range batch[agent.ID] {
		if r.ToolsetName == "network_only" && r.NetworkBindings != nil && *r.NetworkBindings == netBinds {
			sawNetwork = true
		}
	}
	if !sawNetwork {
		t.Errorf("batch read did not return network_bindings for network_only: %+v", batch[agent.ID])
	}
}
