// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

func eventMeshOperations() []any {
	return []any{
		map[string]any{
			"tool_name":         "get_order_status",
			"tool_description":  "Get the status of a customer order by ID.",
			"topic":             "acme/orders/v1/{order_id}/status",
			"wait_for_response": true,
			"qos":               float64(1),
			"payload_format":    "json",
			"input_schema": map[string]any{
				"type":     "object",
				"required": []any{"order_id"},
				"properties": map[string]any{
					"order_id": map[string]any{"type": "string", "description": "The customer order identifier"},
				},
			},
		},
		map[string]any{
			"tool_name":         "publish_inventory_adjustment",
			"tool_description":  "Publish an inventory adjustment for a store SKU.",
			"topic":             "acme/inventory/v1/{store_id}/adjustment",
			"wait_for_response": false,
			"qos":               float64(1),
			"payload_format":    "json",
			"input_schema": map[string]any{
				"type":     "object",
				"required": []any{"store_id"},
				"properties": map[string]any{
					"store_id": map[string]any{"type": "string", "description": "Store identifier"},
				},
			},
		},
	}
}

func eventMeshConnectorSpec(ops []any) map[string]any {
	return map[string]any{
		"type":    "event_mesh",
		"subtype": "solace",
		"values": map[string]any{
			"broker_url":      "tcps://platform-broker.example.com:55443",
			"broker_vpn":      "platform-vpn",
			"broker_username": "platform-user",
			"broker_password": "real-secret",
			"operations":      ops,
		},
	}
}

func TestPlanConnectors_EventMeshMultiOp_NoOpWhenSecretRedacted(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedConnector("orders-event-mesh", eventMeshConnectorSpec(eventMeshOperations()))}

	actual := []wire.ConnectorResponse{{
		ID:               "id-em-1",
		Name:             "orders-event-mesh",
		Description:      sp("fixture"),
		ConnectorType:    "event_mesh",
		ConnectorSubtype: "solace",
		Values: map[string]any{
			"broker_url":      "tcps://platform-broker.example.com:55443",
			"broker_vpn":      "platform-vpn",
			"broker_username": "platform-user",
			"broker_password": "<REDACTED>",
			"operations":      eventMeshOperations(),
		},
	}}

	plan, err := planConnectors(t.Context(), nil, desired, actual)
	if err != nil {
		t.Fatalf("planConnectors: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("Unchanged = %+v", plan.Unchanged)
	}
	if len(plan.Creates)+len(plan.Updates)+len(plan.Deletes) != 0 {
		t.Errorf("expected no diff, got creates=%d updates=%d deletes=%d",
			len(plan.Creates), len(plan.Updates), len(plan.Deletes))
	}
}

func TestPlanConnectors_EventMeshMultiOp_UpdateOnOperationChange(t *testing.T) {
	changed := eventMeshOperations()
	changed[0].(map[string]any)["topic"] = "acme/orders/v2/{order_id}/status"

	desired := []resolve.ResolvedResource{resolvedConnector("orders-event-mesh", eventMeshConnectorSpec(changed))}

	actual := []wire.ConnectorResponse{{
		ID:               "id-em-1",
		Name:             "orders-event-mesh",
		Description:      sp("fixture"),
		ConnectorType:    "event_mesh",
		ConnectorSubtype: "solace",
		Values: map[string]any{
			"broker_url":      "tcps://platform-broker.example.com:55443",
			"broker_vpn":      "platform-vpn",
			"broker_username": "platform-user",
			"broker_password": "<REDACTED>",
			"operations":      eventMeshOperations(),
		},
	}}

	plan, err := planConnectors(t.Context(), nil, desired, actual)
	if err != nil {
		t.Fatalf("planConnectors: %v", err)
	}
	if len(plan.Updates) != 1 || plan.Updates[0].ID != "id-em-1" {
		t.Errorf("Updates = %+v", plan.Updates)
	}
}
