// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventmesh"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sacexpr"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func TestTranslatePromptTemplate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		template string
		format   string
		want     string
	}{
		{
			name:     "empty template defaults to json payload",
			template: "",
			format:   "json",
			want:     "template:{{json://input.payload}}",
		},
		{
			name:     "payload placeholder json",
			template: "Process: {payload}",
			format:   "json",
			want:     "template:Process: {{json://input.payload}}",
		},
		{
			name:     "payload placeholder text",
			template: "Process: {payload}",
			format:   "text",
			want:     "template:Process: {{text://input.payload}}",
		},
		{
			name:     "topic placeholder",
			template: "Event from {topic}: {payload}",
			format:   "json",
			want:     "template:Event from {{text://input.topic}}: {{json://input.payload}}",
		},
		{
			name:     "nested field accessor",
			template: "Order ID: {payload.order.id}",
			format:   "json",
			want:     "template:Order ID: {{text://input.payload:order.id}}",
		},
		{
			name:     "mixed placeholders",
			template: "Topic {topic} has order {payload.orderId} with data {payload}",
			format:   "json",
			want:     "template:Topic {{text://input.topic}} has order {{text://input.payload:orderId}} with data {{json://input.payload}}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := translatePromptTemplate(tt.template, tt.format)
			if got != tt.want {
				t.Errorf("translatePromptTemplate(%q, %q)\n  got  %q\n  want %q", tt.template, tt.format, got, tt.want)
			}
		})
	}
}

func TestTranslateResponseType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		respType string
		custom   string
		want     string
	}{
		{"text", "", "task_response:text"},
		{"full", "", "task_response:a2a_task_response"},
		{"structured", "", "task_response:structured_output"},
		{"error", "", "task_response:a2a_task_response.error"},
		{"custom", "my_custom:expr", "my_custom:expr"},
		{"custom", "", "task_response:text"},
		{"unknown", "", "task_response:text"},
		{"full_json", "", "task_response:a2a_task_response"},
		{"error_json", "", "task_response:a2a_task_response.error"},
	}
	for _, tt := range tests {
		t.Run(tt.respType, func(t *testing.T) {
			t.Parallel()
			got := translateResponseType(tt.respType, tt.custom)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTranslateEventRules_FullFeatureFidelity(t *testing.T) {
	rules := []any{
		map[string]any{
			"name": "full_feature",
			"subscriptions": []any{
				map[string]any{"topic": "events/orders/>", "qos": 1},
			},
			"messageFormat":          "json",
			"payloadEncoding":        "utf-8",
			"targetWorkflowName":     "OrderWorkflow",
			"inputExpression":        "input.payload",
			"defaultUserIdentity":    "order_event_user",
			"userIdentityExpression": "input.user_properties.requester",
			"forwardContext": map[string]any{
				"trace_id": "input.user_properties.trace_id",
			},
			"structuredInvocation": map[string]any{
				"outputSchema": map[string]any{
					"type":     "object",
					"required": []any{"id"},
				},
			},
			"acknowledgmentPolicy": map[string]any{
				"mode":           "on_completion",
				"timeoutSeconds": 600,
				"onFailure": map[string]any{
					"action":      "nack",
					"nackOutcome": "failed",
				},
			},
			"artifactProcessing": map[string]any{
				"extractExpression": "input.payload.attachments",
				"artifactDefinition": map[string]any{
					"filename":        "input.iteration.name",
					"content":         "input.iteration.bytes",
					"mimeType":        "input.iteration.contentType",
					"contentEncoding": "base64",
				},
			},
			"successOutput": map[string]any{
				"enabled":                   true,
				"topic":                     "events/orders/enriched",
				"topicType":                 "static",
				"format":                    "json",
				"payloadEncoding":           "utf-8",
				"responseType":              "structured",
				"outputSchema":              map[string]any{"type": "object"},
				"onValidationError":         "log",
				"maxFileSizeForBase64Bytes": 1048576,
				"outputTransforms": []any{
					map[string]any{
						"type":              "copy",
						"source_expression": "input.payload.foo",
						"dest_expression":   "input.payload.bar",
					},
				},
			},
			"errorOutput": map[string]any{
				"enabled":          true,
				"topic":            "events/orders/errors",
				"topicType":        "dynamic",
				"responseType":     "custom",
				"customExpression": "task_response:a2a_task_response.error.message",
			},
		},
	}

	handlers, outputs := translateEventRules(rules)
	if len(handlers) != 1 {
		t.Fatalf("event_handlers len = %d; want 1", len(handlers))
	}
	if len(outputs) != 2 {
		t.Fatalf("output_handlers len = %d; want 2", len(outputs))
	}

	eh := handlers[0]
	if eh["target_workflow_name"] != "OrderWorkflow" {
		t.Errorf("target_workflow_name = %v", eh["target_workflow_name"])
	}
	if eh["input_expression"] != "input.payload" {
		t.Errorf("input_expression = %v; want raw passthrough for workflow target", eh["input_expression"])
	}
	if eh["payload_format"] != "json" || eh["payload_encoding"] != "utf-8" {
		t.Errorf("payload format/encoding lost: %v / %v", eh["payload_format"], eh["payload_encoding"])
	}
	if eh["default_user_identity"] != "order_event_user" {
		t.Errorf("default_user_identity = %v; want override, not event-mesh-gw-xyz", eh["default_user_identity"])
	}
	if eh["user_identity_expression"] != "input.user_properties.requester" {
		t.Errorf("user_identity_expression = %v", eh["user_identity_expression"])
	}
	if _, ok := eh["forward_context"]; !ok {
		t.Errorf("forward_context lost")
	}
	si, ok := eh["structured_invocation"].(map[string]any)
	if !ok || si["output_schema"] == nil {
		t.Errorf("structured_invocation.output_schema lost: %v", eh["structured_invocation"])
	}
	ack, _ := eh["acknowledgment_policy"].(map[string]any)
	if ack == nil || ack["mode"] != "on_completion" || ack["timeout_seconds"] != 600 {
		t.Errorf("acknowledgment_policy wrong: %v", ack)
	}
	of, _ := ack["on_failure"].(map[string]any)
	if of["action"] != "nack" || of["nack_outcome"] != "failed" {
		t.Errorf("on_failure wrong: %v", of)
	}
	ap, _ := eh["artifact_processing"].(map[string]any)
	if ap == nil || ap["extract_artifacts_expression"] != "input.payload.attachments" {
		t.Errorf("artifact_processing.extract_artifacts_expression lost: %v", ap)
	}
	apDef, _ := ap["artifact_definition"].(map[string]any)
	if apDef["mime_type"] != "input.iteration.contentType" || apDef["content_encoding"] != "base64" {
		t.Errorf("artifact_definition mime_type/content_encoding lost: %v", apDef)
	}

	if eh["on_success"] == nil || eh["on_error"] == nil {
		t.Errorf("on_success/on_error refs missing on handler: %v", eh)
	}
	outByName := make(map[string]map[string]any, len(outputs))
	for _, o := range outputs {
		outByName[o["name"].(string)] = o
	}
	successName, _ := eh["on_success"].(string)
	success := outByName[successName]
	if success == nil {
		t.Fatalf("success output handler %q not emitted", successName)
	}
	if success["topic_expression"] != "static:events/orders/enriched" {
		t.Errorf("success topic_expression = %v", success["topic_expression"])
	}
	if success["payload_expression"] != "task_response:structured_output" {
		t.Errorf("success payload_expression = %v; want structured_output", success["payload_expression"])
	}
	if success["payload_format"] != "json" || success["payload_encoding"] != "utf-8" {
		t.Errorf("success format/encoding lost: %v / %v", success["payload_format"], success["payload_encoding"])
	}
	if success["on_validation_error"] != "log" {
		t.Errorf("success on_validation_error = %v", success["on_validation_error"])
	}
	if success["max_file_size_for_base64_bytes"] != 1048576 {
		t.Errorf("success max_file_size_for_base64_bytes = %v", success["max_file_size_for_base64_bytes"])
	}
	if _, ok := success["output_schema"]; !ok {
		t.Errorf("success output_schema lost")
	}
	if _, ok := success["output_transforms"].([]any); !ok {
		t.Errorf("success output_transforms lost: %v", success["output_transforms"])
	}

	errorName, _ := eh["on_error"].(string)
	errOut := outByName[errorName]
	if errOut == nil {
		t.Fatalf("error output handler %q not emitted", errorName)
	}
	if errOut["topic_expression"] != "template:events/orders/errors" {
		t.Errorf("error topic_expression = %v; want template: prefix from topicType=dynamic", errOut["topic_expression"])
	}
	if errOut["payload_expression"] != "task_response:a2a_task_response.error.message" {
		t.Errorf("error payload_expression = %v; want customExpression", errOut["payload_expression"])
	}
}

func TestTranslateTopic(t *testing.T) {
	t.Parallel()
	if got := translateTopic("events/orders", "static"); got != "static:events/orders" {
		t.Errorf("static: got %q", got)
	}
	if got := translateTopic("events/{{id}}", "dynamic"); got != "template:events/{{id}}" {
		t.Errorf("dynamic: got %q", got)
	}
}

func TestTranslateEventRules_BasicAgent(t *testing.T) {
	t.Parallel()
	rules := []any{
		map[string]any{
			"name": "order_rule",
			"subscriptions": []any{
				map[string]any{"topic": "events/order/created"},
			},
			"targetAgent":    "OrderAgent",
			"promptTemplate": "Process order: {payload}",
			"messageFormat":  "json",
		},
	}

	handlers, outputs := translateEventRules(rules)
	if len(handlers) != 1 {
		t.Fatalf("expected 1 handler, got %d", len(handlers))
	}
	if len(outputs) != 0 {
		t.Fatalf("expected 0 output handlers, got %d", len(outputs))
	}

	h := handlers[0]
	if h["name"] != "order_rule" {
		t.Errorf("name = %v", h["name"])
	}
	if h["target_agent_name"] != "OrderAgent" {
		t.Errorf("target_agent_name = %v", h["target_agent_name"])
	}
	if h["payload_format"] != "json" {
		t.Errorf("payload_format = %v", h["payload_format"])
	}
	if _, ok := h["default_user_identity"]; ok {
		t.Errorf("default_user_identity should be unset, got %v", h["default_user_identity"])
	}
	if ra, ok := h["run_as"]; !ok || ra != "" {
		t.Errorf("run_as = %v (ok=%v), want stamped blank (default system user)", h["run_as"], ok)
	}
	expr, _ := h["input_expression"].(string)
	if expr != "template:Process order: {{json://input.payload}}" {
		t.Errorf("input_expression = %q", expr)
	}
}

func TestTranslateEventRules_ExistingQueueName(t *testing.T) {
	t.Parallel()
	handlers, _ := translateEventRules([]any{
		map[string]any{
			"name":              "orders",
			"existingQueueName": "preprov/q/orders",
			"targetAgent":       "OrderAgent",
			"promptTemplate":    "Process {payload}",
		},
	})
	if len(handlers) != 1 {
		t.Fatalf("expected 1 handler, got %d", len(handlers))
	}
	h := handlers[0]
	if h["existing_queue_name"] != "preprov/q/orders" {
		t.Errorf("existing_queue_name = %v, want preprov/q/orders", h["existing_queue_name"])
	}
	if _, present := h["subscriptions"]; present {
		t.Errorf("subscriptions must not be emitted when the clean-spec rule has existingQueueName only; got %v", h["subscriptions"])
	}
}

func TestTranslateEventRules_EventFilterReachesTheGateway(t *testing.T) {
	t.Parallel()
	var filter any
	if err := json.Unmarshal([]byte(`{"function": "equal", "params": {"positional": ["evaluate_expression(input.payload:type)", "order.created"]}}`), &filter); err != nil {
		t.Fatalf("decode filter: %v", err)
	}
	handlers, _ := translateEventRules([]any{map[string]any{
		"name":           "orders",
		"subscriptions":  []any{map[string]any{"topic": "orders/>"}},
		"targetAgent":    "OrderAgent",
		"promptTemplate": "Process {payload}",
		"eventFilter":    filter,
	}})
	cfg, err := eventmesh.ParseConfig(map[string]any{"event_handlers": toAnySlice(handlers)}, true)
	if err != nil {
		t.Fatalf("gateway rejected the translated config: %v", err)
	}
	f := cfg.EventHandlers[0].EventFilter
	if f == nil {
		t.Fatal("eventFilter did not reach the gateway handler")
	}
	for payloadType, want := range map[string]bool{"order.created": true, "order.cancelled": false} {
		got, err := f.Match(sacexpr.NewContext(map[string]any{"type": payloadType}, "orders/eu", nil))
		if err != nil || got != want {
			t.Errorf("type %s: Match = %v, %v; want %v", payloadType, got, err, want)
		}
	}
}

func TestTranslateEventRules_NoEventFilterEmitsNone(t *testing.T) {
	t.Parallel()
	handlers, _ := translateEventRules([]any{map[string]any{
		"name":           "orders",
		"subscriptions":  []any{map[string]any{"topic": "orders/>"}},
		"targetAgent":    "OrderAgent",
		"promptTemplate": "Process {payload}",
	}})
	if _, present := handlers[0]["event_filter"]; present {
		t.Errorf("a rule without eventFilter emitted event_filter: %v", handlers[0]["event_filter"])
	}
}

type successDefaultCase struct {
	name                string
	rule                map[string]any
	successResponseType string
	want                string
}

func (tc successDefaultCase) eventRule() map[string]any {
	success := map[string]any{"enabled": true, "topic": "out"}
	if tc.successResponseType != "" {
		success["responseType"] = tc.successResponseType
	}
	rule := map[string]any{
		"name":          "r",
		"subscriptions": []any{map[string]any{"topic": "in"}},
		"successOutput": success,
		"errorOutput":   map[string]any{"enabled": true, "topic": "errors"},
	}
	for k, v := range tc.rule {
		rule[k] = v
	}
	return rule
}

func successDefaultCases() []successDefaultCase {
	schema := map[string]any{"outputSchema": map[string]any{"type": "object"}}
	return []successDefaultCase{
		{name: "agent with structured invocation", rule: map[string]any{"targetAgent": "Decider", "structuredInvocation": schema}, want: "task_response:structured_output"},
		{name: "agent with input schema only", rule: map[string]any{"targetAgent": "Decider", "structuredInvocation": map[string]any{"inputSchema": map[string]any{"type": "object"}}}, want: "task_response:structured_output"},
		{name: "agent with accept-anything output schema", rule: map[string]any{"targetAgent": "Decider", "structuredInvocation": map[string]any{"outputSchema": map[string]any{}}}, want: "task_response:structured_output"},
		{name: "default agent with structured invocation", rule: map[string]any{"structuredInvocation": schema}, want: "task_response:structured_output"},
		{name: "agent without structured invocation", rule: map[string]any{"targetAgent": "Decider"}, want: "task_response:text"},
		{name: "agent with empty structured invocation", rule: map[string]any{"targetAgent": "Decider", "structuredInvocation": map[string]any{}}, want: "task_response:text"},
		{name: "agent with null output schema", rule: map[string]any{"targetAgent": "Decider", "structuredInvocation": map[string]any{"outputSchema": nil}}, want: "task_response:text"},
		{name: "agent with null schemas", rule: map[string]any{"targetAgent": "Decider", "structuredInvocation": map[string]any{"inputSchema": nil, "outputSchema": nil}}, want: "task_response:text"},
		{name: "agent with empty-string schema", rule: map[string]any{"targetAgent": "Decider", "structuredInvocation": map[string]any{"outputSchema": ""}}, want: "task_response:text"},
		{name: "workflow with structured invocation", rule: map[string]any{"targetWorkflowName": "Classify", "structuredInvocation": schema}, want: "task_response:text"},
		{name: "explicit responseType wins", rule: map[string]any{"targetAgent": "Decider", "structuredInvocation": schema}, successResponseType: "full", want: "task_response:a2a_task_response"},
	}
}

func TestTranslateEventRules_SuccessDefaultMatchesRuntime(t *testing.T) {
	t.Parallel()
	for _, tc := range successDefaultCases() {
		if tc.successResponseType != "" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			handlers, outputs := translateEventRules([]any{tc.eventRule()})
			appConfig := map[string]any{
				"event_handlers":  toAnySlice(handlers),
				"output_handlers": toAnySlice(outputs),
			}

			cfg, err := eventmesh.ParseConfig(appConfig, true)
			if err != nil {
				t.Fatalf("gateway rejected the translated config: %v", err)
			}

			h := cfg.EventHandlers[0]
			sendsStructured := h.SendsStructuredRequest()
			readsStructured := strings.Contains(cfg.OutputHandlers[h.OnSuccess].PayloadExpression, "structured_output")
			if sendsStructured != readsStructured {
				t.Errorf("gateway sends structured request = %v, success reads structured_output = %v; they must agree", sendsStructured, readsStructured)
			}
		})
	}
}

func TestTranslateEventRules_SuccessDefaultResponseType(t *testing.T) {
	t.Parallel()
	for _, tc := range successDefaultCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, outputs := translateEventRules([]any{tc.eventRule()})

			if len(outputs) != 2 {
				t.Fatalf("expected success and error output handlers, got %d", len(outputs))
			}
			if got := outputs[0]["payload_expression"]; got != tc.want {
				t.Errorf("success payload_expression = %v, want %v", got, tc.want)
			}
			if got := outputs[1]["payload_expression"]; got != "task_response:a2a_task_response.error" {
				t.Errorf("error payload_expression = %v, want the error default", got)
			}
		})
	}
}

func TestTranslateEventRules_DefaultAgent(t *testing.T) {
	t.Parallel()
	rules := []any{
		map[string]any{
			"name": "default_rule",
			"subscriptions": []any{
				map[string]any{"topic": "events/>"},
			},
		},
	}

	handlers, _ := translateEventRules(rules)
	h := handlers[0]
	if h["target_agent_name"] != "Orchestrator" {
		t.Errorf("expected Orchestrator default, got %v", h["target_agent_name"])
	}
	if h["default_user_identity"] != nil {
		t.Errorf("expected no default_user_identity without gateway ID, got %v", h["default_user_identity"])
	}
	if ra, ok := h["run_as"]; !ok || ra != "" {
		t.Errorf("run_as = %v (ok=%v), want stamped blank (default system user)", h["run_as"], ok)
	}
}

func TestTranslateEventRules_WorkflowTarget(t *testing.T) {
	t.Parallel()
	rules := []any{
		map[string]any{
			"name":               "wf_rule",
			"subscriptions":      []any{map[string]any{"topic": "events/data"}},
			"targetWorkflowName": "DataPipeline",
		},
	}

	handlers, _ := translateEventRules(rules)
	h := handlers[0]
	if h["target_workflow_name"] != "DataPipeline" {
		t.Errorf("target_workflow_name = %v", h["target_workflow_name"])
	}
	if _, ok := h["target_agent_name"]; ok {
		t.Error("workflow target should not have target_agent_name")
	}
	if _, ok := h["input_expression"]; ok {
		t.Error("workflow target should not have input_expression")
	}
}

func TestTranslateEventRules_WorkflowTargetInputSource(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		rule     map[string]any
		wantExpr string
		wantSet  bool
	}{
		{
			name:     "promptTemplate renders like an agent target",
			rule:     map[string]any{"messageFormat": "json", "promptTemplate": "Failed order: {payload}"},
			wantExpr: "template:Failed order: {{json://input.payload}}",
			wantSet:  true,
		},
		{
			name:     "promptTemplate with text format and topic",
			rule:     map[string]any{"messageFormat": "text", "promptTemplate": "{topic}: {payload}"},
			wantExpr: "template:{{text://input.topic}}: {{text://input.payload}}",
			wantSet:  true,
		},
		{
			name:     "inputExpression wins over promptTemplate",
			rule:     map[string]any{"promptTemplate": "Failed order: {payload}", "inputExpression": "input.payload:"},
			wantExpr: "input.payload:",
			wantSet:  true,
		},
		{
			name:     "responseFormat is not appended to a workflow input",
			rule:     map[string]any{"promptTemplate": "{payload}", "responseFormat": "JSON"},
			wantExpr: "template:{{json://input.payload}}",
			wantSet:  true,
		},
		{
			name: "neither leaves the input unset",
			rule: map[string]any{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rule := map[string]any{
				"name":               "wf_rule",
				"subscriptions":      []any{map[string]any{"topic": "test/order/failed/>"}},
				"targetWorkflowName": "Echo",
			}
			for k, v := range tt.rule {
				rule[k] = v
			}

			handlers, _ := translateEventRules([]any{rule})
			got, set := handlers[0]["input_expression"]
			if set != tt.wantSet {
				t.Fatalf("input_expression set = %v (%v), want set = %v", set, got, tt.wantSet)
			}
			if tt.wantSet && got != tt.wantExpr {
				t.Errorf("input_expression = %q, want %q", got, tt.wantExpr)
			}
		})
	}
}

func TestTranslateEventRules_WithOutputHandlers(t *testing.T) {
	t.Parallel()
	rules := []any{
		map[string]any{
			"name":          "output_rule",
			"subscriptions": []any{map[string]any{"topic": "events/x"}},
			"successOutput": map[string]any{
				"enabled":      true,
				"topic":        "results/success",
				"topicType":    "static",
				"responseType": "text",
			},
			"errorOutput": map[string]any{
				"enabled":      true,
				"topic":        "results/error",
				"topicType":    "static",
				"responseType": "error_json",
			},
		},
	}

	handlers, outputs := translateEventRules(rules)
	if len(outputs) != 2 {
		t.Fatalf("expected 2 output handlers, got %d", len(outputs))
	}

	h := handlers[0]
	if h["on_success"] != "output_rule_success" {
		t.Errorf("on_success = %v", h["on_success"])
	}
	if h["on_error"] != "output_rule_error" {
		t.Errorf("on_error = %v", h["on_error"])
	}

	successOut := outputs[0]
	if successOut["name"] != "output_rule_success" {
		t.Errorf("success output name = %v", successOut["name"])
	}
	if successOut["topic_expression"] != "static:results/success" {
		t.Errorf("success topic_expression = %v", successOut["topic_expression"])
	}
	if successOut["payload_expression"] != "task_response:text" {
		t.Errorf("success payload_expression = %v", successOut["payload_expression"])
	}

	errorOut := outputs[1]
	if errorOut["name"] != "output_rule_error" {
		t.Errorf("error output name = %v", errorOut["name"])
	}
	if errorOut["payload_expression"] != "task_response:a2a_task_response.error" {
		t.Errorf("error payload_expression = %v", errorOut["payload_expression"])
	}
}

func TestTranslateEventRules_AckPolicy(t *testing.T) {
	t.Parallel()
	rules := []any{
		map[string]any{
			"name":          "ack_rule",
			"subscriptions": []any{map[string]any{"topic": "events/x"}},
			"acknowledgmentPolicy": map[string]any{
				"mode":           "on_completion",
				"timeoutSeconds": float64(120),
				"onFailure": map[string]any{
					"action":      "nack",
					"nackOutcome": "rejected",
				},
			},
		},
	}

	handlers, _ := translateEventRules(rules)
	h := handlers[0]
	ack, ok := h["acknowledgment_policy"].(map[string]any)
	if !ok {
		t.Fatal("expected acknowledgment_policy block")
	}
	if ack["mode"] != "on_completion" {
		t.Errorf("mode = %v", ack["mode"])
	}
	if ack["timeout_seconds"] != float64(120) {
		t.Errorf("timeout_seconds = %v", ack["timeout_seconds"])
	}
	onFailure, ok := ack["on_failure"].(map[string]any)
	if !ok {
		t.Fatal("expected on_failure block")
	}
	if onFailure["action"] != "nack" {
		t.Errorf("action = %v", onFailure["action"])
	}
}

func TestTranslateEventRules_OnReceiveAckOmitted(t *testing.T) {
	t.Parallel()
	rules := []any{
		map[string]any{
			"name":          "no_ack_rule",
			"subscriptions": []any{map[string]any{"topic": "events/x"}},
			"acknowledgmentPolicy": map[string]any{
				"mode": "on_receive",
			},
		},
	}

	handlers, _ := translateEventRules(rules)
	if _, ok := handlers[0]["acknowledgment_policy"]; ok {
		t.Error("on_receive ack policy should be omitted from handler")
	}
}

func TestTranslateEventRules_ResponseFormat(t *testing.T) {
	t.Parallel()
	rules := []any{
		map[string]any{
			"name":           "fmt_rule",
			"subscriptions":  []any{map[string]any{"topic": "events/x"}},
			"promptTemplate": "Do: {payload}",
			"responseFormat": "Return as JSON array",
		},
	}

	handlers, _ := translateEventRules(rules)
	expr, _ := handlers[0]["input_expression"].(string)
	expected := "template:Do: {{json://input.payload}}\n\nResponse Format:\nReturn as JSON array"
	if expr != expected {
		t.Errorf("input_expression =\n%q\nwant\n%q", expr, expected)
	}
}

func TestBuildEventMeshConfig_DefaultBrokerNeverInheritsSystemBrokerPassword(t *testing.T) {
	t.Setenv("SOLACE_BROKER_URL", "tcps://platform-broker.example.com:55443")
	t.Setenv("SOLACE_BROKER_VPN", "platform-vpn")
	t.Setenv("SOLACE_BROKER_USERNAME", "platform-user")
	t.Setenv("SOLACE_BROKER_PASSWORD", "platform-pass")
	svc := NewYAMLService("test-ns", nil)
	gw := &store.Gateway{
		Description: strPtr("test gateway for fixture purposes"),
		ID:          "gw-abc-123",
		Name:        "Test EM",
		Type:        "event_mesh",
	}
	values := map[string]any{
		"broker_connection_type": "default",
		"broker_password":        "gw-pass",
		"event_rules":            []any{},
	}

	config := svc.buildEventMeshGatewayConfig(gw, values)
	apps, _ := config["apps"].([]map[string]any)
	appConfig, _ := apps[0]["app_config"].(map[string]any)

	emBroker, _ := appConfig["event_mesh_broker_config"].(map[string]any)
	if emBroker["broker_url"] != "tcps://platform-broker.example.com:55443" {
		t.Errorf("default broker_url should come from SOLACE_BROKER_URL (system broker), got %v", emBroker["broker_url"])
	}
	if emBroker["broker_vpn"] != "platform-vpn" {
		t.Errorf("default broker_vpn should come from SOLACE_BROKER_VPN, got %v", emBroker["broker_vpn"])
	}
	if emBroker["broker_password"] == "platform-pass" {
		t.Error("default mode must NOT inherit SOLACE_BROKER_PASSWORD")
	}
	if emBroker["broker_password"] != "gw-pass" {
		t.Errorf("default broker_password should be the explicit per-gateway value, got %v", emBroker["broker_password"])
	}
}

func TestBuildEventMeshConfig_PlaceholdersKeepEmbeddedDevBrokerFallback(t *testing.T) {
	placeholderBroker := func() map[string]any {
		return map[string]any{
			"broker_url":      "<<__SAM_REQUIRED__>>",
			"broker_vpn":      "<<__SAM_REQUIRED__>>",
			"broker_username": "<<__SAM_REQUIRED__>>",
			"broker_password": "<<__SAM_REQUIRED__>>",
			"event_rules":     []any{},
		}
	}
	brokerConfig := func(values map[string]any) map[string]any {
		svc := NewYAMLService("test-ns", nil)
		gw := &store.Gateway{Description: strPtr("test gateway for fixture purposes"), ID: "gw-abc-123", Name: "Test EM", Type: "event_mesh"}
		config := svc.buildEventMeshGatewayConfig(gw, values)
		apps, _ := config["apps"].([]map[string]any)
		appConfig, _ := apps[0]["app_config"].(map[string]any)
		emBroker, _ := appConfig["event_mesh_broker_config"].(map[string]any)
		return emBroker
	}

	t.Run("no system broker selects the dev broker", func(t *testing.T) {
		t.Setenv("SOLACE_BROKER_URL", "")
		if emBroker := brokerConfig(placeholderBroker()); !samapp.IsDevMode(emBroker) {
			t.Errorf("adapter would not use the dev broker; broker config = %v", emBroker)
		}
	})

	t.Run("system broker keeps the placeholders", func(t *testing.T) {
		t.Setenv("SOLACE_BROKER_URL", "tcps://platform-broker.example.com:55443")
		emBroker := brokerConfig(placeholderBroker())
		if emBroker["broker_url"] != "<<__SAM_REQUIRED__>>" {
			t.Errorf("broker_url = %v, want the placeholder left for pre-flight to reject", emBroker["broker_url"])
		}
	})
}

func TestBuildEventMeshConfig_DefaultBroker_PerGatewayOverride(t *testing.T) {
	t.Setenv("SOLACE_BROKER_URL", "tcps://platform-broker.example.com:55443")
	t.Setenv("BROKER_URL_GW_ABC_123", "tcps://per-gateway-broker.example.com:55443")
	svc := NewYAMLService("test-ns", nil)
	gw := &store.Gateway{
		Description: strPtr("test gateway for fixture purposes"),
		ID:          "gw-abc-123",
		Name:        "Test EM",
		Type:        "event_mesh",
	}
	values := map[string]any{
		"broker_connection_type": "default",
		"event_rules":            []any{},
	}

	config := svc.buildEventMeshGatewayConfig(gw, values)
	apps, _ := config["apps"].([]map[string]any)
	appConfig, _ := apps[0]["app_config"].(map[string]any)

	emBroker, _ := appConfig["event_mesh_broker_config"].(map[string]any)
	if emBroker["broker_url"] != "tcps://per-gateway-broker.example.com:55443" {
		t.Errorf("per-gateway env var should override shared SOLACE_BROKER_URL, got %v", emBroker["broker_url"])
	}
}

func TestBuildEventMeshConfig_CustomBroker_FromValues(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", nil)
	gw := &store.Gateway{
		Description: strPtr("test gateway for fixture purposes"),
		ID:          "gw-abc-123",
		Name:        "Test EM",
		Type:        "event_mesh",
	}
	values := map[string]any{
		"broker_connection_type": "custom",
		"broker_url":             "tcps://user-broker.example.com:55443",
		"broker_vpn":             "user-vpn",
		"broker_username":        "user",
		"broker_password":        "secret",
		"event_rules":            []any{},
	}

	config := svc.buildEventMeshGatewayConfig(gw, values)
	apps, _ := config["apps"].([]map[string]any)
	appConfig, _ := apps[0]["app_config"].(map[string]any)

	emBroker, _ := appConfig["event_mesh_broker_config"].(map[string]any)
	if emBroker["broker_url"] != "tcps://user-broker.example.com:55443" {
		t.Errorf("custom broker_url should come from values, got %v", emBroker["broker_url"])
	}
	if emBroker["broker_password"] != "secret" {
		t.Errorf("custom broker_password should come from values, got %v", emBroker["broker_password"])
	}
}

func TestBuildEventMeshConfig_CustomBroker_FromEnv(t *testing.T) {
	t.Setenv("BROKER_URL_GW_ABC_123", "tcps://env-broker.example.com:55443")
	t.Setenv("BROKER_VPN_GW_ABC_123", "env-vpn")
	svc := NewYAMLService("test-ns", nil)
	gw := &store.Gateway{
		Description: strPtr("test gateway for fixture purposes"),
		ID:          "gw-abc-123",
		Name:        "Test EM",
		Type:        "event_mesh",
	}
	values := map[string]any{
		"broker_connection_type": "custom",
		"event_rules":            []any{},
	}

	config := svc.buildEventMeshGatewayConfig(gw, values)
	apps, _ := config["apps"].([]map[string]any)
	appConfig, _ := apps[0]["app_config"].(map[string]any)

	emBroker, _ := appConfig["event_mesh_broker_config"].(map[string]any)
	if emBroker["broker_url"] != "tcps://env-broker.example.com:55443" {
		t.Errorf("custom broker_url should fall back to per-gateway env var, got %v", emBroker["broker_url"])
	}
}

func TestBuildEventMeshConfig_EventHandlersWired(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", nil)
	gw := &store.Gateway{
		Description: strPtr("test gateway for fixture purposes"),
		ID:          "gw-123",
		Name:        "EM with rules",
		Type:        "event_mesh",
	}
	values := map[string]any{
		"broker_connection_type": "default",
		"event_rules": []any{
			map[string]any{
				"name":           "my_rule",
				"subscriptions":  []any{map[string]any{"topic": "events/test"}},
				"targetAgent":    "TestAgent",
				"promptTemplate": "{payload}",
				"successOutput": map[string]any{
					"enabled":      true,
					"topic":        "results/ok",
					"topicType":    "static",
					"responseType": "text",
				},
			},
		},
	}

	config := svc.buildEventMeshGatewayConfig(gw, values)
	apps, _ := config["apps"].([]map[string]any)
	appConfig, _ := apps[0]["app_config"].(map[string]any)

	handlers, _ := appConfig["event_handlers"].([]map[string]any)
	if len(handlers) != 1 {
		t.Fatalf("expected 1 event handler, got %d", len(handlers))
	}
	if handlers[0]["name"] != "my_rule" {
		t.Errorf("handler name = %v", handlers[0]["name"])
	}

	outputs, _ := appConfig["output_handlers"].([]map[string]any)
	if len(outputs) != 1 {
		t.Fatalf("expected 1 output handler, got %d", len(outputs))
	}

	b, _ := json.Marshal(appConfig)
	configStr := string(b)
	if configStr == "" {
		t.Fatal("config should not be empty")
	}
	if appConfig["gateway_type"] != "event_mesh" {
		t.Errorf("gateway_type = %v, want event_mesh", appConfig["gateway_type"])
	}
}
