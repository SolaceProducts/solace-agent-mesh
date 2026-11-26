// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const boundMongoPassword = "stored-mongo-password"

func createBoundMongoConnector(t *testing.T, app *apiClient, name string) Connector {
	t.Helper()
	rec := app.call(http.MethodPost, "/connectors", map[string]any{
		"name": name, "description": "A MongoDB connector an agent is bound to.",
		"connectorType": "document_db", "connectorSubtype": "mongodb",
		"values": map[string]any{"scheme": "mongodb+srv", "hostname": "old.example.net", "username": "u", "password": boundMongoPassword, "database": "db", "collection": "events_v1"},
	})
	expectStatus(t, rec, http.StatusCreated)
	return decodeData[Connector](t, rec)
}

func bindAgentToConnector(t *testing.T, db *store.DB, agentID, connectorID string) {
	t.Helper()
	description, prompt := "Agent bound to a connector.", "You answer questions from the bound connector."
	agent := &store.Agent{ID: agentID, Name: "agent-" + agentID[:8], Description: &description, SystemPrompt: &prompt,
		Type: "standard", CreatedBy: "tester", UpdatedBy: "tester"}
	if err := store.NewAgentRepo(db).Create(t.Context(), agent, nil, nil, nil, []string{connectorID}, nil); err != nil {
		t.Fatalf("bind an agent to connector %s: %v", connectorID, err)
	}
}

func connectorRedeployAgentIDs(t *testing.T, db *store.DB, eventType, connectorID string) []string {
	t.Helper()
	rows, err := dbutil.QueryRows[struct {
		Payload string `db:"payload"`
	}](t.Context(), db, `SELECT payload FROM outbox_events WHERE event_type = ? AND aggregate_id = ?`, eventType, connectorID)
	if err != nil {
		t.Fatalf("read the outbox: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("%d %s events for connector %s, want 1", len(rows), eventType, connectorID)
	}
	var payload struct {
		AgentIDs []string `json:"agentIds"`
	}
	if err := json.Unmarshal([]byte(rows[0].Payload), &payload); err != nil {
		t.Fatalf("decode the %s payload: %v", eventType, err)
	}
	return payload.AgentIDs
}

func storedConnectorValues(t *testing.T, deps *entplatform.RouteDeps, connectorID string) map[string]any {
	t.Helper()
	_, _, values, err := deps.ConnectorSvc.GetValuesCleartext(t.Context(), connectorID)
	if err != nil {
		t.Fatalf("read connector %s values: %v", connectorID, err)
	}
	return values
}

func TestConnectors_PatchKeepsAnOmittedSecretAndRedeploysBoundAgents(t *testing.T) {
	core, deps := newTestCore(t)
	app := newAPIClient(t, core)
	connector := createBoundMongoConnector(t, app, "update-mongo")
	const agentID = "aaaaaaaa-1111-2222-3333-444444444444"
	bindAgentToConnector(t, deps.DB, agentID, connector.ID)

	expectStatus(t, app.call(http.MethodPatch, "/connectors/"+connector.ID, map[string]any{
		"values": map[string]any{"collection": "events_v2"},
	}), http.StatusOK)

	if got := connectorRedeployAgentIDs(t, deps.DB, "connector.updated", connector.ID); !slices.Equal(got, []string{agentID}) {
		t.Errorf("connector.updated redeploys %v, want [%s]", got, agentID)
	}
	values := storedConnectorValues(t, deps, connector.ID)
	if values["collection"] != "events_v2" {
		t.Errorf("collection = %v, want events_v2", values["collection"])
	}
	if values["password"] != boundMongoPassword {
		t.Errorf("password = %v, want the stored one kept when the edit leaves it out", values["password"])
	}
}

func TestConnectors_PatchWithTheRedactedSentinelKeepsTheStoredSecret(t *testing.T) {
	core, deps := newTestCore(t)
	app := newAPIClient(t, core)
	connector := createBoundMongoConnector(t, app, "update-sentinel")

	expectStatus(t, app.call(http.MethodPatch, "/connectors/"+connector.ID, map[string]any{
		"values": map[string]any{"scheme": "mongodb+srv", "hostname": "old.example.net", "username": "u", "password": redacted, "database": "db", "collection": "events_v2"},
	}), http.StatusOK)

	values := storedConnectorValues(t, deps, connector.ID)
	if values["collection"] != "events_v2" {
		t.Errorf("collection = %v, want events_v2", values["collection"])
	}
	if values["password"] != boundMongoPassword {
		t.Errorf("password = %v, want the stored one restored from the sentinel", values["password"])
	}
}

func TestConnectors_DeleteRedeploysBoundAgents(t *testing.T) {
	core, deps := newTestCore(t)
	app := newAPIClient(t, core)
	connector := createBoundMongoConnector(t, app, "delete-mongo")
	const agentID = "bbbbbbbb-1111-2222-3333-444444444444"
	bindAgentToConnector(t, deps.DB, agentID, connector.ID)

	expectStatus(t, app.call(http.MethodDelete, "/connectors/"+connector.ID, nil), http.StatusNoContent)

	if got := connectorRedeployAgentIDs(t, deps.DB, "connector.deleted", connector.ID); !slices.Equal(got, []string{agentID}) {
		t.Errorf("connector.deleted redeploys %v, want [%s]", got, agentID)
	}
}

func TestConnectors_AgentCountFollowsBoundAgents(t *testing.T) {
	core, deps := newTestCore(t)
	app := newAPIClient(t, core)
	connector := createBoundMongoConnector(t, app, "count-mongo")
	if connector.AgentCount != 0 {
		t.Errorf("agentCount = %d on create, want 0", connector.AgentCount)
	}

	bindAgentToConnector(t, deps.DB, "cccccccc-1111-2222-3333-444444444444", connector.ID)

	if got := decodeData[Connector](t, app.call(http.MethodGet, "/connectors/"+connector.ID, nil)); got.AgentCount != 1 {
		t.Errorf("agentCount = %d with one bound agent, want 1", got.AgentCount)
	}
}
