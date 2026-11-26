// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"log/slog"
	"maps"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func gatewayHashFixture() map[string]any {
	return map[string]any{
		"apps": []any{
			map[string]any{
				"name": "gw_1",
				"app_config": map[string]any{
					"namespace":       "prod",
					"gateway_id":      "orders-hook",
					"system_purpose":  "You triage inbound order events.",
					"response_format": "Reply in JSON.",
					"model":           map[string]any{"model": "gpt"},
					"authorization_service": map[string]any{
						"type": "default_rbac",
					},
					"frontend_use_authorization": true,
					"event_mesh_broker_config": map[string]any{
						"broker_url":      "tcps://broker-1:55443",
						"broker_password": "broker-secret-1",
					},
					"gateway_adapter": map[string]any{
						"type": "webhook",
						"adapter_config": map[string]any{
							"routes": []any{
								map[string]any{
									"name":              "orders",
									"match":             map[string]any{"path": "/orders", "method": "POST"},
									"input_expression":  "template:New order {{json://input.body}}",
									"auth":              map[string]any{"token": map[string]any{"location": "bearer", "value": "tok-1"}},
									"target_agent_name": "OrderAgent",
									"run_as":            "svc-orders",
									"max_body_bytes":    1048576,
								},
								map[string]any{
									"name":             "refunds",
									"match":            map[string]any{"path": "/refunds", "method": "POST"},
									"input_expression": "template:Refund {{json://input.body}}",
									"auth":             map[string]any{"hmac": map[string]any{"secret": "hmac-1", "header": "X-Sig"}},
								},
							},
							"slack_bot_token": "xoxb-1",
						},
					},
					"event_handlers": []any{
						map[string]any{
							"name":             "order_created",
							"subscriptions":    []any{map[string]any{"topic": "orders/created"}},
							"payload_format":   "json",
							"input_expression": "template:{{json://input.payload}}",
							"structured_invocation": map[string]any{
								"input_schema":  map[string]any{"type": "object", "required": []any{"id"}},
								"output_schema": map[string]any{"type": "object"},
							},
							"target_agent_name": "OrderAgent",
							"run_as":            "svc-events",
						},
						map[string]any{
							"name":                 "order_cancelled",
							"subscriptions":        []any{map[string]any{"topic": "orders/cancelled"}},
							"input_expression":     "template:Cancelled {{text://input.payload:id}}",
							"target_workflow_name": "cancel_flow",
						},
					},
				},
			},
		},
	}
}

func gatewayFixtureAppConfig(config map[string]any) map[string]any {
	return config["apps"].([]any)[0].(map[string]any)["app_config"].(map[string]any)
}

func gatewayFixtureRoutes(config map[string]any) []any {
	adapter := gatewayFixtureAppConfig(config)["gateway_adapter"].(map[string]any)
	return adapter["adapter_config"].(map[string]any)["routes"].([]any)
}

func gatewayFixtureHandlers(config map[string]any) []any {
	return gatewayFixtureAppConfig(config)["event_handlers"].([]any)
}

func mutatedGatewayFixture(mutate func(config map[string]any)) map[string]any {
	config := deepCloneJSONValue(gatewayHashFixture()).(map[string]any)
	mutate(config)
	return config
}

func mustGatewayPromptTransformHash(t *testing.T, config map[string]any) string {
	t.Helper()
	hash, err := computeGatewayPromptTransformHash(config)
	if err != nil {
		t.Fatalf("computeGatewayPromptTransformHash: %v", err)
	}
	return hash
}

func TestB04_GatewayPromptTransformHash(t *testing.T) {
	t.Parallel()
	base := mustGatewayPromptTransformHash(t, gatewayHashFixture())
	if len(base) != 64 {
		t.Fatalf("hash = %q, want a hex SHA-256", base)
	}

	t.Run("B04_canonicalization_v1_is_pinned", func(t *testing.T) {
		want := sha256Hex(`{"event_handlers":[` +
			`{"input_expression":"template:Cancelled {{text://input.payload:id}}","name":"order_cancelled"},` +
			`{"input_expression":"template:{{json://input.payload}}","name":"order_created","payload_format":"json",` +
			`"structured_invocation":{"input_schema":{"required":["id"],"type":"object"},"output_schema":{"type":"object"}}}],` +
			`"response_format":"Reply in JSON.","system_purpose":"You triage inbound order events.","v":1,"webhook_routes":[` +
			`{"input_expression":"template:New order {{json://input.body}}","name":"orders"},` +
			`{"input_expression":"template:Refund {{json://input.body}}","name":"refunds"}]}`)
		if base != want {
			t.Errorf("hash = %s, want %s; a canonicalization change must bump the version", base, want)
		}
	})

	t.Run("B04_reordered_routes_and_handlers_hash_identically", func(t *testing.T) {
		reordered := mutatedGatewayFixture(func(config map[string]any) {
			slices.Reverse(gatewayFixtureRoutes(config))
			slices.Reverse(gatewayFixtureHandlers(config))
		})
		if got := mustGatewayPromptTransformHash(t, reordered); got != base {
			t.Errorf("reordered hash = %s, want %s", got, base)
		}
	})

	t.Run("B04_typed_and_decoded_envelopes_hash_identically", func(t *testing.T) {
		encoded, err := json.Marshal(gatewayHashFixture())
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		typed := map[string]any{"apps": []map[string]any{decoded["apps"].([]any)[0].(map[string]any)}}
		if got := mustGatewayPromptTransformHash(t, typed); got != base {
			t.Errorf("typed envelope hash = %s, want %s", got, base)
		}
	})

	includedChanges := map[string]func(config map[string]any){
		"system_purpose":  func(c map[string]any) { gatewayFixtureAppConfig(c)["system_purpose"] = "You audit orders." },
		"response_format": func(c map[string]any) { gatewayFixtureAppConfig(c)["response_format"] = "Reply in YAML." },
		"route_input_expression": func(c map[string]any) {
			gatewayFixtureRoutes(c)[0].(map[string]any)["input_expression"] = "template:Order {{json://input.body}}"
		},
		"route_name": func(c map[string]any) { gatewayFixtureRoutes(c)[1].(map[string]any)["name"] = "returns" },
		"handler_input_expression": func(c map[string]any) {
			gatewayFixtureHandlers(c)[1].(map[string]any)["input_expression"] = "template:{{json://input.payload}}"
		},
		"handler_name": func(c map[string]any) { gatewayFixtureHandlers(c)[0].(map[string]any)["name"] = "order_placed" },
		"structured_invocation_input_schema": func(c map[string]any) {
			si := gatewayFixtureHandlers(c)[0].(map[string]any)["structured_invocation"].(map[string]any)
			si["input_schema"] = map[string]any{"type": "object", "required": []any{"id", "total"}}
		},
		"structured_invocation_output_schema": func(c map[string]any) {
			si := gatewayFixtureHandlers(c)[0].(map[string]any)["structured_invocation"].(map[string]any)
			si["output_schema"] = map[string]any{"type": "string"}
		},
		"route_header_aliases": func(c map[string]any) {
			gatewayFixtureRoutes(c)[0].(map[string]any)["headers"] = map[string]any{"tenant": "X-Tenant-Id"}
		},
		"route_artifact_processing": func(c map[string]any) {
			gatewayFixtureRoutes(c)[0].(map[string]any)["artifact_processing"] = map[string]any{"extract_artifacts_expression": "json://input.body:files"}
		},
		"handler_payload_format": func(c map[string]any) { gatewayFixtureHandlers(c)[0].(map[string]any)["payload_format"] = "text" },
		"handler_payload_encoding": func(c map[string]any) {
			gatewayFixtureHandlers(c)[0].(map[string]any)["payload_encoding"] = "base64"
		},
		"handler_forward_context": func(c map[string]any) {
			gatewayFixtureHandlers(c)[1].(map[string]any)["forward_context"] = map[string]any{"order_id": "json://input.payload:id"}
		},
		"handler_artifact_processing": func(c map[string]any) {
			gatewayFixtureHandlers(c)[1].(map[string]any)["artifact_processing"] = map[string]any{"extract_artifacts_expression": "json://input.payload:files"}
		},
		"added_handler": func(c map[string]any) {
			app := gatewayFixtureAppConfig(c)
			app["event_handlers"] = append(gatewayFixtureHandlers(c), map[string]any{"name": "order_shipped", "input_expression": "template:shipped"})
		},
	}
	for name, mutate := range includedChanges {
		t.Run("B04_included_field_changes_the_hash/"+name, func(t *testing.T) {
			if got := mustGatewayPromptTransformHash(t, mutatedGatewayFixture(mutate)); got == base {
				t.Errorf("changing %s left the hash unchanged", name)
			}
		})
	}

	excludedChanges := map[string]func(config map[string]any){
		"route_auth_token": func(c map[string]any) {
			gatewayFixtureRoutes(c)[0].(map[string]any)["auth"] = map[string]any{"token": map[string]any{"location": "header", "value": "tok-2"}}
		},
		"route_hmac_secret": func(c map[string]any) {
			gatewayFixtureRoutes(c)[1].(map[string]any)["auth"] = map[string]any{"hmac": map[string]any{"secret": "hmac-2", "header": "X-Sig"}}
		},
		"route_match": func(c map[string]any) {
			gatewayFixtureRoutes(c)[0].(map[string]any)["match"] = map[string]any{"path": "/v2/orders", "method": "PUT"}
		},
		"route_target_and_identity": func(c map[string]any) {
			route := gatewayFixtureRoutes(c)[0].(map[string]any)
			route["target_agent_name"] = "OtherAgent"
			route["run_as"] = "svc-other"
			route["max_body_bytes"] = 10
		},
		"handler_routing": func(c map[string]any) {
			handler := gatewayFixtureHandlers(c)[0].(map[string]any)
			handler["subscriptions"] = []any{map[string]any{"topic": "orders/v2/created"}}
			handler["target_agent_name"] = "OtherAgent"
			handler["run_as"] = "svc-other"
		},
		"adapter_credentials": func(c map[string]any) {
			adapter := gatewayFixtureAppConfig(c)["gateway_adapter"].(map[string]any)
			adapter["adapter_config"].(map[string]any)["slack_bot_token"] = "xoxb-2"
		},
		"broker_connection": func(c map[string]any) {
			gatewayFixtureAppConfig(c)["event_mesh_broker_config"] = map[string]any{"broker_url": "tcps://broker-2:55443", "broker_password": "broker-secret-2"}
		},
		"deployment_and_auth_settings": func(c map[string]any) {
			app := gatewayFixtureAppConfig(c)
			app["gateway_id"] = "orders-hook-2"
			app["namespace"] = "staging"
			app["frontend_use_authorization"] = false
			app["authorization_service"] = map[string]any{"type": "none"}
			app["model"] = map[string]any{"model": "other"}
		},
	}
	for name, mutate := range excludedChanges {
		t.Run("B04_excluded_field_does_not_change_the_hash/"+name, func(t *testing.T) {
			if got := mustGatewayPromptTransformHash(t, mutatedGatewayFixture(mutate)); got != base {
				t.Errorf("changing %s moved the hash", name)
			}
		})
	}

	t.Run("B04_absent_prompt_surface_is_still_hashed", func(t *testing.T) {
		empty := map[string]any{"apps": []any{map[string]any{"app_config": map[string]any{"namespace": "prod"}}}}
		if got := mustGatewayPromptTransformHash(t, empty); len(got) != 64 || got == base {
			t.Errorf("empty-surface hash = %q", got)
		}
	})
}

func TestB04_GatewayPromptTransformHash_FromBuiltConfigs(t *testing.T) {
	t.Parallel()
	yamlSvc := NewYAMLService("test-ns", slog.Default())
	build := func(t *testing.T, gwType string, values map[string]any) string {
		t.Helper()
		encoded, err := json.Marshal(values)
		if err != nil {
			t.Fatalf("marshal values: %v", err)
		}
		raw := string(encoded)
		config, err := yamlSvc.BuildGatewayConfig(&store.Gateway{ID: "11111111-2222-3333-4444-555555555555", Name: "gw", Type: gwType, Values: &raw})
		if err != nil {
			t.Fatalf("build %s config: %v", gwType, err)
		}
		return mustGatewayPromptTransformHash(t, config)
	}

	webhook := map[string]any{
		"routePath": "/orders", "method": "POST", "inputExpression": "template:{{json://input.body}}",
		"authMode": "token", "authToken": "tok-1", "systemPurpose": "Triage orders.",
	}
	eventMesh := map[string]any{
		"broker_url": "tcps://broker:55443", "broker_password": "pw-1", "systemPurpose": "Triage events.",
		"event_rules": []any{map[string]any{
			"name": "created", "subscriptions": []any{map[string]any{"topic": "orders/created"}},
			"promptTemplate": "New order {payload}", "targetAgent": "OrderAgent",
		}},
	}

	cases := []struct {
		name     string
		gwType   string
		base     map[string]any
		included map[string]any
		excluded map[string]any
	}{
		{
			name: "webhook", gwType: "webhook", base: webhook,
			included: map[string]any{"inputExpression": "template:Order {{json://input.body}}"},
			excluded: map[string]any{"authToken": "tok-2", "routePath": "/v2/orders", "targetAgentName": "Other"},
		},
		{
			name: "event_mesh", gwType: "event_mesh", base: eventMesh,
			included: map[string]any{"event_rules": []any{map[string]any{
				"name": "created", "subscriptions": []any{map[string]any{"topic": "orders/created"}},
				"promptTemplate": "Order placed {payload}", "targetAgent": "OrderAgent",
			}}},
			excluded: map[string]any{"broker_password": "pw-2", "broker_url": "tcps://broker-2:55443"},
		},
		{
			name: "slack", gwType: "slack", base: map[string]any{"bot_token": "xoxb-1", "responseFormat": "Short."},
			included: map[string]any{"responseFormat": "Long."},
			excluded: map[string]any{"bot_token": "xoxb-2", "app_token": "xapp-2"},
		},
	}
	for _, tc := range cases {
		t.Run("B04_"+tc.name, func(t *testing.T) {
			baseHash := build(t, tc.gwType, tc.base)
			included := maps.Clone(tc.base)
			maps.Copy(included, tc.included)
			if build(t, tc.gwType, included) == baseHash {
				t.Errorf("included change %v left the hash unchanged", tc.included)
			}
			excluded := maps.Clone(tc.base)
			maps.Copy(excluded, tc.excluded)
			if got := build(t, tc.gwType, excluded); got != baseHash {
				t.Errorf("excluded change %v moved the hash", tc.excluded)
			}
		})
	}
}

func createGatewayRow(t *testing.T, repo *store.GatewayRepo, gwType string, values map[string]any) *store.Gateway {
	t.Helper()
	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal values: %v", err)
	}
	raw := string(encoded)
	gw := &store.Gateway{
		Name: "prompt-transform-" + gwType, Description: new("prompt transform hash fixture"), Type: gwType,
		Values: &raw, CreatedBy: "test-user", UpdatedBy: "test-user",
	}
	if err := repo.Create(t.Context(), gw); err != nil {
		t.Fatalf("create gateway: %v", err)
	}
	return gw
}

func expectedGatewayPromptTransformHash(t *testing.T, yamlSvc *YAMLService, gw *store.Gateway) string {
	t.Helper()
	config, err := yamlSvc.BuildGatewayConfig(gw)
	if err != nil {
		t.Fatalf("build gateway config: %v", err)
	}
	return mustGatewayPromptTransformHash(t, config)
}

func TestB05_FailedGatewayPostRetainsPromptTransformHash(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db := testOpenDB(t)
	logger := slog.Default()
	gatewayRepo := store.NewGatewayRepo(db)
	deploymentRepo := store.NewGatewayDeploymentRepo(db)
	yamlSvc := NewYAMLService("test-ns", logger)
	service := NewGatewayDeploymentService(deploymentRepo, gatewayRepo, yamlSvc, unreachableControlClient(t), logger)

	gw := createGatewayRow(t, gatewayRepo, "webhook", map[string]any{
		"routePath": "/orders", "inputExpression": "template:{{json://input.body}}", "authToken": "tok-1",
		"systemPurpose": "Triage orders.",
	})
	want := expectedGatewayPromptTransformHash(t, yamlSvc, gw)

	if _, err := service.Deploy(ctx, CreateGatewayDeploymentRequest{GatewayID: gw.ID, Action: DeployActionDeploy}, "test-user"); err == nil {
		t.Fatal("expected the control-plane POST to fail")
	}
	row, err := deploymentRepo.GetLatestByGatewayID(ctx, gw.ID)
	if err != nil || row == nil {
		t.Fatalf("load failed row: %v, %v", row, err)
	}
	if row.Status != DeployStatusFailed {
		t.Fatalf("row status = %q, want failed", row.Status)
	}
	if row.PromptTransformHash == nil || *row.PromptTransformHash != want {
		t.Errorf("failed POST row prompt_transform_hash = %v, want %q", row.PromptTransformHash, want)
	}
}

func TestB04_GatewayDeployAndUpdatePersistPromptTransformHash(t *testing.T) {
	t.Parallel()
	h := setupE2EWithGWE(t, nil)

	gatewayID := h.createGateway("prompt_transform_gw")
	var previous string
	for _, step := range []struct {
		action string
		values map[string]any
	}{
		{action: DeployActionDeploy},
		{action: DeployActionUpdate, values: map[string]any{"systemPurpose": "Updated purpose."}},
	} {
		if step.values != nil {
			if _, err := h.gatewaySvc.Update(h.ctx, gatewayID, UpdateGatewayRequest{Values: step.values}, "e2e-user"); err != nil {
				t.Fatalf("update gateway: %v", err)
			}
		}
		gw, err := h.gatewayRepo.GetByID(h.ctx, gatewayID)
		if err != nil || gw == nil {
			t.Fatalf("load gateway: %v, %v", gw, err)
		}
		want := expectedGatewayPromptTransformHash(t, h.yamlSvc, gw)
		if _, deployErr := h.gwDeploySvc.Deploy(h.ctx, CreateGatewayDeploymentRequest{GatewayID: gatewayID, Action: step.action}, "e2e-user"); deployErr != nil {
			t.Fatalf("%s gateway: %v", step.action, deployErr)
		}
		row, err := h.gwDeployRepo.GetLatestByGatewayID(h.ctx, gatewayID)
		if err != nil || row == nil {
			t.Fatalf("load %s row: %v, %v", step.action, row, err)
		}
		if row.Status != DeployStatusSuccess || row.Action != step.action {
			t.Fatalf("latest row = %s/%s, want success/%s", row.Status, row.Action, step.action)
		}
		if row.PromptTransformHash == nil || *row.PromptTransformHash != want {
			t.Errorf("%s row prompt_transform_hash = %v, want %q", step.action, row.PromptTransformHash, want)
		}
		if row.PromptTransformHash != nil && *row.PromptTransformHash == previous {
			t.Errorf("%s row kept the previous hash although system_purpose changed", step.action)
		}
		if row.PromptTransformHash != nil {
			previous = *row.PromptTransformHash
		}
	}
}
