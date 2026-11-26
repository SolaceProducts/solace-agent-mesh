// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

var flagGatedConnectorTypes = []struct {
	connType, flag, subtype string
	values                  map[string]any
}{
	{"event_mesh", "event_mesh_connector", "solace", map[string]any{
		"broker_url": "tcp://broker.example.com:55555", "broker_vpn": "default", "broker_username": "user", "broker_password": "broker-password",
	}},
	{"document_db", "document_db_connector", "mongodb", map[string]any{
		"scheme": "mongodb+srv", "hostname": "cluster.example.net", "username": "user", "password": "pass", "database": "orders", "collection": "events",
	}},
	{"graph_db", "graph_db_connector", "neo4j", map[string]any{
		"scheme": "bolt+s", "hostname": "neo4j.example.com", "port": 7687, "username": "neo4j", "password": "secret", "database": "graph",
	}},
	{"search", "search_connector", "elasticsearch", map[string]any{
		"scheme": "https", "hostname": "elastic.example.com", "port": 9200, "api_key": "ZGVtbzpkZW1v",
	}},
	{"web_search", "web_search_connector", "tavily", map[string]any{
		"api_key": "<<__SAM_REQUIRED__>>",
	}},
}

func TestConnectors_FlagTurnsATypeOffAndOnAgain(t *testing.T) {
	for _, tc := range flagGatedConnectorTypes {
		t.Run(tc.connType, func(t *testing.T) {
			t.Setenv(features.EnvVarName(tc.flag), "false")
			core, deps := newTestCore(t)
			app := newAPIClient(t, core)

			schemas := decodeList[ConnectorSchema](t, app.call(http.MethodGet, "/connectorSchemas?pageSize=100", nil))
			if slices.Contains(schemaIDs(schemas.Data), tc.connType) {
				t.Errorf("schemas list %s while %s is off", tc.connType, tc.flag)
			}
			if schemas.Meta.Pagination.Count != len(schemas.Data) {
				t.Errorf("schemas count = %d, want %d", schemas.Meta.Pagination.Count, len(schemas.Data))
			}
			expectStatus(t, app.call(http.MethodGet, "/connectorSchemas/"+tc.connType, nil), http.StatusNotFound)

			rec := app.call(http.MethodPost, "/connectors", map[string]any{
				"name": "blocked-" + tc.connType, "description": "A connector of a type switched off here.",
				"connectorType": tc.connType, "connectorSubtype": tc.subtype, "values": tc.values,
			})
			expectDetail(t, rec, http.StatusUnprocessableEntity, "connectorType")
			if detail := decodeError(t, rec).ValidationDetails["connectorType"]; !strings.Contains(strings.Join(detail, " "), "not enabled") {
				t.Errorf("connectorType detail = %v, want it to say the type is not enabled", detail)
			}

			subtype := tc.subtype
			seeded := &store.Connector{Name: "seeded-" + tc.connType, Type: tc.connType, Subtype: &subtype, CreatedBy: "u", UpdatedBy: "u"}
			if err := store.NewConnectorRepo(deps.DB).Create(t.Context(), seeded); err != nil {
				t.Fatalf("seed a %s connector: %v", tc.connType, err)
			}
			hidden := decodeList[Connector](t, app.call(http.MethodGet, "/connectors", nil))
			if len(hidden.Data) != 0 || hidden.Meta.Pagination.Count != 0 {
				t.Fatalf("list = %v (count %d) while %s is off, want nothing", connectorNames(hidden.Data), hidden.Meta.Pagination.Count, tc.flag)
			}

			t.Setenv(features.EnvVarName(tc.flag), "true")

			schemas = decodeList[ConnectorSchema](t, app.call(http.MethodGet, "/connectorSchemas?pageSize=100", nil))
			if !slices.Contains(schemaIDs(schemas.Data), tc.connType) {
				t.Errorf("schemas leave out %s while %s is on", tc.connType, tc.flag)
			}
			shown := decodeList[Connector](t, app.call(http.MethodGet, "/connectors", nil))
			if len(shown.Data) != 1 || shown.Data[0].ID != seeded.ID {
				t.Errorf("list = %v while %s is on, want the seeded %s connector", connectorNames(shown.Data), tc.flag, tc.connType)
			}
			created := app.call(http.MethodPost, "/connectors", map[string]any{
				"name": "allowed-" + tc.connType, "description": "A connector of a type switched on here.",
				"connectorType": tc.connType, "connectorSubtype": tc.subtype, "values": tc.values,
			})
			expectStatus(t, created, http.StatusCreated)
		})
	}
}

func TestConnectorSchemas_EventMeshBrokerFieldsHaveNoDefaults(t *testing.T) {
	t.Setenv("SOLACE_BROKER_URL", "tcps://platform-broker.example.com:55443")
	t.Setenv("SOLACE_BROKER_VPN", "platform-vpn")
	t.Setenv("SOLACE_BROKER_USERNAME", "platform-user")
	app := newConnectorAPI(t)

	schema := decodeData[ConnectorSchema](t, app.call(http.MethodGet, "/connectorSchemas/event_mesh", nil))
	i := slices.IndexFunc(schema.Subtypes, func(s ConnectorSubtypeSchema) bool { return s.Subtype == "solace" })
	if i < 0 {
		t.Fatalf("event_mesh subtypes = %+v, want solace", schema.Subtypes)
	}
	fields := schema.Subtypes[i].FieldSchema
	for _, name := range []string{"broker_url", "broker_vpn", "broker_username", "broker_password"} {
		field, ok := fields[name]
		if !ok {
			t.Errorf("event_mesh/solace has no %s field", name)
			continue
		}
		if field.Default != nil {
			t.Errorf("%s default = %v, want none", name, field.Default)
		}
	}
}

func TestConnectorSchemas_ListTheDatabaseAndSearchTypesWithTheirSubtypes(t *testing.T) {
	app := newConnectorAPI(t)

	schemas := decodeList[ConnectorSchema](t, app.call(http.MethodGet, "/connectorSchemas?pageSize=100", nil))
	for connType, want := range map[string][]string{
		"sql":         {"postgres", "mysql", "mariadb", "mssql", "oracle"},
		"document_db": {"mongodb", "dynamodb"},
		"graph_db":    {"neo4j", "neptune"},
		"search":      {"elasticsearch", "opensearch"},
		"web_search":  {"tavily", "exa"},
	} {
		i := slices.IndexFunc(schemas.Data, func(s ConnectorSchema) bool { return s.ID == connType })
		if i < 0 {
			t.Errorf("schemas leave out %s", connType)
			continue
		}
		var got []string
		for _, sub := range schemas.Data[i].Subtypes {
			got = append(got, sub.Subtype)
			if len(sub.FieldSchema) == 0 {
				t.Errorf("%s/%s has no fields, which leaves the form empty", connType, sub.Subtype)
			}
		}
		for _, subtype := range want {
			if !slices.Contains(got, subtype) {
				t.Errorf("%s subtypes = %v, want %s among them", connType, got, subtype)
			}
		}
	}
}
