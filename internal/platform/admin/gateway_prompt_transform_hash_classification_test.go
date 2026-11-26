// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"log/slog"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

var (
	webhookRouteRoutingKeys = map[string]struct{}{
		"match": {}, "auth": {}, "target_agent_name": {}, "target_workflow_name": {}, "run_as": {}, "max_body_bytes": {},
	}
	eventHandlerRoutingKeys = map[string]struct{}{
		"subscriptions": {}, "target_agent_name": {}, "target_agent_name_expression": {}, "target_workflow_name": {},
		"target_workflow_name_expression": {}, "on_success": {}, "on_error": {}, "user_identity_expression": {},
		"default_user_identity": {}, "run_as": {}, "acknowledgment_policy": {},
	}
)

func builtGatewayAppConfig(t *testing.T, gwType string, values map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal values: %v", err)
	}
	raw := string(encoded)
	config, err := NewYAMLService("test-ns", slog.Default()).BuildGatewayConfig(&store.Gateway{
		ID: "11111111-2222-3333-4444-555555555555", Name: "gw", Type: gwType, Values: &raw,
	})
	if err != nil {
		t.Fatalf("build %s config: %v", gwType, err)
	}
	appConfig := firstAppConfig(config)
	if appConfig == nil {
		t.Fatalf("%s config has no app_config", gwType)
	}
	return appConfig
}

func assertTransformKeysClassified(t *testing.T, kind string, value any, hashed []string, routing map[string]struct{}) {
	t.Helper()
	entries, err := jsonObjectList(value)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no %s emitted (%v); the builder was not exercised", kind, err)
	}
	for _, entry := range entries {
		for key := range entry {
			if _, isRouting := routing[key]; !isRouting && !slices.Contains(hashed, key) {
				t.Errorf("%s key %q is neither in the prompt/transform projection nor declared routing-only", kind, key)
			}
		}
	}
}

func fullEventRule(name string) map[string]any {
	return map[string]any{
		"name": name, "subscriptions": []any{map[string]any{"topic": "orders/" + name}},
		"messageFormat": "json", "payloadEncoding": "base64", "promptTemplate": "New {payload}", "responseFormat": "JSON",
		"inputExpression": "template:{{json://input.payload}}", "targetAgent": "OrderAgent",
		"structuredInvocation": map[string]any{"inputSchema": map[string]any{"type": "object"}, "outputSchema": map[string]any{"type": "object"}},
		"acknowledgmentPolicy": map[string]any{
			"mode": "on_completion", "timeoutSeconds": 30, "onFailure": map[string]any{"action": "nack", "nackOutcome": "rejected"},
		},
		"defaultUserIdentity": "svc", "runAs": "svc", "userIdentityExpression": "json://input.payload:user",
		"forwardContext": map[string]any{"id": "json://input.payload:id"},
		"artifactProcessing": map[string]any{
			"extractExpression": "json://input.payload:files",
			"artifactDefinition": map[string]any{
				"filename": "f", "content": "c", "mimeType": "text/plain", "contentEncoding": "text",
			},
		},
		"successOutput": map[string]any{"enabled": true, "topic": "orders/done"},
		"errorOutput":   map[string]any{"enabled": true, "topic": "orders/failed"},
	}
}

func TestB04_EveryEmittedRouteAndHandlerKeyIsClassified(t *testing.T) {
	t.Parallel()
	for _, authMode := range []string{"token", "hmac"} {
		appConfig := builtGatewayAppConfig(t, "webhook", map[string]any{
			"routePath": "/orders", "method": "POST", "inputExpression": "template:{{json://input.body}}",
			"authMode": authMode, "authToken": "tok-1", "authTokenName": "X-Token", "hmacSecret": "s",
			"targetAgentName": "OrderAgent", "targetWorkflowName": "flow", "runAs": "svc", "maxBodyBytes": 1024,
			"systemPurpose": "Triage orders.",
		})
		adapter, _ := appConfig["gateway_adapter"].(map[string]any)
		adapterConfig, _ := adapter["adapter_config"].(map[string]any)
		assertTransformKeysClassified(t, "webhook route", adapterConfig["routes"], webhookRouteTransformKeys, webhookRouteRoutingKeys)
	}

	workflowRule := fullEventRule("to_workflow")
	delete(workflowRule, "targetAgent")
	workflowRule["targetWorkflowName"] = "order_flow"
	appConfig := builtGatewayAppConfig(t, "event_mesh", map[string]any{
		"broker_url": "tcps://broker:55443", "broker_password": "pw", "systemPurpose": "Triage events.",
		"event_rules": []any{fullEventRule("to_agent"), workflowRule},
	})
	assertTransformKeysClassified(t, "event handler", appConfig["event_handlers"], eventHandlerTransformKeys, eventHandlerRoutingKeys)
}
