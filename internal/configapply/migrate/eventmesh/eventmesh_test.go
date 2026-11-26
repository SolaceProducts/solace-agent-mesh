// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"slices"
	"strings"
	"testing"
)

func firstRule(t *testing.T, values map[string]any) map[string]any {
	t.Helper()
	rules, ok := values["event_rules"].([]any)
	if !ok || len(rules) == 0 {
		t.Fatalf("expected event_rules, got %v", values["event_rules"])
	}
	r, ok := rules[0].(map[string]any)
	if !ok {
		t.Fatalf("event_rules[0] not a map: %T", rules[0])
	}
	return r
}

func TestMigrateValues_ExistingQueueNameRoundTrips(t *testing.T) {
	out, warnings := MigrateValues(map[string]any{
		"event_handlers": []any{
			map[string]any{
				"name":                "orders",
				"existing_queue_name": "preprov/q/orders",
				"target_agent_name":   "Agent",
			},
		},
	})
	for _, w := range warnings {
		if strings.Contains(w, "existing_queue_name") {
			t.Errorf("existing_queue_name must not surface as a dropped-key warning; got: %s", w)
		}
	}
	r := firstRule(t, out)
	if r["existingQueueName"] != "preprov/q/orders" {
		t.Fatalf("existingQueueName = %v, want preprov/q/orders", r["existingQueueName"])
	}
	if _, present := r["subscriptions"]; present {
		t.Errorf("subscriptions must not be synthesised when existing_queue_name is the only ingress source; got %v", r["subscriptions"])
	}
}

func TestMigrateValues_DefaultBrokerAgentTargetWithOutputs(t *testing.T) {
	out, warnings := MigrateValues(map[string]any{
		"event_handlers": []any{
			map[string]any{
				"name":              "h",
				"subscriptions":     []any{map[string]any{"topic": "a/>", "qos": 1}},
				"input_expression":  "template:Do: {{json://input.payload}}",
				"target_agent_name": "Agent",
				"on_success":        "ok",
			},
		},
		"output_handlers": []any{
			map[string]any{"name": "ok", "topic_expression": "static:a/done", "payload_expression": "task_response:text"},
		},
	})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none for a broker-less legacy config", warnings)
	}
	if _, ok := out["broker_connection_type"]; ok {
		t.Fatalf("broker_connection_type must no longer be emitted, got %v", out["broker_connection_type"])
	}
	if _, ok := out["broker_password"]; ok {
		t.Fatalf("broker_password must not be synthesised for a broker-less config, got %v", out["broker_password"])
	}
	r := firstRule(t, out)
	if r["targetAgent"] != "Agent" {
		t.Fatalf("targetAgent = %v", r["targetAgent"])
	}
	if r["promptTemplate"] != "Do: {payload}" {
		t.Fatalf("promptTemplate = %q, want %q", r["promptTemplate"], "Do: {payload}")
	}
	so, _ := r["successOutput"].(map[string]any)
	if so["topic"] != "a/done" || so["topicType"] != "static" || so["responseType"] != "text" || so["enabled"] != true {
		t.Fatalf("successOutput = %v", so)
	}
}

func TestMigrateValues_CustomBrokerAndWorkflowTarget(t *testing.T) {
	out, _ := MigrateValues(map[string]any{
		"event_mesh_broker_config": map[string]any{
			"broker_url":      "tcps://b:55443",
			"broker_vpn":      "prod",
			"broker_username": "u",
		},
		"event_handlers": []any{
			map[string]any{
				"name":                 "h",
				"subscriptions":        []any{map[string]any{"topic": "s/>", "qos": 1}},
				"input_expression":     "input.payload:field",
				"target_workflow_name": "WF",
			},
		},
	})
	if _, ok := out["broker_connection_type"]; ok {
		t.Fatalf("broker_connection_type must no longer be emitted, got %v", out["broker_connection_type"])
	}
	if out["broker_url"] != "tcps://b:55443" || out["broker_vpn"] != "prod" || out["broker_username"] != "u" {
		t.Fatalf("explicit broker not migrated to flat keys: %v", out)
	}
	r := firstRule(t, out)
	if r["targetWorkflowName"] != "WF" {
		t.Fatalf("targetWorkflowName = %v", r["targetWorkflowName"])
	}
	if r["inputExpression"] != "input.payload:field" {
		t.Fatalf("inputExpression = %v", r["inputExpression"])
	}
	if _, hasPT := r["promptTemplate"]; hasPT {
		t.Fatal("raw input_expression must not become a promptTemplate")
	}
}

func TestMigrateValues_WorkflowTemplateInputKeptVerbatim(t *testing.T) {
	const input = "template:Temperature: {{json://input.payload:}}"
	out, _ := MigrateValues(map[string]any{
		"event_handlers": []any{
			map[string]any{
				"name":                 "h",
				"target_workflow_name": "W",
				"input_expression":     input,
			},
		},
	})
	r := firstRule(t, out)
	if r["inputExpression"] != input {
		t.Fatalf("inputExpression = %v, want %q", r["inputExpression"], input)
	}
	if pt, hasPT := r["promptTemplate"]; hasPT {
		t.Fatalf("workflow target must not recover a promptTemplate, got %v", pt)
	}
}

func TestMigrateValues_SAMBrokerWithExplicitPassword(t *testing.T) {
	out, warnings := MigrateValues(map[string]any{
		"event_mesh_broker_config": map[string]any{
			"broker_password": "<<__SAM_REQUIRED__>>",
		},
		"event_handlers": []any{
			map[string]any{"name": "h", "subscriptions": []any{map[string]any{"topic": "a/>"}}, "target_agent_name": "Agent"},
		},
	})
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if _, ok := out["broker_connection_type"]; ok {
		t.Fatalf("broker_connection_type must no longer be emitted, got %v", out["broker_connection_type"])
	}
	if out["broker_password"] != "<<__SAM_REQUIRED__>>" {
		t.Fatalf("broker_password = %v, want it carried through", out["broker_password"])
	}
	if _, hasURL := out["broker_url"]; hasURL {
		t.Fatalf("a password-only broker block must not carry a broker_url: %v", out)
	}
}

func TestMigrateValues_GatewayIDDropped(t *testing.T) {
	_, warnings := MigrateValues(map[string]any{
		"gateway_id": "abc",
		"event_handlers": []any{
			map[string]any{"name": "h", "target_agent_name": "A"},
		},
	})
	found := false
	for _, w := range warnings {
		if w == `event_mesh: legacy "gateway_id" dropped — the platform now assigns gateway IDs` {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected gateway_id drop warning, got %v", warnings)
	}
}

func TestMigrateValues_WarnsOnHandlerKeysItDrops(t *testing.T) {
	out, warnings := MigrateValues(map[string]any{
		"event_handlers": []any{
			map[string]any{
				"name":                         "h",
				"targetWorkflowName":           "W",
				"target_agent_name_expression": "input.payload:agent",
				"run_as":                       "svc-user",
			},
		},
	})
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{
		`"targetWorkflowName" is not an event_handlers field; use target_workflow_name`,
		`"target_agent_name_expression" dropped`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, `"target_agent_name_expression" is not an event_handlers field`) {
		t.Errorf("a dynamic-target key must get only the dynamic-routing warning:\n%s", joined)
	}
	if strings.Contains(joined, "run_as") {
		t.Errorf("run_as migrates and must not warn:\n%s", joined)
	}
	if got := firstRule(t, out)["runAs"]; got != "svc-user" {
		t.Errorf("runAs = %v, want svc-user", got)
	}
}

func TestDroppedHandlerKeys_Suggestions(t *testing.T) {
	got := DroppedHandlerKeys(map[string]any{
		"name":               "h",
		"targetAgent":        "A",
		"target":             "A",
		"promptTemplate":     "x",
		"messageFormat":      "json",
		"successOutput":      map[string]any{},
		"errorOutput":        map[string]any{},
		"targetWorkflowName": "W",
		"inputExpression":    "input.payload:",
		"bogus":              1,
	})
	want := []DroppedHandlerKey{
		{Key: "bogus"},
		{Key: "errorOutput", Suggestion: "on_error (naming an output_handlers entry)"},
		{Key: "inputExpression", Suggestion: "input_expression"},
		{Key: "messageFormat", Suggestion: "payload_format"},
		{Key: "promptTemplate", Suggestion: `input_expression ("template:" + sacexpr, e.g. "template:Summarize {{json://input.payload}}")`},
		{Key: "successOutput", Suggestion: "on_success (naming an output_handlers entry)"},
		{Key: "target", Suggestion: "target_agent_name or target_workflow_name"},
		{Key: "targetAgent", Suggestion: "target_agent_name"},
		{Key: "targetWorkflowName", Suggestion: "target_workflow_name"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("DroppedHandlerKeys = %+v, want %+v", got, want)
	}
}

func TestDroppedHandlerKey_Describe(t *testing.T) {
	for _, tc := range []struct {
		in   DroppedHandlerKey
		want string
	}{
		{DroppedHandlerKey{Key: "bogus"}, `"bogus" is not an event_handlers field`},
		{DroppedHandlerKey{Key: "targetAgent", Suggestion: "target_agent_name"}, `"targetAgent" is not an event_handlers field; use target_agent_name`},
	} {
		if got := tc.in.Describe(); got != tc.want {
			t.Errorf("%+v.Describe() = %q, want %q", tc.in, got, tc.want)
		}
	}
}

var migratedKeyRuleField = map[string]string{
	"name":                     "name",
	"subscriptions":            "subscriptions",
	"existing_queue_name":      "existingQueueName",
	"payload_format":           "messageFormat",
	"payload_encoding":         "payloadEncoding",
	"event_filter":             "eventFilter",
	"target_agent_name":        "targetAgent",
	"target_workflow_name":     "targetWorkflowName",
	"input_expression":         "inputExpression",
	"default_user_identity":    "defaultUserIdentity",
	"user_identity_expression": "userIdentityExpression",
	"run_as":                   "runAs",
	"forward_context":          "forwardContext",
	"structured_invocation":    "structuredInvocation",
	"acknowledgment_policy":    "acknowledgmentPolicy",
	"artifact_processing":      "artifactProcessing",
	"on_success":               "successOutput",
	"on_error":                 "errorOutput",
}

func fullyPopulatedHandler(targetKey, target string) map[string]any {
	return map[string]any{
		"name":                     "h",
		"subscriptions":            []any{map[string]any{"topic": "a/>", "qos": 1}},
		"existing_queue_name":      "preprov/q/h",
		"payload_format":           "json",
		"payload_encoding":         "utf-8",
		"event_filter":             map[string]any{"function": "not_op", "params": map[string]any{"positional": []any{false}}},
		targetKey:                  target,
		"input_expression":         "input.payload:",
		"default_user_identity":    "svc-user",
		"user_identity_expression": "input.user_properties:u",
		"run_as":                   "svc",
		"forward_context":          map[string]any{"k": "v"},
		"structured_invocation":    map[string]any{"input_schema": map[string]any{"type": "object"}},
		"acknowledgment_policy": map[string]any{
			"mode":            "on_completion",
			"timeout_seconds": 30,
			"on_failure":      map[string]any{"action": "nack", "nack_outcome": "rejected"},
		},
		"artifact_processing": map[string]any{"extract_artifacts_expression": "input.payload:files"},
		"on_success":          "ok",
		"on_error":            "fail",
	}
}

func TestMigrateValues_CarriesEveryMigratedHandlerKey(t *testing.T) {
	type handlerCase struct {
		name        string
		handler     map[string]any
		targetField string
		target      string
		otherField  string
	}
	cases := []handlerCase{
		{"workflow target", fullyPopulatedHandler("target_workflow_name", "W"), "targetWorkflowName", "W", "targetAgent"},
		{"agent target", fullyPopulatedHandler("target_agent_name", "A"), "targetAgent", "A", "targetWorkflowName"},
	}

	for k := range migratedHandlerKeys {
		if _, ok := migratedKeyRuleField[k]; !ok {
			t.Errorf("migratedHandlerKeys has %q but migratedKeyRuleField does not name its rule field", k)
		}
		if !slices.ContainsFunc(cases, func(c handlerCase) bool {
			_, ok := c.handler[k]
			return ok
		}) {
			t.Errorf("migratedHandlerKeys has %q but no fixture handler sets it", k)
		}
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, warnings := MigrateValues(map[string]any{
				"event_handlers": []any{tc.handler},
				"output_handlers": []any{
					map[string]any{"name": "ok", "topic_expression": "static:a/done", "payload_expression": "task_response:text"},
					map[string]any{"name": "fail", "topic_expression": "static:a/err", "payload_expression": "task_response:a2a_task_response.error"},
				},
			})
			for _, w := range warnings {
				if strings.Contains(w, "is not an event_handlers field") {
					t.Errorf("migrated key reported as dropped: %s", w)
				}
			}
			rule := firstRule(t, out)
			for k := range tc.handler {
				field := migratedKeyRuleField[k]
				if _, ok := rule[field]; !ok {
					t.Errorf("handler key %q did not reach rule field %q; rule = %v", k, field, rule)
				}
			}
			if got := rule[tc.targetField]; got != tc.target {
				t.Errorf("%s = %v, want %q", tc.targetField, got, tc.target)
			}
			if got, ok := rule[tc.otherField]; ok {
				t.Errorf("%s = %v, want absent: the handler sets only %s", tc.otherField, got, tc.targetField)
			}
		})
	}
}
