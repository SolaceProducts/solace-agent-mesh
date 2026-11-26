// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"strings"
	"testing"
)

func TestUsesPlannedConnector(t *testing.T) {
	t.Parallel()
	urlOnly := map[string]any{"specification_url": "http://127.0.0.1:1/openapi.json"}

	t.Run("a connector the save creates, called by name", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		if !f.svc.NewPlannedChecks().UsesPlannedConnector(t.Context(), "", plannedConfig(petNode("listPets")), plannedAPI("petstore", urlOnly)) {
			t.Fatal("a node calling a planned connector by name was not reported")
		}
	})

	t.Run("a stored connector the save does not write", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		f.shipmentConnector(t, "carrier-api")
		if f.svc.NewPlannedChecks().UsesPlannedConnector(t.Context(), "", plannedConfig(shipNode("get_shipment", nil)), plannedAPI("petstore", urlOnly)) {
			t.Fatal("a node calling an untouched connector was reported")
		}
	})

	t.Run("a stored connector the save updates", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		f.shipmentConnector(t, "carrier-api")
		stored := onlyConnectorNamed(t, f, "carrier-api")
		planned := map[string]PlannedConnector{"carrier-api": {ID: stored.ID, Name: "carrier-api", Values: map[string]any{"allow_list": "getShipment"}}}
		if !f.svc.NewPlannedChecks().UsesPlannedConnector(t.Context(), "", plannedConfig(shipNode("get_shipment", nil)), planned) {
			t.Fatal("a node calling a connector the save updates was not reported")
		}
	})

	t.Run("a connector the save renames away from the node's name", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		f.shipmentConnector(t, "carrier-api")
		stored := onlyConnectorNamed(t, f, "carrier-api")
		planned := map[string]PlannedConnector{"carrier-api-renamed": {ID: stored.ID, Name: "carrier-api-renamed", Values: map[string]any{}}}
		if !f.svc.NewPlannedChecks().UsesPlannedConnector(t.Context(), "", plannedConfig(shipNode("get_shipment", nil)), planned) {
			t.Fatal("a node whose connector the save renames was not reported")
		}
	})

	t.Run("a body with no connector node", func(t *testing.T) {
		t.Parallel()
		f := newInputCheckFixture(t)
		node := petNode("listPets")
		node["type"] = "agent"
		delete(node, "connector")
		if f.svc.NewPlannedChecks().UsesPlannedConnector(t.Context(), "", plannedConfig(node), plannedAPI("petstore", urlOnly)) {
			t.Fatal("a workflow with no connector node was reported")
		}
	})
}

func TestRejectUndeployableConnectorNodes_BuilderWordingOffersNoSpecUpload(t *testing.T) {
	t.Parallel()
	f := newInputCheckFixture(t)
	urlOnly := map[string]any{"specification_url": "http://127.0.0.1:1/openapi.json"}
	err := rejectUndeployable(t.Context(), f.svc, "", plannedConfig(petNode("listPets")), plannedAPI("petstore", urlOnly))
	requireRefusalNaming(t, err, "set allow_list on the connector")
	if strings.Contains(err.Error(), "specification_file") {
		t.Errorf("refusal %q offers a spec upload, which a Builder save cannot carry", err)
	}
}
