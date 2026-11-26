// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func eventCatalogFeatureRegistry(t *testing.T, enabled bool) *features.Registry {
	t.Helper()
	t.Setenv("SAM_FEATURE_EVENT_CATALOG", "")
	reg := features.NewRegistry()
	reg.Register(features.Definition{
		Key:          features.KeyEventCatalog,
		Name:         "Event Catalog",
		ReleasePhase: features.PhaseExperimental,
		Default:      enabled,
	})
	return reg
}

func newEventMeshGatewayServiceOn(t *testing.T, db *store.DB) *GatewayService {
	t.Helper()
	return newEventMeshGatewayServiceWith(t, db, eventCatalogFeatureRegistry(t, true))
}

func newEventMeshGatewayServiceWith(t *testing.T, db *store.DB, reg *features.Registry) *GatewayService {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewGatewayService(store.NewGatewayRepo(db), store.NewGatewayDeploymentRepo(db), nil, nil, reg, logger)
}

func eventMeshGatewayRequest(name string) CreateGatewayRequest {
	return CreateGatewayRequest{
		Name:        name,
		Description: "An event-mesh entrypoint for the catalog tests.",
		Type:        "event_mesh",
		Values: map[string]any{
			"broker_url":      "tcps://broker.example.com:55443",
			"broker_vpn":      "default",
			"broker_username": "admin",
			"broker_password": "secret",
			"event_rules": []any{
				map[string]any{
					"name":           "shipments",
					"subscriptions":  []any{map[string]any{"topic": "acme/shipment/>", "qos": 1}},
					"targetAgent":    "ShipmentDesk",
					"promptTemplate": "{payload}",
				},
			},
		},
	}
}

func addCatalogEntry(t *testing.T, db *store.DB, entrypointID, key, name string) *store.EventCatalogEntry {
	t.Helper()
	entry := &store.EventCatalogEntry{
		ID: idgen.New(), Name: name, Key: key, Description: "A carrier reports a late shipment.",
		EntrypointID: entrypointID, TopicPattern: "acme/shipment/*/delayed", Enabled: true,
		CreatedBy: "alice@example.com", UpdatedBy: "alice@example.com",
	}
	if err := store.NewEventCatalogRepo(db).Create(t.Context(), entry); err != nil {
		t.Fatalf("create catalog entry: %v", err)
	}
	return entry
}

func TestGatewayDelete_IsRefusedWhileEventCatalogEntriesPointAtTheEntrypoint(t *testing.T) {
	const refusal = "cannot delete this entrypoint while event catalog entries point at it (2); delete them or move them to another entrypoint first"
	db := testOpenDB(t)
	svc := newEventMeshGatewayServiceOn(t, db)
	created, err := svc.Create(t.Context(), eventMeshGatewayRequest("Acme mesh"), "alice@example.com")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	delayed := addCatalogEntry(t, db, created.ID, "shipment_delayed", "Shipment delayed")
	lost := addCatalogEntry(t, db, created.ID, "shipment_lost", "Shipment lost")

	plain := svc.Delete(t.Context(), created.ID)
	withUndeploy := svc.DeleteWithUndeploy(t.Context(), created.ID, "alice@example.com", nil)

	for name, err := range map[string]error{"Delete": plain, "DeleteWithUndeploy": withUndeploy} {
		verr, ok := errors.AsType[*ValidationError](err)
		if !ok || verr.Message != refusal || verr.Field != fieldEntrypointID {
			t.Errorf("%s = %v, want a refusal on entrypointId saying %q", name, err, refusal)
		}
	}
	for name, err := range map[string]error{"Delete": plain, "DeleteWithUndeploy": withUndeploy} {
		if strings.Contains(err.Error(), delayed.Name) || strings.Contains(err.Error(), lost.Name) {
			t.Errorf("%s refusal %q names catalog entries to a caller who may not read the catalog", name, err)
		}
	}
	if got, err := svc.GetByID(t.Context(), created.ID); err != nil || got == nil {
		t.Fatalf("the refused delete removed the entrypoint: %v", err)
	}

	repo := store.NewEventCatalogRepo(db)
	for _, entry := range []*store.EventCatalogEntry{delayed, lost} {
		if _, err := repo.Delete(t.Context(), entry.ID); err != nil {
			t.Fatalf("delete catalog entry: %v", err)
		}
	}
	if err := svc.Delete(t.Context(), created.ID); err != nil {
		t.Errorf("Delete with no catalog entry left = %v, want it deleted", err)
	}
}

func TestGatewayDeleteWithUndeploy_RefusesBeforeUndeployingADeployedEntrypoint(t *testing.T) {
	db := testOpenDB(t)
	svc := newEventMeshGatewayServiceOn(t, db)
	created, err := svc.Create(t.Context(), eventMeshGatewayRequest("Acme mesh"), "alice@example.com")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	addCatalogEntry(t, db, created.ID, "shipment_delayed", "Shipment delayed")
	deployment := &store.GatewayDeployment{GatewayID: created.ID, Status: "success", Action: DeployActionDeploy, CreatedBy: "alice@example.com"}
	if err = store.NewGatewayDeploymentRepo(db).Create(t.Context(), deployment); err != nil {
		t.Fatalf("record deployment: %v", err)
	}

	var undeployAttempted bool
	err = func() error {
		defer func() { undeployAttempted = recover() != nil }()
		return svc.DeleteWithUndeploy(t.Context(), created.ID, "alice@example.com", &GatewayDeploymentService{})
	}()

	if undeployAttempted {
		t.Fatal("the entrypoint was undeployed before the delete was refused")
	}
	if _, ok := errors.AsType[*ValidationError](err); !ok {
		t.Errorf("DeleteWithUndeploy = %v, want the catalog refusal before any undeploy", err)
	}
}

func TestGatewayDelete_IgnoresCatalogEntriesOfOtherEntrypoints(t *testing.T) {
	db := testOpenDB(t)
	svc := newEventMeshGatewayServiceOn(t, db)
	mine, err := svc.Create(t.Context(), eventMeshGatewayRequest("Acme mesh"), "alice@example.com")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	other, err := svc.Create(t.Context(), eventMeshGatewayRequest("Other mesh"), "alice@example.com")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	addCatalogEntry(t, db, other.ID, "shipment_delayed", "Shipment delayed")

	if err := svc.Delete(t.Context(), mine.ID); err != nil {
		t.Errorf("Delete = %v, want an entrypoint nothing in the catalog points at deleted", err)
	}
}

func TestGatewayDelete_IgnoresCatalogEntriesWhileTheEventCatalogFeatureIsOff(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "flag off"
		if enabled {
			name = "flag on"
		}
		t.Run(name, func(t *testing.T) {
			db := testOpenDB(t)
			svc := newEventMeshGatewayServiceWith(t, db, eventCatalogFeatureRegistry(t, enabled))
			plain, err := svc.Create(t.Context(), eventMeshGatewayRequest("Acme mesh"), "alice@example.com")
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			withUndeploy, err := svc.Create(t.Context(), eventMeshGatewayRequest("Other mesh"), "alice@example.com")
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			addCatalogEntry(t, db, plain.ID, "shipment_delayed", "Shipment delayed")
			addCatalogEntry(t, db, withUndeploy.ID, "shipment_lost", "Shipment lost")

			errs := map[string]error{
				"Delete":             svc.Delete(t.Context(), plain.ID),
				"DeleteWithUndeploy": svc.DeleteWithUndeploy(t.Context(), withUndeploy.ID, "alice@example.com", nil),
			}

			for op, err := range errs {
				_, refused := errors.AsType[*ValidationError](err)
				switch {
				case enabled && !refused:
					t.Errorf("%s with the event catalog on = %v, want a refusal", op, err)
				case !enabled && err != nil:
					t.Errorf("%s with the event catalog off = %v, want the entrypoint deleted", op, err)
				}
			}
		})
	}
}

func TestGatewayDelete_WithoutAFeatureRegistryTreatsTheEventCatalogAsOff(t *testing.T) {
	db := testOpenDB(t)
	svc := newEventMeshGatewayServiceWith(t, db, nil)
	created, err := svc.Create(t.Context(), eventMeshGatewayRequest("Acme mesh"), "alice@example.com")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	addCatalogEntry(t, db, created.ID, "shipment_delayed", "Shipment delayed")

	if err := svc.Delete(t.Context(), created.ID); err != nil {
		t.Errorf("Delete = %v, want an entrypoint deleted when no registry says the catalog is on", err)
	}
}

func TestGatewayDelete_FailsClosedWhenTheCatalogEntriesCannotBeCounted(t *testing.T) {
	db := testOpenDB(t)
	svc := newEventMeshGatewayServiceOn(t, db)
	plain, err := svc.Create(t.Context(), eventMeshGatewayRequest("Acme mesh"), "alice@example.com")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	withUndeploy, err := svc.Create(t.Context(), eventMeshGatewayRequest("Other mesh"), "alice@example.com")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err = db.ExecContext(t.Context(), `DROP TABLE event_catalog_entries`); err != nil {
		t.Fatalf("drop the catalog table: %v", err)
	}

	errs := map[string]error{
		"Delete":             svc.Delete(t.Context(), plain.ID),
		"DeleteWithUndeploy": svc.DeleteWithUndeploy(t.Context(), withUndeploy.ID, "alice@example.com", nil),
	}

	for op, err := range errs {
		if err == nil {
			t.Errorf("%s = nil, want an error when the catalog entries cannot be counted", op)
			continue
		}
		if _, refused := errors.AsType[*ValidationError](err); refused {
			t.Errorf("%s = %v, want a storage error, not a refusal the caller could fix", op, err)
		}
	}
	for _, id := range []string{plain.ID, withUndeploy.ID} {
		if got, err := svc.GetByID(t.Context(), id); err != nil || got == nil {
			t.Errorf("the failed delete removed entrypoint %s: %v", id, err)
		}
	}
}
