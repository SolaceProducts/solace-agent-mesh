// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const connectorBaseURL = "https://orders.example.com"

const updateConnectorFailureMessage = "Unable to update connector"

const storedNameBreakingTheNameRules = "legacy\tname"

const unregisteredConnectorType = "not_a_registered_type"

const gatedConnectorType = "email"

func connectorNames(connectors []Connector) []string {
	names := make([]string, len(connectors))
	for i, c := range connectors {
		names[i] = c.Name
	}
	return names
}

func newConnectorAPI(t *testing.T) *apiClient {
	t.Helper()
	core, _ := newTestCore(t)
	return newAPIClient(t, core)
}

func createConnector(t *testing.T, app *apiClient) Connector {
	t.Helper()
	return createNamedConnector(t, app, "orders-api")
}

func createNamedConnector(t *testing.T, app *apiClient, name string) Connector {
	t.Helper()
	rec := app.call(http.MethodPost, "/connectors", map[string]any{
		"name": name, "description": "The orders service the fulfilment agent calls.",
		"connectorType": "api", "connectorSubtype": "openapi",
		"values": map[string]any{"base_url": connectorBaseURL},
	})
	expectStatus(t, rec, http.StatusCreated)
	return decodeData[Connector](t, rec)
}

func TestConnectors_Lifecycle(t *testing.T) {
	app := newConnectorAPI(t)

	rec := app.call(http.MethodPost, "/connectors", map[string]any{
		"name": "my-openapi", "description": "a connector for the orders API",
		"connectorType": "api", "connectorSubtype": "openapi",
		"values": map[string]any{"base_url": connectorBaseURL},
	})
	expectStatus(t, rec, http.StatusCreated)
	created := decodeData[Connector](t, rec)
	expectLocation(t, rec, "/connectors/"+created.ID)

	if created.Type != "connector" {
		t.Errorf("type = %q, want %q", created.Type, "connector")
	}
	if created.ConnectorType != "api" || created.ConnectorSubtype != "openapi" {
		t.Errorf("connectorType/Subtype = %q/%q", created.ConnectorType, created.ConnectorSubtype)
	}
	if !regexp.MustCompile(`"createdTime":"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z"`).Match(rec.Body.Bytes()) {
		t.Errorf("createdTime is not an ISO millisecond UTC instant: %s", rec.Body.String())
	}

	raw := app.callRaw(http.MethodGet, "/connectors/"+created.ID, nil)
	for _, absent := range []string{`"createdAt"`, `"updatedAt"`, `"subtype"`} {
		if bytes.Contains(raw, []byte(absent)) {
			t.Errorf("v1 key %s still on the v2 wire: %s", absent, raw)
		}
	}
	for _, present := range []string{`"agentCount"`, `"hasResource"`, `"contentHash"`, `"warnings"`, `"agentRegistrations"`} {
		if !bytes.Contains(raw, []byte(present)) {
			t.Errorf("%s must always be sent, never omitted: %s", present, raw)
		}
	}

	listed := decodeList[Connector](t, app.call(http.MethodGet, "/connectors", nil))
	if len(listed.Data) != 1 || listed.Data[0].ID != created.ID {
		t.Errorf("list = %v, want just %q", connectorNames(listed.Data), created.Name)
	}

	expectStatus(t, app.call(http.MethodDelete, "/connectors/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, app.call(http.MethodGet, "/connectors/"+created.ID, nil), http.StatusNotFound)
}

func TestConnectors_UnknownTypeRedactsEverything(t *testing.T) {
	app := newConnectorAPI(t)

	rec := app.call(http.MethodPost, "/connectors", map[string]any{
		"name": "plugin-thing", "description": "a connector of a type this build does not know",
		"connectorType": unregisteredConnectorType, "connectorSubtype": "generic",
		"values": map[string]any{"api_key": "super-secret", "endpoint": "https://x.example.com"},
	})
	expectStatus(t, rec, http.StatusCreated)
	created := decodeData[Connector](t, rec)

	got := decodeData[Connector](t, app.call(http.MethodGet, "/connectors/"+created.ID, nil))
	if got.Values["api_key"] == "super-secret" {
		t.Error("an unknown connector type must not return its values in clear")
	}
	if got.Values["api_key"] != redacted {
		t.Errorf("api_key = %v, want %s", got.Values["api_key"], redacted)
	}
	if got.Values["endpoint"] != redacted {
		t.Errorf("endpoint = %v, want %s", got.Values["endpoint"], redacted)
	}
}

func TestConnectors_KnownTypeKeepsNonSecretValues(t *testing.T) {
	app := newConnectorAPI(t)
	created := createConnector(t, app)

	got := decodeData[Connector](t, app.call(http.MethodGet, "/connectors/"+created.ID, nil))
	if got.Values["base_url"] != connectorBaseURL {
		t.Errorf("base_url = %v, want %q", got.Values["base_url"], connectorBaseURL)
	}
}

func TestConnectors_PatchChangesOnlyWhatItSends(t *testing.T) {
	app := newConnectorAPI(t)
	created := createConnector(t, app)

	rec := app.call(http.MethodPatch, "/connectors/"+created.ID, map[string]any{
		"description": "The orders service, now also used by the returns agent.",
	})
	expectStatus(t, rec, http.StatusOK)
	updated := decodeData[Connector](t, rec)

	if updated.Description == nil || *updated.Description != "The orders service, now also used by the returns agent." {
		t.Errorf("description = %v, want the one sent", updated.Description)
	}
	if updated.Name != created.Name {
		t.Errorf("name = %q, want %q: a field left out of the patch must be kept", updated.Name, created.Name)
	}
	if updated.Values["base_url"] != connectorBaseURL {
		t.Errorf("base_url = %v, want it kept when values are left out", updated.Values["base_url"])
	}

	const movedBaseURL = "https://orders-v2.example.com"
	rec = app.call(http.MethodPatch, "/connectors/"+created.ID, map[string]any{
		"values": map[string]any{"base_url": movedBaseURL},
	})
	expectStatus(t, rec, http.StatusOK)
	if replaced := decodeData[Connector](t, rec); replaced.Values["base_url"] != movedBaseURL {
		t.Errorf("base_url = %v, want %q: values sent replace the stored settings", replaced.Values["base_url"], movedBaseURL)
	}
}

func TestConnectors_Validation(t *testing.T) {
	app := newConnectorAPI(t)
	created := createConnector(t, app)

	t.Run("an immutable field is 422", func(t *testing.T) {
		rec := app.call(http.MethodPatch, "/connectors/"+created.ID, map[string]any{"connectorType": "mcp"})
		expectStatus(t, rec, http.StatusUnprocessableEntity)
		if err := decodeError(t, rec); err.ValidationDetails["connectorType"] == nil {
			t.Error("want a connectorType detail, got none")
		}
	})
	t.Run("a required field sent null is 422", func(t *testing.T) {
		rec := app.call(http.MethodPatch, "/connectors/"+created.ID, map[string]any{"name": nil})
		expectStatus(t, rec, http.StatusUnprocessableEntity)
		if err := decodeError(t, rec); err.ValidationDetails["name"] == nil {
			t.Error("want a name detail, got none")
		}
	})
	t.Run("a required field sent empty is 422", func(t *testing.T) {
		rec := app.call(http.MethodPatch, "/connectors/"+created.ID, map[string]any{"description": ""})
		expectStatus(t, rec, http.StatusUnprocessableEntity)
		if err := decodeError(t, rec); err.ValidationDetails["description"] == nil {
			t.Error("want a description detail: \"\" must not quietly mean unchanged")
		}
	})
	t.Run("values sent null is 422", func(t *testing.T) {
		rec := app.call(http.MethodPatch, "/connectors/"+created.ID, map[string]any{"values": nil})
		expectStatus(t, rec, http.StatusUnprocessableEntity)
		if err := decodeError(t, rec); err.ValidationDetails["values"] == nil {
			t.Error("want a values detail, got none")
		}
	})
	t.Run("an unknown field is 400", func(t *testing.T) {
		expectStatus(t, app.call(http.MethodPatch, "/connectors/"+created.ID, map[string]any{"nope": 1}), http.StatusBadRequest)
	})
	t.Run("a public OAuth client with PKCE disabled is 422 on the setting", func(t *testing.T) {
		rec := app.call(http.MethodPatch, "/connectors/"+created.ID, map[string]any{"values": map[string]any{
			"base_url":                              connectorBaseURL,
			"auth_type":                             "oauth",
			"auth_oauth_authorization_url":          "https://idp.example.com/authorize",
			"auth_oauth_token_url":                  "https://idp.example.com/token",
			"auth_oauth_client_id":                  "orders",
			"auth_oauth_token_endpoint_auth_method": "none",
			"auth_oauth_pkce":                       "disabled",
		}})
		expectStatus(t, rec, http.StatusUnprocessableEntity)
		if decodeError(t, rec).ValidationDetails["values.auth_oauth_pkce"] == nil {
			t.Errorf("want a values.auth_oauth_pkce detail, got %s", rec.Body.String())
		}
	})
	t.Run("a malformed id is 400, not v1's 422", func(t *testing.T) {
		expectStatus(t, app.call(http.MethodGet, "/connectors/not-a-uuid", nil), http.StatusBadRequest)
	})
	t.Run("a missing connector is 404", func(t *testing.T) {
		expectStatus(t, app.call(http.MethodGet, "/connectors/"+missingID, nil), http.StatusNotFound)
	})
	t.Run("a duplicate name is 409", func(t *testing.T) {
		expectStatus(t, app.call(http.MethodPost, "/connectors", map[string]any{
			"name": created.Name, "description": "A second connector under a name already taken.",
			"connectorType": "api", "connectorSubtype": "openapi",
			"values": map[string]any{"base_url": connectorBaseURL},
		}), http.StatusConflict)
	})
}

func connectorCreateBody(name string) map[string]any {
	return map[string]any{
		"name": name, "description": "The orders service the fulfilment agent calls.",
		"connectorType": "api", "connectorSubtype": "openapi",
		"values": map[string]any{"base_url": connectorBaseURL},
	}
}

func TestConnectors_RenameAppliesTheNameRulesOfCreate(t *testing.T) {
	app := newConnectorAPI(t)
	created := createConnector(t, app)

	for name, badName := range map[string]string{
		"too short":           "ab",
		"too long":            strings.Repeat("a", 256),
		"a control character": "orders\u0007api",
		"only whitespace":     "   ",
	} {
		t.Run(name+" is refused like create refuses it", func(t *testing.T) {
			createRec := app.call(http.MethodPost, "/connectors", connectorCreateBody(badName))
			expectStatus(t, createRec, http.StatusUnprocessableEntity)
			want := decodeError(t, createRec)

			rec := app.call(http.MethodPatch, "/connectors/"+created.ID, map[string]any{"name": badName})
			expectStatus(t, rec, http.StatusUnprocessableEntity)
			got := decodeError(t, rec)
			if got.Message != want.Message {
				t.Errorf("message = %q, want create's %q", got.Message, want.Message)
			}
			if !slices.Equal(got.ValidationDetails["name"], want.ValidationDetails["name"]) || len(got.ValidationDetails["name"]) == 0 {
				t.Errorf("validationDetails = %v, want create's %v", got.ValidationDetails, want.ValidationDetails)
			}

			stored := decodeData[Connector](t, app.call(http.MethodGet, "/connectors/"+created.ID, nil))
			if stored.Name != created.Name {
				t.Errorf("stored name = %q, want %q: a refused rename must change nothing", stored.Name, created.Name)
			}
		})
	}

	t.Run("a rename that breaks a rule reports the other bad fields with it", func(t *testing.T) {
		rec := app.call(http.MethodPatch, "/connectors/"+created.ID, map[string]any{"name": "ab", "description": "short"})
		expectStatus(t, rec, http.StatusUnprocessableEntity)
		details := decodeError(t, rec).ValidationDetails
		if details["name"] == nil || details["description"] == nil {
			t.Errorf("validationDetails = %v, want both name and description", details)
		}
	})

	for name, goodName := range map[string]string{
		"an ordinary name":       "Orders API v2",
		"exactly 3 characters":   "abc",
		"exactly 255 characters": strings.Repeat("b", 255),
	} {
		t.Run("a valid rename to "+name+" succeeds", func(t *testing.T) {
			rec := app.call(http.MethodPatch, "/connectors/"+created.ID, map[string]any{"name": goodName})
			expectStatus(t, rec, http.StatusOK)
			if got := decodeData[Connector](t, rec).Name; got != goodName {
				t.Errorf("name = %q, want the new one", got)
			}
			stored := decodeData[Connector](t, app.call(http.MethodGet, "/connectors/"+created.ID, nil))
			if stored.Name != goodName {
				t.Errorf("stored name = %q, want %q", stored.Name, goodName)
			}
		})
	}
}

func TestConnectors_RenameToANameAnotherConnectorHoldsIs409(t *testing.T) {
	app := newConnectorAPI(t)
	first := createNamedConnector(t, app, "billing-api")
	second := createNamedConnector(t, app, "shipping-api")

	rec := app.call(http.MethodPatch, "/connectors/"+second.ID, map[string]any{"name": first.Name})
	expectStatus(t, rec, http.StatusConflict)

	stored := decodeData[Connector](t, app.call(http.MethodGet, "/connectors/"+second.ID, nil))
	if stored.Name != second.Name {
		t.Errorf("stored name = %q, want %q: a refused rename must change nothing", stored.Name, second.Name)
	}
}

func TestConnectors_RenameOfAConnectorThatCannotBeReadIsNotAValidationError(t *testing.T) {
	t.Run("a missing connector is 404 even when the name is bad", func(t *testing.T) {
		app := newConnectorAPI(t)
		expectStatus(t, app.call(http.MethodPatch, "/connectors/"+missingID, map[string]any{"name": "ab"}), http.StatusNotFound)
	})

	t.Run("a read failure is 500, not a complaint about the name", func(t *testing.T) {
		core, deps := newTestCore(t)
		app := newAPIClient(t, core)
		created := createConnector(t, app)
		if _, err := deps.DB.ExecContext(t.Context(), "ALTER TABLE connectors RENAME TO connectors_hidden_by_test"); err != nil {
			t.Fatalf("hide connectors to force the read error: %v", err)
		}

		rec := app.call(http.MethodPatch, "/connectors/"+created.ID, map[string]any{"name": "ab"})
		expectStatus(t, rec, http.StatusInternalServerError)
		got := decodeError(t, rec)
		want := errorBody{Message: updateConnectorFailureMessage, ErrorID: got.ErrorID}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("500 body must be the constant message and nothing the driver said (-want +got):\n%s", diff)
		}
	})
}

func TestConnectors_PatchKeepsAStoredNameThatBreaksTheRules(t *testing.T) {
	core, deps := newTestCore(t)
	app := newAPIClient(t, core)
	repo := store.NewConnectorRepo(deps.DB)
	subtype := "openapi"
	legacy := &store.Connector{
		Name: storedNameBreakingTheNameRules, Type: "api", Subtype: &subtype, CreatedBy: "u", UpdatedBy: "u",
	}
	if err := repo.Create(t.Context(), legacy); err != nil {
		t.Fatalf("seed a connector whose name create would refuse: %v", err)
	}

	for name, body := range map[string]map[string]any{
		"another field alone":      {"description": "Now describes the orders service."},
		"the same name sent again": {"name": storedNameBreakingTheNameRules, "description": "Now describes the orders service."},
	} {
		t.Run(name, func(t *testing.T) {
			rec := app.call(http.MethodPatch, "/connectors/"+legacy.ID, body)
			expectStatus(t, rec, http.StatusOK)
			if got := decodeData[Connector](t, rec).Name; got != storedNameBreakingTheNameRules {
				t.Errorf("name = %q, want the stored name kept", got)
			}
		})
	}

	t.Run("renaming it to a name create would refuse is still refused", func(t *testing.T) {
		expectStatus(t, app.call(http.MethodPatch, "/connectors/"+legacy.ID, map[string]any{"name": "xy"}), http.StatusUnprocessableEntity)
	})
}

func TestConnectors_ListSortsByTheFieldAsked(t *testing.T) {
	app := newConnectorAPI(t)
	for _, name := range []string{"sorted-alpha", "sorted-bravo", "sorted-charlie"} {
		createNamedConnector(t, app, name)
	}

	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"?name=sorted-", []string{"sorted-charlie", "sorted-bravo", "sorted-alpha"}},
		{"?name=sorted-&sort=name:asc", []string{"sorted-alpha", "sorted-bravo", "sorted-charlie"}},
		{"?name=sorted-&sort=name:desc", []string{"sorted-charlie", "sorted-bravo", "sorted-alpha"}},
		{"?name=sorted-&sort=createdTime:asc", []string{"sorted-alpha", "sorted-bravo", "sorted-charlie"}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			listed := decodeList[Connector](t, app.call(http.MethodGet, "/connectors"+tc.query, nil))
			if got := connectorNames(listed.Data); !slices.Equal(got, tc.want) {
				t.Errorf("names = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("a field the list cannot sort by is 422", func(t *testing.T) {
		expectStatus(t, app.call(http.MethodGet, "/connectors?sort=values:asc", nil), http.StatusUnprocessableEntity)
	})
}

func TestConnectors_PatchNullClearsDescription(t *testing.T) {
	app := newConnectorAPI(t)
	created := createConnector(t, app)

	rec := app.call(http.MethodPatch, "/connectors/"+created.ID, map[string]any{"description": nil})
	expectStatus(t, rec, http.StatusOK)
	updated := decodeData[Connector](t, rec)
	if updated.Description != nil {
		t.Errorf("description = %q, want null after an explicit null", *updated.Description)
	}
	if updated.Name != created.Name {
		t.Errorf("name = %q, want %q: a field left out of the patch must be kept", updated.Name, created.Name)
	}

	fetched := decodeData[Connector](t, app.call(http.MethodGet, "/connectors/"+created.ID, nil))
	if fetched.Description != nil {
		t.Errorf("description = %q on re-read, want null", *fetched.Description)
	}
}

func TestConnectors_GetReportsAgentRegistrations(t *testing.T) {
	api, _ := newAWEAPI(t)
	connector := createConnector(t, api.apiClient)
	agent := expectCreated[Agent](t, api.call(http.MethodPost, "/agents", map[string]any{
		"name": "orders-agent", "description": "Agent bound to the orders connector.",
		"systemPrompt": "You help with tests.", "inputModes": []string{"text"},
		"connectorIds": []string{connector.ID},
	}), "agents")

	api.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: agent.InstanceName,
		Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{{
			URI: platpkg.ExtensionURIToolRegistrations,
			Params: map[string]any{"entries": []any{map[string]any{
				"tool_type": "openapi", "connector_id": connector.ID,
				"status": "failed", "error": "load spec: HTTP 500", "tool_count": 0,
			}}},
		}}},
	})

	got := decodeData[Connector](t, api.call(http.MethodGet, "/connectors/"+connector.ID, nil))
	if len(got.AgentRegistrations) != 1 {
		t.Fatalf("agentRegistrations = %+v, want the bound agent's entry", got.AgentRegistrations)
	}
	reg := got.AgentRegistrations[0]
	if reg.Agent.ID != agent.ID || reg.Agent.Name != agent.Name {
		t.Errorf("agent = %+v, want %s named %q", reg.Agent, agent.ID, agent.Name)
	}
	if reg.Status != "failed" || reg.Error == nil {
		t.Errorf("status = %q error = %v, want the failed entry with its reason", reg.Status, reg.Error)
	}

	listed := decodeList[Connector](t, api.call(http.MethodGet, "/connectors", nil))
	for _, c := range listed.Data {
		if len(c.AgentRegistrations) != 0 {
			t.Errorf("%s: a list carries agentRegistrations %+v, want none", c.Name, c.AgentRegistrations)
		}
	}
}

func TestConnectors_GetWarnsWhenRegistrationsAreIncomplete(t *testing.T) {
	core, deps := newTestCore(t)
	app := newAPIClient(t, core)
	created := createConnector(t, app)

	if _, err := deps.DB.ExecContext(t.Context(), "DROP TABLE agent_connectors"); err != nil {
		t.Fatalf("drop agent_connectors to force the listing error: %v", err)
	}

	rec := app.call(http.MethodGet, "/connectors/"+created.ID, nil)
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[Connector](t, rec); !slices.Contains(got.Warnings, entplatform.MsgAgentRegistrationsIncomplete) {
		t.Errorf("warnings = %v, want the incomplete-registrations warning", got.Warnings)
	}
}

func TestConnectors_FlagGatedTypeIsRefusedAndHidden(t *testing.T) {
	core, deps := newTestCore(t)
	app := newAPIClient(t, core)
	visible := createConnector(t, app)

	rec := app.call(http.MethodPost, "/connectors", map[string]any{
		"name": "mail-out", "description": "An email connector this deployment has turned off.",
		"connectorType": gatedConnectorType, "connectorSubtype": "smtp",
	})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if decodeError(t, rec).ValidationDetails["connectorType"] == nil {
		t.Error("want a connectorType detail saying the type is not enabled here")
	}

	repo := store.NewConnectorRepo(deps.DB)
	if err := repo.Create(t.Context(), &store.Connector{
		Name: "seeded-mail", Type: gatedConnectorType, CreatedBy: "u", UpdatedBy: "u",
	}); err != nil {
		t.Fatalf("seed a %s connector: %v", gatedConnectorType, err)
	}

	listed := decodeList[Connector](t, app.call(http.MethodGet, "/connectors", nil))
	if len(listed.Data) != 1 || listed.Data[0].ID != visible.ID {
		t.Fatalf("list = %v, want only %q, the connector of an enabled type", connectorNames(listed.Data), visible.Name)
	}
	if listed.Meta.Pagination.Count != 1 {
		t.Errorf("count = %d, want 1: a hidden row must leave the count too", listed.Meta.Pagination.Count)
	}
}

func TestConnectors_Scopes(t *testing.T) {
	app := newConnectorAPI(t)

	readOnly := app.as(rbac.ScopeConnectorRead)
	expectStatus(t, readOnly.call(http.MethodGet, "/connectors", nil), http.StatusOK)
	expectStatus(t, readOnly.call(http.MethodPost, "/connectors", map[string]any{"name": "x"}), http.StatusForbidden)
	expectStatus(t, readOnly.call(http.MethodDelete, "/connectors/"+missingID, nil), http.StatusForbidden)
	expectStatus(t, readOnly.call(http.MethodPatch, "/connectors/"+missingID, map[string]any{"name": "x"}), http.StatusForbidden)
}

func TestConnectors_RejectedValuesNameTheField(t *testing.T) {
	app := newConnectorAPI(t)
	invalid := []struct {
		connectorType, connectorSubtype string
		values                          map[string]any
		saying                          string
	}{
		{"document_db", "mongodb", map[string]any{"hostname": "invalid host name", "collection": "events"}, "host"},
		{"document_db", "dynamodb", map[string]any{"region": "Not A Region", "table_name": "Orders", "auth_type": "access_key", "aws_access_key_id": "AKIAIOSFODNN7EXAMPLE", "aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLE"}, "region"},
		{"graph_db", "neo4j", map[string]any{"hostname": "bad host name", "username": "neo4j", "password": "secret"}, "host"},
		{"graph_db", "neptune", map[string]any{"hostname": "bad endpoint host", "region": "us-east-1", "auth_type": "iam", "role_arn": "arn:aws:iam::123456789012:role/SolaceNeptune"}, "Endpoint"},
		{"search", "elasticsearch", map[string]any{"hostname": "bad es host", "api_key": "ZGVtbzpkZW1v"}, "host"},
		{"search", "opensearch", map[string]any{"scheme": "https", "hostname": "my-domain.us-east-1.es.amazonaws.com", "region": "US-East-1", "auth_type": "iam", "role_arn": "arn:aws:iam::123456789012:role/SolaceOS"}, "region"},
	}
	for _, tc := range invalid {
		t.Run(tc.connectorSubtype, func(t *testing.T) {
			expectDetailSaying(t, app.call(http.MethodPost, "/connectors", map[string]any{
				"name": "invalid-" + tc.connectorSubtype, "description": "A connector with a malformed setting.",
				"connectorType": tc.connectorType, "connectorSubtype": tc.connectorSubtype, "values": tc.values,
			}), "values", tc.saying)
		})
	}

	undeclared := []struct{ connectorType, connectorSubtype string }{
		{"document_db", "cassandra"}, {"graph_db", "janusgraph"}, {"search", "solr"},
	}
	for _, tc := range undeclared {
		t.Run(tc.connectorType+"_"+tc.connectorSubtype, func(t *testing.T) {
			expectDetailSaying(t, app.call(http.MethodPost, "/connectors", map[string]any{
				"name": "undeclared-" + tc.connectorSubtype, "description": "A connector with a subtype its type lacks.",
				"connectorType": tc.connectorType, "connectorSubtype": tc.connectorSubtype, "values": map[string]any{},
			}), "connectorSubtype", tc.connectorSubtype)
		})
	}
}

func TestConnectors_MalformedApprovalGateIsRefusedInPlainWords(t *testing.T) {
	app := newConnectorAPI(t)
	created := createConnector(t, app)

	for name, tc := range map[string]struct {
		hil    any
		saying string
	}{
		"not a map":      {"tools: [unclosed", "hil: must be a map"},
		"not a duration": {map[string]any{"tools": map[string]any{"getOrder": map[string]any{"timeout": "soon"}}}, "must be a duration string"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := app.call(http.MethodPatch, "/connectors/"+created.ID, map[string]any{
				"values": map[string]any{"base_url": connectorBaseURL, "hil": tc.hil},
			})
			expectDetailSaying(t, rec, "values", tc.saying)
			for _, parserText := range []string{"yaml:", "time:"} {
				if strings.Contains(rec.Body.String(), parserText) {
					t.Errorf("the response carries the parser's text %q: %s", parserText, rec.Body.String())
				}
			}
		})
	}
}

func TestConnectors_MalformedEventMeshParametersAreRefusedInPlainWords(t *testing.T) {
	app := newConnectorAPI(t)
	created := createEventMeshConnector(t, app)

	rec := app.call(http.MethodPatch, "/connectors/"+created.ID, map[string]any{
		"values": map[string]any{
			"broker_url": "tcp://broker.example.com:55555", "broker_vpn": "default",
			"broker_username": "user", "broker_password": storedBrokerPassword,
			"parameters": `[{"name":`,
		},
	})
	expectDetailSaying(t, rec, "values", "Parameters must be a valid JSON array")
	if strings.Contains(rec.Body.String(), "JSON input") {
		t.Errorf("the response carries the parser's text: %s", rec.Body.String())
	}
}
