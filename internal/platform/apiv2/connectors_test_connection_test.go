// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"strings"
	"testing"
)

const nothingListensBrokerURL = "tcp://127.0.0.1:1"

func TestConnectorTestConnection_MissingCredentialsIs422(t *testing.T) {
	app := newConnectorAPI(t)
	expectDetailSaying(t, app.call(http.MethodPut, "/connectors/testConnection", map[string]any{
		"values": map[string]any{"broker_url": "tcps://broker.example.com:55443"},
	}), "values", "required to test the connection")
}

func TestConnectorTestConnection_StoredCredentialsOnlyGoToTheStoredBroker(t *testing.T) {
	app := newConnectorAPI(t)
	const storedPassword = "stored-probe-password"
	rec := app.call(http.MethodPost, "/connectors", map[string]any{
		"name": "stored-event-mesh", "description": "Event mesh connector holding a broker password.",
		"connectorType": "event_mesh", "connectorSubtype": "solace",
		"values": map[string]any{"broker_url": nothingListensBrokerURL, "broker_vpn": "stored-vpn", "broker_username": "stored-user", "broker_password": storedPassword},
	})
	expectStatus(t, rec, http.StatusCreated)
	connector := decodeData[Connector](t, rec)

	refused := app.call(http.MethodPut, "/connectors/testConnection", map[string]any{
		"connectorId": connector.ID, "values": map[string]any{"broker_url": "tcp://127.0.0.1:2"},
	})
	expectDetailSaying(t, refused, "values", "enter the credentials again")
	if strings.Contains(refused.Body.String(), storedPassword) {
		t.Error("the stored secret reached the response")
	}

	kept := app.call(http.MethodPut, "/connectors/testConnection", map[string]any{
		"connectorId": connector.ID, "values": map[string]any{"broker_url": nothingListensBrokerURL, "broker_password": redacted},
	})
	expectStatus(t, kept, http.StatusOK)
	if result := decodeData[TestConnectionResult](t, kept); result.Success || result.Message == nil {
		t.Errorf("result = %+v, want the probe of the stored broker to have run and failed", result)
	}
}

func TestConnectorTestConnection_OtherConnectorTypesNameTheConnector(t *testing.T) {
	app := newConnectorAPI(t)
	expectDetailSaying(t, app.call(http.MethodPut, "/connectors/testConnection", map[string]any{
		"connectorId": createConnector(t, app).ID,
	}), "connectorId", "only supported for Event Mesh connectors")
}
