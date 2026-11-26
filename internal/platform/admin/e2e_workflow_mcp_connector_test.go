// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/crmmcp"
)

const acmeCRMConnector = "acme-crm"

func crmConnectorValues(serverURL string) string {
	return crmConnectorValuesWith(serverURL, nil)
}

func crmConnectorValuesWith(serverURL string, extra map[string]any) string {
	manifest := make([]any, 0, 3)
	for _, name := range []string{crmmcp.ToolFindCustomer, crmmcp.ToolListOrders, crmmcp.ToolGetOrder} {
		manifest = append(manifest, map[string]any{"name": name, "description": name, "inputSchema": map[string]any{"type": "object"}})
	}
	values := map[string]any{
		"server_url": serverURL, "connection_type": "streamable-http", "auth_type": "none", "manifest": manifest,
	}
	for k, v := range extra {
		values[k] = v
	}
	b, _ := json.Marshal(values)
	return string(b)
}

func customerOrdersWorkflow(findTool, ordersTool string) map[string]any {
	return map[string]any{
		"output_mapping": map[string]any{
			"customer": "{{find.output.mcp_structured_content}}",
			"orders":   "{{orders.output.mcp_structured_content}}",
		},
		"nodes": []any{
			map[string]any{
				"id": "find", "type": "tool", "connector": acmeCRMConnector, "tool": findTool,
				"input": map[string]any{"company": "Globex"},
			},
			map[string]any{
				"id": "orders", "type": "tool", "connector": acmeCRMConnector, "tool": ordersTool,
				"depends_on": []any{"find"},
				"input":      map[string]any{"customerId": "{{find.output.mcp_structured_content.customerId}}"},
			},
		},
	}
}

func runWorkflow(t *testing.T, h *e2eHarness, wfID string) string {
	t.Helper()
	name := workflowInstanceName(wfID)
	h.waitForInstance(name, 5*time.Second)
	events := h.sendMessage(name, "go", 30*time.Second)
	h.assertHasResponseEvent(events)
	var sb strings.Builder
	for _, e := range events {
		if e.Type == "response" {
			sb.Write(e.Payload)
		}
	}
	return sb.String()
}

func TestE2E_WorkflowToolNodesBindToMCPConnector(t *testing.T) {
	t.Parallel()
	crm := crmmcp.Start(t, crmmcp.Config{Label: "crm-one"})
	h := setupE2E(t, nil)
	createHydrationConnector(t, h.connectorRepo, acmeCRMConnector, "mcp", "remote", crmConnectorValues(crm.URL))

	wfID := h.createWorkflow("e2e_mcp_connector", customerOrdersWorkflow(crmmcp.ToolFindCustomer, crmmcp.ToolListOrders))
	h.deployWorkflow(wfID)

	response := runWorkflow(t, h, wfID)
	for _, want := range []string{"C-GLOBEX", "gold", "C-GLOBEX-O1", "crm-one"} {
		if !strings.Contains(response, want) {
			t.Errorf("response is missing %q, so a tool of the connector did not answer:\n%s", want, response)
		}
	}
	if got := crm.ToolCalls(); len(got) != 2 || got[0] != crmmcp.ToolFindCustomer || got[1] != crmmcp.ToolListOrders {
		t.Errorf("server saw tool calls %v, want [find_customer list_orders]", got)
	}
}

func TestE2E_PrefixedMCPConnectorToolsRunUnderTheNameThePlatformComputed(t *testing.T) {
	t.Parallel()
	crm := crmmcp.Start(t, crmmcp.Config{Label: "crm-one"})
	h := setupE2E(t, nil)
	createHydrationConnector(t, h.connectorRepo, acmeCRMConnector, "mcp", "remote",
		crmConnectorValuesWith(crm.URL, map[string]any{"tool_name_prefix": "crm"}))

	wfID := h.createWorkflow("e2e_mcp_connector_prefix", customerOrdersWorkflow(crmmcp.ToolFindCustomer, crmmcp.ToolListOrders))
	h.deployWorkflow(wfID)

	response := runWorkflow(t, h, wfID)
	if !strings.Contains(response, "C-GLOBEX-O1") {
		t.Errorf("the prefixed tools did not answer:\n%s", response)
	}
	if got := crm.ToolCalls(); len(got) != 2 || got[0] != crmmcp.ToolFindCustomer || got[1] != crmmcp.ToolListOrders {
		t.Errorf("server saw tool calls %v, want [find_customer list_orders]", got)
	}
}

func TestE2E_MCPConnectorChangeReachesRunningWorkflow(t *testing.T) {
	t.Parallel()
	first := crmmcp.Start(t, crmmcp.Config{Label: "crm-old"})
	second := crmmcp.Start(t, crmmcp.Config{Label: "crm-new"})
	h := setupE2E(t, nil)
	conn := createHydrationConnector(t, h.connectorRepo, acmeCRMConnector, "mcp", "remote", crmConnectorValues(first.URL))

	wfID := h.createWorkflow("e2e_mcp_connector_drift", customerOrdersWorkflow(crmmcp.ToolFindCustomer, crmmcp.ToolListOrders))
	h.deployWorkflow(wfID)
	if response := runWorkflow(t, h, wfID); !strings.Contains(response, "crm-old") {
		t.Fatalf("before the change the workflow should reach the old server:\n%s", response)
	}

	handler := &connectorRedeployer{workflowRepo: h.workflowRepo}
	if affected := handler.findWorkflowsByConnector(h.ctx, conn.ID); len(affected) != 1 || affected[0] != wfID {
		t.Fatalf("findWorkflowsByConnector = %v, want [%s]: the mcp binding must persist through workflow_connectors", affected, wfID)
	}
	if err := h.connectorRepo.UpdateValues(h.ctx, conn.ID, crmConnectorValues(second.URL)); err != nil {
		t.Fatalf("update connector values: %v", err)
	}
	pub := NewRedeployPublisher(h.deploySvc, h.agentRepo, testLogger()).WithWorkflowRedeploy(h.wfDeploySvc, h.workflowRepo)
	event := store.OutboxEvent{
		EventType:   "connector.updated",
		AggregateID: conn.ID,
		Payload:     fmt.Sprintf(`{"agentIds":[],"workflowIds":[%q],"userId":"editor"}`, wfID),
	}
	if err := pub.PublishOutboxEvent(h.ctx, event); err != nil {
		t.Fatalf("publish outbox event: %v", err)
	}

	response := runWorkflow(t, h, wfID)
	if !strings.Contains(response, "crm-new") || strings.Contains(response, "crm-old") {
		t.Fatalf("after the connector change the workflow should reach only the new server:\n%s", response)
	}
}

func TestE2E_WorkflowSaveRefusesMCPToolTheConnectorLacks(t *testing.T) {
	t.Parallel()
	crm := crmmcp.Start(t, crmmcp.Config{Label: "crm-one"})
	h := setupE2E(t, nil)
	createHydrationConnector(t, h.connectorRepo, acmeCRMConnector, "mcp", "remote", crmConnectorValues(crm.URL))

	_, err := h.workflowSvc.Create(h.ctx, CreateWorkflowRequest{
		Name:        "e2e_mcp_connector_missing_tool",
		Description: "E2E test workflow for testing",
		AppConfig:   customerOrdersWorkflow(crmmcp.ToolFindCustomer, "list_invoices"),
	}, "e2e-user")
	if err == nil {
		t.Fatal("save accepted a node naming a tool the connector does not serve")
	}
	for _, want := range []string{"list_invoices", "available: find_customer, list_orders, get_order"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("save error %q should mention %q", err, want)
		}
	}
}
