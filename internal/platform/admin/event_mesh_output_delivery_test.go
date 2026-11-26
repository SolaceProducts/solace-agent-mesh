// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventmesh"
)

func ruleWithSuccessOutput(output map[string]any) map[string]any {
	output["enabled"] = true
	output["topic"] = "orders/reply"
	return map[string]any{
		"name":           "orders",
		"subscriptions":  []any{map[string]any{"topic": "orders/in/>"}},
		"targetAgent":    "agent1",
		"promptTemplate": "Process {payload}",
		"successOutput":  output,
	}
}

func ruleWithErrorOutput(output map[string]any) map[string]any {
	output["enabled"] = true
	output["topic"] = "orders/failed"
	return map[string]any{
		"name":           "orders",
		"subscriptions":  []any{map[string]any{"topic": "orders/in/>"}},
		"targetAgent":    "agent1",
		"promptTemplate": "Process {payload}",
		"errorOutput":    output,
	}
}

func translatedGuaranteedHeaders() map[string]any {
	return map[string]any{
		"solace_dmqEligible":          true,
		"solace_timeToLive":           30000,
		"solace_priority":             7,
		"solace_applicationMessageId": "static:lt-1",
		"solace_correlationId":        "template:{{text://user_data.forward_context:corr}}",
	}
}

func translatedGuaranteedUserProperties() map[string]any {
	return map[string]any{
		"source":  "static:sam",
		"attempt": 2,
		"orderId": "template:{{text://input.payload:orderId}}",
	}
}

func guaranteedOutput() map[string]any {
	return map[string]any{
		"deliveryMode": "guaranteed",
		"headers": map[string]any{
			"solace_dmqEligible":          true,
			"solace_timeToLive":           30000,
			"solace_priority":             7,
			"solace_applicationMessageId": "lt-1",
			"solace_correlationId":        "{{text://user_data.forward_context:corr}}",
		},
		"userProperties": map[string]any{
			"source":  "sam",
			"attempt": 2,
			"orderId": "{{text://input.payload:orderId}}",
		},
	}
}

func TestValidateEventMeshValues_OutputDelivery(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		output map[string]any
		errMsg string
	}{
		{name: "guaranteed with headers and user properties", output: guaranteedOutput()},
		{name: "no delivery settings", output: map[string]any{}},
		{name: "template for a typed header is checked at publish time", output: map[string]any{
			"headers": map[string]any{"solace_priority": "{{text://input.payload:p}}"},
		}},
		{name: "unknown delivery mode", output: map[string]any{"deliveryMode": "persistent"}, errMsg: "deliveryMode"},
		{name: "DMQ eligible on a direct output", output: map[string]any{
			"headers": map[string]any{"solace_dmqEligible": true},
		}, errMsg: `event_rules[0].successOutput.headers.solace_dmqEligible: requires deliveryMode "guaranteed"`},
		{name: "time-to-live on an explicit direct output", output: map[string]any{
			"deliveryMode": "direct",
			"headers":      map[string]any{"solace_timeToLive": 5000},
		}, errMsg: `headers.solace_timeToLive: requires deliveryMode "guaranteed"`},
		{name: "is-reply is unsupported", output: map[string]any{
			"deliveryMode": "guaranteed",
			"headers":      map[string]any{"solace_isReply": true},
		}, errMsg: "solace_isReply is not supported"},
		{name: "sender timestamp is unsupported", output: map[string]any{
			"headers": map[string]any{"solace_senderTimestamp": 1},
		}, errMsg: "solace_senderTimestamp is not supported"},
		{name: "template with an unknown expression source", output: map[string]any{
			"headers": map[string]any{"solace_correlationId": "{{text://bogus:x}}"},
		}, errMsg: "headers.solace_correlationId: unknown expression source"},
		{name: "user property template with an unknown expression source", output: map[string]any{
			"userProperties": map[string]any{"ref": "{{text://bogus:x}}"},
		}, errMsg: "userProperties: ref: unknown expression source"},
		{name: "task_response template is accepted", output: map[string]any{
			"headers":        map[string]any{"solace_applicationMessageId": "{{text://task_response:text}}"},
			"userProperties": map[string]any{"reply": "{{text://task_response:text}}"},
		}},
		{name: "unknown header", output: map[string]any{
			"headers": map[string]any{"priority": 1},
		}, errMsg: `unknown header "priority"`},
		{name: "priority literal out of range", output: map[string]any{
			"headers": map[string]any{"solace_priority": 256},
		}, errMsg: "between 0 and 255"},
		{name: "headers is not a map", output: map[string]any{"headers": "x"}, errMsg: "successOutput.headers: must be an object"},
		{name: "reserved user property", output: map[string]any{
			"userProperties": map[string]any{"solace.messaging.message.priority": 9},
		}, errMsg: "reserved"},
		{name: "nested user property", output: map[string]any{
			"userProperties": map[string]any{"x": []any{1}},
		}, errMsg: "x: must be a string, number or boolean"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateEventMeshValues(withRules(ruleWithSuccessOutput(tc.output)))
			if tc.errMsg == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.errMsg) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.errMsg)
			}
		})
	}
}

func TestTranslateEventRules_OutputDeliveryReachesRuntime(t *testing.T) {
	t.Parallel()
	handlers, outputs := translateEventRules([]any{ruleWithSuccessOutput(guaranteedOutput())})
	success := outputs[0]

	if success["delivery_mode"] != "guaranteed" {
		t.Errorf("delivery_mode = %v", success["delivery_mode"])
	}
	wantHeaders := translatedGuaranteedHeaders()
	if !reflect.DeepEqual(success["headers"], wantHeaders) {
		t.Errorf("headers = %v, want %v", success["headers"], wantHeaders)
	}
	wantProps := translatedGuaranteedUserProperties()
	if !reflect.DeepEqual(success["user_properties"], wantProps) {
		t.Errorf("user_properties = %v, want %v", success["user_properties"], wantProps)
	}

	cfg, err := eventmesh.ParseConfig(map[string]any{
		"event_handlers":  toAnySlice(handlers),
		"output_handlers": toAnySlice(outputs),
	}, true)
	if err != nil {
		t.Fatalf("gateway rejected the translated output: %v", err)
	}
	parsed := cfg.OutputHandlers[cfg.EventHandlers[0].OnSuccess]
	if parsed.DeliveryMode != "guaranteed" || len(parsed.Headers) != 5 || len(parsed.UserProperties) != 3 {
		t.Fatalf("runtime parsed %+v, want guaranteed with 5 headers and 3 user properties", parsed)
	}
}

func TestValidateEventMeshValues_ErrorOutputDelivery(t *testing.T) {
	t.Parallel()
	if err := validateEventMeshValues(withRules(ruleWithErrorOutput(guaranteedOutput()))); err != nil {
		t.Fatalf("a guaranteed error output must validate: %v", err)
	}

	err := validateEventMeshValues(withRules(ruleWithErrorOutput(map[string]any{
		"headers": map[string]any{"solace_dmqEligible": true},
	})))
	want := `event_rules[0].errorOutput.headers.solace_dmqEligible: requires deliveryMode "guaranteed"`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want it to contain %q", err, want)
	}
}

func TestTranslateEventRules_ErrorOutputDeliveryReachesRuntime(t *testing.T) {
	t.Parallel()
	_, outputs := translateEventRules([]any{ruleWithErrorOutput(guaranteedOutput())})
	var errorHandler map[string]any
	for _, output := range outputs {
		if output["name"] == "orders_error" {
			errorHandler = output
		}
	}
	if errorHandler == nil {
		t.Fatalf("no orders_error output handler in %v", outputs)
	}

	if errorHandler["delivery_mode"] != "guaranteed" {
		t.Errorf("delivery_mode = %v", errorHandler["delivery_mode"])
	}
	if want := translatedGuaranteedHeaders(); !reflect.DeepEqual(errorHandler["headers"], want) {
		t.Errorf("headers = %v, want %v", errorHandler["headers"], want)
	}
	if want := translatedGuaranteedUserProperties(); !reflect.DeepEqual(errorHandler["user_properties"], want) {
		t.Errorf("user_properties = %v, want %v", errorHandler["user_properties"], want)
	}
}

func TestTranslateEventRules_NoDeliverySettingsEmitsNoNewKeys(t *testing.T) {
	t.Parallel()
	_, outputs := translateEventRules([]any{ruleWithSuccessOutput(map[string]any{})})
	for _, key := range []string{"delivery_mode", "headers", "user_properties"} {
		if _, ok := outputs[0][key]; ok {
			t.Errorf("output handler has %q for an output that set nothing new: %v", key, outputs[0])
		}
	}
}
