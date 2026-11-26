// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"slices"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var testActor = identityWith(
	rbac.ScopeConnectorRead, rbac.ScopeConnectorCreate, rbac.ScopeConnectorUpdate,
	rbac.ScopeSkillRead, rbac.ScopeSkillCreate, rbac.ScopeSkillUpdate,
	rbac.ScopeAgentBuilderRead, rbac.ScopeAgentBuilderCreate, rbac.ScopeAgentBuilderUpdate,
	rbac.ScopeWorkflowBuilderRead, rbac.ScopeWorkflowBuilderCreate, rbac.ScopeWorkflowBuilderUpdate,
	rbac.ScopeEntrypointRead, rbac.ScopeEntrypointCreate, rbac.ScopeEntrypointUpdate,
)

func opFor(t *testing.T, resp *ApplyBuildResponse, kind, name string) BuildOperationResult {
	t.Helper()
	for _, op := range resp.Operations {
		if op.Kind == kind && op.Name == name {
			return op
		}
	}
	t.Fatalf("no operation for %s %q in %+v", kind, name, resp.Operations)
	return BuildOperationResult{}
}

func TestApply_CreatesComponents(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{connectorComponent("test-db"), agentComponent("test_agent")},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if resp.Summary.Failed != 0 {
		t.Fatalf("summary reports %d failure(s): %+v", resp.Summary.Failed, resp.Operations)
	}
	if resp.Summary.Created != 2 {
		t.Errorf("Created = %d, want 2", resp.Summary.Created)
	}

	agents, err := svc.deps.listAgents(ctx)
	if err != nil {
		t.Fatalf("listAgents: %v", err)
	}
	if len(agents) != 1 || agents[0].Name != "test_agent" {
		t.Fatalf("agents = %+v, want the created agent", agents)
	}
	if len(agents[0].Toolsets) == 0 {
		t.Error("the created agent has no toolsets")
	}
	if op := opFor(t, resp, kindAgent, "test_agent"); op.ResourceID == "" {
		t.Error("a successful create must report the new id")
	}
}

func TestApply_UpdatesExistingComponent(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	first := connectorComponent("test-db")
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{first}}, testActor); err != nil {
		t.Fatalf("first Apply: %v", err)
	}

	edited := connectorComponent("test-db")
	edited.Content = `{"name":"test-db","description":"A connector used in tests.","type":"sql","subtype":"postgres","values":{"hostname":"edited-host"}}`

	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{edited}}, testActor)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	op := opFor(t, resp, kindConnector, "test-db")
	if op.Action != actionUpdate || op.Status != statusSucceeded {
		t.Fatalf("op = %+v, want a succeeded update", op)
	}

	list, err := svc.deps.listConnectors(ctx)
	if err != nil {
		t.Fatalf("listConnectors: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want exactly one connector, got %d — the update created a duplicate", len(list))
	}
	values, _ := list[0].Values.(map[string]any)
	if values["hostname"] != "edited-host" {
		t.Errorf("hostname = %v, want the edit to have persisted", values["hostname"])
	}
}

func TestApply_DryRunOfAnUpdateLeavesTheStoredResourceUnchanged(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		original BuildComponentInput
		edit     func(string) string
	}{
		{"connector", connectorComponent("test-db"), func(c string) string { return strings.Replace(c, `"hostname":"db"`, `"hostname":"edited-host"`, 1) }},
		{"agent", agentComponent("test_agent"), func(c string) string {
			return strings.Replace(c, "You are a test agent that answers questions.", "You are an edited test agent that answers questions.", 1)
		}},
		{"workflow", workflowComponent("dry_flow", "DryFlow", "test_agent"), func(c string) string {
			return strings.Replace(c, "A workflow exercised by the apply tests.", "An edited workflow exercised by the apply tests.", 1)
		}},
		{"gateway", gatewayComponent("dry_gateway", "test_agent"), func(c string) string {
			return strings.Replace(c, "Routes inbound events to an agent, used in tests.", "Edited routing of inbound events, used in tests.", 1)
		}},
		{"a2a_agent", BuildComponentInput{Kind: kindA2AAgent, Name: "dry_remote", Content: `{"name":"dry_remote","agentUrl":"https://example.com/a2a","agentCardLocation":"well_known"}`}, func(c string) string {
			return strings.Replace(c, "https://example.com/a2a", "https://example.com/a2a-edited", 1)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t)
			ctx := t.Context()

			setup := []BuildComponentInput{tc.original}
			if tc.original.Kind != kindAgent {
				setup = append([]BuildComponentInput{agentComponent("test_agent")}, setup...)
			}
			first, err := svc.Apply(ctx, ApplyBuildRequest{Components: setup}, testActor)
			if err != nil {
				t.Fatalf("first Apply: %v", err)
			}
			created := opFor(t, first, tc.original.Kind, tc.original.Name)
			if created.Status != statusSucceeded || created.ResourceID == "" {
				t.Fatalf("setup op = %+v, want a succeeded create with a resource id", created)
			}

			edited := tc.original
			edited.Content = tc.edit(tc.original.Content)
			if edited.Content == tc.original.Content {
				t.Fatal("the edit changed nothing; the test would prove nothing")
			}

			preview, err := svc.Apply(ctx, ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{edited}}, testActor)
			if err != nil {
				t.Fatalf("dry run of the edit: %v", err)
			}
			op := opFor(t, preview, tc.original.Kind, tc.original.Name)
			if op.Action != actionUpdate || op.Status != statusPlanned {
				t.Fatalf("op = %+v, want a planned update", op)
			}
			if op.ResourceID != created.ResourceID {
				t.Errorf("planned update names resource %q, want the existing %q so a client can tell whose it is", op.ResourceID, created.ResourceID)
			}
			if len(op.ChangedFields) == 0 {
				t.Error("a planned update should say which fields would change")
			}

			again, err := svc.Apply(ctx, ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{tc.original}}, testActor)
			if err != nil {
				t.Fatalf("dry run of the original: %v", err)
			}
			if got := opFor(t, again, tc.original.Kind, tc.original.Name); got.Action != actionUnchanged {
				t.Errorf("after the preview the original plans %q, want %q: the dry run rewrote the stored resource", got.Action, actionUnchanged)
			}
		})
	}
}

func TestApply_IsIdempotent(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	comps := []BuildComponentInput{connectorComponent("test-db"), agentComponent("test_agent")}

	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: comps}, testActor); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: comps}, testActor)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if resp.Summary.Created != 0 || resp.Summary.Updated != 0 {
		t.Errorf("second apply churned: %+v (ops %+v)", resp.Summary, resp.Operations)
	}
	if resp.Summary.Unchanged != 2 {
		t.Errorf("Unchanged = %d, want 2", resp.Summary.Unchanged)
	}
}

func TestApply_BindsPreExistingConnector(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{connectorComponent("test-db")},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	agent := agentComponent("bound_agent")
	agent.Connectors = []string{"test-db"}
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{agent}}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if op := opFor(t, resp, kindAgent, "bound_agent"); op.Status != statusSucceeded {
		t.Fatalf("op = %+v, want success", op)
	}

	agents, err := svc.deps.listAgents(ctx)
	if err != nil {
		t.Fatalf("listAgents: %v", err)
	}
	idx := slices.IndexFunc(agents, func(a entplatform.AgentResponse) bool { return a.Name == "bound_agent" })
	if idx < 0 {
		t.Fatal("bound_agent was not created")
	}
	if len(agents[idx].Connectors) == 0 {
		t.Error("the pre-existing connector did not bind to the agent")
	}
}

func TestApply_BindsConnectorCreatedInSameBuild(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	agent := agentComponent("fresh_agent")
	agent.Connectors = []string{"test-db"}

	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{agent, connectorComponent("test-db")},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if resp.Summary.Failed != 0 {
		t.Fatalf("failures: %+v", resp.Operations)
	}

	agents, err := svc.deps.listAgents(ctx)
	if err != nil {
		t.Fatalf("listAgents: %v", err)
	}
	idx := slices.IndexFunc(agents, func(a entplatform.AgentResponse) bool { return a.Name == "fresh_agent" })
	if idx < 0 {
		t.Fatal("fresh_agent was not created")
	}
	bound := agents[idx].Connectors
	if len(bound) != 1 {
		t.Fatalf("Connectors = %v, want the same-build connector bound", bound)
	}
	if bound[0] == "test-db" {
		t.Error("the connector reference is still the placeholder name, not a platform id")
	}
}

func TestApply_StopsAtFirstFailure(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{agentComponent("a_written"), agentWithUnknownToolset("b_broken"), agentComponent("c_later")},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply returned a transport error; per-component failures belong in the response: %v", err)
	}
	if resp.Summary.Failed != 1 {
		t.Fatalf("Failed = %d, want 1: %+v", resp.Summary.Failed, resp.Operations)
	}

	if written := opFor(t, resp, kindAgent, "a_written"); written.Status != statusSucceeded {
		t.Errorf("a_written status = %q, want succeeded: the pre-pass passed it, so it is written ahead of the failure", written.Status)
	}
	failed := opFor(t, resp, kindAgent, "b_broken")
	if failed.Status != statusFailed {
		t.Errorf("broken agent status = %q", failed.Status)
	}
	if failed.Error == nil || failed.Error.ErrorID == "" {
		t.Error("a failure must carry an errorId so support can join it to the logs")
	}

	later := opFor(t, resp, kindAgent, "c_later")
	if later.Status != statusSkipped {
		t.Errorf("c_later status = %q, want skipped", later.Status)
	}
	agents, err := svc.deps.listAgents(t.Context())
	if err != nil {
		t.Fatalf("listAgents: %v", err)
	}
	if len(agents) != 1 || agents[0].Name != "a_written" {
		t.Errorf("agents = %+v, want only a_written: the failed and skipped components must not be written", agents)
	}
}

func TestApply_DryRunWritesNothing(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		DryRun:     true,
		Components: []BuildComponentInput{connectorComponent("test-db"), agentComponent("test_agent")},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !resp.DryRun {
		t.Error("response should report itself as a dry run")
	}
	if len(resp.Operations) != 2 {
		t.Errorf("want both operations previewed, got %+v", resp.Operations)
	}
	for _, op := range resp.Operations {
		if op.Status != statusPlanned {
			t.Errorf("%s %q status = %q, want planned", op.Kind, op.Name, op.Status)
		}
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
		t.Errorf("dry run wrote to the platform: %d connector(s), %d agent(s)", len(connectors), len(agents))
	}
}

func TestApply_DryRunReportsEveryOperationAsPlanned(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	seeded := sqlConnector("already_there", "postgres", fullSQLValues())
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{seeded}}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		DryRun:     true,
		Components: []BuildComponentInput{seeded, sqlConnector("brand_new", "postgres", fullSQLValues())},
	}, testActor)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !resp.DryRun {
		t.Fatal("response is not marked as a dry run")
	}
	if len(resp.Operations) != 2 {
		t.Fatalf("operations = %d, want both components reported", len(resp.Operations))
	}

	sawUnchanged := false
	for _, op := range resp.Operations {
		if op.Action == actionUnchanged {
			sawUnchanged = true
		}
		if op.Status != statusPlanned {
			t.Errorf("op %s/%s reports status %q, want %q.\n"+
				"Nothing was written, so nothing succeeded. The client records a component as saved on succeeded plus a resourceId, and an unchanged row carries the id it already had — so this status makes a preview look like a completed save.",
				op.Kind, op.Name, op.Status, statusPlanned)
		}
	}
	if !sawUnchanged {
		t.Fatal("no operation planned as unchanged, so the arm this test is about was never reached")
	}
}
