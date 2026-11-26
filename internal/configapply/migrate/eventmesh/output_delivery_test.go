// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"reflect"
	"testing"
)

func TestMigrateValues_OutputDeliverySettings(t *testing.T) {
	out, warnings := MigrateValues(map[string]any{
		"event_handlers": []any{
			map[string]any{
				"name":              "h",
				"subscriptions":     []any{map[string]any{"topic": "a/>"}},
				"target_agent_name": "Agent",
				"on_success":        "ok",
			},
		},
		"output_handlers": []any{
			map[string]any{
				"name":               "ok",
				"topic_expression":   "static:a/done",
				"payload_expression": "task_response:text",
				"delivery_mode":      "guaranteed",
				"headers": map[string]any{
					"solace_dmqEligible":          true,
					"solace_timeToLive":           3000,
					"solace_applicationMessageId": "static:lt-1",
					"solace_correlationId":        "template:{{text://user_data.forward_context:corr}}",
					"solace_senderId":             "input.user_properties:sender",
				},
				"user_properties": map[string]any{
					"source":  "static:sam",
					"attempt": 2,
				},
			},
		},
	})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	so, _ := firstRule(t, out)["successOutput"].(map[string]any)
	if so["deliveryMode"] != "guaranteed" {
		t.Errorf("deliveryMode = %v", so["deliveryMode"])
	}
	wantHeaders := map[string]any{
		"solace_dmqEligible":          true,
		"solace_timeToLive":           3000,
		"solace_applicationMessageId": "lt-1",
		"solace_correlationId":        "{{text://user_data.forward_context:corr}}",
		"solace_senderId":             "{{text://input.user_properties:sender}}",
	}
	if !reflect.DeepEqual(so["headers"], wantHeaders) {
		t.Errorf("headers = %v, want %v", so["headers"], wantHeaders)
	}
	wantProps := map[string]any{"source": "sam", "attempt": 2}
	if !reflect.DeepEqual(so["userProperties"], wantProps) {
		t.Errorf("userProperties = %v, want %v", so["userProperties"], wantProps)
	}
}

func TestMigrateValues_WarnsOnALiteralContainingTemplateBraces(t *testing.T) {
	_, warnings := MigrateValues(map[string]any{
		"event_handlers": []any{
			map[string]any{"name": "h", "subscriptions": []any{map[string]any{"topic": "a/>"}}, "target_agent_name": "Agent", "on_success": "ok"},
		},
		"output_handlers": []any{
			map[string]any{
				"name":               "zeta",
				"topic_expression":   "static:a/done",
				"payload_expression": "task_response:text",
				"headers":            map[string]any{"solace_applicationMessageId": "static:order {{id}}"},
			},
			map[string]any{
				"name":               "alpha",
				"topic_expression":   "static:a/done",
				"payload_expression": "task_response:text",
				"headers": map[string]any{
					"solace_senderId":      "static:sam",
					"solace_correlationId": "template:corr-{{text://user_data.forward_context:id}}",
					"solace_priority":      7,
				},
				"user_properties": map[string]any{
					"ref":    "static:order {{id}}",
					"echo":   "{{text://input.payload:text}}",
					"source": "static:sam",
				},
			},
		},
	})
	want := []string{
		`event_mesh: output handler "alpha": user_properties.ref is a literal containing "{{", which the authored form reads as a template; rewrite the value without "{{"`,
		`event_mesh: output handler "zeta": headers.solace_applicationMessageId is a literal containing "{{", which the authored form reads as a template; rewrite the value without "{{"`,
	}
	if !reflect.DeepEqual(warnings, want) {
		t.Fatalf("warnings = %q, want %q", warnings, want)
	}
}

func TestMigrateValues_OutputWithoutDeliverySettingsAddsNothing(t *testing.T) {
	out, _ := MigrateValues(map[string]any{
		"event_handlers": []any{
			map[string]any{"name": "h", "subscriptions": []any{map[string]any{"topic": "a/>"}}, "target_agent_name": "Agent", "on_success": "ok"},
		},
		"output_handlers": []any{
			map[string]any{"name": "ok", "topic_expression": "static:a/done", "payload_expression": "task_response:text"},
		},
	})
	so, _ := firstRule(t, out)["successOutput"].(map[string]any)
	for _, key := range []string{"deliveryMode", "headers", "userProperties"} {
		if _, ok := so[key]; ok {
			t.Errorf("successOutput has %q for a handler that set nothing new: %v", key, so)
		}
	}
}
