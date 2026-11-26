// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"slices"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func connectorComponent(name string) BuildComponentInput {
	return BuildComponentInput{
		Kind:    kindConnector,
		Name:    name,
		Content: `{"name":"` + name + `","description":"A connector used in tests.","type":"sql","subtype":"postgres","values":{"hostname":"db"}}`,
	}
}

func agentComponent(name string) BuildComponentInput {
	return BuildComponentInput{
		Kind: kindAgent,
		Name: name,
		Content: `
apps:
  - name: ` + name + `
    app_config:
      agent_name: ` + name + `
      instruction: You are a test agent that answers questions.
      tools:
        - tool_type: builtin-group
          group_name: artifact_management
      agent_card:
        description: "An agent created by the plan integration test."
`,
	}
}

func findOp(t *testing.T, plans []kindPlan, componentKind, name string) BuildOperationResult {
	t.Helper()
	for _, kp := range plans {
		for _, op := range kp.operations(statusPlanned) {
			if op.Kind == componentKind && op.Name == name {
				return op
			}
		}
	}
	t.Fatalf("no planned operation for %s %q", componentKind, name)
	return BuildOperationResult{}
}

func TestPlan_EmptyPlatformPlansCreates(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	plans, _, err := svc.Plan(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{connectorComponent("test-db"), agentComponent("test_agent")},
	}, testActor)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, want := range []struct{ kind, name string }{
		{kindConnector, "test-db"},
		{kindAgent, "test_agent"},
	} {
		if op := findOp(t, plans, want.kind, want.name); op.Action != actionCreate {
			t.Errorf("%s %q: Action = %q, want create", want.kind, want.name, op.Action)
		}
	}
}

func TestPlan_OrdersByDependency(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	plans, _, err := svc.Plan(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{agentComponent("test_agent"), connectorComponent("test-db")},
	}, testActor)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var order []string
	for _, kp := range plans {
		order = append(order, kp.componentKind)
	}
	connectorAt := slices.Index(order, kindConnector)
	agentAt := slices.Index(order, kindAgent)
	if connectorAt == -1 || agentAt == -1 {
		t.Fatalf("both kinds should be planned, got %v", order)
	}
	if connectorAt > agentAt {
		t.Errorf("planned order %v puts connectors after agents", order)
	}
}

func TestPlan_ExistingResourcePlansUpdate(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.deps.Connectors.Create(ctx, entplatform.CreateConnectorRequest{
		Name:        "test-db",
		Description: "The already-saved connector.",
		Type:        "sql",
		Subtype:     "postgres",
		Values:      map[string]any{"hostname": "old-host"},
	}, "tester"); err != nil {
		t.Fatalf("seed connector: %v", err)
	}

	changed := connectorComponent("test-db")
	changed.Content = `{"name":"test-db","description":"The already-saved connector.","type":"sql","subtype":"postgres","values":{"hostname":"new-host"}}`

	plans, _, err := svc.Plan(ctx, ApplyBuildRequest{Components: []BuildComponentInput{changed}}, testActor)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	op := findOp(t, plans, kindConnector, "test-db")
	if op.Action != actionUpdate {
		t.Fatalf("Action = %q, want update", op.Action)
	}
	if op.ResourceID == "" {
		t.Error("an update must carry the platform id")
	}
	if !slices.Contains(op.ChangedFields, "values") {
		t.Errorf("ChangedFields = %v, want values named for the preview diff", op.ChangedFields)
	}
}

func TestPlan_IdenticalResourcePlansUnchanged(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	comp := connectorComponent("test-db")
	if _, err := svc.deps.Connectors.Create(ctx, entplatform.CreateConnectorRequest{
		Name:        "test-db",
		Description: "A connector used in tests.",
		Type:        "sql",
		Subtype:     "postgres",
		Values:      map[string]any{"hostname": "db"},
	}, "tester"); err != nil {
		t.Fatalf("seed connector: %v", err)
	}

	plans, _, err := svc.Plan(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if op := findOp(t, plans, kindConnector, "test-db"); op.Action != actionUnchanged {
		t.Errorf("Action = %q, want unchanged (ChangedFields=%v)", op.Action, op.ChangedFields)
	}
}

func TestPlan_NeverPlansADelete(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.deps.Connectors.Create(ctx, entplatform.CreateConnectorRequest{
		Name:        "someone-elses-connector",
		Description: "Created outside this build entirely.",
		Type:        "sql",
		Subtype:     "postgres",
		Values:      map[string]any{"hostname": "db"},
	}, "someone"); err != nil {
		t.Fatalf("seed connector: %v", err)
	}

	plans, _, err := svc.Plan(ctx, ApplyBuildRequest{Components: []BuildComponentInput{connectorComponent("test-db")}}, testActor)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, kp := range plans {
		if n := len(kp.plan.Deletes); n != 0 {
			t.Fatalf("%s plan carries %d delete(s)", kp.componentKind, n)
		}
		for _, op := range kp.operations(statusPlanned) {
			if op.Name == "someone-elses-connector" {
				t.Fatalf("an unrelated resource appeared in the plan as %q", op.Action)
			}
		}
	}

	list, err := svc.deps.listConnectors(ctx)
	if err != nil {
		t.Fatalf("listConnectors: %v", err)
	}
	if !slices.ContainsFunc(list, func(c entplatform.ConnectorResponse) bool {
		return c.Name == "someone-elses-connector"
	}) {
		t.Error("the unrelated connector is gone")
	}
}

func TestPlan_RejectsDuplicateNames(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	_, _, err := svc.Plan(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{connectorComponent("dupe"), connectorComponent("dupe")},
	}, testActor)
	if err == nil {
		t.Fatal("expected an error for two components with the same name")
	}
}

func TestPlan_WritesNothing(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, _, err := svc.Plan(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{connectorComponent("test-db"), agentComponent("test_agent")},
	}, testActor); err != nil {
		t.Fatalf("Plan: %v", err)
	}

	connectors, err := svc.deps.listConnectors(ctx)
	if err != nil {
		t.Fatalf("listConnectors: %v", err)
	}
	agents, err := svc.deps.listAgents(ctx)
	if err != nil {
		t.Fatalf("listAgents: %v", err)
	}
	if len(connectors) != 0 || len(agents) != 0 {
		t.Errorf("planning wrote to the platform: %d connector(s), %d agent(s)", len(connectors), len(agents))
	}
}
