// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func newEventMeshGatewayService(t *testing.T) *GatewayService {
	t.Helper()
	db := testOpenDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewGatewayService(store.NewGatewayRepo(db), store.NewGatewayDeploymentRepo(db), nil, nil, nil, logger)
}

func TestCreateGateway_EventMesh_AWEShapeMigratedAndPersisted(t *testing.T) {
	svc := newEventMeshGatewayService(t)

	req := CreateGatewayRequest{
		Name:        "AweCreateGateway",
		Description: "AWE-shape event-mesh gateway created via the platform API.",
		Type:        "event_mesh",
		Values: map[string]any{
			"event_mesh_broker_config": map[string]any{
				"broker_url":      "tcps://broker.example.com:55443",
				"broker_vpn":      "default",
				"broker_username": "admin",
				"broker_password": "secret",
			},
			"event_handlers": []any{
				map[string]any{
					"name":              "orders",
					"subscriptions":     []any{map[string]any{"topic": "orders/created/>", "qos": 1}},
					"input_expression":  "input.payload:",
					"target_agent_name": "OrderValidator",
					"on_success":        "ok",
				},
			},
			"output_handlers": []any{
				map[string]any{"name": "ok", "topic_expression": "static:orders/validated"},
			},
		},
	}

	resp, err := svc.Create(context.Background(), req, "user1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	values, ok := resp.Values.(map[string]any)
	if !ok {
		t.Fatalf("resp.Values not a map: %T", resp.Values)
	}
	if values["broker_url"] != "tcps://broker.example.com:55443" {
		t.Fatalf("broker_url = %v, want the hoisted URL (event_mesh_broker_config was migrated to flat broker_* keys)", values["broker_url"])
	}
	if values["broker_password"] != redactedSentinel {
		t.Fatalf("broker_password = %v, want %q (hoisted from event_mesh_broker_config and redacted on read)", values["broker_password"], redactedSentinel)
	}
	rules, ok := values["event_rules"].([]any)
	if !ok || len(rules) != 1 {
		t.Fatalf("event_rules = %v, want one migrated rule", values["event_rules"])
	}
	rule := rules[0].(map[string]any)
	if rule["targetAgent"] != "OrderValidator" {
		t.Fatalf("targetAgent = %v", rule["targetAgent"])
	}
	if rule["promptTemplate"] != "{payload}" {
		t.Fatalf("promptTemplate = %v, want {payload}", rule["promptTemplate"])
	}

	got, err := svc.GetByID(context.Background(), resp.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	gotValues := got.Values.(map[string]any)
	if _, stillAWE := gotValues["event_handlers"]; stillAWE {
		t.Fatal("persisted values still carry event_handlers — migration did not take effect")
	}
	if _, hasRules := gotValues["event_rules"]; !hasRules {
		t.Fatal("persisted values missing event_rules")
	}
}

func TestCreateGateway_EventMesh_DynamicTargetRejected(t *testing.T) {
	svc := newEventMeshGatewayService(t)

	req := CreateGatewayRequest{
		Name:        "DynamicTargetGateway",
		Description: "Dynamic-target event-mesh gateway that must be rejected.",
		Type:        "event_mesh",
		Values: map[string]any{
			"event_handlers": []any{
				map[string]any{
					"name":                         "dyn",
					"subscriptions":                []any{map[string]any{"topic": "x/>", "qos": 1}},
					"target_agent_name_expression": "input.payload:agent",
				},
			},
		},
	}

	_, err := svc.Create(context.Background(), req, "user1")
	if err == nil {
		t.Fatal("expected ValidationError for dynamic target, got nil")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %T, want *ValidationError", err)
	}
}
