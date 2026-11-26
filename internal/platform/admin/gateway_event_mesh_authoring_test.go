// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/migrate/eventmesh"
)

func rulesOf(t *testing.T, values map[string]any) []any {
	t.Helper()
	rules, ok := values["event_rules"].([]any)
	if !ok {
		t.Fatalf("expected event_rules slice, got %T (%v)", values["event_rules"], values["event_rules"])
	}
	return rules
}

func rule0(t *testing.T, values map[string]any) map[string]any {
	t.Helper()
	rules := rulesOf(t, values)
	if len(rules) == 0 {
		t.Fatal("expected at least one event_rule")
	}
	r, ok := rules[0].(map[string]any)
	if !ok {
		t.Fatalf("event_rules[0] not a map: %T", rules[0])
	}
	return r
}

func TestNormalizeEventMeshAuthoredValues_PassThroughWhenAlreadyCleanSpec(t *testing.T) {
	in := map[string]any{
		"broker_connection_type": "default",
		"event_rules": []any{
			map[string]any{"name": "r", "targetAgent": "A", "promptTemplate": "{payload}"},
		},
	}
	out, warnings := normalizeEventMeshAuthoredValues(in)
	if len(warnings) != 0 {
		t.Fatalf("clean-spec input should not warn, got %v", warnings)
	}
	if _, ok := out["event_rules"].([]any); !ok {
		t.Fatal("clean-spec event_rules dropped")
	}
}

func TestNormalizeEventMeshAuthoredValues_AgentTargetRawPayloadBecomesPromptTemplate(t *testing.T) {
	in := map[string]any{
		"event_handlers": []any{
			map[string]any{
				"name":              "order_events",
				"subscriptions":     []any{map[string]any{"topic": "orders/created/>", "qos": 1}},
				"input_expression":  "input.payload:",
				"target_agent_name": "OrderValidator",
			},
		},
	}
	out, _ := normalizeEventMeshAuthoredValues(in)
	if _, ok := out["broker_connection_type"]; ok {
		t.Fatal("broker_connection_type must no longer be emitted")
	}
	if url, _ := out["broker_url"].(string); url != "" {
		t.Fatalf("expected empty broker_url (dev fallback), got %q", url)
	}
	r := rule0(t, out)
	if r["targetAgent"] != "OrderValidator" {
		t.Fatalf("targetAgent not carried: %v", r["targetAgent"])
	}
	if pt, _ := r["promptTemplate"].(string); pt != "{payload}" {
		t.Fatalf("expected promptTemplate {payload}, got %q", pt)
	}
	if _, leftover := r["inputExpression"]; leftover {
		t.Fatal("inputExpression should be removed for the normalized agent target")
	}
}

func TestNormalizeEventMeshAuthoredValues_AgentTargetMissingExpressionGetsDefaultPrompt(t *testing.T) {
	in := map[string]any{
		"event_handlers": []any{
			map[string]any{
				"name":              "h",
				"subscriptions":     []any{map[string]any{"topic": "x/>", "qos": 0}},
				"target_agent_name": "A",
			},
		},
	}
	out, _ := normalizeEventMeshAuthoredValues(in)
	if pt, _ := rule0(t, out)["promptTemplate"].(string); pt != "{payload}" {
		t.Fatalf("expected default promptTemplate {payload}, got %q", pt)
	}
}

func TestNormalizeEventMeshAuthoredValues_TemplateExpressionRecoveredNotOverwritten(t *testing.T) {
	in := map[string]any{
		"event_handlers": []any{
			map[string]any{
				"name":              "alerts",
				"subscriptions":     []any{map[string]any{"topic": "alerts/critical/>", "qos": 0}},
				"input_expression":  "template:Analyze: {{json://input.payload}}",
				"target_agent_name": "AlertAnalyzer",
			},
		},
	}
	out, _ := normalizeEventMeshAuthoredValues(in)
	pt, _ := rule0(t, out)["promptTemplate"].(string)
	if pt != "Analyze: {payload}" {
		t.Fatalf("template input_expression should recover to a promptTemplate with {payload} placeholder, got %q", pt)
	}
}

func TestNormalizeEventMeshAuthoredValues_WorkflowTargetAndDynamicTopicSurvive(t *testing.T) {
	in := map[string]any{
		"event_handlers": []any{
			map[string]any{
				"name":                 "sensor_readings",
				"subscriptions":        []any{map[string]any{"topic": "sensors/temp/>", "qos": 1}},
				"input_expression":     "input.payload:",
				"target_workflow_name": "SensorPipeline",
				"on_success":           "sensor_result",
				"forward_context":      map[string]any{"sensor_id": "input.payload:sensor_id"},
			},
		},
		"output_handlers": []any{
			map[string]any{
				"name":               "sensor_result",
				"topic_expression":   "template:sensors/{{text://user_data.forward_context:sensor_id}}/analysis",
				"payload_expression": "task_response:text",
			},
		},
	}
	out, _ := normalizeEventMeshAuthoredValues(in)
	r := rule0(t, out)
	if r["targetWorkflowName"] != "SensorPipeline" {
		t.Fatalf("workflow target dropped: %v", r["targetWorkflowName"])
	}
	if ie, _ := r["inputExpression"].(string); ie != "input.payload:" {
		t.Fatalf("workflow inputExpression override dropped/changed: %q", ie)
	}
	if _, hasPT := r["promptTemplate"]; hasPT {
		t.Fatal("workflow target must not get a synthesized promptTemplate")
	}
	if r["forwardContext"] == nil {
		t.Fatal("forward_context dropped")
	}
	so, ok := r["successOutput"].(map[string]any)
	if !ok {
		t.Fatalf("successOutput missing: %v", r["successOutput"])
	}
	if so["topicType"] != "dynamic" {
		t.Fatalf("template: topic should map to topicType dynamic, got %v", so["topicType"])
	}
	if so["topic"] != "sensors/{{text://user_data.forward_context:sensor_id}}/analysis" {
		t.Fatalf("dynamic topic not preserved: %v", so["topic"])
	}
	if so["responseType"] != "text" {
		t.Fatalf("payload_expression task_response:text should map to responseType text, got %v", so["responseType"])
	}
}

func TestNormalizeEventMeshAuthoredValues_WorkflowTemplateInputSurvives(t *testing.T) {
	const templateInput = "template:Temperature reading: {{json://input.payload:}}"
	in := map[string]any{
		"event_handlers": []any{
			map[string]any{
				"name":                 "sensor",
				"subscriptions":        []any{map[string]any{"topic": "sensors/>"}},
				"target_workflow_name": "SensorAnalysisPipeline",
				"input_expression":     templateInput,
			},
		},
	}
	out, _ := normalizeEventMeshAuthoredValues(in)
	r := rule0(t, out)
	if r["targetWorkflowName"] != "SensorAnalysisPipeline" {
		t.Fatalf("workflow target dropped: %v", r["targetWorkflowName"])
	}
	if ie, _ := r["inputExpression"].(string); ie != templateInput {
		t.Fatalf("workflow template inputExpression = %q, want %q verbatim", ie, templateInput)
	}
	if _, hasPT := r["promptTemplate"]; hasPT {
		t.Fatalf("workflow target must not get a promptTemplate, got %v", r["promptTemplate"])
	}

	t.Setenv("SOLACE_BROKER_URL", "")
	if err := validateGatewayValues("event_mesh", out); err != nil {
		t.Fatalf("normalized workflow-template bag should validate, got %v", err)
	}

	handlers, _ := translateEventRules(rulesOf(t, out))
	if len(handlers) != 1 {
		t.Fatalf("expected 1 translated event handler, got %d", len(handlers))
	}
	if got := handlers[0]["input_expression"]; got != templateInput {
		t.Fatalf("translated input_expression = %v, want %q so the workflow receives its input at deploy", got, templateInput)
	}
}

func TestNormalizeEventMeshAuthoredValues_CustomBrokerSurvives(t *testing.T) {
	in := map[string]any{
		"event_mesh_broker_config": map[string]any{
			"broker_url":      "tcps://broker.example.com:55443",
			"broker_vpn":      "prod",
			"broker_username": "svc",
			"broker_password": "<<__SAM_REQUIRED__>>",
		},
		"event_handlers": []any{
			map[string]any{
				"name":              "h",
				"subscriptions":     []any{map[string]any{"topic": "x/>", "qos": 1}},
				"input_expression":  "input.payload:",
				"target_agent_name": "A",
			},
		},
	}
	out, _ := normalizeEventMeshAuthoredValues(in)
	if _, ok := out["broker_connection_type"]; ok {
		t.Fatal("broker_connection_type must no longer be emitted")
	}
	if out["broker_url"] != "tcps://broker.example.com:55443" {
		t.Fatalf("broker_url dropped: %v", out["broker_url"])
	}
	if out["broker_vpn"] != "prod" || out["broker_username"] != "svc" {
		t.Fatalf("explicit broker coordinates dropped: vpn=%v user=%v", out["broker_vpn"], out["broker_username"])
	}
}

func TestRejectUnsupportedEventMeshAuthoring(t *testing.T) {
	for _, key := range []string{"target_agent_name_expression", "target_workflow_name_expression"} {
		values := map[string]any{
			"event_handlers": []any{
				map[string]any{"name": "dyn", key: "input.payload:agent"},
			},
		}
		ve, ok := errors.AsType[*ValidationError](rejectUnsupportedEventMeshAuthoring(values))
		if !ok {
			t.Fatalf("%s: expected *ValidationError", key)
		}
		keyPath := "event_handlers[0]." + key
		if got := ve.Details[keyPath]; len(got) != 1 || !strings.Contains(got[0], "dynamic target routing") {
			t.Errorf("Details[%q] = %v, want exactly one entry naming dynamic target routing", keyPath, got)
		}
		if targetless, reported := ve.Details["event_handlers[0]"]; reported {
			t.Errorf("%s: dynamic-target handler also reported as having no target: %v", key, targetless)
		}
	}

	for name, values := range map[string]map[string]any{
		"fixed agent target": {"event_handlers": []any{map[string]any{"name": "h", "target_agent_name": "A"}}},
		"clean-spec":         {"event_rules": []any{map[string]any{"name": "r", "targetAgent": "A"}}},
		"empty bag":          {},
	} {
		if err := rejectUnsupportedEventMeshAuthoring(values); err != nil {
			t.Errorf("%s: expected acceptance, got %v", name, err)
		}
	}
}

func TestIsPayloadPassthrough(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"input.payload:", true},
		{"input.payload", true},
		{" input.payload: ", true},
		{"input.payload:order_id", false},
		{"template:{{json://input.payload}}", false},
		{"", false},
	} {
		if got := isPayloadPassthrough(tc.in); got != tc.want {
			t.Errorf("isPayloadPassthrough(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestRejectUnsupportedEventMeshAuthoring_MisspelledWorkflowTargetNamesTheKey(t *testing.T) {
	values := map[string]any{
		"event_handlers": []any{
			map[string]any{
				"name":               "mule_events",
				"subscriptions":      []any{map[string]any{"topic": "txn/>"}},
				"inputExpression":    "input.payload:",
				"targetWorkflowName": "MuleDetection",
			},
		},
	}
	ve, ok := errors.AsType[*ValidationError](rejectUnsupportedEventMeshAuthoring(values))
	if !ok {
		t.Fatal("expected *ValidationError")
	}
	for path, want := range map[string]string{
		"event_handlers[0].targetWorkflowName": "use target_workflow_name",
		"event_handlers[0].inputExpression":    "use input_expression",
		"event_handlers[0]":                    "set target_agent_name or target_workflow_name",
	} {
		got := ve.Details[path]
		if len(got) != 1 || !strings.Contains(got[0], want) {
			t.Errorf("Details[%q] = %v, want one entry containing %q", path, got, want)
		}
	}
	if ve.Message != invalidEventHandlersMessage {
		t.Errorf("Message = %q, want the summary %q", ve.Message, invalidEventHandlersMessage)
	}
}

func TestRejectUnsupportedEventMeshAuthoring_ReportsEveryHandler(t *testing.T) {
	values := map[string]any{
		"event_handlers": []any{
			map[string]any{"name": "nested", "target": map[string]any{"workflow": "W"}},
			map[string]any{"name": "ok", "target_workflow_name": "W", "input_expression": "input.payload:"},
			map[string]any{"name": "untargeted", "input_expression": "input.payload:"},
		},
	}
	ve, ok := errors.AsType[*ValidationError](rejectUnsupportedEventMeshAuthoring(values))
	if !ok {
		t.Fatal("expected *ValidationError")
	}
	for _, path := range []string{"event_handlers[0].target", "event_handlers[0]", "event_handlers[2]"} {
		if len(ve.Details[path]) == 0 {
			t.Errorf("missing Details[%q]; got %v", path, ve.Details)
		}
	}
	for path := range ve.Details {
		if strings.HasPrefix(path, "event_handlers[1]") {
			t.Errorf("valid handler reported: %q → %v", path, ve.Details[path])
		}
	}
}

func TestRejectUnsupportedEventMeshAuthoring_TranslatorShapedHandlerIsAccepted(t *testing.T) {
	subscriptions := []any{map[string]any{"topic": "a/>", "qos": 1}}
	eventHandlers, outputHandlers := translateEventRules([]any{
		map[string]any{
			"name":                   "workflow_rule",
			"subscriptions":          subscriptions,
			"messageFormat":          "json",
			"payloadEncoding":        "utf-8",
			"targetWorkflowName":     "W",
			"inputExpression":        "input.payload:",
			"runAs":                  "svc",
			"defaultUserIdentity":    "svc",
			"userIdentityExpression": "input.user_properties:user",
			"forwardContext":         map[string]any{"k": "input.payload:k"},
			"structuredInvocation": map[string]any{
				"inputSchema":  map[string]any{"type": "object"},
				"outputSchema": map[string]any{"type": "object"},
			},
			"acknowledgmentPolicy": map[string]any{
				"mode":           "on_completion",
				"timeoutSeconds": 30,
				"onFailure":      map[string]any{"action": "nack", "nackOutcome": "rejected"},
			},
			"artifactProcessing": map[string]any{
				"extractExpression": "input.payload:files",
				"artifactDefinition": map[string]any{
					"filename":        "f",
					"content":         "c",
					"mimeType":        "text/plain",
					"contentEncoding": "text",
				},
			},
			"successOutput": map[string]any{"enabled": true, "topic": "a/done", "topicType": "static", "responseType": "text"},
			"errorOutput":   map[string]any{"enabled": true, "topic": "a/err", "topicType": "static", "responseType": "error"},
		},
		map[string]any{
			"name":           "agent_rule",
			"subscriptions":  subscriptions,
			"targetAgent":    "A",
			"promptTemplate": "{payload}",
		},
	})
	values := map[string]any{
		"event_handlers":  toAnySlice(eventHandlers),
		"output_handlers": toAnySlice(outputHandlers),
	}

	if err := rejectUnsupportedEventMeshAuthoring(values); err != nil {
		t.Fatalf("translator-shaped handlers rejected: %v", err)
	}
	_, warnings := eventmesh.MigrateValues(values)
	for _, w := range warnings {
		if strings.Contains(w, "is not an event_handlers field") {
			t.Errorf("migrator drops a key the translator emits: %s", w)
		}
	}
}

func toAnySlice(maps []map[string]any) []any {
	out := make([]any, len(maps))
	for i, m := range maps {
		out[i] = m
	}
	return out
}
