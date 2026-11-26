// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventmesh"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const builderGatewayYAML = `
apps:
  - name: order_events
    app_config:
      gateway_id: order_events
      agent_card:
        description: "Routes inbound order events to the fulfilment agent."
      event_mesh_broker_config:
        broker_url: tcp://broker.example.com:55555
        broker_vpn: default
        broker_username: gw
        broker_password: "<<__SAM_REQUIRED__>>"
      event_handlers:
        - name: on_order
          target_agent_name: Fulfilment
          input_expression: "input.payload:text"
          subscriptions:
            - topic: orders/created
`

func TestDesiredGateway_ConvertsBuilderArtifact(t *testing.T) {
	t.Parallel()
	got, err := desiredGateway("order_events", builderGatewayYAML)
	if err != nil {
		t.Fatalf("desiredGateway: %v", err)
	}
	create, ok := got.create.(entplatform.CreateGatewayRequest)
	if !ok {
		t.Fatalf("create is %T, want CreateGatewayRequest", got.create)
	}
	if create.Name != "OrderEvents" {
		t.Errorf("Name = %q, want the PascalCase form the manifest references", create.Name)
	}
	if create.Type != "event_mesh" {
		t.Errorf("Type = %q", create.Type)
	}
	if create.Deploy {
		t.Error("Deploy must be false — saving is not deploying")
	}
	if create.Description != "Routes inbound order events to the fulfilment agent." {
		t.Errorf("Description = %q", create.Description)
	}
}

func TestDesiredGateway_ValuesAreNormalizedBeforePlanning(t *testing.T) {
	t.Parallel()
	got, err := desiredGateway("order_events", builderGatewayYAML)
	if err != nil {
		t.Fatalf("desiredGateway: %v", err)
	}
	values, ok := got.create.(entplatform.CreateGatewayRequest).Values.(map[string]any)
	if !ok {
		t.Fatalf("Values is %T, want map", got.create.(entplatform.CreateGatewayRequest).Values)
	}
	if _, raw := values["event_handlers"]; raw {
		t.Error("event_handlers survived; the AWE bag was not migrated before planning")
	}
	if _, migrated := values["event_rules"]; !migrated {
		t.Errorf("no event_rules in %v; expected the clean-spec shape", values)
	}
	if values["broker_url"] != "tcp://broker.example.com:55555" {
		t.Errorf("broker_url = %v, want the hoisted flat key", values["broker_url"])
	}
	if _, nested := values["event_mesh_broker_config"]; nested {
		t.Error("event_mesh_broker_config survived; it should have been hoisted")
	}
}

func TestDesiredGateway_NormalizationIsIdempotent(t *testing.T) {
	t.Parallel()
	got, err := desiredGateway("order_events", builderGatewayYAML)
	if err != nil {
		t.Fatalf("desiredGateway: %v", err)
	}
	once := got.create.(entplatform.CreateGatewayRequest).Values.(map[string]any)
	twice, _ := entplatform.NormalizeEventMeshAuthoredValues(once)
	if len(twice) != len(once) {
		t.Errorf("second normalization changed the bag: %d keys then %d", len(once), len(twice))
	}
	if _, ok := twice["event_rules"]; !ok {
		t.Error("event_rules lost on the second pass")
	}
}

func TestDesiredGateway_SynthesizesShortDescription(t *testing.T) {
	t.Parallel()
	const y = `
apps:
  - name: tiny
    app_config:
      gateway_id: tiny
      description: "short"
      event_handlers:
        - name: h
          target_agent_name: A
          subscriptions:
            - topic: t
`
	got, err := desiredGateway("tiny", y)
	if err != nil {
		t.Fatalf("desiredGateway: %v", err)
	}
	if desc := got.create.(entplatform.CreateGatewayRequest).Description; len(desc) < minGatewayDescription {
		t.Errorf("Description = %q, still under the platform minimum", desc)
	}
}

func TestDesiredGateway_RejectsNonEventMesh(t *testing.T) {
	t.Parallel()
	const y = `
apps:
  - name: web
    app_config:
      gateway_id: web
      description: "An HTTP gateway the Builder should not author."
`
	if _, err := desiredGateway("web", y); err == nil {
		t.Fatal("expected an error: only event mesh entrypoints are Builder-authored")
	}
}

func TestToPascalCase(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"order_events":    "OrderEvents",
		"order-events":    "OrderEvents",
		"OrderEvents":     "OrderEvents",
		"order events":    "OrderEvents",
		"orderevents":     "Orderevents",
		"":                "",
		"multi_word_name": "MultiWordName",
	} {
		if got := toPascalCase(in); got != want {
			t.Errorf("toPascalCase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDesiredGateway_ManifestNameOutranksAppName(t *testing.T) {
	t.Parallel()
	const yaml = `
apps:
  - name: ent_orders_gateway
    app_config:
      agent_card:
        description: "Routes inbound order events to the pet workflow."
      event_handlers:
        - name: on_order
          target_agent_name: EntPetFlow
          subscriptions:
            - topic: ent/orders/>
`
	for _, tc := range []struct {
		name      string
		content   string
		component string
		want      string
	}{
		{"app name differs from the manifest name", yaml, "EntOrders", "EntOrders"},
		{"snake_case manifest name is normalized", yaml, "ent_orders", "EntOrders"},
		{"a blank manifest name falls back to the app name", yaml, "  ", "EntOrdersGateway"},
		{
			"agent_name outranks the manifest name",
			strings.Replace(yaml, "    app_config:\n", "    app_config:\n      agent_name: pet_agent_gw\n", 1),
			"EntOrders", "PetAgentGw",
		},
		{"app name is the fallback without a manifest name", yaml, "", "EntOrdersGateway"},
		{
			"an explicit gateway_id still wins",
			strings.Replace(yaml, "    app_config:\n", "    app_config:\n      gateway_id: pet_orders\n", 1),
			"EntOrders", "PetOrders",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := desiredGateway(tc.component, tc.content)
			if err != nil {
				t.Fatalf("desiredGateway: %v", err)
			}
			create, ok := got.create.(entplatform.CreateGatewayRequest)
			if !ok {
				t.Fatalf("create is %T, want CreateGatewayRequest", got.create)
			}
			if create.Name != tc.want {
				t.Errorf("Name = %q, want %q", create.Name, tc.want)
			}
		})
	}
}

func TestDesiredGateway_WarnsWhenTheStoredNameIsNotTheManifestName(t *testing.T) {
	t.Parallel()
	const yaml = `
apps:
  - name: ent_orders_gateway
    app_config:
%s      agent_card:
        description: "Routes inbound order events to the pet workflow."
      event_handlers:
        - name: on_order
          target_agent_name: EntPetFlow
          subscriptions:
            - topic: ent/orders/>
`
	for _, tc := range []struct {
		name     string
		extra    string
		wantWarn bool
		wantName string
	}{
		{"no gateway_id", "", false, "EntOrders"},
		{"gateway_id that normalizes to the manifest name", "      gateway_id: ent_orders\n", false, "EntOrders"},
		{"gateway_id that differs", "      gateway_id: orders_bridge\n", true, "OrdersBridge"},
		{"blank gateway_id is skipped", "      gateway_id: \"  \"\n", false, "EntOrders"},
		{"agent_name that differs", "      agent_name: orders_bridge\n", true, "OrdersBridge"},
		{"gateway_id outranks a differing agent_name", "      gateway_id: ent_orders\n      agent_name: orders_bridge\n", false, "EntOrders"},
		{"gateway_id that differs only in case", "      gateway_id: ENT_ORDERS\n", false, "ENTORDERS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := desiredGateway("EntOrders", strings.Replace(yaml, "%s", tc.extra, 1))
			if err != nil {
				t.Fatalf("desiredGateway: %v", err)
			}
			if name := got.create.(entplatform.CreateGatewayRequest).Name; name != tc.wantName {
				t.Errorf("Name = %q, want %q", name, tc.wantName)
			}
			var warned bool
			for _, w := range got.warnings {
				warned = warned || strings.Contains(w, "is stored as")
			}
			if warned != tc.wantWarn {
				t.Errorf("stored-name warning = %v, want %v (warnings: %v)", warned, tc.wantWarn, got.warnings)
			}
		})
	}
}

func TestDesiredGateway_NoWarningWithoutAnExplicitName(t *testing.T) {
	t.Parallel()
	for _, component := range []string{"ent_orders", "ent-orders", "ENT_ORDERS", "my_em_gateway", "EntOrders"} {
		t.Run(component, func(t *testing.T) {
			got, err := desiredGateway(component, `
apps:
  - name: ent_orders_gateway
    app_config:
      agent_card:
        description: "Routes inbound order events to the pet workflow."
      event_handlers:
        - name: on_order
          target_agent_name: EntPetFlow
          subscriptions:
            - topic: ent/orders/>
`)
			if err != nil {
				t.Fatalf("desiredGateway: %v", err)
			}
			for _, w := range got.warnings {
				if strings.Contains(w, "is stored as") {
					t.Errorf("unexpected warning for manifest name %q: %s", component, w)
				}
			}
		})
	}
}

func TestDesiredGateway_NoWarningWithoutAManifestName(t *testing.T) {
	t.Parallel()
	got, err := desiredGateway("  ", `
apps:
  - name: ent_orders_gateway
    app_config:
      gateway_id: orders_bridge
      agent_card:
        description: "Routes inbound order events to the pet workflow."
      event_handlers:
        - name: on_order
          target_agent_name: EntPetFlow
          subscriptions:
            - topic: ent/orders/>
`)
	if err != nil {
		t.Fatalf("desiredGateway: %v", err)
	}
	if name := got.create.(entplatform.CreateGatewayRequest).Name; name != "OrdersBridge" {
		t.Errorf("Name = %q, want OrdersBridge", name)
	}
	for _, w := range got.warnings {
		if strings.Contains(w, "is stored as") {
			t.Errorf("a stored-name warning has nothing to compare against: %s", w)
		}
	}
}

const ackGatewayYAML = `
apps:
  - name: ent_ack
    app_config:
      agent_card:
        description: "Routes inbound order events to the pet workflow."
%s      event_handlers:
        - name: first
          target_agent_name: A
          input_expression: "input.payload:"
          subscriptions:
            - topic: ent/first/>
        - name: second
          target_agent_name: B
          input_expression: "input.payload:"
%s          subscriptions:
            - topic: ent/second/>
`

const entrypointAckPolicy = `      acknowledgment_policy:
        mode: on_completion
        timeout_seconds: 120
        on_failure:
          action: nack
          nack_outcome: rejected
`

const entrypointAckPolicyWithOtherFailureHandling = `      acknowledgment_policy:
        mode: on_completion
        timeout_seconds: 45
        on_failure:
          action: ack
          nack_outcome: failed
`

func ackGateway(entrypointLines, secondHandlerLines string) string {
	return strings.Replace(strings.Replace(ackGatewayYAML, "%s", entrypointLines, 1), "%s", secondHandlerLines, 1)
}

func gatewayRules(t *testing.T, d desiredComponent) []map[string]any {
	t.Helper()
	var decoded struct {
		Values struct {
			EventRules []map[string]any `json:"event_rules"`
		} `json:"values"`
	}
	if err := json.Unmarshal([]byte(mustJSON(t, d.create)), &decoded); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	return decoded.Values.EventRules
}

func storedPolicy(rule map[string]any) map[string]any {
	policy, _ := rule["acknowledgmentPolicy"].(map[string]any)
	return policy
}

func TestDesiredGateway_EntrypointAckPolicyIsTheDefaultForEveryHandler(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		handler2   string
		wantSecond map[string]any
	}{
		{"every handler inherits the entrypoint policy", "", map[string]any{"mode": "on_completion", "timeoutSeconds": float64(120), "onFailure": map[string]any{"action": "nack", "nackOutcome": "rejected"}}},
		{"a handler's own policy replaces it outright", "          acknowledgment_policy:\n            timeout_seconds: 10\n", map[string]any{"timeoutSeconds": float64(10)}},
		{"a handler's own on_receive wins", "          acknowledgment_policy:\n            mode: on_receive\n", map[string]any{"mode": "on_receive"}},
		{"an empty handler mapping is its own policy and resets to on_receive", "          acknowledgment_policy: {}\n", map[string]any(nil)},
		{"a null handler policy states none", "          acknowledgment_policy:\n", map[string]any{"mode": "on_completion", "timeoutSeconds": float64(120), "onFailure": map[string]any{"action": "nack", "nackOutcome": "rejected"}}},
		{"a string handler policy states none", "          acknowledgment_policy: on_receive\n", map[string]any{"mode": "on_completion", "timeoutSeconds": float64(120), "onFailure": map[string]any{"action": "nack", "nackOutcome": "rejected"}}},
		{"a list handler policy states none", "          acknowledgment_policy: [on_receive]\n", map[string]any{"mode": "on_completion", "timeoutSeconds": float64(120), "onFailure": map[string]any{"action": "nack", "nackOutcome": "rejected"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := desiredGateway("EntAck", ackGateway(entrypointAckPolicy, tc.handler2))
			if err != nil {
				t.Fatalf("desiredGateway: %v", err)
			}
			for _, w := range got.warnings {
				t.Errorf("unexpected warning: %s", w)
			}
			rules := gatewayRules(t, got)
			if len(rules) != 2 {
				t.Fatalf("rules = %d, want 2", len(rules))
			}
			if mode := storedPolicy(rules[0])["mode"]; mode != "on_completion" {
				t.Errorf("first rule policy = %v, want the entrypoint's", storedPolicy(rules[0]))
			}
			if got := storedPolicy(rules[1]); !reflect.DeepEqual(got, tc.wantSecond) {
				t.Errorf("second rule policy = %v, want %v", got, tc.wantSecond)
			}
		})
	}
}

func TestDesiredGateway_NoEntrypointAckPolicyLeavesTheRulesAlone(t *testing.T) {
	t.Parallel()
	got, err := desiredGateway("EntAck", ackGateway("", ""))
	if err != nil {
		t.Fatalf("desiredGateway: %v", err)
	}
	for i, rule := range gatewayRules(t, got) {
		if _, has := rule["acknowledgmentPolicy"]; has {
			t.Errorf("rule %d carries a policy the artifact did not state", i)
		}
	}
}

func TestDesiredGateway_EntrypointAckPolicyTheRuntimeIgnoresIsReported(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		lines string
		want  string
	}{
		{"a mode name instead of a mapping", "      acknowledgment_policy: on_completion\n", "must be a mapping"},
		{"a number", "      acknowledgment_policy: 42\n", "must be a mapping"},
		{"a list", "      acknowledgment_policy: [on_completion]\n", "must be a mapping"},
		{"an empty mapping", "      acknowledgment_policy: {}\n", "has none of mode"},
		{"a mapping with only unknown keys", "      acknowledgment_policy:\n        ack_mode: on_completion\n", "has none of mode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := desiredGateway("EntAck", ackGateway(tc.lines, ""))
			if err != nil {
				t.Fatalf("desiredGateway: %v", err)
			}
			var reported bool
			for _, w := range got.warnings {
				reported = reported || strings.Contains(w, tc.want)
				if strings.Contains(w, "no platform equivalent") {
					t.Errorf("the key is consumed, not unknown: %s", w)
				}
			}
			if !reported {
				t.Errorf("warnings = %v, want one containing %q", got.warnings, tc.want)
			}
			for i, rule := range gatewayRules(t, got) {
				if _, has := rule["acknowledgmentPolicy"]; has {
					t.Errorf("rule %d carries a policy the runtime would not apply", i)
				}
			}
		})
	}
}

func TestDesiredGateway_PartialEntrypointAckPolicyIsPushedWithoutWarning(t *testing.T) {
	t.Parallel()
	for _, lines := range []string{
		"      acknowledgment_policy:\n        mode: on_completion\n",
		"      acknowledgment_policy:\n        timeout_seconds: 90\n",
		"      acknowledgment_policy:\n        on_failure:\n          action: ack\n",
	} {
		got, err := desiredGateway("EntAck", ackGateway(lines, ""))
		if err != nil {
			t.Fatalf("desiredGateway(%q): %v", lines, err)
		}
		for _, w := range got.warnings {
			t.Errorf("unexpected warning for %q: %s", lines, w)
		}
		for i, rule := range gatewayRules(t, got) {
			if len(storedPolicy(rule)) == 0 {
				t.Errorf("rule %d has no policy for %q", i, lines)
			}
		}
	}
}

func TestDesiredGateway_NullEntrypointAckPolicyIsAbsent(t *testing.T) {
	t.Parallel()
	got, err := desiredGateway("EntAck", ackGateway("      acknowledgment_policy:\n", ""))
	if err != nil {
		t.Fatalf("desiredGateway: %v", err)
	}
	for _, w := range got.warnings {
		t.Errorf("unexpected warning: %s", w)
	}
}

func TestPushAckPolicyDefault_SkipsEntriesThatAreNotHandlers(t *testing.T) {
	t.Parallel()
	appConfig := map[string]any{
		ackPolicyKey:     map[string]any{"mode": "on_completion"},
		"event_handlers": []any{"not a handler", map[string]any{"name": "h"}},
	}

	if warnings := pushAckPolicyDefault(appConfig); len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	handler := appConfig["event_handlers"].([]any)[1].(map[string]any)
	if got := handler[ackPolicyKey]; !reflect.DeepEqual(got, map[string]any{"mode": "on_completion"}) {
		t.Errorf("handler policy = %v", got)
	}
}

func TestPushAckPolicyDefault_GivesEachHandlerItsOwnCopy(t *testing.T) {
	t.Parallel()
	appConfig := map[string]any{
		ackPolicyKey:     map[string]any{"mode": "on_completion"},
		"event_handlers": []any{map[string]any{"name": "a"}, map[string]any{"name": "b"}},
	}

	pushAckPolicyDefault(appConfig)
	handlers := appConfig["event_handlers"].([]any)
	handlers[0].(map[string]any)[ackPolicyKey].(map[string]any)["mode"] = "changed"

	if got := handlers[1].(map[string]any)[ackPolicyKey].(map[string]any)["mode"]; got != "on_completion" {
		t.Errorf("second handler's policy = %v; the handlers share one mapping", got)
	}
}

func TestDesiredGateway_StoredAckPolicyMatchesWhatTheRuntimeAppliesToEachHandler(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		entrypoint string
		handler2   string
	}{
		{"the entrypoint policy reaches both handlers", entrypointAckPolicy, ""},
		{"a handler overrides on_receive", entrypointAckPolicy, "          acknowledgment_policy:\n            mode: on_receive\n"},
		{"a handler override replaces rather than merges", entrypointAckPolicy, "          acknowledgment_policy:\n            timeout_seconds: 10\n"},
		{"a null handler policy", entrypointAckPolicy, "          acknowledgment_policy:\n"},
		{"a string handler policy", entrypointAckPolicy, "          acknowledgment_policy: on_completion\n"},
		{"a string entrypoint policy", "      acknowledgment_policy: on_completion\n", ""},
		{"no entrypoint policy", "", ""},
		{"only a timeout", "      acknowledgment_policy:\n        timeout_seconds: 30\n", ""},
		{"a mode with no timeout or failure handling", "      acknowledgment_policy:\n        mode: on_completion\n", ""},
		{"only failure handling", "      acknowledgment_policy:\n        on_failure:\n          action: ack\n", ""},
		{"an empty handler mapping", entrypointAckPolicy, "          acknowledgment_policy: {}\n"},
		{"failure handling that is not the runtime default", entrypointAckPolicyWithOtherFailureHandling, ""},
		{"a handler with its own non-default nack outcome", entrypointAckPolicy, "          acknowledgment_policy:\n            mode: on_completion\n            on_failure:\n              nack_outcome: failed\n"},
		{"a zero timeout", "      acknowledgment_policy:\n        mode: on_completion\n        timeout_seconds: 0\n", ""},
		{"a negative timeout", "      acknowledgment_policy:\n        mode: on_completion\n        timeout_seconds: -5\n", ""},
		{"a handler with its own failure handling", entrypointAckPolicy, "          acknowledgment_policy:\n            mode: on_completion\n            on_failure:\n              action: ack\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := ackGateway(tc.entrypoint, tc.handler2)
			app, err := parseBuilderApp(content)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			runtime, err := eventmesh.ParseConfig(app.appConfig, true)
			if err != nil {
				t.Fatalf("runtime ParseConfig: %v", err)
			}
			got, err := desiredGateway("EntAck", content)
			if err != nil {
				t.Fatalf("desiredGateway: %v", err)
			}
			rules := gatewayRules(t, got)
			if len(rules) != len(runtime.EventHandlers) || len(rules) == 0 {
				t.Fatalf("stored %d rules for %d runtime handlers", len(rules), len(runtime.EventHandlers))
			}
			for i, handler := range runtime.EventHandlers {
				if rules[i]["name"] != handler.Name {
					t.Fatalf("rule %d is %v, runtime handler %d is %q", i, rules[i]["name"], i, handler.Name)
				}
				want := runtime.AckPolicy
				if handler.AckPolicy != nil {
					want = *handler.AckPolicy
				}
				stored := storedPolicy(rules[i])
				mode, _ := stored["mode"].(string)
				if mode == "" {
					mode = "on_receive"
				}
				if mode != want.Mode {
					t.Fatalf("handler %d: stored mode %q, runtime applies %q", i, mode, want.Mode)
				}
				if want.Mode != "on_completion" {
					continue
				}
				timeout, _ := stored["timeoutSeconds"].(float64)
				if timeout <= 0 {
					timeout = 300
				}
				if int(timeout) != want.TimeoutSeconds {
					t.Errorf("handler %d: stored timeout %v, runtime applies %d", i, stored["timeoutSeconds"], want.TimeoutSeconds)
				}
				onFailure, _ := stored["onFailure"].(map[string]any)
				action, outcome := "nack", "rejected"
				if v, ok := onFailure["action"].(string); ok {
					action = v
				}
				if v, ok := onFailure["nackOutcome"].(string); ok {
					outcome = v
				}
				if action != want.OnFailure.Action || outcome != want.OnFailure.NackOutcome {
					t.Errorf("handler %d: stored failure handling %s/%s, runtime applies %s/%s", i, action, outcome, want.OnFailure.Action, want.OnFailure.NackOutcome)
				}
			}
		})
	}
}
