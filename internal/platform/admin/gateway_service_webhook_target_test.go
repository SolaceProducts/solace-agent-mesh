// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"

	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/webhook"
)

func newWebhookGatewayServiceWithRepo(t *testing.T) (*GatewayService, *store.GatewayRepo) {
	t.Helper()

	db := testOpenDB(t)
	repo := store.NewGatewayRepo(db)
	svc := NewGatewayService(repo, store.NewGatewayDeploymentRepo(db), nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return svc, repo
}

func TestWebhook_SchemaVisibleWithoutAFeatureRegistry(t *testing.T) {
	svc, _ := newWebhookGatewayServiceWithRepo(t)

	if !slices.ContainsFunc(svc.GetSchemas(), func(s GatewaySchemaResponse) bool { return s.Type == "webhook" }) {
		t.Error("webhook schema missing from GetSchemas")
	}
	if !slices.ContainsFunc(VisibleGatewaySchemas(nil), func(s GatewaySchemaResponse) bool { return s.Type == "webhook" }) {
		t.Error("webhook schema missing from VisibleGatewaySchemas")
	}
}

func storedValues(t *testing.T, repo *store.GatewayRepo, id string) map[string]any {
	t.Helper()
	gw, err := repo.GetByID(context.Background(), id)
	if err != nil || gw == nil {
		t.Fatalf("GetByID(%s) = %v, %v", id, gw, err)
	}
	var values map[string]any
	if gw.Values == nil {
		t.Fatal("stored gateway has no values")
	}
	if err := json.Unmarshal([]byte(*gw.Values), &values); err != nil {
		t.Fatalf("parse stored values: %v", err)
	}
	return values
}

func legacyWebhookValuesJSON(t *testing.T) string {
	t.Helper()
	values := validWebhookValues()
	delete(values, "targetType")
	b, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal legacy values: %v", err)
	}
	return string(b)
}

func TestCreateGateway_Webhook_StampsTheDerivedTargetType(t *testing.T) {
	svc, repo := newWebhookGatewayServiceWithRepo(t)

	values := validWebhookValues()
	delete(values, "targetType")
	delete(values, "targetAgentName")
	values["targetWorkflowName"] = "OrderIntake"

	resp, err := svc.Create(context.Background(), CreateGatewayRequest{
		Name:        "LegacyShapedWebhook",
		Description: "Authored without the target discriminator.",
		Type:        "webhook",
		Values:      values,
	}, "user1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	stored := storedValues(t, repo, resp.ID)
	if stored["targetType"] != "workflow" {
		t.Errorf("stored targetType = %v, want workflow", stored["targetType"])
	}
	if stored["targetWorkflowName"] != "OrderIntake" {
		t.Errorf("stored targetWorkflowName = %v, want OrderIntake", stored["targetWorkflowName"])
	}
}

func TestUpdateGateway_Webhook_PartialPatchKeepsALegacyTarget(t *testing.T) {
	svc, repo := newWebhookGatewayServiceWithRepo(t)

	legacy := legacyWebhookValuesJSON(t)
	gw := &store.Gateway{
		Name:        "LegacyStoredWebhook",
		Description: strPtr("Stored before the target discriminator existed."),
		Type:        "webhook",
		Values:      &legacy,
		CreatedBy:   "user1",
		UpdatedBy:   "user1",
	}
	if err := repo.Create(context.Background(), gw); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	if _, err := svc.Update(context.Background(), gw.ID, UpdateGatewayRequest{
		Values: map[string]any{"routePath": "/orders-v2"},
	}, "user2"); err != nil {
		t.Fatalf("Update: %v", err)
	}

	stored := storedValues(t, repo, gw.ID)
	if stored["targetAgentName"] != "Orchestrator" {
		t.Errorf("stored targetAgentName = %v, want the untouched Orchestrator", stored["targetAgentName"])
	}
	if stored["targetType"] != "agent" {
		t.Errorf("stored targetType = %v, want agent", stored["targetType"])
	}
	if stored["routePath"] != "/orders-v2" {
		t.Errorf("stored routePath = %v, want the patched /orders-v2", stored["routePath"])
	}
}

func TestUpdateGateway_Webhook_SwitchingTargetDropsTheOldOne(t *testing.T) {
	svc, repo := newWebhookGatewayServiceWithRepo(t)

	resp, err := svc.Create(context.Background(), CreateGatewayRequest{
		Name:        "SwitchableWebhook",
		Description: "Starts out pointed at an agent.",
		Type:        "webhook",
		Values:      validWebhookValues(),
	}, "user1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	switched := validWebhookValues()
	switched["targetType"] = "workflow"
	switched["targetAgentName"] = ""
	switched["targetWorkflowName"] = "OrderIntake"
	if _, err := svc.Update(context.Background(), resp.ID, UpdateGatewayRequest{Values: switched}, "user2"); err != nil {
		t.Fatalf("Update: %v", err)
	}

	stored := storedValues(t, repo, resp.ID)
	if _, present := stored["targetAgentName"]; present {
		t.Errorf("stored targetAgentName = %v, want it dropped", stored["targetAgentName"])
	}
	if stored["targetWorkflowName"] != "OrderIntake" {
		t.Errorf("stored targetWorkflowName = %v, want OrderIntake", stored["targetWorkflowName"])
	}
	if stored["targetType"] != "workflow" {
		t.Errorf("stored targetType = %v, want workflow", stored["targetType"])
	}
}
