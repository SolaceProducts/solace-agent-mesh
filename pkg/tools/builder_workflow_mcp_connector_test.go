// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"encoding/json"
	"testing"
)

const crmWorkflowYAML = `
apps:
  - name: CustomerLookup
    app_config:
      agent_type: workflow
      output_mapping:
        customer: "{{find.output.mcp_structured_content}}"
        orders: "{{orders.output.mcp_structured_content}}"
      nodes:
        - id: find
          type: tool
          connector: acme-crm
          tool: find_customer
          input: {company: "Globex"}
        - id: orders
          type: tool
          connector: acme-crm
          tool: list_orders
          depends_on: [find]
          input: {customerId: "{{find.output.mcp_structured_content.customerId}}"}
`

func mcpConnectorArtifact(t *testing.T) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"name": "acme-crm", "type": "mcp", "subtype": "remote",
		"values": map[string]any{"server_url": "https://crm.example.com/mcp", "connection_type": "streamable-http", "auth_type": "none"},
	})
	if err != nil {
		t.Fatalf("marshal connector: %v", err)
	}
	return string(raw)
}

func TestValidateBuildManifest_AcceptsMCPConnectorBindingThePlatformDeploys(t *testing.T) {
	files := []buildFile{
		{filename: "customer_lookup.yaml", typ: "workflow", name: "CustomerLookup", origin: "session", content: crmWorkflowYAML},
		{filename: "acme_crm.json", typ: "connector", name: "acme-crm", origin: "session", content: mcpConnectorArtifact(t)},
	}
	valid, errs := bindingRun(t, files, namedLookups{})
	if !valid {
		t.Fatalf("a workflow whose tool nodes bind an MCP connector was rejected by the Builder's validation: %v", errs)
	}
}
