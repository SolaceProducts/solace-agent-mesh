// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

func TestEventMeshConnectorPullRoundTrip(t *testing.T) {
	operations := []any{
		map[string]any{
			"tool_name":         "get_order_status",
			"tool_description":  "Get the status of a customer order by ID.",
			"topic":             "acme/orders/v1/{order_id}/status",
			"wait_for_response": true,
			"request_expiry_ms": float64(60000),
			"reply_mode":        "temp_queue",
			"qos":               float64(1),
			"payload_format":    "json",
			"input_schema": map[string]any{
				"type":     "object",
				"required": []any{"order_id"},
				"properties": map[string]any{
					"order_id": map[string]any{
						"type":        "string",
						"description": "The customer order identifier",
					},
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
				"required": []any{"store_id", "sku", "delta"},
				"properties": map[string]any{
					"store_id": map[string]any{"type": "string", "description": "Store identifier (dynamic topic level)"},
					"sku":      map[string]any{"type": "string", "description": "Product SKU", "x-solace-payload-path": "item.sku"},
					"delta":    map[string]any{"type": "integer", "description": "Signed change in quantity", "x-solace-payload-path": "item.delta"},
				},
			},
		},
	}

	desc := "Publish order lookups to the event mesh."
	createdBy, updatedBy := "alice", "bob"
	resp := wire.ConnectorResponse{
		ID:               "conn-em-1",
		Type:             "connector",
		Name:             "orders-event-mesh",
		Description:      &desc,
		ConnectorType:    "event_mesh",
		ConnectorSubtype: "solace",
		Values: map[string]any{
			"broker_url":      "tcps://platform-broker.example.com:55443",
			"broker_vpn":      "platform-vpn",
			"broker_username": "platform-user",
			"broker_password": redactedSentinel,
			"operations":      operations,
		},
		CreatedBy: &createdBy,
		UpdatedBy: &updatedBy,
	}

	spec, err := translate.ReverseConnectorV2(resp, translate.ConnectorV2Hooks{})
	if err != nil {
		t.Fatalf("ReverseConnectorV2: %v", err)
	}
	spec, warnings := substituteSecrets("connector", resp.Name, spec)

	if spec["type"] != "event_mesh" || spec["subtype"] != "solace" {
		t.Fatalf("type/subtype drift: type=%v subtype=%v", spec["type"], spec["subtype"])
	}

	values, ok := spec["values"].(map[string]any)
	if !ok {
		t.Fatalf("values type = %T", spec["values"])
	}

	wantNonSecret := map[string]any{
		"broker_url":      "tcps://platform-broker.example.com:55443",
		"broker_vpn":      "platform-vpn",
		"broker_username": "platform-user",
		"operations":      operations,
	}
	for k, want := range wantNonSecret {
		if diff := cmp.Diff(want, values[k]); diff != "" {
			t.Errorf("values[%q] drift (-want +got):\n%s", k, diff)
		}
	}

	const wantPlaceholder = "${CONNECTOR_ORDERS_EVENT_MESH_BROKER_PASSWORD}"
	if got := values["broker_password"]; got != wantPlaceholder {
		t.Errorf("broker_password placeholder = %v, want %q", got, wantPlaceholder)
	}

	for k, v := range values {
		if s, ok := v.(string); ok && strings.Contains(s, "SOLACE_BROKER") {
			t.Errorf("values[%q]=%q must not reference SOLACE_BROKER_*", k, s)
		}
	}

	var sawPwd bool
	for _, w := range warnings {
		if w.FieldPath == "values.broker_password" {
			sawPwd = true
			if w.Placeholder != wantPlaceholder {
				t.Errorf("warning placeholder = %q, want %q", w.Placeholder, wantPlaceholder)
			}
		}
	}
	if !sawPwd {
		t.Errorf("expected a secret warning for values.broker_password; got %+v", warnings)
	}
}
