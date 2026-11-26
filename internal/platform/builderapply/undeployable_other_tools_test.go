// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func mcpConnectorWithManifest(name string) BuildComponentInput {
	return connectorWith(name, func(b map[string]any) {
		b["type"], b["subtype"] = "mcp", "remote"
		b["values"] = map[string]any{
			"server_url":      "http://127.0.0.1:1/mcp",
			"connection_type": "streamable-http",
			"auth_type":       "none",
			"manifest":        []any{map[string]any{"name": "find_customer"}, map[string]any{"name": "list_orders"}},
		}
	})
}

func workflowWithToolNode(name, nodeProperties, topLevel string) BuildComponentInput {
	return BuildComponentInput{Kind: kindWorkflow, Name: name, Content: `
apps:
  - name: ` + name + `
    app_config:
      name: ` + name + `
      agent_type: workflow
` + topLevel + `      workflow:
        output_mapping:
          result: "{{call.output}}"
        agent_card:
          description: "A workflow that calls a non-API tool."
        nodes:
          - id: call
            type: tool
` + nodeProperties}
}

func requireAcceptedAndWritten(t *testing.T, svc *Service, workflowName string, components ...BuildComponentInput) {
	t.Helper()
	dry, applied := applyBoth(t, svc, components...)
	for _, resp := range []*ApplyBuildResponse{dry, applied} {
		op := opFor(t, resp, kindWorkflow, workflowName)
		if op.Status == statusFailed || len(op.Warnings) != 0 {
			t.Errorf("dryRun=%v: workflow = %+v, want accepted with no warning", resp.DryRun, op)
		}
	}
	if got := opFor(t, applied, kindWorkflow, workflowName).Status; got != statusSucceeded {
		t.Fatalf("apply: workflow = %q, want succeeded", got)
	}
	if applied.Summary.Failed != 0 {
		t.Fatalf("apply failed: %+v", applied.Operations)
	}
	if workflowNamed(t, svc, workflowName).Name == "" {
		t.Fatal("the workflow was not written")
	}
}

func TestApply_OtherTools_MCPConnectorNodePassesThePrePass(t *testing.T) {
	t.Parallel()

	t.Run("a connector created in the same build", func(t *testing.T) {
		t.Parallel()
		svc := newTestService(t)
		requireAcceptedAndWritten(t, svc, "mcp_flow",
			mcpConnectorWithManifest("acme-crm"),
			workflowCallingConnector("mcp_flow", "acme-crm", "find_customer", ""))
	})

	t.Run("a stored connector", func(t *testing.T) {
		t.Parallel()
		svc := newTestService(t)
		if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{mcpConnectorWithManifest("acme-crm")}}, testActor); err != nil {
			t.Fatalf("seed connector: %v", err)
		}
		requireAcceptedAndWritten(t, svc, "mcp_flow", workflowCallingConnector("mcp_flow", "acme-crm", "list_orders", ""))
	})
}

func seedReadyToolPackage(t *testing.T, svc *Service, name string, toolNames ...string) {
	t.Helper()
	repo := store.NewToolPackageRepo(svc.deps.DB)
	pkg := &store.ToolPackage{Name: name, DiscoveryStatus: "ready", Capabilities: "{}", CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(t.Context(), pkg); err != nil {
		t.Fatalf("create package %q: %v", name, err)
	}
	for _, tool := range toolNames {
		if err := repo.UpsertToolFile(t.Context(), &store.ToolPackageFile{
			PackageID: pkg.ID, ToolName: tool, TimeoutSeconds: 30, SandboxProfile: "default",
		}); err != nil {
			t.Fatalf("upsert tool %q: %v", tool, err)
		}
	}
}

func TestApply_OtherTools_ToolsetToolNodePassesThePrePass(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	seedReadyToolPackage(t, svc, "echo-tools", "greet", "shout")
	requireAcceptedAndWritten(t, svc, "toolset_flow",
		workflowWithToolNode("toolset_flow",
			"            tool_name: echo-tools__greet\n            input:\n              name: \"{{workflow.input.name}}\"\n",
			"      toolsets: [echo-tools]\n"))
}

func TestApply_OtherTools_StoredSpecHappyPathPasses(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	seedStoredShipmentConnector(t, svc, "carrier-api")
	requireAcceptedAndWritten(t, svc, "ship_flow",
		workflowCallingConnector("ship_flow", "carrier-api", "get_shipment", shipInput("tracking")))
}
