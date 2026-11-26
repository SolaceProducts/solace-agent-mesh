// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

func TestMCPConnectorSSLConfig_ApplyThenPullRoundTrip_KeepsItsPathsAsWrittenNotAsSecrets(t *testing.T) {
	authored := map[string]any{
		"name":        "internal-mcp",
		"description": "An MCP server behind mutual TLS.",
		"type":        "mcp",
		"subtype":     "remote",
		"values": map[string]any{
			"server_url":      "https://mcp.internal.example.com/mcp",
			"connection_type": "streamable-http",
			"ssl_config": map[string]any{
				"ca_bundle":   "/etc/sam/mcp-tls/ca.pem",
				"client_cert": "/etc/sam/mcp-tls/client.crt",
				"client_key":  "/etc/sam/mcp-tls/client.key",
				"server_name": "mcp.internal.example.com",
			},
		},
	}

	create, err := translate.ForwardConnectorV2(authored, translate.ConnectorV2Hooks{})
	if err != nil {
		t.Fatalf("ForwardConnectorV2: %v", err)
	}
	if diff := cmp.Diff(authored["values"], any(create.Values)); diff != "" {
		t.Fatalf("the request carries different values than the YAML (-yaml +request):\n%s", diff)
	}

	desc := create.Description
	stored := wire.ConnectorResponse{
		ID: "conn-1", Type: "connector", Name: create.Name, Description: &desc,
		ConnectorType: create.ConnectorType, ConnectorSubtype: create.ConnectorSubtype, Values: create.Values,
	}
	pulled, err := translate.ReverseConnectorV2(stored, translate.ConnectorV2Hooks{})
	if err != nil {
		t.Fatalf("ReverseConnectorV2: %v", err)
	}
	pulled, warnings := substituteSecrets("connector", create.Name, pulled)

	if diff := cmp.Diff(authored, pulled); diff != "" {
		t.Errorf("pulled connector differs from the applied one (-applied +pulled):\n%s", diff)
	}
	if len(warnings) != 0 {
		t.Errorf("pull treated something in the connector as a secret: %+v", warnings)
	}
}
