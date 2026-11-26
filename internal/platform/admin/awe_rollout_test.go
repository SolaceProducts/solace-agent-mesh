// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestE2E_DeployedEntrypointsFollowTheTable(t *testing.T) {
	h := setupE2EWithGWE(t, nil)
	gwID := h.createGateway("table_gw")
	h.deployGateway(gwID)

	payloads, err := DeployedEntrypoints(h.ctx, h.db, h.yamlSvc, slog.Default())
	if err != nil || len(payloads) != 1 || payloads[0].Name != gwID {
		t.Fatalf("deployed = %v, %v; want the deployed entrypoint", payloads, err)
	}
	if _, err = h.gwDeploySvc.Deploy(h.ctx, CreateGatewayDeploymentRequest{GatewayID: gwID, Action: DeployActionUndeploy}, "e2e-user"); err != nil {
		t.Fatalf("undeploy: %v", err)
	}
	if after, _ := DeployedEntrypoints(h.ctx, h.db, h.yamlSvc, slog.Default()); len(after) != 1 || !after[0].Undeploy {
		t.Fatalf("deployed after undeploy = %+v, want the undeploy", after)
	}
	if one, err := DeployedEntrypoint(h.ctx, h.db, h.yamlSvc, slog.Default(), gwID); err != nil || one == nil || !one.Undeploy {
		t.Fatalf("one entrypoint after undeploy = %+v, %v; want the undeploy", one, err)
	}

	pending := store.GatewayDeployment{ID: idgen.New(), GatewayID: gwID, Action: DeployActionDeploy}
	body, _ := json.Marshal(entrypointIntent{Deployment: pending, Payloads: []ControlPayload{{"name": gwID, "app_config": map[string]any{"gateway_id": gwID}}}})
	if err := store.NewDeploymentIntentRepo(h.db).Put(h.ctx, &store.DeploymentIntent{ID: pending.ID, Kind: store.IntentKindEntrypoint, ObjectID: gwID, Payload: string(body)}); err != nil {
		t.Fatal(err)
	}
	if one, err := DeployedEntrypoint(h.ctx, h.db, h.yamlSvc, slog.Default(), gwID); err != nil || one == nil || one.DeploymentID != pending.ID {
		t.Fatalf("one entrypoint with a deploy in flight = %+v, %v; want the deploy in flight", one, err)
	}
	if one, err := DeployedEntrypoint(h.ctx, h.db, h.yamlSvc, slog.Default(), idgen.New()); err != nil || one != nil {
		t.Fatalf("an unknown entrypoint = %+v, %v; want nothing", one, err)
	}
}

func TestE2E_DeployedEntrypointsRegenerateMissingSnapshots(t *testing.T) {
	h := setupE2EWithGWE(t, nil)
	bad := "not: [valid"
	for name, snapshot := range map[string]*string{"no_snapshot_gw": nil, "bad_snapshot_gw": &bad} {
		gwID := h.createGateway(name)
		if err := h.gwDeployRepo.Create(h.ctx, &store.GatewayDeployment{
			GatewayID: gwID, Status: DeployStatusSuccess, Action: DeployActionDeploy,
			GatewayConfigSnapshot: snapshot, CreatedBy: "e2e-user",
		}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := DeployedEntrypoints(h.ctx, h.db, h.yamlSvc, slog.Default())
	if err != nil || len(got) != 2 {
		t.Fatalf("deployed = %v, %v; want both listed", got, err)
	}
	withConfig := 0
	for _, d := range got {
		if d.AppConfig != nil {
			withConfig++
		}
	}
	if withConfig != 1 {
		t.Fatalf("deployed = %+v; want the regenerated one with its config and the unparseable one without", got)
	}
}
