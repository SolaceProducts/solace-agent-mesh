// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestValidateSlackValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		values  map[string]any
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid tokens",
			values:  map[string]any{"bot_token": "xoxb-123", "app_token": "xapp-456"},
			wantErr: false,
		},
		{
			name:    "empty tokens ok",
			values:  map[string]any{},
			wantErr: false,
		},
		{
			name:    "bad bot_token prefix",
			values:  map[string]any{"bot_token": "invalid-token"},
			wantErr: true,
			errMsg:  "bot_token must start with 'xoxb-'",
		},
		{
			name:    "bad app_token prefix",
			values:  map[string]any{"bot_token": "xoxb-ok", "app_token": "invalid-token"},
			wantErr: true,
			errMsg:  "app_token must start with 'xapp-'",
		},
		{
			name:    "placeholder tokens bypass validation",
			values:  map[string]any{"bot_token": "<<__SAM_REQUIRED__>>", "app_token": "<<__SAM_REQUIRED__>>"},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateSlackValues(tt.values)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSlackValues() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" && err.Error() != tt.errMsg {
				t.Errorf("error message = %q, want %q", err.Error(), tt.errMsg)
			}
		})
	}
}

const validGUID = "12345678-1234-1234-1234-123456789abc"

func TestValidateTeamsValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		values     map[string]any
		wantErr    bool
		wantFields []string
	}{
		{
			name:    "valid app_id and password",
			values:  map[string]any{"microsoft_app_id": validGUID, "microsoft_app_password": "secret"},
			wantErr: false,
		},
		{
			name:       "missing app_id",
			values:     map[string]any{"microsoft_app_password": "secret"},
			wantErr:    true,
			wantFields: []string{"microsoft_app_id"},
		},
		{
			name:       "malformed app_id",
			values:     map[string]any{"microsoft_app_id": "not-a-guid", "microsoft_app_password": "secret"},
			wantErr:    true,
			wantFields: []string{"microsoft_app_id"},
		},
		{
			name:       "empty password",
			values:     map[string]any{"microsoft_app_id": validGUID, "microsoft_app_password": ""},
			wantErr:    true,
			wantFields: []string{"microsoft_app_password"},
		},
		{
			name:       "missing password",
			values:     map[string]any{"microsoft_app_id": validGUID},
			wantErr:    true,
			wantFields: []string{"microsoft_app_password"},
		},
		{
			name:       "malformed app_id and empty password",
			values:     map[string]any{"microsoft_app_id": "not-a-guid", "microsoft_app_password": ""},
			wantErr:    true,
			wantFields: []string{"microsoft_app_id", "microsoft_app_password"},
		},
		{
			name: "placeholders bypass validation",
			values: map[string]any{
				"microsoft_app_id":       "<<__SAM_REQUIRED__>>",
				"microsoft_app_password": "<<__SAM_REQUIRED__>>",
			},
			wantErr: false,
		},
		{
			name: "optional tenant_id valid GUID",
			values: map[string]any{
				"microsoft_app_id":        validGUID,
				"microsoft_app_password":  "secret",
				"microsoft_app_tenant_id": validGUID,
			},
			wantErr: false,
		},
		{
			name: "optional tenant_id empty is allowed",
			values: map[string]any{
				"microsoft_app_id":        validGUID,
				"microsoft_app_password":  "secret",
				"microsoft_app_tenant_id": "",
			},
			wantErr: false,
		},
		{
			name: "optional tenant_id malformed",
			values: map[string]any{
				"microsoft_app_id":        validGUID,
				"microsoft_app_password":  "secret",
				"microsoft_app_tenant_id": "not-a-guid",
			},
			wantErr:    true,
			wantFields: []string{"microsoft_app_tenant_id"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateTeamsValues(tt.values)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateTeamsValues() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil {
				return
			}
			validErr, ok := errors.AsType[*ValidationError](err)
			if !ok {
				t.Fatalf("expected *ValidationError, got %T", err)
			}
			for _, field := range tt.wantFields {
				if len(validErr.Details[field]) == 0 {
					t.Errorf("expected validationDetails[%q], got %+v", field, validErr.Details)
				}
			}
		})
	}
}

func TestMergeGatewayValuesForUpdate(t *testing.T) {
	t.Parallel()
	const guid = "12345678-1234-1234-1234-123456789abc"

	t.Run("teams partial PATCH preserves required id", func(t *testing.T) {
		t.Parallel()
		existing := map[string]any{"microsoft_app_id": guid, "microsoft_app_password": "stored-secret", "default_agent_name": "Orchestrator"}
		incoming := map[string]any{"default_agent_name": "WeatherAgent"}
		merged, err := mergeGatewayValuesForUpdate("teams", incoming, existing, getSecretRegistry().GatewaySecretFields("teams"))
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if merged["microsoft_app_id"] != guid {
			t.Errorf("microsoft_app_id = %v, want preserved %q", merged["microsoft_app_id"], guid)
		}
		if merged["default_agent_name"] != "WeatherAgent" {
			t.Errorf("default_agent_name = %v, want WeatherAgent", merged["default_agent_name"])
		}
		if err := validateGatewayValues("teams", merged); err != nil {
			t.Errorf("validate after merge: %v", err)
		}
	})

	t.Run("email basic to oauth2 drops inactive imap_password", func(t *testing.T) {
		t.Parallel()
		existing := map[string]any{
			"mailbox": "alice@vendor.com", "auth_mode": "basic", "imap_password": "stored-imap",
			"trusted_authserv_ids": "mx.google.com",
		}
		incoming := map[string]any{
			"mailbox": "alice@vendor.com", "auth_mode": "oauth2",
			"oauth2_tenant_id": guid, "oauth2_client_id": guid, "oauth2_client_secret": "new-secret",
			"trusted_authserv_ids": "protection.outlook.com",
		}
		merged, err := mergeGatewayValuesForUpdate("email", incoming, existing, getSecretRegistry().GatewaySecretFields("email"))
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if v, present := merged["imap_password"]; present && v != "" && v != nil {
			t.Errorf("imap_password = %v, want dropped on switch to oauth2", v)
		}
		if err := validateGatewayValues("email", merged); err != nil {
			t.Errorf("validate after basic→oauth2 merge: %v", err)
		}
	})

	t.Run("email oauth2 to basic drops inactive oauth2 fields", func(t *testing.T) {
		t.Parallel()
		existing := map[string]any{
			"mailbox": "alice@vendor.com", "auth_mode": "oauth2",
			"oauth2_tenant_id": guid, "oauth2_client_id": guid, "oauth2_client_secret": "stored-secret",
			"trusted_authserv_ids": "protection.outlook.com",
		}
		incoming := map[string]any{
			"mailbox": "alice@vendor.com", "auth_mode": "basic", "imap_password": "new-imap",
			"trusted_authserv_ids": "mx.google.com",
		}
		merged, err := mergeGatewayValuesForUpdate("email", incoming, existing, getSecretRegistry().GatewaySecretFields("email"))
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		for _, f := range []string{"oauth2_tenant_id", "oauth2_client_id", "oauth2_client_secret"} {
			if v, present := merged[f]; present && v != "" && v != nil {
				t.Errorf("%s = %v, want dropped on switch to basic", f, v)
			}
		}
		if err := validateGatewayValues("email", merged); err != nil {
			t.Errorf("validate after oauth2→basic merge: %v", err)
		}
	})

	webhookBase := func() map[string]any {
		return map[string]any{
			"routePath": "/orders", "method": "POST",
			"authMode": "token", "authToken": "stored-token",
			"targetType": "agent", "targetAgentName": "OrderAgent",
			"inputExpression": "input.payload",
		}
	}

	t.Run("webhook header to bearer drops inactive authTokenName", func(t *testing.T) {
		t.Parallel()
		existing := webhookBase()
		existing["authTokenLocation"] = "header"
		existing["authTokenName"] = "X-Webhook-Token"

		incoming := webhookBase()
		incoming["authTokenLocation"] = "bearer"

		merged, err := mergeGatewayValuesForUpdate("webhook", incoming, existing, getSecretRegistry().GatewaySecretFields("webhook"))
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if v, present := merged["authTokenName"]; present && v != "" && v != nil {
			t.Errorf("authTokenName = %v, want dropped on switch to bearer", v)
		}
		if err := validateGatewayValues("webhook", merged); err != nil {
			t.Errorf("validate after header→bearer merge: %v", err)
		}
	})

	t.Run("webhook partial PATCH preserves active authTokenName", func(t *testing.T) {
		t.Parallel()
		existing := webhookBase()
		existing["authTokenLocation"] = "query"
		existing["authTokenName"] = "webhook_token"

		merged, err := mergeGatewayValuesForUpdate("webhook", map[string]any{"targetAgentName": "InvoiceAgent"}, existing, getSecretRegistry().GatewaySecretFields("webhook"))
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if merged["authTokenName"] != "webhook_token" {
			t.Errorf("authTokenName = %v, want preserved while location is query", merged["authTokenName"])
		}
		if err := validateGatewayValues("webhook", merged); err != nil {
			t.Errorf("validate after partial merge: %v", err)
		}
	})
}

func TestValidateEventMeshValues_CustomBroker(t *testing.T) {
	t.Parallel()
	rules := []any{
		map[string]any{
			"name":           "default_rule",
			"subscriptions":  []any{map[string]any{"topic": "events/>"}},
			"targetAgent":    "agent1",
			"promptTemplate": "Process {{message}}",
		},
	}
	tests := []struct {
		name    string
		values  map[string]any
		wantErr bool
	}{
		{
			name: "valid explicit broker",
			values: map[string]any{
				"broker_url":      "tcps://broker.example.com:55443",
				"broker_vpn":      "default",
				"broker_username": "admin",
				"broker_password": "secret",
				"event_rules":     rules,
			},
			wantErr: false,
		},
		{
			name:    "empty values missing rules",
			values:  map[string]any{},
			wantErr: true,
		},
		{
			name: "explicit broker bad url format",
			values: map[string]any{
				"broker_url":      "http://not-a-broker",
				"broker_vpn":      "default",
				"broker_username": "admin",
				"broker_password": "secret",
				"event_rules":     rules,
			},
			wantErr: true,
		},
		{
			name: "stray broker_connection_type ignored (back-compat)",
			values: map[string]any{
				"broker_connection_type": "bogus",
				"broker_url":             "tcps://broker.example.com:55443",
				"broker_vpn":             "default",
				"broker_username":        "admin",
				"broker_password":        "secret",
				"event_rules":            rules,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateEventMeshValues(tt.values)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateEventMeshValues() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateEventMeshValues_BrokerFieldsByEnv(t *testing.T) {
	rules := []any{
		map[string]any{
			"name":           "default_rule",
			"subscriptions":  []any{map[string]any{"topic": "events/>"}},
			"targetAgent":    "agent1",
			"promptTemplate": "Process {{message}}",
		},
	}
	noBroker := map[string]any{"event_rules": rules}
	explicitBroker := map[string]any{
		"broker_url":      "tcps://broker.example.com:55443",
		"broker_vpn":      "default",
		"broker_username": "admin",
		"broker_password": "secret",
		"event_rules":     rules,
	}

	t.Run("dev (no system broker) — broker fields optional", func(t *testing.T) {
		t.Setenv("SOLACE_BROKER_URL", "")
		if err := validateEventMeshValues(noBroker); err != nil {
			t.Errorf("expected empty broker fields to be valid in dev (dev-broker fallback), got %v", err)
		}
	})

	t.Run("prod (system broker set) — broker fields required", func(t *testing.T) {
		t.Setenv("SOLACE_BROKER_URL", "tcps://platform-broker.example.com:55443")
		if err := validateEventMeshValues(noBroker); err == nil {
			t.Error("expected empty broker fields to be rejected when a system broker is configured")
		}
		if err := validateEventMeshValues(explicitBroker); err != nil {
			t.Errorf("expected explicit broker fields to be valid when a system broker is configured, got %v", err)
		}
	})
}

func withRules(rules ...map[string]any) map[string]any {
	rs := make([]any, len(rules))
	for i, r := range rules {
		rs[i] = r
	}
	return map[string]any{
		"broker_url":      "tcps://broker.example.com:55443",
		"broker_vpn":      "default",
		"broker_username": "admin",
		"broker_password": "secret",
		"event_rules":     rs,
	}
}

func TestValidateEventMeshValues_EventRules(t *testing.T) {
	t.Parallel()
	validRule := func() map[string]any {
		return map[string]any{
			"name": "my_rule",
			"subscriptions": []any{
				map[string]any{"topic": "events/order/created"},
			},
			"targetAgent":    "agent1",
			"promptTemplate": "Process {{message}}",
		}
	}
	tests := []struct {
		name    string
		values  map[string]any
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid single rule",
			values:  withRules(validRule()),
			wantErr: false,
		},
		{
			name: "missing name",
			values: withRules(map[string]any{
				"subscriptions": []any{map[string]any{"topic": "t"}},
			}),
			wantErr: true,
			errMsg:  "name is required",
		},
		{
			name: "invalid name chars",
			values: withRules(map[string]any{
				"name":          "bad name!",
				"subscriptions": []any{map[string]any{"topic": "t"}},
			}),
			wantErr: true,
		},
		{
			name: "duplicate names case-insensitive",
			values: withRules(
				map[string]any{
					"name":           "MyRule",
					"subscriptions":  []any{map[string]any{"topic": "t"}},
					"targetAgent":    "agent1",
					"promptTemplate": "Process {{message}}",
				},
				map[string]any{
					"name":           "myrule",
					"subscriptions":  []any{map[string]any{"topic": "t"}},
					"targetAgent":    "agent1",
					"promptTemplate": "Process {{message}}",
				},
			),
			wantErr: true,
			errMsg:  "duplicate rule name",
		},
		{
			name:    "missing subscriptions",
			values:  withRules(map[string]any{"name": "rule1"}),
			wantErr: true,
			errMsg:  "subscriptions is required",
		},
		{
			name: "empty subscriptions list",
			values: withRules(map[string]any{
				"name":          "rule1",
				"subscriptions": []any{},
			}),
			wantErr: true,
			errMsg:  "subscriptions is required",
		},
		{
			name: "subscription missing topic",
			values: withRules(map[string]any{
				"name":          "rule1",
				"subscriptions": []any{map[string]any{}},
			}),
			wantErr: true,
		},
		{
			name: "existingQueueName alone is valid",
			values: withRules(map[string]any{
				"name":              "rule1",
				"existingQueueName": "preprov/q/orders",
				"targetAgent":       "agent1",
				"promptTemplate":    "Process {{message}}",
			}),
			wantErr: false,
		},
		{
			name: "existingQueueName and subscriptions mutually exclusive",
			values: withRules(map[string]any{
				"name":              "rule1",
				"existingQueueName": "preprov/q/orders",
				"subscriptions":     []any{map[string]any{"topic": "t"}},
				"targetAgent":       "agent1",
				"promptTemplate":    "Process {{message}}",
			}),
			wantErr: true,
			errMsg:  "existingQueueName and subscriptions are mutually exclusive",
		},
		{
			name: "whitespace-only existingQueueName is rejected",
			values: withRules(map[string]any{
				"name":              "rule1",
				"existingQueueName": "   ",
				"targetAgent":       "agent1",
				"promptTemplate":    "P",
			}),
			wantErr: true,
			errMsg:  "subscriptions is required",
		},
		{
			name: "existingQueueName naming a SAM-managed eventmesh queue path is rejected",
			values: withRules(map[string]any{
				"name":              "rule1",
				"existingQueueName": "other-ns/q/eventmesh/other-gw/other-rule",
				"targetAgent":       "agent1",
				"promptTemplate":    "P",
			}),
			wantErr: true,
			errMsg:  "Agent Mesh-managed event-mesh queue path",
		},
		{
			name: "eventFilter that compiles is accepted",
			values: withRules(map[string]any{
				"name":           "rule1",
				"subscriptions":  []any{map[string]any{"topic": "t"}},
				"targetAgent":    "agent1",
				"promptTemplate": "P",
				"eventFilter": map[string]any{
					"function": "greater_than",
					"params":   map[string]any{"positional": []any{"evaluate_expression(input.payload:total)", float64(1000)}},
				},
			}),
			wantErr: false,
		},
		{
			name: "eventFilter that does not compile is refused at save",
			values: withRules(map[string]any{
				"name":           "rule1",
				"subscriptions":  []any{map[string]any{"topic": "t"}},
				"targetAgent":    "agent1",
				"promptTemplate": "P",
				"eventFilter": map[string]any{
					"function": "equal",
					"params":   map[string]any{"positional": []any{"evaluate_expression(user_data.x:y)", "a"}},
				},
			}),
			wantErr: true,
			errMsg:  "event_rules[0].eventFilter.params.positional[0]",
		},
		{
			name: "eventFilter that is not an object is refused",
			values: withRules(map[string]any{
				"name":           "rule1",
				"subscriptions":  []any{map[string]any{"topic": "t"}},
				"targetAgent":    "agent1",
				"promptTemplate": "P",
				"eventFilter":    "input.payload:urgent",
			}),
			wantErr: true,
			errMsg:  "eventFilter",
		},
		{
			name: "customer naming convention preprov/q/<name> is accepted",
			values: withRules(map[string]any{
				"name":              "rule1",
				"existingQueueName": "preprov/q/customer-owned",
				"targetAgent":       "agent1",
				"promptTemplate":    "P",
			}),
			wantErr: false,
		},
		{
			name: "duplicate existingQueueName across rules rejected",
			values: withRules(
				map[string]any{
					"name":              "orders",
					"existingQueueName": "preprov/q/shared",
					"targetAgent":       "agent1",
					"promptTemplate":    "P",
				},
				map[string]any{
					"name":              "invoices",
					"existingQueueName": "preprov/q/shared",
					"targetAgent":       "agent1",
					"promptTemplate":    "P",
				},
			),
			wantErr: true,
			errMsg:  "two rules cannot bind the same queue",
		},
		{
			name: "target mutual exclusivity",
			values: withRules(map[string]any{
				"name":               "rule1",
				"subscriptions":      []any{map[string]any{"topic": "t"}},
				"targetAgent":        "agent1",
				"targetWorkflowName": "wf1",
				"promptTemplate":     "Process {{message}}",
			}),
			wantErr: true,
			errMsg:  "mutually exclusive",
		},
		{
			name: "promptTemplate required with agent target",
			values: withRules(map[string]any{
				"name":          "rule1",
				"subscriptions": []any{map[string]any{"topic": "t"}},
				"targetAgent":   "agent1",
			}),
			wantErr: true,
			errMsg:  "promptTemplate is required",
		},
		{
			name: "workflow target with inputExpression is valid",
			values: withRules(map[string]any{
				"name":               "rule1",
				"subscriptions":      []any{map[string]any{"topic": "t"}},
				"targetWorkflowName": "wf1",
				"inputExpression":    "input.payload",
			}),
			wantErr: false,
		},
		{
			name: "workflow target without promptTemplate or inputExpression is valid",
			values: withRules(map[string]any{
				"name":               "rule1",
				"subscriptions":      []any{map[string]any{"topic": "t"}},
				"targetWorkflowName": "wf1",
			}),
			wantErr: false,
		},
		{
			name: "target with promptTemplate is valid",
			values: withRules(map[string]any{
				"name":           "rule1",
				"subscriptions":  []any{map[string]any{"topic": "t"}},
				"targetAgent":    "agent1",
				"promptTemplate": "Process {{message}}",
			}),
			wantErr: false,
		},
		{
			name: "valid acknowledgmentPolicy object form",
			values: withRules(map[string]any{
				"name":           "rule1",
				"subscriptions":  []any{map[string]any{"topic": "t"}},
				"targetAgent":    "agent1",
				"promptTemplate": "Process {{message}}",
				"acknowledgmentPolicy": map[string]any{
					"mode":           "on_completion",
					"timeoutSeconds": 60,
				},
			}),
			wantErr: false,
		},
		{
			name: "acknowledgmentPolicy string shorthand",
			values: withRules(map[string]any{
				"name":                 "rule1",
				"subscriptions":        []any{map[string]any{"topic": "t"}},
				"targetAgent":          "agent1",
				"promptTemplate":       "Process {{message}}",
				"acknowledgmentPolicy": "on_completion",
			}),
			wantErr: false,
		},
		{
			name: "invalid acknowledgmentPolicy mode",
			values: withRules(map[string]any{
				"name":          "rule1",
				"subscriptions": []any{map[string]any{"topic": "t"}},
				"acknowledgmentPolicy": map[string]any{
					"mode": "invalid",
				},
			}),
			wantErr: true,
		},
		{
			name: "invalid messageFormat",
			values: withRules(map[string]any{
				"name":          "rule1",
				"subscriptions": []any{map[string]any{"topic": "t"}},
				"messageFormat": "invalid",
			}),
			wantErr: true,
		},
		{
			name: "structured invocation passes",
			values: withRules(map[string]any{
				"name":               "rule1",
				"subscriptions":      []any{map[string]any{"topic": "t"}},
				"targetWorkflowName": "wf1",
				"inputExpression":    "input.payload",
				"structuredInvocation": map[string]any{
					"outputSchema": map[string]any{
						"type":     "object",
						"required": []any{"id"},
					},
				},
			}),
			wantErr: false,
		},
		{
			name: "responseType=custom needs customExpression",
			values: withRules(map[string]any{
				"name":           "rule1",
				"subscriptions":  []any{map[string]any{"topic": "t"}},
				"targetAgent":    "agent1",
				"promptTemplate": "x",
				"successOutput": map[string]any{
					"enabled":      true,
					"topic":        "out/topic",
					"responseType": "custom",
				},
			}),
			wantErr: true,
			errMsg:  "customExpression is required",
		},
		{
			name: "responseType=structured with customExpression empty is ok",
			values: withRules(map[string]any{
				"name":               "rule1",
				"subscriptions":      []any{map[string]any{"topic": "t"}},
				"targetWorkflowName": "wf1",
				"inputExpression":    "input.payload",
				"successOutput": map[string]any{
					"enabled":      true,
					"topic":        "out/topic",
					"responseType": "structured",
				},
			}),
			wantErr: false,
		},
		{
			name: "invalid responseType on output",
			values: withRules(map[string]any{
				"name":           "rule1",
				"subscriptions":  []any{map[string]any{"topic": "t"}},
				"targetAgent":    "agent1",
				"promptTemplate": "x",
				"successOutput": map[string]any{
					"enabled":      true,
					"topic":        "out/topic",
					"responseType": "summary",
				},
			}),
			wantErr: true,
		},
		{
			name: "qos out of range",
			values: withRules(map[string]any{
				"name": "rule1",
				"subscriptions": []any{
					map[string]any{"topic": "t", "qos": 5},
				},
			}),
			wantErr: true,
		},
		{
			name: "dynamic topic with template syntax accepted",
			values: withRules(map[string]any{
				"name":           "rule1",
				"subscriptions":  []any{map[string]any{"topic": "t"}},
				"targetAgent":    "agent1",
				"promptTemplate": "x",
				"successOutput": map[string]any{
					"enabled":      true,
					"topic":        "fossa/analysis/result/{{text://user_data.forward_context:event_id}}",
					"topicType":    "dynamic",
					"responseType": "text",
				},
			}),
			wantErr: false,
		},
		{
			name: "dynamic topic with filter syntax accepted",
			values: withRules(map[string]any{
				"name":           "rule1",
				"subscriptions":  []any{map[string]any{"topic": "t"}},
				"targetAgent":    "agent1",
				"promptTemplate": "x",
				"successOutput": map[string]any{
					"enabled":      true,
					"topic":        "fossa/analysis/result/{{payload.event_id | default: 'unknown'}}",
					"topicType":    "dynamic",
					"responseType": "text",
				},
			}),
			wantErr: false,
		},
		{
			name: "dynamic topic with bracket-indexed template accepted",
			values: withRules(map[string]any{
				"name":           "rule1",
				"subscriptions":  []any{map[string]any{"topic": "t"}},
				"targetAgent":    "agent1",
				"promptTemplate": "x",
				"successOutput": map[string]any{
					"enabled":      true,
					"topic":        "fossa/analysis/result/{{items[0]}}",
					"topicType":    "dynamic",
					"responseType": "text",
				},
			}),
			wantErr: false,
		},
		{
			name: "topic with question-mark rejected",
			values: withRules(map[string]any{
				"name":           "rule1",
				"subscriptions":  []any{map[string]any{"topic": "t"}},
				"targetAgent":    "agent1",
				"promptTemplate": "x",
				"successOutput": map[string]any{
					"enabled":      true,
					"topic":        "fossa/analysis/result?probe=1",
					"topicType":    "static",
					"responseType": "text",
				},
			}),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateEventMeshValues(tt.values)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateEventMeshValues() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestGetSchemas_RegistersExpectedTypes(t *testing.T) {
	t.Parallel()
	schemas := getAllGatewaySchemas()

	got := make(map[string]bool, len(schemas))
	for _, s := range schemas {
		got[s.Type] = true
	}
	for _, want := range []string{"slack", "email", "event_mesh", "teams", "mcp"} {
		if !got[want] {
			t.Errorf("expected schema for %q to be registered", want)
		}
	}
}

func TestValidateGatewayValues_Dispatch(t *testing.T) {
	t.Parallel()
	if err := validateGatewayValues("unknown_type", map[string]any{"foo": "bar"}); err != nil {
		t.Errorf("unknown type should not error: %v", err)
	}

	err := validateGatewayValues("slack", map[string]any{"bot_token": "bad-token"})
	if err == nil {
		t.Error("expected error for bad slack bot_token")
	}

	err = validateGatewayValues("event_mesh", map[string]any{})
	if err == nil {
		t.Error("expected error for event_mesh missing required event_rules")
	}

	err = validateGatewayValues("teams", map[string]any{
		"microsoft_app_id":       "not-a-guid",
		"microsoft_app_password": "",
	})
	if err == nil {
		t.Error("expected error for teams with bad app_id and empty password")
	}

	err = validateGatewayValues("whatsapp", map[string]any{
		"phone_number_id": "1234567890",
	})
	if err == nil {
		t.Error("expected error for whatsapp missing required credentials")
	}
}

func TestValidateWhatsAppValues(t *testing.T) {
	t.Parallel()
	valid := map[string]any{
		"phone_number_id": "1234567890",
		"access_token":    "EAAtoken",
		"app_secret":      "appsecret",
		"verify_token":    "verifytoken",
	}
	clone := func(m map[string]any) map[string]any {
		c := make(map[string]any, len(m))
		for k, v := range m {
			c[k] = v
		}
		return c
	}

	if err := validateWhatsAppValues(valid); err != nil {
		t.Fatalf("valid whatsapp values should pass, got %v", err)
	}

	if err := validateWhatsAppValues(valid); err != nil {
		t.Fatalf("default_agent_name is optional: %v", err)
	}

	for _, field := range []string{"phone_number_id", "access_token", "app_secret", "verify_token"} {
		v := clone(valid)
		delete(v, field)
		err := validateWhatsAppValues(v)
		if err == nil {
			t.Fatalf("missing %s should fail validation", field)
		}
		verr, ok := err.(*ValidationError)
		if !ok {
			t.Fatalf("expected *ValidationError for missing %s, got %T", field, err)
		}
		if _, has := verr.Details[field]; !has {
			t.Errorf("missing %s should produce a validationDetails entry, got %v", field, verr.Details)
		}
	}

	err := validateWhatsAppValues(map[string]any{})
	verr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("empty bag should fail with *ValidationError, got %T", err)
	}
	if len(verr.Details) != 4 {
		t.Errorf("empty bag should batch all 4 required-field errors, got %d: %v", len(verr.Details), verr.Details)
	}

	ws := clone(valid)
	ws["access_token"] = "   "
	if err := validateWhatsAppValues(ws); err == nil {
		t.Error("whitespace-only access_token should be rejected")
	}

	badPhone := clone(valid)
	badPhone["phone_number_id"] = "+1 555 0100"
	phoneErr := validateWhatsAppValues(badPhone)
	phoneVErr, isValidationErr := phoneErr.(*ValidationError)
	if !isValidationErr {
		t.Fatalf("non-numeric phone_number_id should fail with *ValidationError, got %T", phoneErr)
	}
	if _, has := phoneVErr.Details["phone_number_id"]; !has {
		t.Errorf("non-numeric phone_number_id should produce a validationDetails entry, got %v", phoneVErr.Details)
	}

	placeholderPhone := clone(valid)
	placeholderPhone["phone_number_id"] = "<<__SAM_REQUIRED__>>"
	if err := validateWhatsAppValues(placeholderPhone); err != nil {
		t.Errorf("placeholder phone_number_id should pass (substituted at runtime): %v", err)
	}
}

func TestValidateEmailValues(t *testing.T) {
	t.Parallel()
	validBasic := map[string]any{
		"mailbox":              "alice@vendor.com",
		"auth_mode":            "basic",
		"imap_password":        "app-password",
		"trusted_authserv_ids": "mx.google.com",
	}
	validOAuth2 := map[string]any{
		"mailbox":              "alice@vendor.com",
		"auth_mode":            "oauth2",
		"oauth2_tenant_id":     "tenant-uuid",
		"oauth2_client_id":     "client-uuid",
		"oauth2_client_secret": "client-secret",
		"trusted_authserv_ids": "protection.outlook.com",
	}
	clone := func(m map[string]any) map[string]any {
		c := make(map[string]any, len(m))
		for k, v := range m {
			c[k] = v
		}
		return c
	}

	tests := []struct {
		name    string
		values  map[string]any
		wantErr bool
		errMsg  string
	}{
		{name: "valid basic auth", values: validBasic, wantErr: false},
		{name: "valid oauth2 auth", values: validOAuth2, wantErr: false},
		{
			name: "missing mailbox",
			values: func() map[string]any {
				m := clone(validBasic)
				delete(m, "mailbox")
				return m
			}(),
			wantErr: true,
			errMsg:  "mailbox is required",
		},
		{
			name: "mailbox without @",
			values: func() map[string]any {
				m := clone(validBasic)
				m["mailbox"] = "not-an-email"
				return m
			}(),
			wantErr: true,
			errMsg:  "mailbox must be a valid email address",
		},
		{
			name: "auth_mode missing",
			values: func() map[string]any {
				m := clone(validBasic)
				delete(m, "auth_mode")
				return m
			}(),
			wantErr: true,
			errMsg:  "auth_mode must be 'basic' or 'oauth2'",
		},
		{
			name: "basic without imap_password",
			values: func() map[string]any {
				m := clone(validBasic)
				delete(m, "imap_password")
				return m
			}(),
			wantErr: true,
			errMsg:  "imap_password is required when auth_mode=basic",
		},
		{
			name: "oauth2 missing tenant_id",
			values: func() map[string]any {
				m := clone(validOAuth2)
				delete(m, "oauth2_tenant_id")
				return m
			}(),
			wantErr: true,
			errMsg:  "oauth2_tenant_id is required when auth_mode=oauth2",
		},
		{
			name: "oauth2 missing client_id",
			values: func() map[string]any {
				m := clone(validOAuth2)
				delete(m, "oauth2_client_id")
				return m
			}(),
			wantErr: true,
			errMsg:  "oauth2_client_id is required when auth_mode=oauth2",
		},
		{
			name: "oauth2 missing client_secret",
			values: func() map[string]any {
				m := clone(validOAuth2)
				delete(m, "oauth2_client_secret")
				return m
			}(),
			wantErr: true,
			errMsg:  "oauth2_client_secret is required when auth_mode=oauth2",
		},
		{
			name: "basic rejects oauth2_client_secret leftover",
			values: func() map[string]any {
				m := clone(validBasic)
				m["oauth2_client_secret"] = "stale-secret"
				return m
			}(),
			wantErr: true,
			errMsg:  "oauth2_client_secret must be empty when auth_mode=basic",
		},
		{
			name: "oauth2 rejects imap_password leftover",
			values: func() map[string]any {
				m := clone(validOAuth2)
				m["imap_password"] = "stale-password"
				return m
			}(),
			wantErr: true,
			errMsg:  "imap_password must be empty when auth_mode=oauth2",
		},
		{
			name: "basic with placeholder oauth2 secret is allowed",
			values: func() map[string]any {
				m := clone(validBasic)
				m["oauth2_client_secret"] = "<<__SAM_REQUIRED__>>"
				return m
			}(),
			wantErr: false,
		},
		{
			name: "invalid policy_mode",
			values: func() map[string]any {
				m := clone(validBasic)
				m["policy_mode"] = "wide_open"
				return m
			}(),
			wantErr: true,
			errMsg:  "policy_mode must be one of: log_only, require_dmarc, strict",
		},
		{
			name: "policy_mode strict is valid",
			values: func() map[string]any {
				m := clone(validBasic)
				m["policy_mode"] = "strict"
				return m
			}(),
			wantErr: false,
		},
		{
			name: "invalid delivery_mode",
			values: func() map[string]any {
				m := clone(validBasic)
				m["delivery_mode"] = "magic"
				return m
			}(),
			wantErr: true,
			errMsg:  "delivery_mode must be one of: auto, idle, poll",
		},
		{
			name: "delivery_mode idle is valid",
			values: func() map[string]any {
				m := clone(validBasic)
				m["delivery_mode"] = "idle"
				return m
			}(),
			wantErr: false,
		},
		{
			name: "delivery_mode poll is valid",
			values: func() map[string]any {
				m := clone(validBasic)
				m["delivery_mode"] = "poll"
				return m
			}(),
			wantErr: false,
		},
		{
			name: "delivery_mode omitted is valid (default auto)",
			values: func() map[string]any {
				return clone(validBasic)
			}(),
			wantErr: false,
		},
		{
			name: "trusted_authserv_ids empty string",
			values: func() map[string]any {
				m := clone(validBasic)
				m["trusted_authserv_ids"] = ""
				return m
			}(),
			wantErr: true,
			errMsg:  "at least one trusted_authserv_ids entry is required",
		},
		{
			name: "trusted_authserv_ids comma-only",
			values: func() map[string]any {
				m := clone(validBasic)
				m["trusted_authserv_ids"] = ",, ,,"
				return m
			}(),
			wantErr: true,
			errMsg:  "at least one trusted_authserv_ids entry is required",
		},
		{
			name: "trusted_authserv_ids array shape accepted",
			values: func() map[string]any {
				m := clone(validBasic)
				m["trusted_authserv_ids"] = []any{"mx.google.com"}
				return m
			}(),
			wantErr: false,
		},
		{
			name: "trusted_authserv_ids missing",
			values: func() map[string]any {
				m := clone(validBasic)
				delete(m, "trusted_authserv_ids")
				return m
			}(),
			wantErr: true,
			errMsg:  "at least one trusted_authserv_ids entry is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateEmailValues(tt.values)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateEmailValues() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" && err.Error() != tt.errMsg {
				t.Errorf("error message = %q, want %q", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestValidateMCPGatewayValues_AuthOnHappyPath(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"enableAuth": "true",
		"allowedRedirectUris": []any{
			map[string]any{"uri": "https://claude.ai/api/mcp/auth_callback"},
			map[string]any{"uri": "http://127.0.0.1"},
		},
	}
	if err := validateMCPGatewayValues(values); err != nil {
		t.Errorf("validateMCPGatewayValues happy-path: %v", err)
	}
}

func TestValidateMCPGatewayValues_AuthOffAllowsUnsetDefaultIdentity(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"enableAuth": "false",
	}
	if err := validateMCPGatewayValues(values); err != nil {
		t.Errorf("enableAuth=false with no defaultUserIdentity should be accepted: %v", err)
	}
}

func TestValidateMCPGatewayValues_RejectsSystemPrefixIdentity(t *testing.T) {
	t.Parallel()
	for _, identity := range []string{"system:default", "system:orders-bot"} {
		values := map[string]any{
			"enableAuth":          "false",
			"defaultUserIdentity": identity,
		}
		err := validateMCPGatewayValues(values)
		if err == nil {
			t.Fatalf("expected %q to be rejected", identity)
		}
		if !strings.Contains(err.Error(), "reserved") {
			t.Errorf("unexpected error message for %q: %v", identity, err)
		}
	}
}

func TestValidateMCPGatewayValues_AuthOnRejectsDefaultIdentityOnHTTP(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"enableAuth":          "true",
		"defaultUserIdentity": "alice",
	}
	err := validateMCPGatewayValues(values)
	if err == nil {
		t.Fatal("expected error when enableAuth=true and defaultUserIdentity set on HTTP")
	}
	if !strings.Contains(err.Error(), "incompatible with enableAuth") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateMCPGatewayValues_StateRedirectionBlankURLAllowedWhenDerivable(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://api.example.com")
	values := map[string]any{
		"enableAuth":              "true",
		"stateRedirectionEnabled": "true",
		"allowedRedirectUris":     []any{},
	}
	if err := validateMCPGatewayValues(values); err != nil {
		t.Errorf("blank oauthDispatcherUrl must be allowed when derivable: %v", err)
	}
}

func TestValidateMCPGatewayValues_StateRedirectionExplicitNoDispatcherRejected(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "")
	values := map[string]any{
		"enableAuth":              "true",
		"stateRedirectionEnabled": "true",
		"allowedRedirectUris":     []any{},
	}
	err := validateMCPGatewayValues(values)
	if err == nil {
		t.Fatal("expected error: explicit stateRedirectionEnabled with no derivable dispatcher URL must fail closed")
	}
	if !strings.Contains(err.Error(), "oauthDispatcherUrl") {
		t.Errorf("error must be actionable about the dispatcher URL: %v", err)
	}
}

func TestValidateMCPGatewayValues_StateRedirectionAuthOffNoDispatcherAllowed(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "")
	values := map[string]any{
		"enableAuth":              "false",
		"defaultUserIdentity":     "alice",
		"stateRedirectionEnabled": "true",
	}
	if err := validateMCPGatewayValues(values); err != nil {
		t.Errorf("auth-off gateway must not be blocked by a stale stateRedirectionEnabled: %v", err)
	}
}

func TestValidateMCPGatewayValues_StateRedirectionWithAuthOffAllowed(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"enableAuth":              "false",
		"defaultUserIdentity":     "alice",
		"stateRedirectionEnabled": "true",
		"oauthDispatcherUrl":      "https://api.example.com/api/v1/auth/callback",
	}
	if err := validateMCPGatewayValues(values); err != nil {
		t.Errorf("state redirection with auth off must be ignored, not rejected: %v", err)
	}
}

func TestValidateMCPGatewayValues_StateRedirectionHappyPath(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"enableAuth":              "true",
		"stateRedirectionEnabled": "true",
		"oauthDispatcherUrl":      "https://api.example.com/api/v1/auth/callback",
		"allowedRedirectUris":     []any{},
	}
	if err := validateMCPGatewayValues(values); err != nil {
		t.Errorf("validateMCPGatewayValues state-redirection happy-path: %v", err)
	}
}

func TestValidateMCPGatewayValues_StateRedirectionNativeBoolRejected(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"enableAuth":              "true",
		"stateRedirectionEnabled": true,
		"allowedRedirectUris":     []any{},
	}
	err := validateMCPGatewayValues(values)
	if err == nil {
		t.Fatal("expected error: native bool stateRedirectionEnabled must be rejected (select widget expects string)")
	}
	if !strings.Contains(err.Error(), "must be a string") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateMCPGatewayValues_MalformedDispatcherURLRejectedWithoutFlag(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"enableAuth":          "true",
		"oauthDispatcherUrl":  "htps://typo.example/cb",
		"allowedRedirectUris": []any{},
	}
	err := validateMCPGatewayValues(values)
	if err == nil {
		t.Fatal("expected error: malformed oauthDispatcherUrl must be rejected even without stateRedirectionEnabled")
	}
	if !strings.Contains(err.Error(), "oauthDispatcherUrl") {
		t.Errorf("error must name oauthDispatcherUrl: %v", err)
	}
}

func TestValidateMCPGatewayValues_ValidDispatcherURLWithoutFlagAllowed(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"enableAuth":          "true",
		"oauthDispatcherUrl":  "https://api.example.com/api/v1/auth/callback",
		"allowedRedirectUris": []any{},
	}
	if err := validateMCPGatewayValues(values); err != nil {
		t.Errorf("well-formed dispatcher URL without flag must be allowed: %v", err)
	}
}

func TestValidateMCPGatewayValues_StdioAllowsDefaultIdentityWithAuth(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"enableAuth":          "true",
		"transport":           "stdio",
		"defaultUserIdentity": "alice",
	}
	if err := validateMCPGatewayValues(values); err != nil {
		t.Errorf("stdio + enableAuth + defaultUserIdentity should be allowed: %v", err)
	}
}

func TestValidateMCPGatewayValues_NativeBoolEnableAuth(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"enableAuth":          true,
		"defaultUserIdentity": "alice",
	}
	err := validateMCPGatewayValues(values)
	if err == nil {
		t.Fatal("expected error: native bool enableAuth must be rejected (select widget expects string)")
	}
	if !strings.Contains(err.Error(), "must be a string") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestValidateMCPGatewayValues_EmptyAllowedUrisWithAuthIsAllowed(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"enableAuth":          "true",
		"allowedRedirectUris": []any{},
	}
	if err := validateMCPGatewayValues(values); err != nil {
		t.Errorf("empty allowedRedirectUris with enableAuth should not be a plan-time error (runtime logs a WARN instead): %v", err)
	}
}

func TestValidateGatewayValuesForDeploy_MessageIsTheUIContract(t *testing.T) {
	verr, ok := errors.AsType[*ValidationError](validateGatewayValuesForDeploy(map[string]any{"broker_password": "<<__SAM_REQUIRED__>>"}))
	if !ok {
		t.Fatal("expected *ValidationError")
	}
	if verr.Message != "entrypoint cannot be deployed until all required fields are provided" {
		t.Errorf("Message = %q; the Builder UI matches the previous text", verr.Message)
	}
}

func TestValidateGatewayValuesForDeploy(t *testing.T) {
	t.Parallel()

	t.Run("event mesh placeholders rejected per-field", func(t *testing.T) {
		t.Parallel()
		values := map[string]any{
			"broker_connection_type": "custom",
			"broker_url":             "<<__SAM_REQUIRED__>>",
			"broker_vpn":             "<<__SAM_REQUIRED__>>",
			"broker_username":        "<<__SAM_REQUIRED__>>",
			"broker_password":        "real-secret",
		}
		err := validateGatewayValuesForDeploy(values)
		verr, ok := errors.AsType[*ValidationError](err)
		if !ok {
			t.Fatalf("expected *ValidationError, got %T (%v)", err, err)
		}
		for _, field := range []string{"broker_url", "broker_vpn", "broker_username"} {
			if len(verr.Details[field]) == 0 {
				t.Errorf("expected validationDetails[%q], got %+v", field, verr.Details)
			}
		}
		if _, has := verr.Details["broker_password"]; has {
			t.Errorf("a filled field must not appear in details, got %+v", verr.Details)
		}
	})

	t.Run("fully filled bag passes", func(t *testing.T) {
		t.Parallel()
		values := map[string]any{
			"broker_url":      "tcps://broker:55443",
			"broker_vpn":      "default",
			"broker_username": "solace-cloud-client",
			"broker_password": "hunter2",
		}
		if err := validateGatewayValuesForDeploy(values); err != nil {
			t.Errorf("filled bag should deploy cleanly, got %v", err)
		}
	})

	t.Run("nested placeholder reported by path", func(t *testing.T) {
		t.Parallel()
		values := map[string]any{
			"auth": map[string]any{"token": "<<__SAM_SECRET__>>"},
		}
		err := validateGatewayValuesForDeploy(values)
		verr, ok := errors.AsType[*ValidationError](err)
		if !ok {
			t.Fatalf("expected *ValidationError, got %T (%v)", err, err)
		}
		if len(verr.Details["auth.token"]) == 0 {
			t.Errorf("expected nested field path auth.token, got %+v", verr.Details)
		}
	})

	t.Run("empty bag passes", func(t *testing.T) {
		t.Parallel()
		if err := validateGatewayValuesForDeploy(map[string]any{}); err != nil {
			t.Errorf("empty bag has no placeholders, got %v", err)
		}
	})
}

func TestValidateGatewayReadyForDeploy_EventMeshBrokerPlaceholders(t *testing.T) {
	const ph = "<<__SAM_REQUIRED__>>"
	eventMesh := func(values map[string]any) *store.Gateway {
		raw, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		return &store.Gateway{ID: "gw-1", Type: "event_mesh", Values: new(string(raw))}
	}
	allPlaceholders := map[string]any{"broker_url": ph, "broker_vpn": ph, "broker_username": ph, "broker_password": ph, "event_rules": []any{}}

	t.Run("dev broker deploys despite placeholders", func(t *testing.T) {
		t.Setenv("SOLACE_BROKER_URL", "")
		if err := validateGatewayReadyForDeploy(eventMesh(allPlaceholders)); err != nil {
			t.Fatalf("expected deploy to pass on the dev broker, got %v", err)
		}
	})

	t.Run("system broker still requires every field", func(t *testing.T) {
		t.Setenv("SOLACE_BROKER_URL", "tcps://broker.example.com:55443")
		verr, ok := errors.AsType[*ValidationError](validateGatewayReadyForDeploy(eventMesh(allPlaceholders)))
		if !ok {
			t.Fatal("expected *ValidationError")
		}
		for _, field := range []string{"broker_url", "broker_vpn", "broker_username", "broker_password"} {
			if len(verr.Details[field]) == 0 {
				t.Errorf("missing validationDetails[%q]: %+v", field, verr.Details)
			}
		}
	})

	t.Run("real password with placeholder coordinates keeps blocking", func(t *testing.T) {
		t.Setenv("SOLACE_BROKER_URL", "")
		overwritten := map[string]any{"broker_url": ph, "broker_vpn": ph, "broker_username": ph, "broker_password": "real-secret"}
		verr, ok := errors.AsType[*ValidationError](validateGatewayReadyForDeploy(eventMesh(overwritten)))
		if !ok || len(verr.Details["broker_url"]) == 0 {
			t.Fatalf("expected broker_url to block deploy, got %v", verr)
		}
	})

	t.Run("a real broker_vpn alone still deploys on the dev broker", func(t *testing.T) {
		t.Setenv("SOLACE_BROKER_URL", "")
		withVPN := map[string]any{"broker_url": ph, "broker_vpn": "default", "broker_username": ph, "broker_password": ph}
		if err := validateGatewayReadyForDeploy(eventMesh(withVPN)); err != nil {
			t.Fatalf("expected deploy to pass on the dev broker, got %v", err)
		}
	})

	t.Run("whitespace broker_url is treated like an empty one", func(t *testing.T) {
		t.Setenv("SOLACE_BROKER_URL", "")
		blank := map[string]any{"broker_url": "   ", "broker_vpn": ph, "broker_username": ph, "broker_password": ph}
		if err := validateGatewayReadyForDeploy(eventMesh(blank)); err != nil {
			t.Fatalf("expected deploy to pass on the dev broker, got %v", err)
		}
	})

	t.Run("remote broker url keeps its password placeholder blocking", func(t *testing.T) {
		t.Setenv("SOLACE_BROKER_URL", "")
		remote := map[string]any{"broker_url": "tcps://remote.example.com:55443", "broker_vpn": "v", "broker_username": "u", "broker_password": ph}
		verr, ok := errors.AsType[*ValidationError](validateGatewayReadyForDeploy(eventMesh(remote)))
		if !ok || len(verr.Details["broker_password"]) == 0 {
			t.Fatalf("expected broker_password to block deploy, got %v", verr)
		}
	})

	t.Run("non-broker placeholders still block on the dev broker", func(t *testing.T) {
		t.Setenv("SOLACE_BROKER_URL", "")
		withSecret := maps.Clone(allPlaceholders)
		withSecret["default_user_identity"] = ph
		verr, ok := errors.AsType[*ValidationError](validateGatewayReadyForDeploy(eventMesh(withSecret)))
		if !ok || len(verr.Details["default_user_identity"]) == 0 {
			t.Fatalf("expected default_user_identity to block deploy, got %v", verr)
		}
		if len(verr.Details["broker_url"]) != 0 {
			t.Errorf("dev-broker placeholders must not be reported: %+v", verr.Details)
		}
	})
}

func TestValidateWhatsAppValues_DisableFileUploads(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"phone_number_id": "1234567890",
		"access_token":    "EAAtoken",
		"app_secret":      "appsecret",
		"verify_token":    "verifytoken",
	}
	for _, v := range []any{true, false} {
		values["disable_file_uploads"] = v
		if err := validateWhatsAppValues(values); err != nil {
			t.Errorf("disable_file_uploads = %v should pass, got %v", v, err)
		}
	}
	values["disable_file_uploads"] = "yes please"
	err := validateWhatsAppValues(values)
	verr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError for a non-boolean toggle, got %T (%v)", err, err)
	}
	if _, has := verr.Details["disable_file_uploads"]; !has {
		t.Errorf("no validationDetails entry for disable_file_uploads, got %v", verr.Details)
	}
}

func waAccessBase() map[string]any {
	return map[string]any{
		"phone_number_id": "1234567890",
		"access_token":    "EAAtoken",
		"app_secret":      "appsecret",
		"verify_token":    "verifytoken",
	}
}

func TestValidateWhatsAppValues_UploadToggleMatchesTheAdapter(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		value   any
		wantErr bool
	}{
		{value: true}, {value: false},
		{value: "true"}, {value: "false"}, {value: " TRUE "},
		{value: nil, wantErr: true},
		{value: "yes please", wantErr: true},
		{value: 1, wantErr: true},
	} {
		values := waAccessBase()
		values["disable_file_uploads"] = tt.value
		err := validateWhatsAppValues(values)
		if tt.wantErr && err == nil {
			t.Errorf("disable_file_uploads = %#v was accepted; the adapter refuses it at startup", tt.value)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("disable_file_uploads = %#v should pass, got %v", tt.value, err)
		}
	}
}

func TestValidateWhatsAppValues_CollectsEveryFieldError(t *testing.T) {
	t.Parallel()
	values := waAccessBase()
	values["phone_number_id"] = ""
	values["disable_file_uploads"] = "yes please"
	values["run_as"] = []any{"ops"}

	err := validateWhatsAppValues(values)
	verr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T (%v)", err, err)
	}
	for _, field := range []string{"phone_number_id", "disable_file_uploads", "run_as"} {
		if len(verr.Details[field]) == 0 {
			t.Errorf("no validationDetails entry for %s; every field error is reported in one pass, got %v", field, verr.Details)
		}
	}
}

func TestValidateWhatsAppValues_RunAsMustBeAString(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		value   any
		wantErr bool
	}{
		{name: "a name", value: "ops"},
		{name: "an already-prefixed name", value: "system:ops"},
		{name: "an explicit clear", value: ""},
		{name: "a list is refused", value: []any{"ops"}, wantErr: true},
		{name: "a map is refused", value: map[string]any{"name": "ops"}, wantErr: true},
		{name: "a null is refused", value: nil, wantErr: true},
		{name: "a bool is refused", value: true, wantErr: true},
		{name: "a number is refused", value: 12345, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			values := waAccessBase()
			values["run_as"] = tt.value
			err := validateWhatsAppValues(values)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("run_as = %#v was accepted; it would deploy as no run_as", tt.value)
				}
				verr, ok := err.(*ValidationError)
				if !ok {
					t.Fatalf("expected *ValidationError, got %T (%v)", err, err)
				}
				if len(verr.Details["run_as"]) == 0 {
					t.Errorf("no validationDetails entry for run_as, got %v", verr.Details)
				}
				return
			}
			if err != nil {
				t.Errorf("run_as = %#v should pass, got %v", tt.value, err)
			}
		})
	}
}

func TestValidateWhatsAppValues_IdentityKeys(t *testing.T) {
	t.Parallel()

	t.Run("the supported lookup key passes", func(t *testing.T) {
		values := waAccessBase()
		values["identity_lookup_key"] = "phone"
		values["identity_claim_key"] = "phone_number"
		if err := validateWhatsAppValues(values); err != nil {
			t.Errorf("the documented configuration was refused: %v", err)
		}
	})

	t.Run("absent keys pass and take the defaults", func(t *testing.T) {
		if err := validateWhatsAppValues(waAccessBase()); err != nil {
			t.Errorf("an entrypoint configuring neither key was refused: %v", err)
		}
	})

	t.Run("an unsupported lookup key is refused", func(t *testing.T) {
		values := waAccessBase()
		values["identity_lookup_key"] = "email"
		verr, ok := validateWhatsAppValues(values).(*ValidationError)
		if !ok {
			t.Fatal("an unsupported lookup key was accepted")
		}
		if _, has := verr.Details["identity_lookup_key"]; !has {
			t.Errorf("no validationDetails entry for identity_lookup_key, got %v", verr.Details)
		}
	})

	t.Run("a phone lookup against a custom claim is refused", func(t *testing.T) {
		values := waAccessBase()
		values["identity_lookup_key"] = "phone"
		values["identity_claim_key"] = "extension_deadbeef_waPhone"
		verr, ok := validateWhatsAppValues(values).(*ValidationError)
		if !ok {
			t.Fatal("a phone lookup against a custom claim was accepted; it can never match")
		}
		if _, has := verr.Details["identity_claim_key"]; !has {
			t.Errorf("no validationDetails entry for identity_claim_key, got %v", verr.Details)
		}
	})

	t.Run("a username lookup against the phone claim is refused", func(t *testing.T) {
		for _, spelling := range []string{ClaimKeyPhoneNumber, "Phone_Number", "PHONE_NUMBER"} {
			values := waAccessBase()
			values["identity_lookup_key"] = "wa_username"
			values["identity_claim_key"] = spelling
			verr, ok := validateWhatsAppValues(values).(*ValidationError)
			if !ok {
				t.Fatalf("a wa_username lookup against %q was accepted; a handle spelled from somebody else's digits would match their claim", spelling)
			}
			if _, has := verr.Details["identity_claim_key"]; !has {
				t.Errorf("%s: no validationDetails entry for identity_claim_key, got %v", spelling, verr.Details)
			}
		}
	})

	t.Run("a phone lookup accepts the phone claim in any case", func(t *testing.T) {
		values := waAccessBase()
		values["identity_lookup_key"] = "phone"
		values["identity_claim_key"] = "Phone_Number"
		if err := validateWhatsAppValues(values); err != nil {
			t.Fatalf("a phone lookup against Phone_Number was refused: %v", err)
		}
	})

	t.Run("a composite identity key is refused", func(t *testing.T) {
		for _, field := range []string{"identity_lookup_key", "identity_claim_key"} {
			for name, bad := range map[string]any{"list": []any{"phone"}, "null": nil, "number": 42} {
				values := waAccessBase()
				values[field] = bad
				verr, ok := validateWhatsAppValues(values).(*ValidationError)
				if !ok {
					t.Errorf("%s/%s was accepted; the adapter will refuse to start on it", field, name)
					continue
				}
				if _, has := verr.Details[field]; !has {
					t.Errorf("%s/%s: no validationDetails entry, got %v", field, name, verr.Details)
				}
			}
		}
	})

	t.Run("a phone lookup with no claim key passes", func(t *testing.T) {
		values := waAccessBase()
		values["identity_lookup_key"] = "phone"
		if err := validateWhatsAppValues(values); err != nil {
			t.Errorf("a phone lookup taking the default claim was refused: %v", err)
		}
	})

	t.Run("wa_username with a claim passes", func(t *testing.T) {
		values := waAccessBase()
		values["identity_lookup_key"] = "wa_username"
		values["identity_claim_key"] = "extension_deadbeef_waUsername"
		if err := validateWhatsAppValues(values); err != nil {
			t.Errorf("a username lookup naming its claim was refused: %v", err)
		}
	})

	t.Run("wa_username without a claim is refused", func(t *testing.T) {
		values := waAccessBase()
		values["identity_lookup_key"] = "wa_username"
		verr, ok := validateWhatsAppValues(values).(*ValidationError)
		if !ok {
			t.Fatal("a username lookup with no claim was accepted; every sender would be refused at runtime")
		}
		if _, has := verr.Details["identity_claim_key"]; !has {
			t.Errorf("no validationDetails entry for identity_claim_key, got %v", verr.Details)
		}
	})

	t.Run("an identity key alongside run_as is refused", func(t *testing.T) {
		for _, field := range []string{"identity_lookup_key", "identity_claim_key"} {
			values := waAccessBase()
			values["run_as"] = "ops"
			values[field] = map[string]string{"identity_lookup_key": "phone", "identity_claim_key": "phone_number"}[field]

			verr, ok := validateWhatsAppValues(values).(*ValidationError)
			if !ok {
				t.Errorf("%s alongside run_as was accepted; one of the two settings is silently ignored", field)
				continue
			}
			if _, has := verr.Details[field]; !has {
				t.Errorf("no validationDetails entry for %s, got %v", field, verr.Details)
			}
		}
	})

	t.Run("a blank identity key alongside run_as passes", func(t *testing.T) {
		values := waAccessBase()
		values["run_as"] = "ops"
		values["identity_lookup_key"] = ""
		values["identity_claim_key"] = ""
		if err := validateWhatsAppValues(values); err != nil {
			t.Errorf("blank identity keys alongside run_as were refused: %v", err)
		}
	})
}

func TestValidateWhatsAppValues_BareUICreateIsAccepted(t *testing.T) {
	t.Parallel()

	values := map[string]any{
		"phone_number_id": "100000000000001",
		"access_token":    "e2e-access-token",
		"app_secret":      "e2e-app-secret",
		"verify_token":    "e2e-verify-token",
	}
	for name, field := range whatsappFieldMap(t) {
		if def, has := field["default"]; has {
			values[name] = def
		}
	}

	if err := validateWhatsAppValues(values); err != nil {
		t.Fatalf("the create form's own default state was refused: %v\nvalues: %v", err, values)
	}
}
