// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

const connectorYAMLFixture = `
name: ops-slack
description: "Slack workspace connector for the ops channel."
type: messaging
subtype: slack
values:
  slack_bot_token: xoxb-test-secret
  slack_channel: "#ops-alerts"
`

func TestForwardConnectorV2_FullSpec(t *testing.T) {
	in := decodeYAMLMap(t, connectorYAMLFixture)

	got, err := ForwardConnectorV2(in, ConnectorV2Hooks{})
	if err != nil {
		t.Fatalf("ForwardConnectorV2: %v", err)
	}

	want := wire.CreateConnectorRequest{
		Name:             "ops-slack",
		Description:      "Slack workspace connector for the ops channel.",
		ConnectorType:    "messaging",
		ConnectorSubtype: "slack",
		Values: map[string]any{
			"slack_bot_token": "xoxb-test-secret",
			"slack_channel":   "#ops-alerts",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("ForwardConnectorV2 mismatch (-want +got):\n%s", diff)
	}
}

func TestReverseConnectorV2_FullResponse(t *testing.T) {
	desc := "Slack workspace connector for the ops channel."
	hash, createdBy, updatedBy := "sha256:abc", "alice", "bob"
	in := wire.ConnectorResponse{
		ID:               "conn-1",
		Type:             "connector",
		Name:             "ops-slack",
		Description:      &desc,
		ConnectorType:    "messaging",
		ConnectorSubtype: "slack",
		Values: map[string]any{
			"slack_bot_token": "<REDACTED>",
			"slack_channel":   "#ops-alerts",
		},
		AgentCount:  3,
		HasResource: true,
		ContentHash: &hash,
		Warnings:    []string{"stale"},
		CreatedBy:   &createdBy,
		UpdatedBy:   &updatedBy,
		CreatedTime: apiv2.Instant(1_700_000_000_000),
		UpdatedTime: apiv2.Instant(1_700_001_000_000),
	}

	got, err := ReverseConnectorV2(in, ConnectorV2Hooks{})
	if err != nil {
		t.Fatalf("ReverseConnectorV2: %v", err)
	}

	want := map[string]any{
		"name":        "ops-slack",
		"description": "Slack workspace connector for the ops channel.",
		"type":        "messaging",
		"subtype":     "slack",
		"values": map[string]any{
			"slack_bot_token": "<REDACTED>",
			"slack_channel":   "#ops-alerts",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("ReverseConnectorV2 mismatch (-want +got):\n%s", diff)
	}
}
