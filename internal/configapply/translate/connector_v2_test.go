// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

const mcpConnectorYAML = `
name: atlassian
description: "Jira and Confluence tools for the support agents."
type: mcp
subtype: remote
values:
  server_url: "https://mcp.example.com/mcp"
  connection_type: streamable-http
`

func TestConnectorV2_YAMLKeepsTypeAndSubtype(t *testing.T) {
	in := decodeYAMLMap(t, mcpConnectorYAML)

	create, err := ForwardConnectorV2(in, ConnectorV2Hooks{})
	if err != nil {
		t.Fatalf("ForwardConnectorV2: %v", err)
	}
	if create.ConnectorType != "mcp" || create.ConnectorSubtype != "remote" {
		t.Fatalf("connectorType/connectorSubtype = %q/%q, want mcp/remote", create.ConnectorType, create.ConnectorSubtype)
	}
	body, err := json.Marshal(create)
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	if err = json.Unmarshal(body, &sent); err != nil {
		t.Fatal(err)
	}
	if sent["connectorType"] != "mcp" || sent["connectorSubtype"] != "remote" {
		t.Errorf("request body %s, want connectorType mcp and connectorSubtype remote", body)
	}
	for _, yamlKey := range []string{"type", "subtype"} {
		if _, onWire := sent[yamlKey]; onWire {
			t.Errorf("request body %s carries the YAML key %q", body, yamlKey)
		}
	}

	var stored wire.ConnectorResponse
	if err = json.Unmarshal([]byte(`{
		"id": "conn-1", "type": "connector", "connectorType": "mcp", "connectorSubtype": "remote",
		"name": "atlassian", "description": "Jira and Confluence tools for the support agents.",
		"values": {"server_url": "https://mcp.example.com/mcp", "connection_type": "streamable-http"},
		"agentCount": 0, "hasResource": false, "contentHash": null, "warnings": [], "agentRegistrations": [],
		"createdBy": null, "updatedBy": null, "createdTime": "2026-10-30T12:00:00.000Z", "updatedTime": "2026-10-30T12:00:00.000Z"
	}`), &stored); err != nil {
		t.Fatal(err)
	}
	pulled, err := ReverseConnectorV2(stored, ConnectorV2Hooks{})
	if err != nil {
		t.Fatalf("ReverseConnectorV2: %v", err)
	}
	if diff := cmp.Diff(in, pulled); diff != "" {
		t.Errorf("pulled connector differs from the YAML (-yaml +pulled):\n%s", diff)
	}
}
