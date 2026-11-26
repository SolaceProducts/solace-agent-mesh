// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestWebhookTargetType(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]any
		want   string
	}{
		{name: "explicit wins over the target names", values: map[string]any{"targetType": "workflow", "targetAgentName": "Orchestrator"}, want: "workflow"},
		{name: "derived from a workflow name", values: map[string]any{"targetWorkflowName": "OrderIntake"}, want: "workflow"},
		{name: "derived from an agent name", values: map[string]any{"targetAgentName": "Orchestrator"}, want: "agent"},
		{name: "empty bag falls back to the schema default", values: map[string]any{}, want: "agent"},
		{name: "blank explicit value is derived", values: map[string]any{"targetType": "  ", "targetWorkflowName": "OrderIntake"}, want: "workflow"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := webhookTargetType(tt.values); got != tt.want {
				t.Fatalf("webhookTargetType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeWebhookValuesDoesNotMutateInput(t *testing.T) {
	original := map[string]any{"targetWorkflowName": "OrderIntake"}

	normalized := normalizeWebhookValues(original)

	if normalized["targetType"] != "workflow" {
		t.Fatalf("targetType = %v, want workflow", normalized["targetType"])
	}
	if _, ok := original["targetType"]; ok {
		t.Fatal("normalizeWebhookValues mutated its input")
	}
	if normalized["targetWorkflowName"] != "OrderIntake" {
		t.Fatalf("targetWorkflowName = %v, want OrderIntake", normalized["targetWorkflowName"])
	}
}

func TestNormalizeWebhookValuesDropsTheClearedTarget(t *testing.T) {
	t.Parallel()

	normalized := normalizeWebhookValues(map[string]any{
		"targetType":         "agent",
		"targetAgentName":    "Orchestrator",
		"targetWorkflowName": "",
	})

	if _, present := normalized["targetWorkflowName"]; present {
		t.Errorf("targetWorkflowName = %v, want the cleared key dropped", normalized["targetWorkflowName"])
	}
	if normalized["targetAgentName"] != "Orchestrator" {
		t.Errorf("targetAgentName = %v, want Orchestrator", normalized["targetAgentName"])
	}
}

func TestMergeGatewayValuesForUpdate_DropsTheAbandonedWebhookTarget(t *testing.T) {
	t.Parallel()

	existing := validWebhookValues()
	existing["targetType"] = "agent"
	incoming := map[string]any{"targetType": "workflow", "targetWorkflowName": "OrderIntake"}

	merged, err := mergeGatewayValuesForUpdate("webhook", incoming, existing, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, present := merged["targetAgentName"]; present {
		t.Errorf("targetAgentName = %v, want it dropped", merged["targetAgentName"])
	}
	if merged["routePath"] != "/orders" {
		t.Errorf("routePath = %v, want the stored /orders preserved", merged["routePath"])
	}
	if err := validateWebhookValues(normalizeWebhookValues(merged)); err != nil {
		t.Fatalf("merged bag failed validation: %v", err)
	}
}

func TestNormalizeValuesForType(t *testing.T) {
	t.Parallel()

	webhook := normalizeValuesForType("webhook", map[string]any{"targetWorkflowName": "OrderIntake"})
	if webhook["targetType"] != "workflow" {
		t.Errorf("targetType = %v, want workflow", webhook["targetType"])
	}

	other := normalizeValuesForType("event_mesh", map[string]any{"broker_url": "tcp://localhost:55555"})
	if _, present := other["targetType"]; present {
		t.Errorf("targetType = %v, want it absent on a non-webhook bag", other["targetType"])
	}
}

func TestMergeGatewayValuesForUpdate_KeepsTheTargetOnALegacyPartialPatch(t *testing.T) {
	t.Parallel()

	legacyStored := validWebhookValues()
	delete(legacyStored, "targetType")

	merged, err := mergeGatewayValuesForUpdate("webhook", map[string]any{"routePath": "/orders-v2"}, normalizeWebhookValues(legacyStored), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if merged["targetAgentName"] != "Orchestrator" {
		t.Errorf("targetAgentName = %v, want the stored Orchestrator preserved", merged["targetAgentName"])
	}
	if merged["routePath"] != "/orders-v2" {
		t.Errorf("routePath = %v, want the patched /orders-v2", merged["routePath"])
	}
	if err := validateWebhookValues(normalizeWebhookValues(merged)); err != nil {
		t.Fatalf("merged bag failed validation: %v", err)
	}
}

func TestBuildResponse_DerivesWebhookTargetTypeForLegacyRow(t *testing.T) {
	t.Parallel()

	svc := &GatewayService{}
	legacyValues := `{"routePath":"/orders","targetWorkflowName":"OrderIntake"}`
	gw := &store.Gateway{
		ID:          "gw-legacy",
		Name:        "legacy-webhook",
		Description: strPtr("stored before the target discriminator existed"),
		Type:        "webhook",
		Values:      &legacyValues,
		CreatedBy:   "u",
		UpdatedBy:   "u",
	}

	resp := svc.buildResponse(gw, DisplayStatusNotDeployed, nil)

	values, ok := resp.Values.(map[string]any)
	if !ok {
		t.Fatalf("Values = %T, want map[string]any", resp.Values)
	}
	if got := values["targetType"]; got != "workflow" {
		t.Fatalf("targetType = %v, want workflow", got)
	}
	if got := values["targetWorkflowName"]; got != "OrderIntake" {
		t.Fatalf("targetWorkflowName = %v, want OrderIntake", got)
	}
}
