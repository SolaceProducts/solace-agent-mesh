// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
)

func eventMeshFixture(targetAgent, targetWorkflow string) map[string]any {
	rule := map[string]any{
		"name":           "incoming-orders",
		"subscriptions":  []any{map[string]any{"topic": "events/orders/>"}},
		"promptTemplate": "Process {{ payload }}",
	}
	if targetAgent != "" {
		rule["targetAgent"] = targetAgent
	}
	if targetWorkflow != "" {
		rule["targetWorkflowName"] = targetWorkflow
	}
	return map[string]any{
		"type": "event_mesh",
		"values": map[string]any{
			"event_rules": []any{rule},
		},
	}
}

func TestPlanGateways_ValidateEventRuleAgentDangling(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedGateway("event-rcvr", eventMeshFixture("typo-here", "")),
	}

	rejecting := func(name string) error {
		if name == "uppercaser" {
			return nil
		}
		return fmt.Errorf("agent %q not found on platform and not declared in the desired manifest", name)
	}
	hooks := translate.GatewayHooks{ValidateAgent: rejecting}

	_, err := planGateways(t.Context(), nil, desired, nil, hooks)
	if err == nil {
		t.Fatal("expected dangling event_rules[].targetAgent to fail plan, got nil")
	}
	for _, want := range []string{"event-rcvr", "incoming-orders", "typo-here", "targetAgent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message missing %q; got: %s", want, err.Error())
		}
	}
}

func TestPlanGateways_ValidateEventRuleWorkflowDangling(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedGateway("event-rcvr", eventMeshFixture("", "missing-workflow")),
	}

	hooks := translate.GatewayHooks{
		ValidateWorkflow: func(name string) error {
			return fmt.Errorf("workflow %q not found on platform and not declared in the desired manifest", name)
		},
	}

	_, err := planGateways(t.Context(), nil, desired, nil, hooks)
	if err == nil {
		t.Fatal("expected dangling event_rules[].targetWorkflow to fail plan, got nil")
	}
	for _, want := range []string{"event-rcvr", "incoming-orders", "missing-workflow", "targetWorkflowName"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message missing %q; got: %s", want, err.Error())
		}
	}
}

func TestPlanGateways_ValidateEventRuleAcceptsKnownAgent(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedGateway("event-rcvr", eventMeshFixture("uppercaser", "")),
	}

	known := map[string]struct{}{"uppercaser": {}}
	hooks := translate.GatewayHooks{
		ValidateAgent: func(name string) error {
			if _, ok := known[name]; ok {
				return nil
			}
			return fmt.Errorf("agent %q not found", name)
		},
	}

	plan, err := planGateways(t.Context(), nil, desired, nil, hooks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Creates) != 1 || plan.Creates[0].Name != "event-rcvr" {
		t.Errorf("expected one Create for event-rcvr, got %+v", plan.Creates)
	}
}

func TestPlanGateways_ValidateRejectsNonStringTargetAgent(t *testing.T) {
	spec := map[string]any{
		"type": "event_mesh",
		"values": map[string]any{
			"event_rules": []any{
				map[string]any{
					"name":           "incoming",
					"subscriptions":  []any{map[string]any{"topic": "events/orders/>"}},
					"promptTemplate": "P",
					"targetAgent":    map[string]any{"from": "parent"},
				},
			},
		},
	}
	desired := []resolve.ResolvedResource{resolvedGateway("event-rcvr", spec)}

	hooks := translate.GatewayHooks{ValidateAgent: func(string) error { return nil }}
	_, err := planGateways(t.Context(), nil, desired, nil, hooks)
	if err == nil {
		t.Fatal("expected non-string targetAgent to fail plan, got nil")
	}
	if !strings.Contains(err.Error(), "targetAgent must be a string") {
		t.Errorf("error must surface the type mismatch; got: %s", err.Error())
	}
}

func TestPlanGateways_ValidateAcceptsBrokerInstanceName(t *testing.T) {
	const brokerName = "agent_019e47f4_2be0_75d8_828e_77a0d1d9221f"
	desired := []resolve.ResolvedResource{
		resolvedGateway("event-rcvr", eventMeshFixture(brokerName, "")),
	}
	accept := map[string]struct{}{
		"uppercaser": {},
		brokerName:   {},
	}
	hooks := translate.GatewayHooks{
		ValidateAgent: func(name string) error {
			if _, ok := accept[name]; ok {
				return nil
			}
			return fmt.Errorf("agent %q not found", name)
		},
	}
	if _, err := planGateways(t.Context(), nil, desired, nil, hooks); err != nil {
		t.Fatalf("broker-id targetAgent must be accepted, got error: %v", err)
	}
}

func TestPlanGateways_NoHooksSkipsEventRuleValidation(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedGateway("event-rcvr", eventMeshFixture("definitely-not-real", "")),
	}

	if _, err := planGateways(t.Context(), nil, desired, nil, translate.GatewayHooks{}); err != nil {
		t.Fatalf("zero hooks must skip validation, got error: %v", err)
	}
}

func defaultAgentFixture(gwType, defaultAgent string) map[string]any {
	return map[string]any{
		"type": gwType,
		"values": map[string]any{
			"default_agent_name": defaultAgent,
		},
	}
}

func TestPlanGateways_ValidateDefaultAgentDangling(t *testing.T) {
	cases := []struct{ gwType string }{
		{"slack"},
		{"teams"},
		{"email"},
		{"google_chat"},
	}
	for _, tc := range cases {
		t.Run(tc.gwType, func(t *testing.T) {
			desired := []resolve.ResolvedResource{
				resolvedGateway("gw-"+tc.gwType, defaultAgentFixture(tc.gwType, "typo-here")),
			}
			hooks := translate.GatewayHooks{
				ValidateAgent: func(name string) error {
					if name == "Orchestrator" {
						return nil
					}
					return fmt.Errorf("agent %q not found on platform and not declared in the desired manifest", name)
				},
			}
			_, err := planGateways(t.Context(), nil, desired, nil, hooks)
			if err == nil {
				t.Fatalf("expected dangling default_agent_name to fail plan (type=%s), got nil", tc.gwType)
			}
			for _, want := range []string{"gw-" + tc.gwType, "default_agent_name", "typo-here"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error message missing %q; got: %s", want, err.Error())
				}
			}
		})
	}
}

func TestPlanGateways_ValidateDefaultAgentAccepts(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedGateway("gw-slack", defaultAgentFixture("slack", "Orchestrator")),
	}
	hooks := translate.GatewayHooks{
		ValidateAgent: func(name string) error {
			if name == "Orchestrator" {
				return nil
			}
			return fmt.Errorf("agent %q not found", name)
		},
	}
	if _, err := planGateways(t.Context(), nil, desired, nil, hooks); err != nil {
		t.Fatalf("known default_agent_name must be accepted, got: %v", err)
	}
}

func TestPlanGateways_DefaultAgentEmptyIsNoOp(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedGateway("gw-slack", defaultAgentFixture("slack", "")),
	}
	hooks := translate.GatewayHooks{
		ValidateAgent: func(_ string) error {
			t.Fatal("ValidateAgent must not fire when default_agent_name is empty")
			return nil
		},
	}
	if _, err := planGateways(t.Context(), nil, desired, nil, hooks); err != nil {
		t.Fatalf("empty default_agent_name must skip validation, got: %v", err)
	}
}
