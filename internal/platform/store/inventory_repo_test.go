// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"slices"
	"testing"
)

func seedInventoryAgent(t *testing.T, db *DB, name string, toolsets ...string) *Agent {
	t.Helper()
	a := &Agent{Name: name, CreatedBy: "alice", UpdatedBy: "alice"}
	if err := NewAgentRepo(db).Create(context.Background(), a, toolsets, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent %q: %v", name, err)
	}
	return a
}

func seedInventoryWorkflow(t *testing.T, db *DB, name string, toolsets ...string) *Workflow {
	t.Helper()
	w := &Workflow{Name: name, AppConfig: "{}", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := NewWorkflowRepo(db).Create(context.Background(), w, WorkflowBindings{Toolsets: toolsets}); err != nil {
		t.Fatalf("create workflow %q: %v", name, err)
	}
	return w
}

const seedDescription = "seeded for inventory tests"

func seedInventoryConnector(t *testing.T, db *DB, name, connType string) {
	t.Helper()
	description := seedDescription
	if err := NewConnectorRepo(db).Create(context.Background(), &Connector{
		Name: name, Description: &description, Type: connType, CreatedBy: "alice", UpdatedBy: "alice",
	}); err != nil {
		t.Fatalf("create connector %q: %v", name, err)
	}
}

func seedInventoryGateway(t *testing.T, db *DB, name, gwType string) {
	t.Helper()
	description := seedDescription
	if err := NewGatewayRepo(db).Create(context.Background(), &Gateway{
		Name: name, Description: &description, Type: gwType, CreatedBy: "alice", UpdatedBy: "alice",
	}); err != nil {
		t.Fatalf("create gateway %q: %v", name, err)
	}
}

func seedInventoryToolPackage(t *testing.T, db *DB, name string) {
	t.Helper()
	if err := NewToolPackageRepo(db).Create(context.Background(), newTestPackage(name)); err != nil {
		t.Fatalf("create tool package %q: %v", name, err)
	}
}

func TestInventorySnapshotOfAnEmptyInstall(t *testing.T) {
	db := TestDB(t)
	inv, err := NewInventoryRepo(db).Snapshot(t.Context())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if inv.Agents != 0 || inv.Workflows != 0 || inv.Connectors != 0 || inv.Gateways != 0 || inv.Toolsets != 0 {
		t.Errorf("empty install reported %+v, want every count zero", inv)
	}
	if inv.AgentsWithCustomToolsets != 0 || inv.WorkflowsWithCustomToolsets != 0 {
		t.Errorf("empty install reported custom-toolset holders: %+v", inv)
	}
	if len(inv.ConnectorTypes) != 0 || len(inv.GatewayTypes) != 0 {
		t.Errorf("empty install reported categories %v / %v", inv.ConnectorTypes, inv.GatewayTypes)
	}
}

func TestInventorySnapshotCountsEachResource(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()

	seedInventoryAgent(t, db, "agent-one")
	seedInventoryAgent(t, db, "agent-two")
	seedInventoryAgent(t, db, "agent-three")
	seedInventoryWorkflow(t, db, "workflow-one")
	seedInventoryWorkflow(t, db, "workflow-two")
	seedInventoryConnector(t, db, "conn-one", "sql")
	seedInventoryGateway(t, db, "gw-one", "slack")
	seedInventoryGateway(t, db, "gw-two", "webhook")
	seedInventoryGateway(t, db, "gw-three", "slack")
	seedInventoryToolPackage(t, db, "pkg-one")
	seedInventoryToolPackage(t, db, "pkg-two")

	inv, err := NewInventoryRepo(db).Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if inv.Agents != 3 {
		t.Errorf("agents = %d, want 3", inv.Agents)
	}
	if inv.Workflows != 2 {
		t.Errorf("workflows = %d, want 2", inv.Workflows)
	}
	if inv.Connectors != 1 {
		t.Errorf("connectors = %d, want 1", inv.Connectors)
	}
	if inv.Gateways != 3 {
		t.Errorf("gateways = %d, want 3", inv.Gateways)
	}
	if inv.Toolsets != 2 {
		t.Errorf("toolsets = %d, want 2", inv.Toolsets)
	}
}

func TestInventoryCountsFollowDeletion(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()

	a := seedInventoryAgent(t, db, "agent-doomed")
	seedInventoryAgent(t, db, "agent-kept")
	if err := NewAgentRepo(db).Delete(ctx, a.ID); err != nil {
		t.Fatalf("delete agent: %v", err)
	}

	inv, err := NewInventoryRepo(db).Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if inv.Agents != 1 {
		t.Errorf("agents = %d, want 1 after a delete", inv.Agents)
	}
}

func TestInventoryExcludesProxyAndEphemeralAgents(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewAgentRepo(db)

	seedInventoryAgent(t, db, "agent-real")
	if err := repo.Create(ctx, &Agent{Name: "agent-proxy", Type: "a2a_proxy", CreatedBy: "a", UpdatedBy: "a"}, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create proxy agent: %v", err)
	}
	if err := repo.Create(ctx, &Agent{Name: "agent-ephemeral", IsEphemeral: true, CreatedBy: "a", UpdatedBy: "a"}, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create ephemeral agent: %v", err)
	}

	inv, err := NewInventoryRepo(db).Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if inv.Agents != 1 {
		t.Errorf("agents = %d, want 1 (proxy and ephemeral excluded)", inv.Agents)
	}
}

func TestCustomToolsetClassification(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()

	seedInventoryToolPackage(t, db, "acme_payroll")
	seedInventoryToolPackage(t, db, "acme_billing")

	seedInventoryAgent(t, db, "agent-builtin-only", "artifact_management", "web")
	seedInventoryAgent(t, db, "agent-custom-only", "acme_payroll")
	seedInventoryAgent(t, db, "agent-both", "artifact_management", "acme_payroll", "acme_billing")
	seedInventoryAgent(t, db, "agent-none")

	seedInventoryWorkflow(t, db, "workflow-builtin-only", "artifact_management")
	seedInventoryWorkflow(t, db, "workflow-custom", "acme_billing")

	inv, err := NewInventoryRepo(db).Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if inv.Agents != 4 {
		t.Fatalf("agents = %d, want 4", inv.Agents)
	}
	if inv.AgentsWithCustomToolsets != 2 {
		t.Errorf("agentsWithCustomToolsets = %d, want 2 (custom-only and both, counted once each)", inv.AgentsWithCustomToolsets)
	}
	if inv.Workflows != 2 {
		t.Fatalf("workflows = %d, want 2", inv.Workflows)
	}
	if inv.WorkflowsWithCustomToolsets != 1 {
		t.Errorf("workflowsWithCustomToolsets = %d, want 1", inv.WorkflowsWithCustomToolsets)
	}
}

func TestCustomToolsetCountIgnoresProxyAndEphemeralAgents(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()

	seedInventoryToolPackage(t, db, "acme_payroll")
	if err := NewAgentRepo(db).Create(ctx,
		&Agent{Name: "agent-ephemeral", IsEphemeral: true, CreatedBy: "a", UpdatedBy: "a"},
		[]string{"acme_payroll"}, nil, nil, nil, nil,
	); err != nil {
		t.Fatalf("create ephemeral agent: %v", err)
	}

	inv, err := NewInventoryRepo(db).Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if inv.Agents != 0 {
		t.Fatalf("agents = %d, want 0", inv.Agents)
	}
	if inv.AgentsWithCustomToolsets != 0 {
		t.Errorf("agentsWithCustomToolsets = %d, want 0", inv.AgentsWithCustomToolsets)
	}
}

func TestInventoryReportsDistinctCategories(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()

	seedInventoryConnector(t, db, "conn-a", "sql")
	seedInventoryConnector(t, db, "conn-b", "sql")
	seedInventoryConnector(t, db, "conn-c", "knowledge_base")
	seedInventoryGateway(t, db, "gw-a", "slack")
	seedInventoryGateway(t, db, "gw-b", "slack")
	seedInventoryGateway(t, db, "gw-c", "mcp")

	inv, err := NewInventoryRepo(db).Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	slices.Sort(inv.ConnectorTypes)
	slices.Sort(inv.GatewayTypes)
	if !slices.Equal(inv.ConnectorTypes, []string{"knowledge_base", "sql"}) {
		t.Errorf("connectorTypes = %v, want [knowledge_base sql]", inv.ConnectorTypes)
	}
	if !slices.Equal(inv.GatewayTypes, []string{"mcp", "slack"}) {
		t.Errorf("gatewayTypes = %v, want [mcp slack]", inv.GatewayTypes)
	}
}
